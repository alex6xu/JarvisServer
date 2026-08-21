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
