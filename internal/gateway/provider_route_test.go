package gateway

import "testing"

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
	svc, err := NewService(Options{Model: "fallback-model", Approve: true, NoTools: true, Cwd: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
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
