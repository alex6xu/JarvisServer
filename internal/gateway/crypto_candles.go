package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	defaultCryptoCandleLimit = 300
	maxCryptoCandleLimit     = 300
	maxCryptoCandleResponse  = 2 << 20
)

var cryptoCandleIntervals = map[string]struct {
	binance string
	okx     string
}{
	"1m":  {binance: "1m", okx: "1m"},
	"5m":  {binance: "5m", okx: "5m"},
	"15m": {binance: "15m", okx: "15m"},
	"1h":  {binance: "1h", okx: "1H"},
	"4h":  {binance: "4h", okx: "4H"},
	"1d":  {binance: "1d", okx: "1Dutc"},
}

type CryptoCandle struct {
	Time      int64   `json:"time"`
	Open      float64 `json:"open"`
	High      float64 `json:"high"`
	Low       float64 `json:"low"`
	Close     float64 `json:"close"`
	Volume    float64 `json:"volume"`
	Turnover  float64 `json:"turnover"`
	Confirmed bool    `json:"confirmed"`
}

type CryptoCandleResult struct {
	Symbol    string         `json:"symbol"`
	Exchange  string         `json:"exchange"`
	Interval  string         `json:"interval"`
	Candles   []CryptoCandle `json:"candles"`
	FetchedAt string         `json:"fetched_at"`
}

func (s *CryptoService) Candles(ctx context.Context, exchange, symbol, interval string, limit int) (CryptoCandleResult, error) {
	exchange = strings.ToLower(strings.TrimSpace(exchange))
	if exchange != "binance" && exchange != "okx" {
		return CryptoCandleResult{}, fmt.Errorf("%w: 不支持的交易所 %q", errInvalidCryptoRequest, exchange)
	}
	normalized, err := normalizeCryptoSymbols([]string{symbol})
	if err != nil {
		return CryptoCandleResult{}, err
	}
	symbol = normalized[0]
	interval = strings.ToLower(strings.TrimSpace(interval))
	providerInterval, ok := cryptoCandleIntervals[interval]
	if !ok {
		return CryptoCandleResult{}, fmt.Errorf("%w: 不支持的 K 线周期 %q", errInvalidCryptoRequest, interval)
	}
	if limit <= 0 {
		limit = defaultCryptoCandleLimit
	}
	if limit > maxCryptoCandleLimit {
		limit = maxCryptoCandleLimit
	}

	var endpoint string
	if exchange == "binance" {
		endpoint, err = buildBinanceCandleURL(s.binanceRESTURL, symbol, providerInterval.binance, limit)
	} else {
		endpoint, err = buildOKXCandleURL(s.okxRESTURL, symbol, providerInterval.okx, limit)
	}
	if err != nil {
		return CryptoCandleResult{}, fmt.Errorf("K 线地址无效")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return CryptoCandleResult{}, fmt.Errorf("创建 K 线请求失败")
	}
	request.Header.Set("Accept", "application/json")
	client := s.httpClient
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	response, err := client.Do(request)
	if err != nil {
		return CryptoCandleResult{}, fmt.Errorf("%s K 线请求失败", strings.ToUpper(exchange))
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return CryptoCandleResult{}, fmt.Errorf("%s K 线返回 HTTP %d", strings.ToUpper(exchange), response.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, maxCryptoCandleResponse))
	if err != nil {
		return CryptoCandleResult{}, fmt.Errorf("读取 %s K 线失败", strings.ToUpper(exchange))
	}
	var candles []CryptoCandle
	if exchange == "binance" {
		candles, err = parseBinanceCandles(body, time.Now())
	} else {
		candles, err = parseOKXCandles(body)
	}
	if err != nil {
		return CryptoCandleResult{}, err
	}
	return CryptoCandleResult{
		Symbol: symbol, Exchange: exchange, Interval: interval, Candles: candles,
		FetchedAt: time.Now().UTC().Format(time.RFC3339Nano),
	}, nil
}

func buildBinanceCandleURL(baseURL, symbol, interval string, limit int) (string, error) {
	parsed, err := url.Parse(strings.TrimRight(baseURL, "/") + "/api/v3/klines")
	if err != nil {
		return "", err
	}
	query := parsed.Query()
	query.Set("symbol", strings.ReplaceAll(symbol, "-", ""))
	query.Set("interval", interval)
	query.Set("limit", strconv.Itoa(limit))
	parsed.RawQuery = query.Encode()
	return parsed.String(), nil
}

func buildOKXCandleURL(baseURL, symbol, interval string, limit int) (string, error) {
	parsed, err := url.Parse(strings.TrimRight(baseURL, "/") + "/api/v5/market/candles")
	if err != nil {
		return "", err
	}
	query := parsed.Query()
	query.Set("instId", symbol)
	query.Set("bar", interval)
	query.Set("limit", strconv.Itoa(limit))
	parsed.RawQuery = query.Encode()
	return parsed.String(), nil
}

func parseBinanceCandles(payload []byte, now time.Time) ([]CryptoCandle, error) {
	var rows [][]json.RawMessage
	if err := json.Unmarshal(payload, &rows); err != nil {
		return nil, fmt.Errorf("解析 Binance K 线失败")
	}
	candles := make([]CryptoCandle, 0, len(rows))
	for _, row := range rows {
		if len(row) < 8 {
			continue
		}
		openTime, timeOK := rawJSONInt64(row[0])
		open, openOK := rawJSONFloat(row[1])
		high, highOK := rawJSONFloat(row[2])
		low, lowOK := rawJSONFloat(row[3])
		closePrice, closeOK := rawJSONFloat(row[4])
		volume, volumeOK := rawJSONFloat(row[5])
		closeTime, closeTimeOK := rawJSONInt64(row[6])
		turnover, turnoverOK := rawJSONFloat(row[7])
		if !timeOK || !openOK || !highOK || !lowOK || !closeOK || !volumeOK || !turnoverOK {
			continue
		}
		candles = append(candles, CryptoCandle{
			Time: openTime / 1000, Open: open, High: high, Low: low, Close: closePrice,
			Volume: volume, Turnover: turnover, Confirmed: closeTimeOK && closeTime < now.UnixMilli(),
		})
	}
	return sortCryptoCandles(candles), nil
}

func parseOKXCandles(payload []byte) ([]CryptoCandle, error) {
	var envelope struct {
		Code string     `json:"code"`
		Data [][]string `json:"data"`
	}
	if err := json.Unmarshal(payload, &envelope); err != nil {
		return nil, fmt.Errorf("解析 OKX K 线失败")
	}
	if envelope.Code != "" && envelope.Code != "0" {
		return nil, fmt.Errorf("OKX K 线返回错误 %s", envelope.Code)
	}
	candles := make([]CryptoCandle, 0, len(envelope.Data))
	for _, row := range envelope.Data {
		if len(row) < 9 {
			continue
		}
		openTime, timeErr := strconv.ParseInt(row[0], 10, 64)
		open, openErr := strconv.ParseFloat(row[1], 64)
		high, highErr := strconv.ParseFloat(row[2], 64)
		low, lowErr := strconv.ParseFloat(row[3], 64)
		closePrice, closeErr := strconv.ParseFloat(row[4], 64)
		volume, volumeErr := strconv.ParseFloat(row[5], 64)
		turnover, turnoverErr := strconv.ParseFloat(row[7], 64)
		if timeErr != nil || openErr != nil || highErr != nil || lowErr != nil || closeErr != nil || volumeErr != nil || turnoverErr != nil {
			continue
		}
		candles = append(candles, CryptoCandle{
			Time: openTime / 1000, Open: open, High: high, Low: low, Close: closePrice,
			Volume: volume, Turnover: turnover, Confirmed: row[8] == "1",
		})
	}
	return sortCryptoCandles(candles), nil
}

func sortCryptoCandles(candles []CryptoCandle) []CryptoCandle {
	sort.Slice(candles, func(i, j int) bool { return candles[i].Time < candles[j].Time })
	return candles
}

func rawJSONFloat(value json.RawMessage) (float64, bool) {
	var text string
	if err := json.Unmarshal(value, &text); err == nil {
		number, parseErr := strconv.ParseFloat(text, 64)
		return number, parseErr == nil
	}
	var number float64
	return number, json.Unmarshal(value, &number) == nil
}

func rawJSONInt64(value json.RawMessage) (int64, bool) {
	var number int64
	if err := json.Unmarshal(value, &number); err == nil {
		return number, true
	}
	var text string
	if err := json.Unmarshal(value, &text); err != nil {
		return 0, false
	}
	number, err := strconv.ParseInt(text, 10, 64)
	return number, err == nil
}
