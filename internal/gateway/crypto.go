package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
)

const maxCryptoSymbols = 10

var (
	cryptoSymbolPattern     = regexp.MustCompile(`^[A-Z0-9]{2,10}-[A-Z0-9]{2,10}$`)
	errInvalidCryptoRequest = errors.New("invalid crypto request")
)

type CryptoService struct {
	binanceURL            string
	okxURL                string
	binanceLiquidationURL string
	okxLiquidationURL     string
	binanceRESTURL        string
	okxRESTURL            string
	dialTimeout           time.Duration
	httpClient            *http.Client
}

type CryptoTicker struct {
	Symbol        string   `json:"symbol"`
	Exchange      string   `json:"exchange"`
	Price         *float64 `json:"price"`
	Change        *float64 `json:"change"`
	ChangePercent *float64 `json:"change_percent"`
	High          *float64 `json:"high"`
	Low           *float64 `json:"low"`
	Volume        *float64 `json:"volume"`
	Turnover      *float64 `json:"turnover"`
	Bid           *float64 `json:"bid"`
	Ask           *float64 `json:"ask"`
	UpdatedAt     string   `json:"updated_at"`
}

type CryptoStreamEvent struct {
	Type     string        `json:"type"`
	Exchange string        `json:"exchange"`
	State    string        `json:"state,omitempty"`
	Message  string        `json:"message,omitempty"`
	Ticker   *CryptoTicker `json:"ticker,omitempty"`
}

type binanceTickerEnvelope struct {
	Data binanceTicker `json:"data"`
}

type binanceTicker struct {
	EventTime     int64  `json:"E"`
	Symbol        string `json:"s"`
	Price         string `json:"c"`
	Change        string `json:"p"`
	ChangePercent string `json:"P"`
	High          string `json:"h"`
	Low           string `json:"l"`
	Volume        string `json:"v"`
	Turnover      string `json:"q"`
	Bid           string `json:"b"`
	Ask           string `json:"a"`
}

type okxTickerEnvelope struct {
	Event string `json:"event"`
	Code  string `json:"code"`
	Msg   string `json:"msg"`
	Arg   struct {
		InstID string `json:"instId"`
	} `json:"arg"`
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

func NewCryptoService(opts Options) *CryptoService {
	opts = opts.withDefaults()
	return &CryptoService{
		binanceURL:            opts.BinanceMarketWSURL,
		okxURL:                opts.OKXMarketWSURL,
		binanceLiquidationURL: binanceFuturesWSURL,
		okxLiquidationURL:     opts.OKXMarketWSURL,
		binanceRESTURL:        strings.TrimRight(opts.BinanceMarketRESTURL, "/"),
		okxRESTURL:            strings.TrimRight(opts.OKXMarketRESTURL, "/"),
		dialTimeout:           10 * time.Second,
		httpClient:            &http.Client{Timeout: 10 * time.Second},
	}
}

func (s *CryptoService) Stream(ctx context.Context, symbols []string) (<-chan CryptoStreamEvent, error) {
	normalized, err := normalizeCryptoSymbols(symbols)
	if err != nil {
		return nil, err
	}
	events := make(chan CryptoStreamEvent, 128)
	var providers sync.WaitGroup
	providers.Add(2)
	go func() {
		defer providers.Done()
		s.runProvider(ctx, "binance", normalized, events, s.streamBinance)
	}()
	go func() {
		defer providers.Done()
		s.runProvider(ctx, "okx", normalized, events, s.streamOKX)
	}()
	go func() {
		providers.Wait()
		close(events)
	}()
	return events, nil
}

type cryptoProviderStream func(context.Context, []string, chan<- CryptoStreamEvent) error

func (s *CryptoService) runProvider(
	ctx context.Context,
	exchange string,
	symbols []string,
	events chan<- CryptoStreamEvent,
	stream cryptoProviderStream,
) {
	delay := time.Second
	for {
		if !sendCryptoEvent(ctx, events, CryptoStreamEvent{Type: "status", Exchange: exchange, State: "connecting"}) {
			return
		}
		err := stream(ctx, symbols, events)
		if ctx.Err() != nil {
			return
		}
		message := "连接中断，正在重试"
		if err != nil {
			message = err.Error()
		}
		if !sendCryptoEvent(ctx, events, CryptoStreamEvent{
			Type: "status", Exchange: exchange, State: "disconnected", Message: message,
		}) {
			return
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
		delay = min(delay*2, 15*time.Second)
	}
}

func (s *CryptoService) streamBinance(ctx context.Context, symbols []string, events chan<- CryptoStreamEvent) error {
	endpoint, err := url.Parse(s.binanceURL)
	if err != nil {
		return fmt.Errorf("Binance WebSocket 地址无效")
	}
	streams := make([]string, 0, len(symbols))
	providerSymbols := make(map[string]string, len(symbols))
	for _, symbol := range symbols {
		providerSymbol := strings.ReplaceAll(symbol, "-", "")
		providerSymbols[providerSymbol] = symbol
		streams = append(streams, strings.ToLower(providerSymbol)+"@ticker")
	}
	streamQuery := "streams=" + strings.Join(streams, "/")
	if endpoint.RawQuery == "" {
		endpoint.RawQuery = streamQuery
	} else {
		endpoint.RawQuery += "&" + streamQuery
	}

	conn, cancelDial, err := s.dial(ctx, endpoint.String())
	if err != nil {
		return fmt.Errorf("Binance 连接失败")
	}
	defer cancelDial()
	defer conn.Close(websocket.StatusNormalClosure, "stream closed")
	conn.SetReadLimit(1 << 20)
	if !sendCryptoEvent(ctx, events, CryptoStreamEvent{Type: "status", Exchange: "binance", State: "connected"}) {
		return ctx.Err()
	}

	for {
		_, payload, err := conn.Read(ctx)
		if err != nil {
			return fmt.Errorf("Binance 实时流中断")
		}
		ticker, ok := parseBinanceTicker(payload, providerSymbols)
		if ok && !sendCryptoEvent(ctx, events, CryptoStreamEvent{Type: "ticker", Exchange: "binance", Ticker: &ticker}) {
			return ctx.Err()
		}
	}
}

func (s *CryptoService) streamOKX(ctx context.Context, symbols []string, events chan<- CryptoStreamEvent) error {
	conn, cancelDial, err := s.dial(ctx, s.okxURL)
	if err != nil {
		return fmt.Errorf("OKX 连接失败")
	}
	defer cancelDial()
	defer conn.Close(websocket.StatusNormalClosure, "stream closed")
	conn.SetReadLimit(1 << 20)

	args := make([]map[string]string, 0, len(symbols))
	for _, symbol := range symbols {
		args = append(args, map[string]string{"channel": "tickers", "instId": symbol})
	}
	if err := wsjson.Write(ctx, conn, map[string]any{"op": "subscribe", "args": args}); err != nil {
		return fmt.Errorf("OKX 订阅失败")
	}
	if !sendCryptoEvent(ctx, events, CryptoStreamEvent{Type: "status", Exchange: "okx", State: "connected"}) {
		return ctx.Err()
	}

	stopPing := make(chan struct{})
	defer close(stopPing)
	go pingOKX(ctx, conn, stopPing)
	for {
		_, payload, err := conn.Read(ctx)
		if err != nil {
			return fmt.Errorf("OKX 实时流中断")
		}
		if string(payload) == "pong" {
			continue
		}
		tickers, streamErr := parseOKXTickers(payload)
		if streamErr != nil {
			return streamErr
		}
		for i := range tickers {
			if !sendCryptoEvent(ctx, events, CryptoStreamEvent{Type: "ticker", Exchange: "okx", Ticker: &tickers[i]}) {
				return ctx.Err()
			}
		}
	}
}

func pingOKX(ctx context.Context, conn *websocket.Conn, stopped <-chan struct{}) {
	ticker := time.NewTicker(20 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-stopped:
			return
		case <-ticker.C:
			pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
			_ = conn.Write(pingCtx, websocket.MessageText, []byte("ping"))
			cancel()
		}
	}
}

func (s *CryptoService) dial(ctx context.Context, endpoint string) (*websocket.Conn, context.CancelFunc, error) {
	timeout := s.dialTimeout
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	dialCtx, cancel := context.WithCancel(ctx)
	timer := time.AfterFunc(timeout, cancel)
	conn, _, err := websocket.Dial(dialCtx, endpoint, nil)
	timerStopped := timer.Stop()
	if err != nil {
		cancel()
		return nil, func() {}, err
	}
	if !timerStopped {
		cancel()
		_ = conn.Close(websocket.StatusGoingAway, "handshake timeout")
		return nil, func() {}, errors.New("crypto WebSocket handshake timed out")
	}
	return conn, cancel, nil
}

func parseBinanceTicker(payload []byte, symbols map[string]string) (CryptoTicker, bool) {
	var envelope binanceTickerEnvelope
	if err := json.Unmarshal(payload, &envelope); err != nil {
		return CryptoTicker{}, false
	}
	item := envelope.Data
	symbol, ok := symbols[strings.ToUpper(strings.TrimSpace(item.Symbol))]
	if !ok || decimalPointer(item.Price) == nil {
		return CryptoTicker{}, false
	}
	return CryptoTicker{
		Symbol: symbol, Exchange: "binance", Price: decimalPointer(item.Price),
		Change: decimalPointer(item.Change), ChangePercent: decimalPointer(item.ChangePercent),
		High: decimalPointer(item.High), Low: decimalPointer(item.Low), Volume: decimalPointer(item.Volume),
		Turnover: decimalPointer(item.Turnover), Bid: decimalPointer(item.Bid), Ask: decimalPointer(item.Ask),
		UpdatedAt: unixMilliString(item.EventTime),
	}, true
}

func parseOKXTickers(payload []byte) ([]CryptoTicker, error) {
	var envelope okxTickerEnvelope
	if err := json.Unmarshal(payload, &envelope); err != nil {
		return nil, nil
	}
	if envelope.Event == "error" || (envelope.Code != "" && envelope.Code != "0") {
		return nil, fmt.Errorf("OKX 返回订阅错误")
	}
	result := make([]CryptoTicker, 0, len(envelope.Data))
	for _, item := range envelope.Data {
		price := decimalPointer(item.Price)
		if !cryptoSymbolPattern.MatchString(item.InstID) || price == nil {
			continue
		}
		open := decimalPointer(item.Open24h)
		change, percent := priceChange(price, open)
		milliseconds, _ := strconv.ParseInt(item.Time, 10, 64)
		result = append(result, CryptoTicker{
			Symbol: item.InstID, Exchange: "okx", Price: price, Change: change, ChangePercent: percent,
			High: decimalPointer(item.High24h), Low: decimalPointer(item.Low24h),
			Volume: decimalPointer(item.Volume), Turnover: decimalPointer(item.Turnover),
			Bid: decimalPointer(item.Bid), Ask: decimalPointer(item.Ask), UpdatedAt: unixMilliString(milliseconds),
		})
	}
	return result, nil
}

func priceChange(price, open *float64) (*float64, *float64) {
	if price == nil || open == nil || *open == 0 {
		return nil, nil
	}
	change := *price - *open
	percent := change / *open * 100
	return &change, &percent
}

func decimalPointer(value string) *float64 {
	number, err := strconv.ParseFloat(strings.TrimSpace(value), 64)
	if err != nil || math.IsNaN(number) || math.IsInf(number, 0) {
		return nil
	}
	return &number
}

func unixMilliString(milliseconds int64) string {
	if milliseconds <= 0 {
		return time.Now().UTC().Format(time.RFC3339Nano)
	}
	return time.UnixMilli(milliseconds).UTC().Format(time.RFC3339Nano)
}

func normalizeCryptoSymbols(symbols []string) ([]string, error) {
	if len(symbols) == 0 || len(symbols) > maxCryptoSymbols {
		return nil, fmt.Errorf("%w: 需要 1-%d 个交易对", errInvalidCryptoRequest, maxCryptoSymbols)
	}
	result := make([]string, 0, len(symbols))
	seen := make(map[string]bool)
	for _, symbol := range symbols {
		symbol = strings.ToUpper(strings.TrimSpace(symbol))
		if !cryptoSymbolPattern.MatchString(symbol) {
			return nil, fmt.Errorf("%w: 无效交易对 %q", errInvalidCryptoRequest, symbol)
		}
		if !seen[symbol] {
			seen[symbol] = true
			result = append(result, symbol)
		}
	}
	return result, nil
}

func sendCryptoEvent(ctx context.Context, events chan<- CryptoStreamEvent, event CryptoStreamEvent) bool {
	select {
	case <-ctx.Done():
		return false
	case events <- event:
		return true
	}
}
