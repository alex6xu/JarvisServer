package gateway

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

type fakeStockNewsProvider struct {
	name   string
	search func(context.Context, string, int, int) ([]StockNewsItem, error)
}

func (p fakeStockNewsProvider) Name() string { return p.name }
func (p fakeStockNewsProvider) Search(ctx context.Context, query string, days, limit int) ([]StockNewsItem, error) {
	return p.search(ctx, query, days, limit)
}

func TestParseStockNewsProviderResponses(t *testing.T) {
	tests := []struct {
		provider string
		payload  string
		want     string
	}{
		{"anspire", `{"code":200,"results":[{"title":"回购公告","content":"公司计划回购","url":"https://news.example/a?utm_source=x","date":"2026-08-22"}]}`, "回购公告"},
		{"tavily", `{"results":[{"title":"Earnings beat","content":"Profit rose","url":"https://news.example/b","published_date":"2026-08-22"}]}`, "Earnings beat"},
		{"bocha", `{"code":200,"data":{"webPages":{"value":[{"name":"处罚公告","summary":"收到处罚","url":"https://news.example/c","siteName":"Example","datePublished":"2026-08-22"}]}}}`, "处罚公告"},
		{"brave", `{"web":{"results":[{"title":"Market update","description":"Latest filing","url":"https://news.example/d","age":"2 hours ago"}]}}`, "Market update"},
	}
	for _, test := range tests {
		t.Run(test.provider, func(t *testing.T) {
			var payload map[string]any
			if err := json.Unmarshal([]byte(test.payload), &payload); err != nil {
				t.Fatal(err)
			}
			items, err := parseStockNewsProviderResponse(test.provider, payload, 5)
			if err != nil {
				t.Fatal(err)
			}
			if len(items) != 1 || items[0].Title != test.want || items[0].Provider != test.provider {
				t.Fatalf("items=%+v", items)
			}
			if strings.Contains(items[0].URL, "utm_source") {
				t.Fatalf("tracking query was not removed: %s", items[0].URL)
			}
		})
	}
}

func TestAnspireStockNewsRequestAndKeyFallback(t *testing.T) {
	var authorizations []string
	provider := &configuredStockNewsProvider{
		kind: "anspire", apiKeys: []string{"bad-key", "good-key"}, apiURL: stockNewsProviderDefaults["anspire"],
		httpClient: &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			authorizations = append(authorizations, req.Header.Get("Authorization"))
			if req.URL.Query().Get("query") != "贵州茅台 600519 最新消息" || req.URL.Query().Get("top_k") != "7" || req.URL.Query().Get("FromTime") == "" {
				t.Fatalf("unexpected Anspire query: %s", req.URL.RawQuery)
			}
			if req.Header.Get("Authorization") == "Bearer bad-key" {
				return &http.Response{StatusCode: http.StatusUnauthorized, Body: io.NopCloser(strings.NewReader(`{}`)), Header: make(http.Header)}, nil
			}
			return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"code":200,"results":[{"title":"最新公告","content":"摘要","url":"https://news.example/a"}]}`)), Header: make(http.Header)}, nil
		})},
	}
	items, err := provider.Search(context.Background(), "贵州茅台 600519 最新消息", 3, 7)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || len(authorizations) != 2 || authorizations[1] != "Bearer good-key" {
		t.Fatalf("items=%+v auth=%v", items, authorizations)
	}
}

func TestStockNewsSentimentLatestConcurrentDedupAndCache(t *testing.T) {
	var mu sync.Mutex
	active, maxActive, calls := 0, 0, 0
	provider := func(name string, items []StockNewsItem) fakeStockNewsProvider {
		return fakeStockNewsProvider{name: name, search: func(_ context.Context, query string, days, limit int) ([]StockNewsItem, error) {
			if !strings.Contains(query, "贵州茅台 600519") || days != 3 || limit != 10 {
				t.Fatalf("query=%q days=%d limit=%d", query, days, limit)
			}
			mu.Lock()
			active++
			calls++
			if active > maxActive {
				maxActive = active
			}
			mu.Unlock()
			time.Sleep(15 * time.Millisecond)
			mu.Lock()
			active--
			mu.Unlock()
			return items, nil
		}}
	}
	service := &StockNewsSentimentService{
		providers: []stockNewsProvider{
			provider("anspire", []StockNewsItem{{Provider: "anspire", Title: "公司回购股份", Snippet: "回购计划获批", URL: "https://news.example/a", PublishedAt: "2026-08-22"}}),
			provider("tavily", []StockNewsItem{{Provider: "tavily", Title: "公司回购股份", Snippet: "更完整的回购计划摘要", URL: "https://news.example/a", PublishedAt: "2026-08-22"}, {Provider: "tavily", Title: "监管处罚", Snippet: "收到处罚决定", URL: "https://news.example/b", PublishedAt: "2026-08-21"}}),
		},
		cacheTTL: time.Hour, maxResults: 10, memory: make(map[string]StockNewsSentimentResult),
	}
	result, err := service.Latest(context.Background(), "600519", "贵州茅台", 3, 10)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "ok" || len(result.Items) != 2 || result.SentimentScore == nil || *result.SentimentScore != 50 {
		t.Fatalf("result=%+v", result)
	}
	if len(result.Items[0].Providers) != 2 || maxActive < 2 || calls != 2 {
		t.Fatalf("dedup/providers=%+v maxActive=%d calls=%d", result.Items, maxActive, calls)
	}
	if !strings.Contains(result.AnalysisContext, "BEGIN_UNTRUSTED_STOCK_NEWS") {
		t.Fatalf("unsafe analysis context: %s", result.AnalysisContext)
	}
	cached, err := service.Latest(context.Background(), "600519", "贵州茅台", 3, 10)
	if err != nil || !cached.Cached || calls != 2 {
		t.Fatalf("cached=%+v err=%v calls=%d", cached, err, calls)
	}
}

func TestStockNewsSentimentPersistence(t *testing.T) {
	store, err := OpenGatewayStore(filepath.Join(t.TempDir(), "gateway.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	now := time.Now().UTC().Truncate(time.Microsecond)
	result := StockNewsSentimentResult{
		Enabled: true, Status: "ok", Symbol: "105.AAPL", Name: "Apple", Query: "Apple AAPL latest news",
		SentimentScore: floatPtr(75), SentimentLabel: "偏正面", SentimentMethod: "keyword_v1",
		Items:       []StockNewsItem{{Provider: "anspire", Providers: []string{"anspire"}, Title: "Buyback", URL: "https://news.example/a", Tone: "positive", ToneScore: 1}},
		Diagnostics: []StockNewsProviderDiagnostic{{Provider: "anspire", Status: "ok", ItemCount: 1}},
		FetchedAt:   now, ExpiresAt: now.Add(time.Hour), AnalysisContext: "BEGIN_UNTRUSTED_STOCK_NEWS\n{}\nEND_UNTRUSTED_STOCK_NEWS", cacheKey: "cache-key",
	}
	if err := store.SaveStockNewsSentiment(context.Background(), result); err != nil {
		t.Fatal(err)
	}
	loaded, err := store.StockNewsSentimentByKey(context.Background(), "cache-key")
	if err != nil {
		t.Fatal(err)
	}
	if loaded == nil || loaded.SentimentScore == nil || *loaded.SentimentScore != 75 || len(loaded.Items) != 1 {
		t.Fatalf("loaded=%+v", loaded)
	}
}

func TestHandleStockNewsSentiment(t *testing.T) {
	service := &StockNewsSentimentService{
		providers: []stockNewsProvider{fakeStockNewsProvider{name: "anspire", search: func(context.Context, string, int, int) ([]StockNewsItem, error) {
			return []StockNewsItem{{Provider: "anspire", Title: "最新公告", URL: "https://news.example/a"}}, nil
		}}},
		cacheTTL: time.Hour, maxResults: 10, memory: make(map[string]StockNewsSentimentResult),
	}
	svc := &Service{NewsSentiment: service}
	req := httptest.NewRequest(http.MethodGet, "/v1/stocks/news-sentiment?symbol=1.600519&name=%E8%B4%B5%E5%B7%9E%E8%8C%85%E5%8F%B0", nil)
	req = req.WithContext(context.WithValue(req.Context(), accountContextKey{}, Account{ID: 1}))
	response := httptest.NewRecorder()
	svc.handleStockNewsSentiment(response, req)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"provider":"anspire"`) {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestStockNewsProviderEnvironmentFallback(t *testing.T) {
	for _, name := range []string{"ANSPIRE_API_KEYS", "ANSPIRE_API_KEY", "TAVILY_API_KEYS", "TAVILY_API_KEY", "BOCHA_API_KEYS", "BOCHA_API_KEY", "BRAVE_API_KEYS", "BRAVE_API_KEY"} {
		t.Setenv(name, "")
	}
	t.Setenv("ANSPIRE_API_KEYS", " key-one, key-two ")
	options := withDefaultStockNewsProviders(nil)
	if len(options) != 4 || len(options[0].APIKeys) != 2 || options[0].APIURL != stockNewsProviderDefaults["anspire"] {
		t.Fatalf("options=%+v", options)
	}
}
