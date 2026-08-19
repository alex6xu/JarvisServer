package gateway

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestParseProviderModels(t *testing.T) {
	got := parseProviderModels("gpt-4o, claude-3")
	if len(got) != 2 || got[0] != "gpt-4o" || got[1] != "claude-3" {
		t.Fatalf("got %#v", got)
	}
	got = parseProviderModels(`["a","b"]`)
	if len(got) != 2 || got[0] != "a" {
		t.Fatalf("json got %#v", got)
	}
}

func TestProviderRouterUsesWeightsWithinSamePriority(t *testing.T) {
	router := NewProviderRouter()
	providers := []Provider{
		{ID: 1, Name: "heavy", Type: 3, Key: "key", BaseURL: "https://one.example/v1", Models: "m", Status: 1, Weight: 2},
		{ID: 2, Name: "light", Type: 3, Key: "key", BaseURL: "https://two.example/v1", Models: "m", Status: 1, Weight: 1},
	}
	var first []int
	for range 3 {
		plan, err := router.Plan(providers, "m", LLMRoute{})
		if err != nil {
			t.Fatal(err)
		}
		first = append(first, plan.Candidates[0].ProviderID)
	}
	if first[0] != 1 || first[1] != 1 || first[2] != 2 {
		t.Fatalf("weighted first choices = %v, want [1 1 2]", first)
	}
}

func TestFetchProviderModelsOpenAICompatible(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" || r.Header.Get("Authorization") != "Bearer secret" {
			t.Fatalf("request path=%q authorization=%q", r.URL.Path, r.Header.Get("Authorization"))
		}
		_, _ = w.Write([]byte(`{"data":[{"id":"model-a"},{"id":"model-b"}]}`))
	}))
	defer server.Close()
	models, err := fetchProviderModels(context.Background(), Provider{Type: 3, BaseURL: server.URL + "/v1", Key: "secret"}, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(models) != 2 || models[0] != "model-a" || models[1] != "model-b" {
		t.Fatalf("models = %#v", models)
	}
}

func TestMapChannelType(t *testing.T) {
	p, n := mapChannelType(2, "")
	if p != "anthropic" || n != "" {
		t.Fatalf("claude: %q %q", p, n)
	}
	p, n = mapChannelType(1, "https://api.openai.com/v1")
	if p != "openai" {
		t.Fatalf("openai: %q %q", p, n)
	}
	p, n = mapChannelType(4, "")
	if n != "deepseek" {
		t.Fatalf("deepseek: %q %q", p, n)
	}
}

func TestResolveLLMUsesMemProvider(t *testing.T) {
	svc, err := NewService(Options{Model: "fallback-model", Approve: true, NoTools: true, Cwd: t.TempDir(), AdminPassword: "test-password"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = svc.Close() })
	svc.Mem.providers = map[int]*Provider{}
	p := svc.Mem.upsertProvider(0, Provider{
		Name: "my-openai", Type: 1, Key: "sk-test",
		BaseURL: "https://example.com/v1", Models: "my-model-1",
		Status: 1, IsDefault: 1, AuthMode: "api_key",
	})
	if p.ID <= 0 {
		t.Fatal("id")
	}
	route, err := svc.resolveLLM("")
	if err != nil {
		t.Fatal(err)
	}
	if route.Model != "my-model-1" || route.APIKey != "sk-test" || route.Protocol != "openai" {
		t.Fatalf("route=%+v", route)
	}
	if route.BaseURL != "https://example.com/v1" {
		t.Fatalf("base=%q", route.BaseURL)
	}
}

func TestProviderRoutingConfigurationRoundTripsThroughSQLite(t *testing.T) {
	store := newTestGatewayStore(t)
	want := Provider{
		ID: 7, Name: "reasoner", Type: 1, Key: "secret", BaseURL: "https://example.test/v1",
		Models: "reason-model", Status: 1, Weight: 2, Priority: 4,
		Capabilities:  ProviderCapabilities{Reasoning: true, Coding: true, Tools: true, Thinking: true},
		ContextWindow: 128_000, QualityTier: 5, CostPerMTok: 12.5,
	}
	if err := store.ReplaceProviders(context.Background(), []Provider{want}); err != nil {
		t.Fatal(err)
	}
	got, err := store.ListProviders(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ContextWindow != want.ContextWindow || got[0].QualityTier != want.QualityTier ||
		got[0].CostPerMTok != want.CostPerMTok || !got[0].Capabilities.Reasoning || got[0].Capabilities.Chat {
		t.Fatalf("provider round trip = %+v", got)
	}
	var capabilities string
	var cost float64
	if err := store.db.QueryRow(`SELECT capabilities_json, cost_per_mtok FROM provider_endpoints WHERE id='provider_7'`).Scan(&capabilities, &cost); err != nil {
		t.Fatal(err)
	}
	if cost != want.CostPerMTok || !strings.Contains(capabilities, `"reasoning":true`) {
		t.Fatalf("endpoint capabilities=%s cost=%v", capabilities, cost)
	}
}
