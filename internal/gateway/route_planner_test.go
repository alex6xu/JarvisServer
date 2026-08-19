package gateway

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/alex6xu/jarvisserver/internal/agentcore"
	"github.com/alex6xu/jarvisserver/internal/provider"
)

func TestProviderRouterPlansByModelDefaultPriorityAndWeight(t *testing.T) {
	router := NewProviderRouter()
	providers := []Provider{
		{ID: 1, Name: "low", Type: 1, Key: "k1", BaseURL: "https://one.test/v1", Models: "m1", Status: 1, Priority: 1, Weight: 100},
		{ID: 2, Name: "high", Type: 1, Key: "k2", BaseURL: "https://two.test/v1", Models: "m1,m2", Status: 1, Priority: 10, Weight: 1},
		{ID: 3, Name: "default", Type: 1, Key: "k3", BaseURL: "https://three.test/v1", Models: "m2", Status: 1, Priority: 0, Weight: 1, IsDefault: 1},
		{ID: 4, Name: "disabled", Type: 1, Key: "k4", BaseURL: "https://four.test/v1", Models: "m1", Status: 0, Priority: 100},
	}
	plan, err := router.Plan(providers, "m1", LLMRoute{})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Candidates) != 2 || plan.Candidates[0].ProviderID != 2 || plan.Candidates[1].ProviderID != 1 {
		t.Fatalf("m1 plan = %#v", plan.Candidates)
	}
	plan, err = router.Plan(providers, "", LLMRoute{})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Candidates) != 3 || plan.Candidates[0].ProviderID != 3 || plan.Candidates[0].Model != "m2" {
		t.Fatalf("default plan = %#v", plan.Candidates)
	}
}

func TestProviderRouterCircuitBreakerSkipsFailedProvider(t *testing.T) {
	router := NewProviderRouter()
	now := time.Date(2026, 8, 18, 0, 0, 0, 0, time.UTC)
	router.now = func() time.Time { return now }
	providers := []Provider{
		{ID: 1, Name: "primary", Type: 1, Key: "k1", BaseURL: "https://one.test/v1", Models: "m1", Status: 1, Priority: 10},
		{ID: 2, Name: "fallback", Type: 1, Key: "k2", BaseURL: "https://two.test/v1", Models: "m1", Status: 1, Priority: 1},
	}
	for range providerFailureThreshold {
		router.Observe(1, errors.New("upstream unavailable"))
	}
	plan, err := router.Plan(providers, "m1", LLMRoute{})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Candidates) != 1 || plan.Candidates[0].ProviderID != 2 {
		t.Fatalf("circuit plan = %#v", plan.Candidates)
	}
	now = now.Add(providerCircuitDuration + time.Second)
	plan, err = router.Plan(providers, "m1", LLMRoute{})
	if err != nil {
		t.Fatal(err)
	}
	if plan.Candidates[0].ProviderID != 1 {
		t.Fatalf("expired circuit should allow a half-open priority probe: %#v", plan.Candidates)
	}
	router.Observe(1, nil)
	plan, _ = router.Plan(providers, "m1", LLMRoute{})
	if plan.Candidates[0].ProviderID != 1 {
		t.Fatalf("successful provider should return to priority order: %#v", plan.Candidates)
	}
}

type scriptedProvider struct {
	name   string
	events []provider.AssistantMessageEvent
	calls  int
}

func (p *scriptedProvider) Name() string             { return p.name }
func (p *scriptedProvider) Models() []provider.Model { return nil }
func (p *scriptedProvider) StreamCompletion(ctx context.Context, _ provider.CompletionRequest) (*provider.AssistantMessageEventStream, error) {
	p.calls++
	stream := provider.NewAssistantMessageEventStream(0)
	go func() {
		defer stream.Close()
		for _, event := range p.events {
			if err := stream.Emit(ctx, event); err != nil {
				return
			}
		}
	}()
	return stream, nil
}

func assistant(text, stop string) agentcore.AssistantMessage {
	return agentcore.AssistantMessage{
		RoleField:  agentcore.RoleAssistant,
		Content:    agentcore.ContentList{agentcore.NewTextContent(text)},
		StopReason: stop,
	}
}

func TestFailoverProviderSwitchesBeforeMeaningfulOutput(t *testing.T) {
	failed := &scriptedProvider{name: "failed", events: []provider.AssistantMessageEvent{
		provider.StreamStartEvent{Partial: assistant("", "")},
		provider.StreamErrorEvent{Message: agentcore.AssistantMessage{RoleField: agentcore.RoleAssistant, StopReason: agentcore.StopReasonError, ErrorMessage: "unavailable"}, Err: errors.New("unavailable")},
	}}
	success := &scriptedProvider{name: "success", events: []provider.AssistantMessageEvent{
		provider.StreamStartEvent{Partial: assistant("", "")},
		provider.StreamTextEvent{Partial: assistant("ok", "")},
		provider.StreamDoneEvent{Message: assistant("ok", agentcore.StopReasonEndTurn)},
	}}
	router := NewProviderRouter()
	routed := &failoverProvider{router: router, candidates: []routedCandidate{
		{route: LLMRoute{ProviderID: 1, Model: "m"}, provider: failed},
		{route: LLMRoute{ProviderID: 2, Model: "m"}, provider: success},
	}}
	stream, err := routed.StreamCompletion(context.Background(), provider.CompletionRequest{Model: "m"})
	if err != nil {
		t.Fatal(err)
	}
	var final agentcore.AssistantMessage
	for event := range stream.Events() {
		if done, ok := event.(provider.StreamDoneEvent); ok {
			final = done.Message
		}
		if _, ok := event.(provider.StreamErrorEvent); ok {
			t.Fatal("pre-output error must not escape when a fallback succeeds")
		}
	}
	if agentcore.ContentToText(final.Content) != "ok" || failed.calls != 1 || success.calls != 1 {
		t.Fatalf("final=%+v calls=%d/%d", final, failed.calls, success.calls)
	}
}

func TestFailoverProviderDoesNotReplayAfterOutput(t *testing.T) {
	partialThenError := &scriptedProvider{name: "partial", events: []provider.AssistantMessageEvent{
		provider.StreamTextEvent{Partial: assistant("partial", "")},
		provider.StreamErrorEvent{Message: agentcore.AssistantMessage{RoleField: agentcore.RoleAssistant, StopReason: agentcore.StopReasonError, ErrorMessage: "stream broke"}, Err: errors.New("stream broke")},
	}}
	unused := &scriptedProvider{name: "unused", events: []provider.AssistantMessageEvent{
		provider.StreamDoneEvent{Message: assistant("duplicate", agentcore.StopReasonEndTurn)},
	}}
	routed := &failoverProvider{router: NewProviderRouter(), candidates: []routedCandidate{
		{route: LLMRoute{ProviderID: 1, Model: "m"}, provider: partialThenError},
		{route: LLMRoute{ProviderID: 2, Model: "m"}, provider: unused},
	}}
	stream, err := routed.StreamCompletion(context.Background(), provider.CompletionRequest{Model: "m"})
	if err != nil {
		t.Fatal(err)
	}
	sawError := false
	for event := range stream.Events() {
		if _, ok := event.(provider.StreamErrorEvent); ok {
			sawError = true
		}
	}
	if !sawError || unused.calls != 0 {
		t.Fatalf("sawError=%v fallback calls=%d", sawError, unused.calls)
	}
}
