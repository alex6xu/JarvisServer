package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/zeromicro/go-zero/core/conf"
	"github.com/zeromicro/go-zero/rest/pathvar"
)

func TestStubEndpoints(t *testing.T) {
	svc, err := NewService(Options{Approve: true, NoTools: true, Cwd: t.TempDir(), AdminPassword: "test-password"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = svc.Close() })

	t.Run("healthz", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
		rr := httptest.NewRecorder()
		svc.handleHealthz(rr, req)
		if rr.Code != http.StatusOK {
			t.Fatalf("status %d", rr.Code)
		}
	})

	t.Run("models", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
		rr := httptest.NewRecorder()
		svc.handleModels(rr, req)
		if rr.Code != http.StatusOK {
			t.Fatalf("status %d body %s", rr.Code, rr.Body.String())
		}
		var body map[string]any
		if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		if body["default"] != "auto" {
			t.Fatalf("default=%v", body["default"])
		}
		data, ok := body["data"].([]any)
		if !ok || len(data) != 1 || data[0].(map[string]any)["id"] != "auto" {
			t.Fatalf("models=%#v", body["data"])
		}
	})

	t.Run("login", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/v1/auth/login", strings.NewReader(`{"username":"dev","password":"test-password"}`))
		rr := httptest.NewRecorder()
		svc.handleAuthLogin(rr, req)
		if rr.Code != http.StatusOK {
			t.Fatalf("status %d body %s", rr.Code, rr.Body.String())
		}
		var body map[string]any
		if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		if body["token"] == nil || body["account"] == nil {
			t.Fatalf("missing token/account: %#v", body)
		}
	})

	t.Run("auth config and registration disabled by default", func(t *testing.T) {
		configReq := httptest.NewRequest(http.MethodGet, "/v1/auth/config", nil)
		configRes := httptest.NewRecorder()
		svc.handleAuthConfig(configRes, configReq)
		if configRes.Code != http.StatusOK || !strings.Contains(configRes.Body.String(), `"registration_enabled":false`) {
			t.Fatalf("config status %d body %s", configRes.Code, configRes.Body.String())
		}

		registerReq := httptest.NewRequest(http.MethodPost, "/v1/auth/register",
			strings.NewReader(`{"username":"public-user","password":"public-password"}`))
		registerRes := httptest.NewRecorder()
		svc.handleAuthRegister(registerRes, registerReq)
		if registerRes.Code != http.StatusForbidden {
			t.Fatalf("register status %d body %s", registerRes.Code, registerRes.Body.String())
		}
	})

	t.Run("registration can be enabled explicitly", func(t *testing.T) {
		svc.Opts.AllowRegistration = true
		t.Cleanup(func() { svc.Opts.AllowRegistration = false })
		req := httptest.NewRequest(http.MethodPost, "/v1/auth/register",
			strings.NewReader(`{"username":"public-user","password":"public-password"}`))
		res := httptest.NewRecorder()
		svc.handleAuthRegister(res, req)
		if res.Code != http.StatusOK {
			t.Fatalf("register status %d body %s", res.Code, res.Body.String())
		}
	})

	t.Run("session pathvar", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/v1/agent/sessions/missing", nil)
		req = req.WithContext(context.WithValue(req.Context(), accountContextKey{}, Account{ID: legacyWorkspaceAccountID}))
		req = pathvar.WithVars(req, map[string]string{"sessionId": "missing"})
		rr := httptest.NewRecorder()
		svc.handleGetSession(rr, req)
		if rr.Code != http.StatusNotFound {
			t.Fatalf("status %d body %s", rr.Code, rr.Body.String())
		}
	})
}

func TestConfigToOptions(t *testing.T) {
	cfg := Config{
		GitHub: GitHubConf{ClientID: "github-client", ClientSecret: "github-secret", GitTimeoutSecs: 42},
		MarketData: MarketDataConf{
			BinanceWSURL:                  "wss://binance.test/stream",
			OKXWSURL:                      "wss://okx.test/public",
			BinanceRESTURL:                "https://binance.test",
			OKXRESTURL:                    "https://okx.test",
			StockSentimentAPIKey:          "sentiment-key",
			StockSentimentAPIURL:          "https://sentiment.test",
			StockSentimentCacheTTLSeconds: 900,
			NewsSentiment: StockNewsSentimentConf{
				CacheTTLSeconds: 600,
				MaxResults:      8,
				Anspire:         StockNewsProviderConf{APIKeys: "key-one,key-two", APIURL: "https://anspire.test/search"},
			},
		},
	}
	cfg.Host = "0.0.0.0"
	cfg.Port = 8080
	cfg.Agent.Approve = true
	cfg.Agent.AllowRegistration = true
	opts := cfg.ToOptions()
	if opts.Addr != ":8080" || !opts.AllowRegistration || opts.GitHubClientID != "github-client" ||
		opts.GitHubClientSecret != "github-secret" || opts.GitHubGitTimeout != 42*time.Second ||
		opts.BinanceMarketWSURL != "wss://binance.test/stream" || opts.OKXMarketWSURL != "wss://okx.test/public" ||
		opts.BinanceMarketRESTURL != "https://binance.test" || opts.OKXMarketRESTURL != "https://okx.test" ||
		opts.StockSentimentAPIKey != "sentiment-key" || opts.StockSentimentAPIURL != "https://sentiment.test" ||
		opts.StockSentimentCacheTTL != 15*time.Minute || opts.StockNewsCacheTTL != 10*time.Minute ||
		opts.StockNewsMaxResults != 8 || len(opts.StockNewsProviders) != 4 ||
		len(opts.StockNewsProviders[0].APIKeys) != 2 || opts.StockNewsProviders[0].APIURL != "https://anspire.test/search" {
		t.Fatalf("opts=%+v", opts)
	}
}

func TestLegacyGatewayModelConfigIsIgnored(t *testing.T) {
	var cfg Config
	err := conf.LoadFromYamlBytes([]byte(`
Name: gateway
Host: 127.0.0.1
Port: 8080
Agent:
  Model: legacy-model
`), &cfg)
	if err != nil {
		t.Fatalf("legacy Model field must not break config loading: %v", err)
	}
}

func TestGatewayConfigFilesEnableRotatingJSONLogs(t *testing.T) {
	for _, path := range []string{"../../etc/gateway.yaml", "../../deploy/gateway.prod.yaml"} {
		t.Run(path, func(t *testing.T) {
			raw, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			var cfg Config
			if err := conf.LoadFromYamlBytes(raw, &cfg); err != nil {
				t.Fatal(err)
			}
			if cfg.Log.Mode != "file" || cfg.Log.Encoding != "json" || cfg.Log.Path == "" {
				t.Fatalf("file logging is not enabled: %+v", cfg.Log)
			}
			if cfg.Log.Rotation != "size" || cfg.Log.MaxSize <= 0 || cfg.Log.MaxBackups <= 0 || cfg.Log.KeepDays <= 0 {
				t.Fatalf("log retention is incomplete: %+v", cfg.Log)
			}
			if cfg.DistributedLog.ServiceName == "" || cfg.DistributedLog.Environment == "" {
				t.Fatalf("distributed log identity is incomplete: %+v", cfg.DistributedLog)
			}
		})
	}
}

func TestNewServerRegisters(t *testing.T) {
	svc, err := NewService(Options{Approve: true, NoTools: true, Cwd: t.TempDir(), AdminPassword: "test-password"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = svc.Close() })
	cfg := Config{}
	cfg.Name = "gateway-test"
	cfg.Host = "127.0.0.1"
	cfg.Port = 0
	cfg.Timeout = 0
	cfg.Mode = "test"
	cfg.Log.Mode = "console"
	cfg.Middlewares.Timeout = false
	// Port 0 may panic in MustNewServer depending on version; use a high port unused for construction only.
	cfg.Port = 18080
	srv := NewServer(svc, cfg)
	if srv.Rest == nil {
		t.Fatal("nil rest server")
	}
	srv.Stop()
}

func TestWorkspaceUploadUsesDedicatedRouteGroup(t *testing.T) {
	svc := &Service{}
	for _, route := range apiRoutes(svc) {
		if route.Method == http.MethodPost && route.Path == "/v1/workspaces/upload" {
			t.Fatal("upload route must not inherit the default 1 MiB request limit")
		}
	}
	routes := workspaceUploadRoutes(svc)
	if len(routes) != 1 || routes[0].Method != http.MethodPost || routes[0].Path != "/v1/workspaces/upload" {
		t.Fatalf("unexpected upload routes: %+v", routes)
	}
}
