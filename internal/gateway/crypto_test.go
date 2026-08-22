package gateway

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
)

func TestParseCryptoTickers(t *testing.T) {
	binance, ok := parseBinanceTicker([]byte(`{"data":{"E":1787366400123,"s":"BTCUSDT","c":"65000.5","p":"1000.5","P":"1.56","h":"66000","l":"63000","v":"123.4","q":"8000000","b":"65000.4","a":"65000.6"}}`), map[string]string{"BTCUSDT": "BTC-USDT"})
	if !ok || binance.Symbol != "BTC-USDT" || binance.Price == nil || *binance.Price != 65000.5 || binance.Bid == nil {
		t.Fatalf("binance=%+v ok=%v", binance, ok)
	}

	okx, err := parseOKXTickers([]byte(`{"arg":{"channel":"tickers","instId":"ETH-USDT"},"data":[{"instId":"ETH-USDT","last":"3200","open24h":"3100","high24h":"3250","low24h":"3050","vol24h":"500","volCcy24h":"1600000","bidPx":"3199.9","askPx":"3200.1","ts":"1787366400456"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(okx) != 1 || okx[0].Change == nil || *okx[0].Change != 100 || okx[0].ChangePercent == nil {
		t.Fatalf("okx=%+v", okx)
	}
	if got := *okx[0].ChangePercent; got < 3.22 || got > 3.23 {
		t.Fatalf("change percent=%f", got)
	}
}

func TestNormalizeCryptoSymbols(t *testing.T) {
	symbols, err := normalizeCryptoSymbols([]string{" btc-usdt ", "ETH-USDT", "BTC-USDT"})
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(symbols, ","); got != "BTC-USDT,ETH-USDT" {
		t.Fatalf("symbols=%q", got)
	}
	for _, invalid := range []string{"", "BTCUSDT", "BTC-USDT/../../", "BTC-USDT,ETH-USDT"} {
		if _, err := normalizeCryptoSymbols([]string{invalid}); err == nil {
			t.Fatalf("expected %q to be rejected", invalid)
		}
	}
}

func TestCryptoStreamHandlerRequiresAccountAndValidSymbols(t *testing.T) {
	svc := &Service{Crypto: NewCryptoService(Options{})}

	unauthorized := httptest.NewRecorder()
	svc.handleCryptoStream(unauthorized, httptest.NewRequest(http.MethodGet, "/v1/crypto/stream?symbols=BTC-USDT", nil))
	if unauthorized.Code != http.StatusUnauthorized {
		t.Fatalf("unauthorized status=%d", unauthorized.Code)
	}

	req := httptest.NewRequest(http.MethodGet, "/v1/crypto/stream?symbols=invalid", nil)
	req = req.WithContext(context.WithValue(req.Context(), accountContextKey{}, Account{ID: 1}))
	invalid := httptest.NewRecorder()
	svc.handleCryptoStream(invalid, req)
	if invalid.Code != http.StatusBadRequest {
		t.Fatalf("invalid status=%d body=%s", invalid.Code, invalid.Body.String())
	}
}

func TestParseCryptoCandles(t *testing.T) {
	binance, err := parseBinanceCandles([]byte(`[
		[1700000000000,"100","110","90","105","12",1700000059999,"1250"],
		[1700000060000,"105","115","101","112","8",1700000119999,"880"]
	]`), time.UnixMilli(1700000200000))
	if err != nil {
		t.Fatal(err)
	}
	if len(binance) != 2 || binance[0].Time != 1700000000 || binance[0].Open != 100 || !binance[0].Confirmed {
		t.Fatalf("binance=%+v", binance)
	}

	okx, err := parseOKXCandles([]byte(`{"code":"0","data":[
		["1700000060000","105","115","101","112","8","840","880","0"],
		["1700000000000","100","110","90","105","12","1200","1250","1"]
	]}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(okx) != 2 || okx[0].Time != 1700000000 || okx[0].Turnover != 1250 || !okx[0].Confirmed || okx[1].Confirmed {
		t.Fatalf("okx=%+v", okx)
	}
}

func TestCryptoCandlesProviderRequests(t *testing.T) {
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v3/klines":
			if r.URL.Query().Get("symbol") != "BTCUSDT" || r.URL.Query().Get("interval") != "15m" || r.URL.Query().Get("limit") != "2" {
				t.Errorf("binance query=%s", r.URL.RawQuery)
			}
			_, _ = w.Write([]byte(`[[1700000000000,"100","110","90","105","12",1700000899999,"1250"]]`))
		case "/api/v5/market/candles":
			if r.URL.Query().Get("instId") != "ETC-USDT" || r.URL.Query().Get("bar") != "4H" || r.URL.Query().Get("limit") != "2" {
				t.Errorf("okx query=%s", r.URL.RawQuery)
			}
			_, _ = w.Write([]byte(`{"code":"0","data":[["1700000000000","20","22","19","21","50","1000","1050","1"]]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(provider.Close)

	service := &CryptoService{
		binanceRESTURL: provider.URL,
		okxRESTURL:     provider.URL,
		httpClient:     provider.Client(),
	}
	binance, err := service.Candles(context.Background(), "binance", "btc-usdt", "15m", 2)
	if err != nil || len(binance.Candles) != 1 || binance.Exchange != "binance" {
		t.Fatalf("binance=%+v err=%v", binance, err)
	}
	okx, err := service.Candles(context.Background(), "okx", "etc-usdt", "4h", 2)
	if err != nil || len(okx.Candles) != 1 || okx.Symbol != "ETC-USDT" {
		t.Fatalf("okx=%+v err=%v", okx, err)
	}
}

func TestCryptoCandlesHandlerValidation(t *testing.T) {
	svc := &Service{Crypto: NewCryptoService(Options{})}

	unauthorized := httptest.NewRecorder()
	svc.handleCryptoCandles(unauthorized, httptest.NewRequest(http.MethodGet, "/v1/crypto/candles", nil))
	if unauthorized.Code != http.StatusUnauthorized {
		t.Fatalf("unauthorized status=%d", unauthorized.Code)
	}

	req := httptest.NewRequest(http.MethodGet, "/v1/crypto/candles?exchange=binance&symbol=BTC-USDT&interval=2m", nil)
	req = req.WithContext(context.WithValue(req.Context(), accountContextKey{}, Account{ID: 1}))
	invalid := httptest.NewRecorder()
	svc.handleCryptoCandles(invalid, req)
	if invalid.Code != http.StatusBadRequest {
		t.Fatalf("invalid status=%d body=%s", invalid.Code, invalid.Body.String())
	}
}

func TestBinanceWebSocketSubscription(t *testing.T) {
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.RawQuery != "streams=btcusdt@ticker/ethusdt@ticker" {
			t.Errorf("raw query=%q", r.URL.RawQuery)
		}
		if got := r.URL.Query().Get("streams"); got != "btcusdt@ticker/ethusdt@ticker" {
			t.Errorf("streams=%q", got)
		}
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			t.Errorf("accept: %v", err)
			return
		}
		defer conn.Close(websocket.StatusNormalClosure, "done")
		_ = wsjson.Write(r.Context(), conn, map[string]any{"data": map[string]any{
			"E": int64(1787366400123), "s": "BTCUSDT", "c": "65000.5", "P": "1.2",
		}})
	}))
	t.Cleanup(provider.Close)

	service := &CryptoService{
		binanceURL:  strings.Replace(provider.URL, "http://", "ws://", 1),
		dialTimeout: 2 * time.Second,
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	events := make(chan CryptoStreamEvent, 4)
	done := make(chan error, 1)
	go func() { done <- service.streamBinance(ctx, []string{"BTC-USDT", "ETH-USDT"}, events) }()

	connected := <-events
	ticker := <-events
	if connected.State != "connected" || ticker.Ticker == nil || ticker.Ticker.Symbol != "BTC-USDT" {
		t.Fatalf("connected=%+v ticker=%+v", connected, ticker)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("binance stream did not stop")
	}
}

func TestOKXWebSocketSubscription(t *testing.T) {
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			t.Errorf("accept: %v", err)
			return
		}
		defer conn.Close(websocket.StatusNormalClosure, "done")
		var subscription struct {
			Operation string `json:"op"`
			Args      []struct {
				Channel string `json:"channel"`
				InstID  string `json:"instId"`
			} `json:"args"`
		}
		if err := wsjson.Read(r.Context(), conn, &subscription); err != nil {
			t.Errorf("read subscription: %v", err)
			return
		}
		if subscription.Operation != "subscribe" || len(subscription.Args) != 2 || subscription.Args[0].InstID != "BTC-USDT" {
			t.Errorf("subscription=%+v", subscription)
		}
		_ = wsjson.Write(r.Context(), conn, map[string]any{
			"arg":  map[string]string{"channel": "tickers", "instId": "BTC-USDT"},
			"data": []map[string]string{{"instId": "BTC-USDT", "last": "65000", "open24h": "64000", "ts": "1787366400123"}},
		})
	}))
	t.Cleanup(provider.Close)

	service := &CryptoService{
		okxURL:      strings.Replace(provider.URL, "http://", "ws://", 1),
		dialTimeout: 2 * time.Second,
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	events := make(chan CryptoStreamEvent, 4)
	done := make(chan error, 1)
	go func() { done <- service.streamOKX(ctx, []string{"BTC-USDT", "ETH-USDT"}, events) }()

	connected := <-events
	ticker := <-events
	if connected.State != "connected" || ticker.Ticker == nil || ticker.Ticker.Exchange != "okx" {
		t.Fatalf("connected=%+v ticker=%+v", connected, ticker)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("okx stream did not stop")
	}
}
