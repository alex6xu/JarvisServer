package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

const stockNewsResponseLimit = 4 << 20

var stockNewsProviderDefaults = map[string]string{
	"anspire": "https://plugin.anspire.cn/api/ntsearch/search",
	"tavily":  "https://api.tavily.com/search",
	"bocha":   "https://api.bocha.cn/v1/web-search",
	"brave":   "https://api.search.brave.com/res/v1/web/search",
}

type StockNewsProviderOptions struct {
	Kind    string
	APIKeys []string
	APIURL  string
}

type stockNewsProvider interface {
	Name() string
	Search(context.Context, string, int, int) ([]StockNewsItem, error)
}

type configuredStockNewsProvider struct {
	kind       string
	apiKeys    []string
	apiURL     string
	httpClient *http.Client
}

func (p *configuredStockNewsProvider) Name() string { return p.kind }

func (p *configuredStockNewsProvider) Search(ctx context.Context, query string, days, limit int) ([]StockNewsItem, error) {
	var lastErr error
	for _, key := range p.apiKeys {
		items, err := p.searchWithKey(ctx, key, query, days, limit)
		if err == nil {
			return items, nil
		}
		lastErr = err
		if ctx.Err() != nil {
			break
		}
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("%s API key is not configured", p.kind)
	}
	return nil, lastErr
}

func (p *configuredStockNewsProvider) searchWithKey(ctx context.Context, key, query string, days, limit int) ([]StockNewsItem, error) {
	var request *http.Request
	var err error
	now := time.Now()
	switch p.kind {
	case "anspire":
		parsed, parseErr := url.Parse(p.apiURL)
		if parseErr != nil {
			return nil, parseErr
		}
		values := parsed.Query()
		values.Set("query", query)
		values.Set("top_k", strconv.Itoa(limit))
		values.Set("FromTime", now.AddDate(0, 0, -days).Format("2006-01-02 15:04:05"))
		values.Set("ToTime", now.Format("2006-01-02 15:04:05"))
		values.Set("region_mode", "0")
		parsed.RawQuery = values.Encode()
		request, err = http.NewRequestWithContext(ctx, http.MethodGet, parsed.String(), nil)
		if err == nil {
			request.Header.Set("Authorization", "Bearer "+key)
		}
	case "tavily":
		request, err = newStockNewsJSONRequest(ctx, p.apiURL, map[string]any{
			"query": query, "max_results": limit, "search_depth": "advanced", "topic": "news", "days": days,
		})
		if err == nil {
			request.Header.Set("Authorization", "Bearer "+key)
		}
	case "bocha":
		freshness := "oneWeek"
		if days <= 1 {
			freshness = "oneDay"
		} else if days > 7 {
			freshness = "oneMonth"
		}
		request, err = newStockNewsJSONRequest(ctx, p.apiURL, map[string]any{
			"query": query, "freshness": freshness, "summary": true, "count": limit,
		})
		if err == nil {
			request.Header.Set("Authorization", "Bearer "+key)
		}
	case "brave":
		parsed, parseErr := url.Parse(p.apiURL)
		if parseErr != nil {
			return nil, parseErr
		}
		values := parsed.Query()
		values.Set("q", query)
		values.Set("count", strconv.Itoa(limit))
		values.Set("freshness", time.Now().AddDate(0, 0, -days).Format("2006-01-02")+"to"+time.Now().Format("2006-01-02"))
		parsed.RawQuery = values.Encode()
		request, err = http.NewRequestWithContext(ctx, http.MethodGet, parsed.String(), nil)
		if err == nil {
			request.Header.Set("X-Subscription-Token", key)
		}
	default:
		return nil, fmt.Errorf("unsupported stock news provider %q", p.kind)
	}
	if err != nil {
		return nil, err
	}
	request.Header.Set("Accept", "application/json")
	response, err := p.httpClient.Do(request)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, stockNewsResponseLimit))
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, fmt.Errorf("HTTP %d", response.StatusCode)
	}
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}
	return parseStockNewsProviderResponse(p.kind, payload, limit)
}

func newStockNewsJSONRequest(ctx context.Context, endpoint string, payload any) (*http.Request, error) {
	raw, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(raw))
	if err == nil {
		request.Header.Set("Content-Type", "application/json")
	}
	return request, err
}

func parseStockNewsProviderResponse(provider string, payload map[string]any, limit int) ([]StockNewsItem, error) {
	var entries []any
	switch provider {
	case "anspire", "tavily":
		if code, ok := numberField(payload, "code"); ok && code != 200 {
			return nil, fmt.Errorf("provider code %.0f", code)
		}
		entries, _ = payload["results"].([]any)
	case "bocha":
		if code, ok := numberField(payload, "code"); ok && code != 200 {
			return nil, fmt.Errorf("provider code %.0f", code)
		}
		data, _ := payload["data"].(map[string]any)
		pages, _ := data["webPages"].(map[string]any)
		entries, _ = pages["value"].([]any)
	case "brave":
		web, _ := payload["web"].(map[string]any)
		entries, _ = web["results"].([]any)
	}
	items := make([]StockNewsItem, 0, minInt(limit, len(entries)))
	for _, raw := range entries {
		entry, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		title := textField(entry, "title", "name")
		link := textField(entry, "url", "link")
		if title == "" || normalizeStockNewsURL(link) == "" {
			continue
		}
		snippet := textField(entry, "summary", "content", "snippet", "description")
		item := StockNewsItem{
			Provider:    provider,
			Title:       truncateExternalText(title, 240),
			Snippet:     truncateExternalText(snippet, 800),
			URL:         normalizeStockNewsURL(link),
			Source:      truncateExternalText(textField(entry, "siteName", "source"), 120),
			PublishedAt: textField(entry, "date", "published_date", "publishedAt", "datePublished", "age"),
		}
		if item.Source == "" {
			if parsed, err := url.Parse(item.URL); err == nil {
				item.Source = strings.TrimPrefix(parsed.Hostname(), "www.")
			}
		}
		items = append(items, item)
		if len(items) == limit {
			break
		}
	}
	return items, nil
}

func stockNewsProviderOptionsFromConfig(config StockNewsSentimentConf) []StockNewsProviderOptions {
	return []StockNewsProviderOptions{
		stockNewsProviderOption("anspire", config.Anspire),
		stockNewsProviderOption("tavily", config.Tavily),
		stockNewsProviderOption("bocha", config.Bocha),
		stockNewsProviderOption("brave", config.Brave),
	}
}

func stockNewsProviderOption(kind string, config StockNewsProviderConf) StockNewsProviderOptions {
	return StockNewsProviderOptions{Kind: kind, APIKeys: splitStockNewsKeys(config.APIKeys), APIURL: strings.TrimSpace(config.APIURL)}
}

func withDefaultStockNewsProviders(options []StockNewsProviderOptions) []StockNewsProviderOptions {
	byKind := make(map[string]StockNewsProviderOptions, len(options))
	for _, option := range options {
		option.Kind = strings.ToLower(strings.TrimSpace(option.Kind))
		if option.Kind != "" {
			byKind[option.Kind] = option
		}
	}
	for _, kind := range []string{"anspire", "tavily", "bocha", "brave"} {
		option := byKind[kind]
		option.Kind = kind
		if len(option.APIKeys) == 0 {
			option.APIKeys = splitStockNewsKeys(stockNewsProviderEnvKeys(kind))
		}
		if option.APIURL == "" {
			option.APIURL = stockNewsProviderDefaults[kind]
		}
		byKind[kind] = option
	}
	result := make([]StockNewsProviderOptions, 0, 4)
	for _, kind := range []string{"anspire", "tavily", "bocha", "brave"} {
		result = append(result, byKind[kind])
	}
	return result
}

func stockNewsProviderEnvKeys(kind string) string {
	var names []string
	switch kind {
	case "anspire":
		names = []string{"ANSPIRE_API_KEYS", "ANSPIRE_API_KEY"}
	case "tavily":
		names = []string{"TAVILY_API_KEYS", "TAVILY_API_KEY"}
	case "bocha":
		names = []string{"BOCHA_API_KEYS", "BOCHA_API_KEY"}
	case "brave":
		names = []string{"BRAVE_API_KEYS", "BRAVE_API_KEY"}
	}
	for _, name := range names {
		if value := strings.TrimSpace(os.Getenv(name)); value != "" {
			return value
		}
	}
	return ""
}

func splitStockNewsKeys(value string) []string {
	seen := make(map[string]bool)
	var result []string
	for _, key := range strings.Split(value, ",") {
		key = strings.TrimSpace(key)
		if key != "" && !seen[key] {
			seen[key] = true
			result = append(result, key)
		}
	}
	return result
}

func normalizeStockNewsURL(raw string) string {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Hostname() == "" {
		return ""
	}
	parsed.Fragment = ""
	query := parsed.Query()
	for key := range query {
		lower := strings.ToLower(key)
		if strings.HasPrefix(lower, "utm_") || lower == "spm" || lower == "from" {
			query.Del(key)
		}
	}
	parsed.RawQuery = query.Encode()
	return parsed.String()
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
