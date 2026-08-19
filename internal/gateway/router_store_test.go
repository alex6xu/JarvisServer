package gateway

import (
	"context"
	"strings"
	"testing"
	"time"

	corerouter "github.com/alex6xu/jarvisserver/internal/router"
)

func TestReplaceProvidersSynchronizesEndpointsAndModels(t *testing.T) {
	store := newTestGatewayStore(t)
	providers := []Provider{{ID: 7, Name: "custom", Type: 3, Key: "secret", BaseURL: "https://example.com/v1", Models: "m1,m2", Status: 1, Priority: 4, Weight: 2}}
	if err := store.ReplaceProviders(context.Background(), providers); err != nil {
		t.Fatal(err)
	}
	var endpointCount, modelCount int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM provider_endpoints WHERE provider_id = 7`).Scan(&endpointCount); err != nil {
		t.Fatal(err)
	}
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM provider_models WHERE endpoint_id = 'provider_7'`).Scan(&modelCount); err != nil {
		t.Fatal(err)
	}
	if endpointCount != 1 || modelCount != 2 {
		t.Fatalf("endpoints=%d models=%d", endpointCount, modelCount)
	}
}

func TestRoutePolicyPublishesImmutableRevisions(t *testing.T) {
	store := newTestGatewayStore(t)
	ctx := context.Background()
	policy := corerouter.DefaultPolicy()
	first, err := store.PublishRoutePolicy(ctx, policy)
	if err != nil {
		t.Fatal(err)
	}
	policy.Mode = "cost-first"
	second, err := store.PublishRoutePolicy(ctx, policy)
	if err != nil {
		t.Fatal(err)
	}
	if first.Revision != 1 || second.Revision != 2 {
		t.Fatalf("revisions=%d/%d", first.Revision, second.Revision)
	}
	var versions int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM route_policy_versions WHERE policy_id = ?`, policy.ID).Scan(&versions); err != nil {
		t.Fatal(err)
	}
	if versions != 2 {
		t.Fatalf("versions=%d", versions)
	}
}

func TestRouterHealthPersistsAcrossEngineRestart(t *testing.T) {
	store := newTestGatewayStore(t)
	engine, err := corerouter.New(store)
	if err != nil {
		t.Fatal(err)
	}
	if err := engine.Observe(context.Background(), corerouter.AttemptResult{
		EndpointID: "provider_8", Success: false, ErrorCategory: "timeout", ErrorText: "timeout",
		Latency: 250 * time.Millisecond, OccurredAt: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}
	reloaded, err := corerouter.New(store)
	if err != nil {
		t.Fatal(err)
	}
	health := reloaded.Health("provider_8")
	if health.ConsecutiveFailures != 1 || health.LastError != "timeout" || health.LatencyP95Ms != 250 {
		t.Fatalf("health=%+v", health)
	}
}

func TestValidateProviderURLBlocksPrivateNetworks(t *testing.T) {
	err := validateProviderURL(context.Background(), "http://127.0.0.1:8080/v1/models", false)
	if err == nil || !strings.Contains(err.Error(), "private or local") {
		t.Fatalf("private URL err=%v", err)
	}
	if err := validateProviderURL(context.Background(), "http://127.0.0.1:8080/v1/models", true); err != nil {
		t.Fatalf("allowed private URL: %v", err)
	}
}
