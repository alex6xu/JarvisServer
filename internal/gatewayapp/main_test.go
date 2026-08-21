package gatewayapp

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/alex6xu/jarvisserver/internal/gateway"
)

func TestGatewayHealthURL(t *testing.T) {
	cfg := gateway.Config{}
	cfg.Host = "0.0.0.0"
	cfg.Port = 8080
	if got, want := gatewayHealthURL(cfg), "http://127.0.0.1:8080/healthz"; got != want {
		t.Fatalf("health URL = %q, want %q", got, want)
	}

	cfg.Host = "::1"
	cfg.Port = 8443
	cfg.CertFile = "server.crt"
	cfg.KeyFile = "server.key"
	if got, want := gatewayHealthURL(cfg), "https://[::1]:8443/healthz"; got != want {
		t.Fatalf("TLS health URL = %q, want %q", got, want)
	}
}

func TestWaitForGatewayReady(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	stopped := make(chan struct{})
	if err := waitForGatewayReady(server.URL, time.Second, stopped); err != nil {
		t.Fatal(err)
	}
}

func TestWaitForGatewayReadyDetectsStoppedServer(t *testing.T) {
	stopped := make(chan struct{})
	close(stopped)
	if err := waitForGatewayReady("http://127.0.0.1:1/healthz", time.Second, stopped); err == nil ||
		!strings.Contains(err.Error(), "stopped") {
		t.Fatalf("readiness error = %v", err)
	}
}

func TestStartupLog(t *testing.T) {
	var output bytes.Buffer
	startupLog(&output, "ready pid=%d", 42)
	if got := output.String(); !strings.Contains(got, "gateway: ready pid=42") {
		t.Fatalf("startup log = %q", got)
	}
}

func TestRedactedGitHubOptionsDoesNotLeakSecrets(t *testing.T) {
	config := gateway.Config{GitHub: gateway.GitHubConf{
		ClientID:       "client-identifier-1234",
		ClientSecret:   "client-secret-value",
		RedirectURL:    "https://user:password@example.com/v1/github/callback?token=query-secret#fragment-secret",
		Scopes:         "repo read:user",
		APIBaseURL:     "https://api.github.com?token=api-query-secret",
		WebBaseURL:     "https://github.com/#web-fragment-secret",
		TokenKey:       "token-encryption-key",
		GitTimeoutSecs: 300,
	}}
	got := redactedGitHubOptions(config.ToOptions())
	for _, secret := range []string{
		config.GitHub.ClientID, config.GitHub.ClientSecret, config.GitHub.TokenKey, "user:password",
		"query-secret", "fragment-secret", "api-query-secret", "web-fragment-secret",
	} {
		if strings.Contains(got, secret) {
			t.Fatalf("redacted GitHub config leaked %q: %s", secret, got)
		}
	}
	for _, expected := range []string{
		`"client_id":"clie...1234"`,
		`"client_secret":"[REDACTED]"`,
		`"redirect_url":"https://example.com/v1/github/callback"`,
		`"scopes":"repo read:user"`,
		`"token_key":"[REDACTED]"`,
		`"git_timeout_secs":300`,
	} {
		if !strings.Contains(got, expected) {
			t.Fatalf("redacted GitHub config missing %q: %s", expected, got)
		}
	}
}

func TestRedactedGitHubOptionsIncludesEnvironmentFallbacks(t *testing.T) {
	t.Setenv("GITHUB_CLIENT_ID", "environment-client-1234")
	t.Setenv("GITHUB_CLIENT_SECRET", "environment-client-secret")
	t.Setenv("GITHUB_REDIRECT_URL", "https://example.com/v1/github/callback?secret=query")
	t.Setenv("GITHUB_TOKEN_KEY", "environment-token-key")

	config := gateway.Config{GitHub: gateway.GitHubConf{GitTimeoutSecs: 300}}
	got := redactedGitHubOptions(config.ToOptions())
	for _, secret := range []string{
		"environment-client-1234", "environment-client-secret", "environment-token-key", "secret=query",
	} {
		if strings.Contains(got, secret) {
			t.Fatalf("effective GitHub config leaked %q: %s", secret, got)
		}
	}
	for _, expected := range []string{
		`"client_id":"envi...1234"`,
		`"client_secret":"[REDACTED]"`,
		`"redirect_url":"https://example.com/v1/github/callback"`,
		`"scopes":"repo read:user"`,
		`"api_base_url":"https://api.github.com"`,
		`"web_base_url":"https://github.com"`,
		`"token_key":"[REDACTED]"`,
	} {
		if !strings.Contains(got, expected) {
			t.Fatalf("effective GitHub config missing %q: %s", expected, got)
		}
	}
}
