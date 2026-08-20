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
	name     string
	events   []provider.AssistantMessageEvent
	calls    int
	buildErr error
}

type cancelAwareProvider struct {
	started chan struct{}
}

func (p *cancelAwareProvider) Name() string             { return "cancel-aware" }
func (p *cancelAwareProvider) Models() []provider.Model { return nil }
func (p *cancelAwareProvider) StreamCompletion(ctx context.Context, _ provider.CompletionRequest) (*provider.AssistantMessageEventStream, error) {
	stream := provider.NewAssistantMessageEventStream(0)
	go func() {
		close(p.started)
		<-ctx.Done()
		stream.SetError(ctx.Err())
		stream.Close()
	}()
	return stream, nil
}

func (p *scriptedProvider) Name() string             { return p.name }
func (p *scriptedProvider) Models() []provider.Model { return nil }
func (p *scriptedProvider) StreamCompletion(ctx context.Context, _ provider.CompletionRequest) (*provider.AssistantMessageEventStream, error) {
	p.calls++
	if p.buildErr != nil {
		return nil, p.buildErr
	}
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

func TestFailoverProviderPersistsBuildFailureBeforeFallback(t *testing.T) {
	store := newTestGatewayStore(t)
	run := &RunState{ID: "run_build_failure", SessionID: "session", Model: "m", Status: runStatusRunning}
	if err := store.CreateRun(context.Background(), run); err != nil {
		t.Fatal(err)
	}
	failed := &scriptedProvider{name: "failed", buildErr: errors.New("upstream unavailable")}
	success := &scriptedProvider{name: "success", events: []provider.AssistantMessageEvent{
		provider.StreamDoneEvent{Message: assistant("ok", agentcore.StopReasonEndTurn)},
	}}
	routed := &failoverProvider{router: NewProviderRouter(), store: store, runID: run.ID, candidates: []routedCandidate{
		{route: LLMRoute{ProviderID: 1, Model: "m"}, provider: failed},
		{route: LLMRoute{ProviderID: 2, Model: "m"}, provider: success},
	}}
	stream, err := routed.StreamCompletion(context.Background(), provider.CompletionRequest{Model: "m"})
	if err != nil {
		t.Fatal(err)
	}
	for range stream.Events() {
	}
	attempts, err := store.ListRunAttempts(context.Background(), run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(attempts) != 2 || attempts[0].Status != "failed" || attempts[0].FailureStage != "before_stream" || attempts[1].Status != "done" {
		t.Fatalf("attempts = %#v", attempts)
	}
}

func TestCancelledAttemptDoesNotPenalizeProviderHealth(t *testing.T) {
	router := NewProviderRouter()
	routed := &failoverProvider{router: router}
	attempt := RunAttempt{EndpointID: "provider_1"}
	routed.finishAttempt(&attempt, time.Now(), time.Time{}, runStatusCancelled, "during_stream", context.Canceled)
	if attempt.ErrorCategory != runStatusCancelled {
		t.Fatalf("cancelled attempt category = %q", attempt.ErrorCategory)
	}
	if health := router.Health(1); health.ConsecutiveFailures != 0 {
		t.Fatalf("cancelled attempt changed provider health: %+v", health)
	}
}

func TestCancelledStreamPersistsCancelledAttempt(t *testing.T) {
	store := newTestGatewayStore(t)
	run := &RunState{ID: "run_cancelled_stream", SessionID: "session", Model: "m", Status: runStatusRunning}
	if err := store.CreateRun(context.Background(), run); err != nil {
		t.Fatal(err)
	}
	blocking := &cancelAwareProvider{started: make(chan struct{})}
	router := NewProviderRouter()
	routed := &failoverProvider{router: router, store: store, runID: run.ID, candidates: []routedCandidate{
		{route: LLMRoute{ProviderID: 1, Model: "m"}, provider: blocking},
		{route: LLMRoute{ProviderID: 2, Model: "m"}, provider: &scriptedProvider{name: "must-not-run"}},
	}}
	ctx, cancel := context.WithCancel(context.Background())
	stream, err := routed.StreamCompletion(ctx, provider.CompletionRequest{Model: "m"})
	if err != nil {
		t.Fatal(err)
	}
	<-blocking.started
	cancel()
	for range stream.Events() {
	}
	attempts, err := store.ListRunAttempts(context.Background(), run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(attempts) != 1 || attempts[0].Status != runStatusCancelled ||
		attempts[0].ErrorCategory != runStatusCancelled {
		t.Fatalf("cancelled attempts = %#v", attempts)
	}
	if health := router.Health(1); health.ConsecutiveFailures != 0 {
		t.Fatalf("cancelled stream changed provider health: %+v", health)
	}
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

func TestFailoverProviderReplansAndPersistsEveryTurn(t *testing.T) {
	store := newTestGatewayStore(t)
	run := &RunState{ID: "run_turns", SessionID: "session_turns", Model: "m", Status: runStatusRunning}
	if err := store.CreateRun(context.Background(), run); err != nil {
		t.Fatal(err)
	}
	success := &scriptedProvider{name: "success", events: []provider.AssistantMessageEvent{
		provider.StreamTextEvent{Partial: assistant("ok", "")},
		provider.StreamDoneEvent{Message: agentcore.AssistantMessage{
			RoleField:  agentcore.RoleAssistant,
			Content:    agentcore.ContentList{agentcore.NewTextContent("ok")},
			StopReason: agentcore.StopReasonEndTurn,
			Usage:      &agentcore.Usage{InputTokens: 12, OutputTokens: 4},
		}},
	}}
	plans := 0
	routed := &failoverProvider{
		router: NewProviderRouter(), store: store, runID: run.ID, sessionID: run.SessionID,
		planner: func(context.Context, RoutePurpose, provider.CompletionRequest) (RoutePlan, []routedCandidate, error) {
			plans++
			plan := RoutePlan{Reason: "test", PolicyRev: 7, Candidates: []LLMRoute{{ProviderID: 3, Model: "m"}}}
			return plan, []routedCandidate{{route: plan.Candidates[0], provider: success}}, nil
		},
	}
	req := provider.CompletionRequest{Model: "m", Context: provider.LlmContext{
		SystemPrompt: "system",
		Messages: agentcore.MessageList{agentcore.UserMessage{
			RoleField: agentcore.RoleUser,
			Content:   agentcore.ContentList{agentcore.NewTextContent("question")},
		}},
	}}
	for turn := 0; turn < 2; turn++ {
		stream, err := routed.StreamCompletion(context.Background(), req)
		if err != nil {
			t.Fatal(err)
		}
		for range stream.Events() {
		}
	}
	if plans != 2 || success.calls != 2 {
		t.Fatalf("plans/calls = %d/%d", plans, success.calls)
	}
	attempts, err := store.ListRunAttempts(context.Background(), run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(attempts) != 2 || attempts[0].Turn != 1 || attempts[1].Turn != 2 || attempts[1].PolicyRevision != 7 || attempts[1].InputTokens != 12 || attempts[1].OutputTokens != 4 {
		t.Fatalf("attempts = %#v", attempts)
	}
	checkpoint, err := store.LoadLatestRunCheckpoint(context.Background(), run.ID)
	if err != nil || checkpoint.Turn != 2 {
		t.Fatalf("checkpoint = %+v, %v", checkpoint, err)
	}
}

func TestProviderRouterPlansByWorkloadPurpose(t *testing.T) {
	router := NewProviderRouter()
	providers := []Provider{
		{ID: 1, Name: "light-chat", Type: 1, Key: "k1", BaseURL: "https://chat.test/v1", Models: "chat-light", Status: 1,
			Capabilities: ProviderCapabilities{Chat: true}, ContextWindow: 32_768, QualityTier: 1, CostPerMTok: 0.2},
		{ID: 2, Name: "strong-reasoner", Type: 1, Key: "k2", BaseURL: "https://reason.test/v1", Models: "reason-pro", Status: 1,
			Capabilities: ProviderCapabilities{Reasoning: true, Coding: true, Tools: true, Thinking: true}, ContextWindow: 128_000, QualityTier: 5, CostPerMTok: 20},
		{ID: 3, Name: "value-coder", Type: 1, Key: "k3", BaseURL: "https://code.test/v1", Models: "code-value", Status: 1,
			Capabilities: ProviderCapabilities{Chat: true, Coding: true, Tools: true}, ContextWindow: 64_000, QualityTier: 3, CostPerMTok: 1},
		{ID: 4, Name: "summary", Type: 1, Key: "k4", BaseURL: "https://summary.test/v1", Models: "summary-small", Status: 1,
			Capabilities: ProviderCapabilities{Chat: true}, ContextWindow: 64_000, QualityTier: 2, CostPerMTok: 0.1},
	}

	tests := []struct {
		purpose RoutePurpose
		wantID  int
	}{
		{RoutePurposeChat, 1},
		{RoutePurposeCodeAnalysis, 2},
		{RoutePurposeCodeExecution, 3},
		{RoutePurposeCompaction, 4},
	}
	for _, tt := range tests {
		plan, err := router.PlanForPurpose(providers, "", LLMRoute{}, tt.purpose, 0)
		if err != nil {
			t.Fatalf("%s plan: %v", tt.purpose, err)
		}
		if got := plan.Candidates[0].ProviderID; got != tt.wantID {
			t.Errorf("%s selected provider %d, want %d; plan=%+v", tt.purpose, got, tt.wantID, plan.Candidates)
		}
	}
}

func TestFailoverProviderSwitchesAfterThinkingOnlyFailure(t *testing.T) {
	failed := &scriptedProvider{name: "failed", events: []provider.AssistantMessageEvent{
		provider.StreamStartEvent{Partial: assistant("", "")},
		provider.StreamThinkingEvent{Partial: assistant("private reasoning", "")},
		provider.StreamErrorEvent{Message: agentcore.AssistantMessage{RoleField: agentcore.RoleAssistant, StopReason: agentcore.StopReasonError, ErrorMessage: "unavailable"}},
	}}
	success := &scriptedProvider{name: "success", events: []provider.AssistantMessageEvent{
		provider.StreamTextEvent{Partial: assistant("ok", "")},
		provider.StreamDoneEvent{Message: assistant("ok", agentcore.StopReasonEndTurn)},
	}}
	routed := &failoverProvider{router: NewProviderRouter(), candidates: []routedCandidate{
		{route: LLMRoute{ProviderID: 1, Model: "m"}, provider: failed},
		{route: LLMRoute{ProviderID: 2, Model: "m"}, provider: success},
	}}
	stream, err := routed.StreamCompletion(context.Background(), provider.CompletionRequest{Model: "m"})
	if err != nil {
		t.Fatal(err)
	}
	for event := range stream.Events() {
		if _, ok := event.(provider.StreamThinkingEvent); ok {
			t.Fatal("thinking from a failed candidate must not commit the route")
		}
		if _, ok := event.(provider.StreamErrorEvent); ok {
			t.Fatal("fallback success must hide the first candidate error")
		}
	}
	if failed.calls != 1 || success.calls != 1 {
		t.Fatalf("calls=%d/%d", failed.calls, success.calls)
	}
}

func TestCompactionRouteDoesNotAdvanceNormalTurn(t *testing.T) {
	store := newTestGatewayStore(t)
	runState := &RunState{ID: "run_compaction_turn", SessionID: "session", Model: "m", Status: runStatusRunning}
	if err := store.CreateRun(context.Background(), runState); err != nil {
		t.Fatal(err)
	}
	success := &scriptedProvider{name: "success", events: []provider.AssistantMessageEvent{
		provider.StreamDoneEvent{Message: assistant("ok", agentcore.StopReasonEndTurn)},
	}}
	var purposes []RoutePurpose
	routed := &failoverProvider{router: NewProviderRouter(), store: store, runID: runState.ID, sessionID: runState.SessionID, mode: "coder",
		planner: func(_ context.Context, purpose RoutePurpose, _ provider.CompletionRequest) (RoutePlan, []routedCandidate, error) {
			purposes = append(purposes, purpose)
			plan := RoutePlan{Purpose: purpose, Candidates: []LLMRoute{{ProviderID: 1, EndpointID: "provider_1", Model: "m"}}}
			return plan, []routedCandidate{{route: plan.Candidates[0], provider: success}}, nil
		},
	}
	runRequest := func(extra map[string]any) {
		t.Helper()
		stream, err := routed.StreamCompletion(context.Background(), provider.CompletionRequest{
			Model: "m", Config: provider.StreamConfig{Extra: extra},
		})
		if err != nil {
			t.Fatal(err)
		}
		for range stream.Events() {
		}
	}
	runRequest(map[string]any{"route_purpose": string(RoutePurposeCompaction)})
	runRequest(nil)
	if routed.turn.Load() != 1 {
		t.Fatalf("normal turn counter = %d, want 1", routed.turn.Load())
	}
	if len(purposes) != 2 || purposes[0] != RoutePurposeCompaction || purposes[1] != RoutePurposeCodeAnalysis {
		t.Fatalf("purposes = %v", purposes)
	}
	checkpoint, err := store.LoadLatestRunCheckpoint(context.Background(), runState.ID)
	if err != nil || checkpoint.Turn != 1 {
		t.Fatalf("checkpoint=%+v err=%v", checkpoint, err)
	}
}

func TestSmartCompactionSettingsScaleWithContextWindow(t *testing.T) {
	small := smartCompactionSettings(8_192)
	large := smartCompactionSettings(128_000)
	if !small.Enabled || small.ReserveTokens != 2_048 || small.KeepRecentTokens != 4_096 {
		t.Fatalf("small settings = %+v", small)
	}
	if !large.Enabled || large.ReserveTokens != 16_000 || large.KeepRecentTokens != 20_000 {
		t.Fatalf("large settings = %+v", large)
	}
}

func TestAdaptiveContextWindowUsesOnlyEligibleCoderRoutes(t *testing.T) {
	svc := &Service{Mem: &MemStore{providers: map[int]*Provider{
		1: {ID: 1, Key: "k", Status: 1, Models: "weak", ContextWindow: 8_192, QualityTier: 1,
			Capabilities: ProviderCapabilities{Reasoning: true, Tools: true}},
		2: {ID: 2, Key: "k", Status: 1, Models: "coder", ContextWindow: 64_000, QualityTier: 3,
			Capabilities: ProviderCapabilities{Coding: true, Tools: true}},
		3: {ID: 3, Key: "k", Status: 1, Models: "reasoner", ContextWindow: 128_000, QualityTier: 5,
			Capabilities: ProviderCapabilities{Reasoning: true, Tools: true}},
	}}}
	if got := svc.adaptiveContextWindow("auto", "coder"); got != 64_000 {
		t.Fatalf("auto coder context window = %d, want 64000", got)
	}
	if got := svc.adaptiveContextWindow("reasoner", "coder"); got != 128_000 {
		t.Fatalf("fixed reasoner context window = %d, want 128000", got)
	}
}
