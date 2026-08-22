package gateway

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestStockQuotes(t *testing.T) {
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("secids"); got != "1.600519,116.00700" {
			t.Fatalf("secids=%q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"rc":0,"data":{"diff":[
			{"f2":1500.25,"f3":1.5,"f4":22.17,"f5":123456,"f6":987654321.5,"f12":"600519","f13":1,"f14":"贵州茅台","f15":1510,"f16":1478,"f17":1480,"f18":1478.08,"f124":1787366400},
			{"f2":"-","f3":null,"f4":"-","f5":0,"f6":0,"f12":"00700","f13":116,"f14":"腾讯控股","f15":"-","f16":"-","f17":"-","f18":600,"f124":"1787366401"}
		]}}`))
	}))
	t.Cleanup(provider.Close)

	stocks := NewStockService()
	stocks.quoteURL = provider.URL
	quotes, err := stocks.Quotes(context.Background(), []string{" 1.600519 ", "116.00700"})
	if err != nil {
		t.Fatal(err)
	}
	if len(quotes) != 2 {
		t.Fatalf("quotes=%+v", quotes)
	}
	if quotes[0].Symbol != "1.600519" || quotes[0].Market != "沪市" || quotes[0].Price == nil || *quotes[0].Price != 1500.25 {
		t.Fatalf("first quote=%+v", quotes[0])
	}
	if quotes[0].UpdatedAt == "" {
		t.Fatal("expected provider update time")
	}
	if quotes[1].Price != nil || quotes[1].ChangePercent != nil || quotes[1].PreviousClose == nil {
		t.Fatalf("second quote=%+v", quotes[1])
	}
}

func TestStockSearch(t *testing.T) {
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("input"); got != "苹果" {
			t.Fatalf("input=%q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"QuotationCodeTable":{"Data":[
			{"Code":"AAPL","Name":"苹果","QuoteID":"105.AAPL","SecurityTypeName":"美股","Classify":"UsStock"},
			{"Code":"bad","Name":"坏数据","QuoteID":"https://invalid","SecurityTypeName":"","Classify":""}
		],"Status":0,"Message":"成功"}}`))
	}))
	t.Cleanup(provider.Close)

	stocks := NewStockService()
	stocks.searchURL = provider.URL
	results, err := stocks.Search(context.Background(), "苹果")
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].Symbol != "105.AAPL" || results[0].Market != "美股" {
		t.Fatalf("results=%+v", results)
	}
}

func TestNormalizeStockSymbols(t *testing.T) {
	symbols, err := normalizeStockSymbols([]string{"1.600519", "1.600519", "105.aapl"})
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(symbols, ","); got != "1.600519,105.AAPL" {
		t.Fatalf("symbols=%q", got)
	}
	for _, invalid := range []string{"", "600519", "1.600519,0.000001", "https://example.com"} {
		if _, err := normalizeStockSymbols([]string{invalid}); err == nil {
			t.Fatalf("expected %q to be rejected", invalid)
		}
	}
}

func TestStockHandlersRequireAccountAndValidateInput(t *testing.T) {
	svc := &Service{Stocks: NewStockService()}

	unauthorized := httptest.NewRecorder()
	svc.handleStockQuotes(unauthorized, httptest.NewRequest(http.MethodGet, "/v1/stocks/quotes?symbols=1.000001", nil))
	if unauthorized.Code != http.StatusUnauthorized {
		t.Fatalf("unauthorized status=%d", unauthorized.Code)
	}

	req := httptest.NewRequest(http.MethodGet, "/v1/stocks/quotes?symbols=not-valid", nil)
	req = req.WithContext(context.WithValue(req.Context(), accountContextKey{}, Account{ID: 1}))
	invalid := httptest.NewRecorder()
	svc.handleStockQuotes(invalid, req)
	if invalid.Code != http.StatusBadRequest {
		t.Fatalf("invalid status=%d body=%s", invalid.Code, invalid.Body.String())
	}
}
