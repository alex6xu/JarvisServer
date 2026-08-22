package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/alex6xu/jarvisserver/internal/agentcore"
)

func TestStockDigestUsesWatchlistAndDegradesWithoutNewsProvider(t *testing.T) {
	store, err := OpenGatewayStore(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	account, err := store.CreateAccount(context.Background(), "digest-user", "", "user", "password-123")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.UpsertWatchlist(context.Background(), account.ID, []WatchlistItem{{
		Symbol: "105.AAPL", Code: "AAPL", Name: "Apple", Market: "NASDAQ", AssetType: "stock",
	}}); err != nil {
		t.Fatal(err)
	}
	quoteServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"rc":0,"data":{"diff":[{"f2":230.5,"f3":1.2,"f4":2.7,"f5":10,"f6":100,"f12":"AAPL","f13":105,"f14":"Apple","f15":232,"f16":225,"f17":226,"f18":227.8,"f124":1787366400}]}}`))
	}))
	t.Cleanup(quoteServer.Close)
	stocks := NewStockService()
	stocks.quoteURL = quoteServer.URL
	news := NewStockNewsSentimentService(Options{StockNewsCacheTTL: time.Minute, StockNewsMaxResults: 10}, store)
	service := NewStockDigestService(stocks, nil, news, nil, nil, store)
	result, err := service.Latest(context.Background(), account.ID, "call-1", StockDigestRequest{IncludeSentiment: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Items) != 1 || result.Items[0].Quote == nil || result.Items[0].Quote.Price == nil {
		t.Fatalf("result=%+v", result)
	}
	if result.Status != "partial" || result.Items[0].News == nil || result.Items[0].News.Status != "disabled" {
		t.Fatalf("expected explicit news degradation: %+v", result)
	}
}

func TestStockDigestNeedsClarificationForAmbiguousName(t *testing.T) {
	searchServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"QuotationCodeTable":{"Data":[
{"Code":"AAA","Name":"Alpha One","QuoteID":"105.AAA","SecurityTypeName":"美股","Classify":"UsStock"},
{"Code":"AAB","Name":"Alpha Two","QuoteID":"105.AAB","SecurityTypeName":"美股","Classify":"UsStock"}
],"Status":0}}`))
	}))
	t.Cleanup(searchServer.Close)
	stocks := NewStockService()
	stocks.searchURL = searchServer.URL
	service := NewStockDigestService(stocks, nil, nil, nil, nil, nil)
	result, err := service.Latest(context.Background(), 1, "call", StockDigestRequest{Symbols: []string{"Alpha"}})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "needs_clarification" || len(result.Candidates) != 1 || len(result.Candidates[0].Matches) != 2 {
		t.Fatalf("result=%+v", result)
	}
}

func TestStockDigestToolReturnsStructuredResult(t *testing.T) {
	service := NewStockDigestService(nil, nil, nil, nil, nil, nil)
	tool := &StockDigestTool{Service: service, AccountID: 1, SessionID: "session"}
	result, err := tool.Execute(context.Background(), "tool-call", json.RawMessage(`{"symbols":[]}`), nil)
	if err != nil {
		t.Fatal(err)
	}
	text, ok := result.Content[0].(agentcore.TextContent)
	if !ok || !strings.Contains(text.Text, `"status":"needs_input"`) {
		t.Fatalf("result=%+v", result)
	}
}
