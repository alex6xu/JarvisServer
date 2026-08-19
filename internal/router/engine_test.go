package router

import (
	"context"
	"errors"
	"testing"
)

func TestPlanFiltersCapabilitiesAndUsesPolicy(t *testing.T) {
	engine, err := New(nil)
	if err != nil {
		t.Fatal(err)
	}
	endpoints := []Endpoint{
		{ID: "cheap", Enabled: true, Models: []string{"m"}, Priority: 1, Weight: 1, CostPerMTok: 1, Capabilities: Capabilities{Tools: false}},
		{ID: "tools", Enabled: true, Models: []string{"m"}, Priority: 1, Weight: 1, CostPerMTok: 5, Capabilities: Capabilities{Tools: true}},
	}
	plan, err := engine.Plan(context.Background(), RouteRequest{RequestedModel: "m", Required: Capabilities{Tools: true}, Policy: DefaultPolicy()}, endpoints)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Primary.Endpoint.ID != "tools" {
		t.Fatalf("primary=%s", plan.Primary.Endpoint.ID)
	}
}

func TestObserveOpensCircuit(t *testing.T) {
	engine, _ := New(nil)
	for range failureThreshold {
		if err := engine.Observe(context.Background(), AttemptResult{EndpointID: "one", ErrorText: "down"}); err != nil {
			t.Fatal(err)
		}
	}
	if !engine.Health("one").CircuitOpenUntil.After(engine.now()) {
		t.Fatal("circuit not opened")
	}
	if err := engine.Observe(context.Background(), AttemptResult{EndpointID: "one", Success: true}); err != nil {
		t.Fatal(err)
	}
	if engine.Health("one").ConsecutiveFailures != 0 {
		t.Fatal(errors.New("health not reset"))
	}
}
