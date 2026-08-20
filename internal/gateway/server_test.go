package gateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/zeromicro/go-zero/rest/pathvar"
)

func TestStubEndpoints(t *testing.T) {
	svc, err := NewService(Options{Model: "test-model", Approve: true, NoTools: true, Cwd: t.TempDir(), AdminPassword: "test-password"})
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
		if !ok || len(data) == 0 || data[0].(map[string]any)["id"] != "auto" {
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
		req = pathvar.WithVars(req, map[string]string{"sessionId": "missing"})
		rr := httptest.NewRecorder()
		svc.handleGetSession(rr, req)
		if rr.Code != http.StatusNotFound {
			t.Fatalf("status %d body %s", rr.Code, rr.Body.String())
		}
	})
}

func TestConfigToOptions(t *testing.T) {
	cfg := Config{}
	cfg.Host = "0.0.0.0"
	cfg.Port = 8080
	cfg.Agent.Model = "m1"
	cfg.Agent.Approve = true
	cfg.Agent.AllowRegistration = true
	opts := cfg.ToOptions()
	if opts.Model != "m1" || opts.Addr != ":8080" || !opts.AllowRegistration {
		t.Fatalf("opts=%+v", opts)
	}
}

func TestNewServerRegisters(t *testing.T) {
	svc, err := NewService(Options{Model: "test-model", Approve: true, NoTools: true, Cwd: t.TempDir(), AdminPassword: "test-password"})
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
	cfg.Agent.Model = "test-model"
	// Port 0 may panic in MustNewServer depending on version; use a high port unused for construction only.
	cfg.Port = 18080
	srv := NewServer(svc, cfg)
	if srv.Rest == nil {
		t.Fatal("nil rest server")
	}
	srv.Stop()
}
