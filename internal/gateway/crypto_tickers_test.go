package gateway

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCryptoTickersAggregatesProviders(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v3/ticker/24hr":
			if !strings.Contains(r.URL.Query().Get("symbols"), "BTCUSDT") {
				t.Fatalf("symbols=%q", r.URL.Query().Get("symbols"))
			}
			_, _ = w.Write([]byte(`[{"symbol":"BTCUSDT","lastPrice":"65000","priceChange":"1000","priceChangePercent":"1.56","highPrice":"66000","lowPrice":"63000","volume":"10","quoteVolume":"650000","bidPrice":"64999","askPrice":"65001","closeTime":1787366400000}]`))
		case "/api/v5/market/tickers":
			_, _ = w.Write([]byte(`{"code":"0","data":[{"instId":"BTC-USDT","last":"65010","open24h":"64000","high24h":"66100","low24h":"63100","vol24h":"11","volCcy24h":"700000","bidPx":"65009","askPx":"65011","ts":"1787366400000"}]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	service := NewCryptoService(Options{BinanceMarketRESTURL: server.URL, OKXMarketRESTURL: server.URL})
	result, err := service.Tickers(context.Background(), []string{"btc-usdt"})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Tickers) != 2 || result.Tickers[0].Price == nil || len(result.Diagnostics) != 2 {
		t.Fatalf("result=%+v", result)
	}
}
