package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

const maxCryptoTickerResponse = 2 << 20

type CryptoTickerDiagnostic struct {
	Exchange string `json:"exchange"`
	Status   string `json:"status"`
	Message  string `json:"message,omitempty"`
}

type CryptoTickerResult struct {
	Tickers     []CryptoTicker           `json:"tickers"`
	Diagnostics []CryptoTickerDiagnostic `json:"diagnostics"`
	FetchedAt   string                   `json:"fetched_at"`
}

// Tickers fetches bounded REST snapshots from both exchanges. A failure from
// one exchange is reported as a diagnostic and does not hide data from the
// other exchange.
func (s *CryptoService) Tickers(ctx context.Context, symbols []string) (CryptoTickerResult, error) {
	normalized, err := normalizeCryptoSymbols(symbols)
	if err != nil {
		return CryptoTickerResult{}, err
	}
	type providerResult struct {
		exchange string
		tickers  []CryptoTicker
		err      error
	}
	results := make(chan providerResult, 2)
	var group sync.WaitGroup
	group.Add(2)
	go func() {
		defer group.Done()
		tickers, fetchErr := s.fetchBinanceTickers(ctx, normalized)
		results <- providerResult{exchange: "binance", tickers: tickers, err: fetchErr}
	}()
	go func() {
		defer group.Done()
		tickers, fetchErr := s.fetchOKXTickers(ctx, normalized)
		results <- providerResult{exchange: "okx", tickers: tickers, err: fetchErr}
	}()
	go func() {
		group.Wait()
		close(results)
	}()

	result := CryptoTickerResult{Tickers: make([]CryptoTicker, 0, len(normalized)*2), Diagnostics: make([]CryptoTickerDiagnostic, 0, 2)}
	failed := 0
	for provider := range results {
		diagnostic := CryptoTickerDiagnostic{Exchange: provider.exchange, Status: "ok"}
		if provider.err != nil {
			failed++
			diagnostic.Status = "error"
			diagnostic.Message = provider.err.Error()
		} else {
			result.Tickers = append(result.Tickers, provider.tickers...)
		}
		result.Diagnostics = append(result.Diagnostics, diagnostic)
	}
	result.FetchedAt = time.Now().UTC().Format(time.RFC3339Nano)
	if failed == 2 {
		return result, fmt.Errorf("all crypto ticker providers are unavailable")
	}
	return result, nil
}

func (s *CryptoService) fetchBinanceTickers(ctx context.Context, symbols []string) ([]CryptoTicker, error) {
	endpoint, err := url.Parse(strings.TrimRight(s.binanceRESTURL, "/") + "/api/v3/ticker/24hr")
	if err != nil {
		return nil, fmt.Errorf("Binance ticker address is invalid")
	}
	rawSymbols := make([]string, 0, len(symbols))
	lookup := make(map[string]string, len(symbols))
	for _, symbol := range symbols {
		raw := strings.ReplaceAll(symbol, "-", "")
		rawSymbols = append(rawSymbols, raw)
		lookup[raw] = symbol
	}
	encoded, _ := json.Marshal(rawSymbols)
	query := endpoint.Query()
	query.Set("symbols", string(encoded))
	endpoint.RawQuery = query.Encode()
	payload, err := s.getCryptoTickerPayload(ctx, endpoint.String(), "Binance")
	if err != nil {
		return nil, err
	}
	var rows []struct {
		Symbol       string `json:"symbol"`
		LastPrice    string `json:"lastPrice"`
		PriceChange  string `json:"priceChange"`
		PricePercent string `json:"priceChangePercent"`
		High         string `json:"highPrice"`
		Low          string `json:"lowPrice"`
		Volume       string `json:"volume"`
		QuoteVolume  string `json:"quoteVolume"`
		Bid          string `json:"bidPrice"`
		Ask          string `json:"askPrice"`
		CloseTime    int64  `json:"closeTime"`
	}
	if err := json.Unmarshal(payload, &rows); err != nil {
		return nil, fmt.Errorf("cannot parse Binance tickers")
	}
	tickers := make([]CryptoTicker, 0, len(rows))
	for _, row := range rows {
		symbol, ok := lookup[strings.ToUpper(row.Symbol)]
		if !ok {
			continue
		}
		tickers = append(tickers, CryptoTicker{
			Symbol: symbol, Exchange: "binance", Price: parseOptionalFloat(row.LastPrice),
			Change: parseOptionalFloat(row.PriceChange), ChangePercent: parseOptionalFloat(row.PricePercent),
			High: parseOptionalFloat(row.High), Low: parseOptionalFloat(row.Low), Volume: parseOptionalFloat(row.Volume),
			Turnover: parseOptionalFloat(row.QuoteVolume), Bid: parseOptionalFloat(row.Bid), Ask: parseOptionalFloat(row.Ask),
			UpdatedAt: time.UnixMilli(row.CloseTime).UTC().Format(time.RFC3339Nano),
		})
	}
	return tickers, nil
}

func (s *CryptoService) fetchOKXTickers(ctx context.Context, symbols []string) ([]CryptoTicker, error) {
	endpoint, err := url.Parse(strings.TrimRight(s.okxRESTURL, "/") + "/api/v5/market/tickers")
	if err != nil {
		return nil, fmt.Errorf("OKX ticker address is invalid")
	}
	query := endpoint.Query()
	query.Set("instType", "SPOT")
	endpoint.RawQuery = query.Encode()
	payload, err := s.getCryptoTickerPayload(ctx, endpoint.String(), "OKX")
	if err != nil {
		return nil, err
	}
	var envelope struct {
		Code string `json:"code"`
		Msg  string `json:"msg"`
		Data []struct {
			InstID   string `json:"instId"`
			Price    string `json:"last"`
			Open24h  string `json:"open24h"`
			High24h  string `json:"high24h"`
			Low24h   string `json:"low24h"`
			Volume   string `json:"vol24h"`
			Turnover string `json:"volCcy24h"`
			Bid      string `json:"bidPx"`
			Ask      string `json:"askPx"`
			Time     string `json:"ts"`
		} `json:"data"`
	}
	if err := json.Unmarshal(payload, &envelope); err != nil || envelope.Code != "0" {
		return nil, fmt.Errorf("cannot parse OKX tickers")
	}
	wanted := make(map[string]bool, len(symbols))
	for _, symbol := range symbols {
		wanted[symbol] = true
	}
	tickers := make([]CryptoTicker, 0, len(symbols))
	for _, row := range envelope.Data {
		symbol := strings.ToUpper(row.InstID)
		if !wanted[symbol] {
			continue
		}
		price := parseOptionalFloat(row.Price)
		open := parseOptionalFloat(row.Open24h)
		var change, percent *float64
		if price != nil && open != nil {
			value := *price - *open
			change = &value
			if *open != 0 {
				value = value / *open * 100
				percent = &value
			}
		}
		updatedAt := ""
		if millis, parseErr := strconv.ParseInt(row.Time, 10, 64); parseErr == nil {
			updatedAt = time.UnixMilli(millis).UTC().Format(time.RFC3339Nano)
		}
		tickers = append(tickers, CryptoTicker{
			Symbol: symbol, Exchange: "okx", Price: price, Change: change, ChangePercent: percent,
			High: parseOptionalFloat(row.High24h), Low: parseOptionalFloat(row.Low24h), Volume: parseOptionalFloat(row.Volume),
			Turnover: parseOptionalFloat(row.Turnover), Bid: parseOptionalFloat(row.Bid), Ask: parseOptionalFloat(row.Ask), UpdatedAt: updatedAt,
		})
	}
	return tickers, nil
}

func (s *CryptoService) getCryptoTickerPayload(ctx context.Context, endpoint, exchange string) ([]byte, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("cannot create %s ticker request", exchange)
	}
	request.Header.Set("Accept", "application/json")
	client := s.httpClient
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	response, err := client.Do(request)
	if err != nil {
		return nil, fmt.Errorf("%s ticker request failed", exchange)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, fmt.Errorf("%s ticker returned HTTP %d", exchange, response.StatusCode)
	}
	payload, err := io.ReadAll(io.LimitReader(response.Body, maxCryptoTickerResponse))
	if err != nil {
		return nil, fmt.Errorf("cannot read %s ticker response", exchange)
	}
	return payload, nil
}

func parseOptionalFloat(value string) *float64 {
	number, err := strconv.ParseFloat(strings.TrimSpace(value), 64)
	if err != nil {
		return nil
	}
	return &number
}
