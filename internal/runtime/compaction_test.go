package runtime

// Tests for auto-compaction wiring into the loop (US-004, #120): when context
// usage exceeds the usable window after a turn settles, runLoop compacts in
// place and emits a CompactionEvent; a compaction failure is non-fatal and is
// reported via a CompactionEvent carrying an error while the original context is
// preserved.

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/alex6xu/jarvisserver/internal/agentcore"
	"github.com/alex6xu/jarvisserver/internal/compaction"
	"github.com/alex6xu/jarvisserver/internal/provider"
)

// bigUserMessages returns n user messages each carrying `chars` characters, used
// to inflate estimated context tokens past a small window.
func bigUserMessages(n, chars int) agentcore.MessageList {
	body := strings.Repeat("x", chars)
	msgs := make(agentcore.MessageList, 0, n)
	for i := 0; i < n; i++ {
		msgs = append(msgs, agentcore.UserMessage{
			RoleField: agentcore.RoleUser,
			Content:   agentcore.ContentList{agentcore.NewTextContent(body)},
		})
	}
	return msgs
}

// findCompaction returns the first CompactionEvent emitted, or nil.
func findCompaction(events []agentcore.AgentEvent) *agentcore.CompactionEvent {
	for _, ev := range events {
		if c, ok := ev.(agentcore.CompactionEvent); ok {
			return &c
		}
	}
	return nil
}

// collectEvents drains a stream returning the concrete events (not just kinds).
func collectEvents(t *testing.T, s *LoopEventStream) []agentcore.AgentEvent {
	t.Helper()
	var out []agentcore.AgentEvent
	for ev := range s.Events() {
		out = append(out, ev)
	}
	if _, err := s.Result(context.Background()); err != nil {
		t.Fatalf("stream result: %v", err)
	}
	return out
}

// summaryStream yields a fixed summary text as an end_turn assistant message,
// standing in for the summarization model.
func summaryStream(text string) provider.StreamFn {
	return func(ctx context.Context, model string, llm provider.LlmContext, cfg provider.StreamConfig) (*provider.AssistantMessageEventStream, error) {
		msg := agentcore.AssistantMessage{
			RoleField:  agentcore.RoleAssistant,
			StopReason: agentcore.StopReasonEndTurn,
			Content:    agentcore.ContentList{agentcore.NewTextContent(text)},
		}
		s := provider.NewAssistantMessageEventStream(0)
		go func() { _ = s.Emit(ctx, provider.StreamDoneEvent{Message: msg}); s.Close() }()
		return s, nil
	}
}

func TestAutoCompactionFiresOnThreshold(t *testing.T) {
	// Main stream just ends the turn with text; the summary stream is separate so
	// the summarization does not consume main-stream scripted turns.
	main := scriptedStream([]agentcore.AssistantMessage{
		{RoleField: agentcore.RoleAssistant, StopReason: agentcore.StopReasonEndTurn, Content: agentcore.ContentList{agentcore.NewTextContent("ok")}},
	})
	cfg := newRunCfg(main)
	cfg.SummaryStream = summaryStream("## Goal\ncompacted")
	// Small window + reserve so a handful of fat messages exceed the threshold.
	cfg.ContextWindow = 2000
	cfg.Compaction = compaction.CompactionSettings{Enabled: true, ReserveTokens: 500, KeepRecentTokens: 100}

	// Seed a long history so EstimateContextTokens > window-reserve.
	agentCtx := &agentcore.AgentContext{Messages: bigUserMessages(12, 800)}

	events := collectEvents(t, agentLoop(context.Background(), agentCtx, cfg))
	ce := findCompaction(events)
	if ce == nil {
		t.Fatalf("expected a CompactionEvent, got events %+v", events)
	}
	// A CompactionStartEvent must precede the CompactionEvent so a front-end can
	// show an in-progress indicator while summarization is in flight.
	var startedBefore bool
	for _, ev := range events {
		if _, ok := ev.(agentcore.CompactionStartEvent); ok {
			startedBefore = true
		}
		if _, ok := ev.(agentcore.CompactionEvent); ok {
			if !startedBefore {
				t.Errorf("CompactionStartEvent must be emitted before CompactionEvent")
			}
			break
		}
	}
	if !startedBefore {
		t.Errorf("expected a CompactionStartEvent, got events %+v", events)
	}
	if ce.ErrorMessage != "" {
		t.Fatalf("compaction should have succeeded, got error %q", ce.ErrorMessage)
	}
	if ce.TokensAfter >= ce.TokensBefore {
		t.Errorf("compaction should reduce tokens: before=%d after=%d", ce.TokensBefore, ce.TokensAfter)
	}
	if ce.SummarizedCount <= 0 {
		t.Errorf("expected some messages summarized, got %d", ce.SummarizedCount)
	}
	// The context must now begin with a compaction checkpoint.
	if len(agentCtx.Messages) == 0 || agentCtx.Messages[0].Role() != agentcore.RoleCompaction {
		t.Errorf("context should start with a compaction checkpoint, got %+v", agentCtx.Messages)
	}
}

func TestAutoCompactionDisabledWhenWindowUnknown(t *testing.T) {
	main := scriptedStream([]agentcore.AssistantMessage{
		{RoleField: agentcore.RoleAssistant, StopReason: agentcore.StopReasonEndTurn, Content: agentcore.ContentList{agentcore.NewTextContent("ok")}},
	})
	cfg := newRunCfg(main)
	cfg.SummaryStream = summaryStream("unused")
	cfg.ContextWindow = 0 // unknown → disabled
	cfg.Compaction = compaction.CompactionSettings{Enabled: true, ReserveTokens: 500, KeepRecentTokens: 100}

	agentCtx := &agentcore.AgentContext{Messages: bigUserMessages(12, 800)}
	before := len(agentCtx.Messages)

	events := collectEvents(t, agentLoop(context.Background(), agentCtx, cfg))
	if ce := findCompaction(events); ce != nil {
		t.Fatalf("no compaction expected when window unknown, got %+v", ce)
	}
	// Context grows by one assistant reply only (no checkpoint replacement).
	if len(agentCtx.Messages) != before+1 {
		t.Errorf("context should be untouched by compaction, got %d messages", len(agentCtx.Messages))
	}
}

func TestCompactionEventEnvelope(t *testing.T) {
	env := eventEnvelope(agentcore.CompactionEvent{
		Reason:          "threshold",
		TokensBefore:    1000,
		TokensAfter:     400,
		SummarizedCount: 8,
		KeptCount:       3,
	})
	if env["type"] != agentcore.EventCompaction {
		t.Errorf("type = %v, want %q", env["type"], agentcore.EventCompaction)
	}
	if env["tokensBefore"] != 1000 || env["tokensAfter"] != 400 {
		t.Errorf("token fields wrong: %+v", env)
	}
	if env["summarizedCount"] != 8 || env["keptCount"] != 3 {
		t.Errorf("count fields wrong: %+v", env)
	}
	if _, hasErr := env["error"]; hasErr {
		t.Errorf("no error key expected on success: %+v", env)
	}
	// Failure envelope carries the error.
	failEnv := eventEnvelope(agentcore.CompactionEvent{Reason: "threshold", ErrorMessage: "boom"})
	if failEnv["error"] != "boom" {
		t.Errorf("error key expected on failure, got %+v", failEnv)
	}
}

func TestContextOverflowErrorDetection(t *testing.T) {
	for _, message := range []string{
		`{"error":{"code":"context_length_exceeded","message":"maximum context length is 8192 tokens"}}`,
		"prompt is too long: 9000 tokens > 8192 maximum",
		"上下文超限，请缩短输入",
	} {
		if !isContextOverflowError(message) {
			t.Errorf("expected overflow error to be recognized: %q", message)
		}
	}
	for _, message := range []string{"rate limit exceeded", "output token limit reached", ""} {
		if isContextOverflowError(message) {
			t.Errorf("non-overflow error was misclassified: %q", message)
		}
	}
}

func TestContextOverflowCompactsAndRetriesOnce(t *testing.T) {
	calls := 0
	main := func(ctx context.Context, model string, llm provider.LlmContext, cfg provider.StreamConfig) (*provider.AssistantMessageEventStream, error) {
		calls++
		msg := agentcore.AssistantMessage{RoleField: agentcore.RoleAssistant}
		if calls == 1 {
			msg.StopReason = agentcore.StopReasonError
			msg.ErrorMessage = "context_length_exceeded: maximum context length is 8192 tokens"
		} else {
			msg.StopReason = agentcore.StopReasonEndTurn
			msg.Content = agentcore.ContentList{agentcore.NewTextContent("recovered")}
		}
		s := provider.NewAssistantMessageEventStream(0)
		go func() { _ = s.Emit(ctx, provider.StreamDoneEvent{Message: msg}); s.Close() }()
		return s, nil
	}
	cfg := newRunCfg(main)
	cfg.SummaryStream = summaryStream("## Goal\ncontinue after overflow")
	// Deliberately overstate the window so normal threshold compaction does not
	// fire; recovery must be driven by the provider error itself.
	cfg.ContextWindow = 1_000_000
	cfg.Compaction = compaction.CompactionSettings{Enabled: true, ReserveTokens: 500, KeepRecentTokens: 20_000}
	agentCtx := &agentcore.AgentContext{Messages: bigUserMessages(12, 800)}

	events := collectEvents(t, agentLoop(context.Background(), agentCtx, cfg))
	if calls != 2 {
		t.Fatalf("main provider calls = %d, want overflow + one retry", calls)
	}
	ce := findCompaction(events)
	if ce == nil || ce.Reason != "overflow" || ce.ErrorMessage != "" {
		t.Fatalf("expected successful overflow compaction, got %+v", ce)
	}
	if ce.TokensAfter >= ce.TokensBefore {
		t.Fatalf("overflow recovery did not reduce context: %+v", ce)
	}
	last, ok := agentCtx.Messages[len(agentCtx.Messages)-1].(agentcore.AssistantMessage)
	if !ok || last.StopReason != agentcore.StopReasonEndTurn || agentcore.ContentToText(last.Content) != "recovered" {
		t.Fatalf("retry result = %+v", agentCtx.Messages[len(agentCtx.Messages)-1])
	}
	for _, msg := range agentCtx.Messages {
		if assistant, ok := msg.(agentcore.AssistantMessage); ok && isContextOverflowError(assistant.ErrorMessage) {
			t.Fatalf("rejected overflow placeholder leaked into context: %+v", assistant)
		}
	}
}

func TestContextOverflowRetriesAtMostOnce(t *testing.T) {
	calls := 0
	main := func(ctx context.Context, model string, llm provider.LlmContext, cfg provider.StreamConfig) (*provider.AssistantMessageEventStream, error) {
		calls++
		msg := agentcore.AssistantMessage{RoleField: agentcore.RoleAssistant, StopReason: agentcore.StopReasonError,
			ErrorMessage: "context_length_exceeded: maximum context length reached"}
		s := provider.NewAssistantMessageEventStream(0)
		go func() { _ = s.Emit(ctx, provider.StreamDoneEvent{Message: msg}); s.Close() }()
		return s, nil
	}
	cfg := newRunCfg(main)
	cfg.SummaryStream = summaryStream("## Goal\nretry once")
	cfg.ContextWindow = 1_000_000
	cfg.Compaction = compaction.CompactionSettings{Enabled: true, ReserveTokens: 500, KeepRecentTokens: 20_000}

	collectEvents(t, agentLoop(context.Background(), &agentcore.AgentContext{Messages: bigUserMessages(12, 800)}, cfg))
	if calls != 2 {
		t.Fatalf("main provider calls = %d, want exactly 2", calls)
	}
}

func TestAutoCompactionFailureIsNonFatal(t *testing.T) {
	main := scriptedStream([]agentcore.AssistantMessage{
		{RoleField: agentcore.RoleAssistant, StopReason: agentcore.StopReasonEndTurn, Content: agentcore.ContentList{agentcore.NewTextContent("ok")}},
	})
	cfg := newRunCfg(main)
	// Summary stream that fails to build: forces compaction.Compact to error.
	cfg.SummaryStream = func(ctx context.Context, model string, llm provider.LlmContext, c provider.StreamConfig) (*provider.AssistantMessageEventStream, error) {
		return nil, errors.New("summarizer down")
	}
	cfg.ContextWindow = 2000
	cfg.Compaction = compaction.CompactionSettings{Enabled: true, ReserveTokens: 500, KeepRecentTokens: 100}

	agentCtx := &agentcore.AgentContext{Messages: bigUserMessages(12, 800)}

	events := collectEvents(t, agentLoop(context.Background(), agentCtx, cfg))
	ce := findCompaction(events)
	if ce == nil {
		t.Fatalf("expected a CompactionEvent reporting the failure")
	}
	if ce.ErrorMessage == "" {
		t.Errorf("failed compaction must carry an ErrorMessage")
	}
	if ce.TokensAfter != ce.TokensBefore {
		t.Errorf("on failure tokens must be unchanged: before=%d after=%d", ce.TokensBefore, ce.TokensAfter)
	}
	// The run must still end normally.
	if events[len(events)-1].EventType() != agentcore.EventAgentEnd {
		t.Errorf("run must end with agent_end despite compaction failure")
	}
	// No compaction checkpoint should have been inserted.
	if len(agentCtx.Messages) > 0 && agentCtx.Messages[0].Role() == agentcore.RoleCompaction {
		t.Errorf("failed compaction must not insert a checkpoint")
	}
}
