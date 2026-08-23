package gateway

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/alex6xu/jarvisserver/internal/agentcore"
)

func TestTranslateHandlerDeltaAndDone(t *testing.T) {
	var got []StreamEvent
	pub := func(ev StreamEvent) { got = append(got, ev) }
	h, finish := NewTranslateHandler(pub, "m1", "s1", nil)

	h.OnText("hello")
	h.OnText(" world")
	h.OnEvent(agentcore.ToolExecutionStartEvent{
		ToolCallID: "t1",
		ToolName:   "bash",
		Args:       json.RawMessage(`{"command":"ls"}`),
	})
	h.OnEvent(agentcore.ToolExecutionEndEvent{
		ToolCallID: "t1",
		ToolName:   "bash",
		Result:     agentcore.AgentToolResult{Content: agentcore.ContentList{agentcore.NewTextContent("ok")}},
	})
	finish(nil)

	if len(got) < 4 {
		t.Fatalf("got %d events, want >= 4: %#v", len(got), got)
	}
	if got[0].Type != "delta" || got[0].Content != "hello" {
		t.Fatalf("first delta: %#v", got[0])
	}
	if got[1].Type != "delta" || got[1].Content != " world" {
		t.Fatalf("second delta: %#v", got[1])
	}
	var sawStart, sawTool, sawDone bool
	for _, ev := range got {
		if ev.Type == "tool_step" && ev.Step != nil && ev.Step.Tool == "bash" {
			if ev.Step.Status == "running" && strings.Contains(ev.Step.Args, "ls") {
				sawStart = true
			}
			if ev.Step.Result == "ok" && strings.Contains(ev.Step.Args, "ls") {
				sawTool = true
			}
		}
		if ev.Type == "done" && ev.Content == "hello world" && len(ev.ToolSteps) == 1 {
			if strings.Contains(ev.ToolSteps[0].Args, "ls") {
				sawDone = true
			}
		}
	}
	if !sawStart {
		t.Fatalf("missing running tool_step with args: %#v", got)
	}
	if !sawTool {
		t.Fatalf("missing tool_step: %#v", got)
	}
	if !sawDone {
		t.Fatalf("missing done: %#v", got)
	}
}

func TestTranslateHandlerPrefersErrorMessageOverPartialContent(t *testing.T) {
	var got []StreamEvent
	h, _ := NewTranslateHandler(func(ev StreamEvent) { got = append(got, ev) }, "m1", "s1", nil)
	h.OnEvent(agentcore.TurnEndEvent{Message: agentcore.AssistantMessage{
		RoleField:    agentcore.RoleAssistant,
		Content:      agentcore.ContentList{agentcore.NewTextContent("useful partial output")},
		StopReason:   agentcore.StopReasonError,
		ErrorMessage: "upstream stream interrupted",
	}})
	if len(got) != 1 || got[0].Type != "error" || got[0].Content != "upstream stream interrupted" {
		t.Fatalf("error event = %#v", got)
	}
}

func TestRunManagerAfterSeq(t *testing.T) {
	m := NewRunManager()
	st, err := m.Register("sess", "m", "", func() {})
	if err != nil {
		t.Fatal(err)
	}
	st.Publish(StreamEvent{Type: "delta", Content: "a"})
	st.Publish(StreamEvent{Type: "delta", Content: "b"})
	st.Finish(nil)

	ch := st.Subscribe(1)
	var seqs []int64
	for ev := range ch {
		seqs = append(seqs, ev.Seq)
	}
	if len(seqs) != 1 || seqs[0] != 2 {
		t.Fatalf("after_seq=1 got %v, want [2]", seqs)
	}
}

func TestRunManagerPersistsAndReloadsEvents(t *testing.T) {
	store, err := OpenGatewayStore(t.TempDir() + "/gateway.db")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	m := NewRunManager(store)
	st, err := m.Register("sess", "m", "workspace", func() {})
	if err != nil {
		t.Fatal(err)
	}
	st.Publish(StreamEvent{Type: "delta", Content: "a"})
	st.Publish(StreamEvent{Type: "done", Content: "ab"})
	st.Finish(nil)

	reloaded, ok := NewRunManager(store).Get(st.ID)
	if !ok {
		t.Fatal("persisted run not found")
	}
	if reloaded.Status != runStatusDone || reloaded.LastSeq != 2 {
		t.Fatalf("reloaded run = status %q seq %d", reloaded.Status, reloaded.LastSeq)
	}
	var events []StoredEvent
	for event := range reloaded.Subscribe(1) {
		events = append(events, event)
	}
	if len(events) != 1 || events[0].Payload.Content != "ab" {
		t.Fatalf("replayed events: %#v", events)
	}
}

func TestRecoverInterruptedRunPreservesCheckpointAndEvent(t *testing.T) {
	store, err := OpenGatewayStore(t.TempDir() + "/gateway.db")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	st := &RunState{ID: "run_interrupted", SessionID: "sess", Status: runStatusRunning}
	if err := store.CreateRun(context.Background(), st); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if err := store.CreateChat(context.Background(), ChatExchange{
		ID: "chat_interrupted", RunID: st.ID, SessionID: st.SessionID,
		Model: "m", RequestText: "resume me", CreatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.CreateProviderExchange(context.Background(), ProviderExchange{
		ID: "provider_interrupted", RunID: st.ID, SessionID: st.SessionID,
		Model: "m", CreatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.CreateRunAttempt(context.Background(), RunAttempt{
		ID: "attempt_interrupted", RunID: st.ID, EndpointID: "provider_1",
		Model: "m", Ordinal: 1, Turn: 1, Status: runStatusRunning,
		CreatedAt: now.Format(time.RFC3339Nano),
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveRunCheckpoint(context.Background(), RunCheckpoint{
		RunID: st.ID, Turn: 1, SessionID: st.SessionID, Model: "m",
		Messages: agentcore.MessageList{agentcore.UserMessage{
			RoleField: agentcore.RoleUser,
			Content:   agentcore.ContentList{agentcore.NewTextContent("resume me")},
		}},
		CreatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}
	if recovered, err := store.RecoverInterruptedRuns(context.Background()); err != nil || recovered != 1 {
		t.Fatalf("recover = %d, %v", recovered, err)
	}

	loaded, err := store.LoadRun(context.Background(), st.ID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Status != runStatusInterrupted || loaded.Err == nil {
		t.Fatalf("loaded interrupted run = status %q err %v", loaded.Status, loaded.Err)
	}
	if loaded.LastSeq != 1 || loaded.Events[0].Payload.Type != "run.interrupted" {
		t.Fatalf("recovery events = %#v", loaded.Events)
	}
	var chatStatus, chatError string
	var chatFinished int
	if err := store.db.QueryRow(`
SELECT status, error, finished_at IS NOT NULL FROM chat_exchanges WHERE run_id = ?`, st.ID).
		Scan(&chatStatus, &chatError, &chatFinished); err != nil {
		t.Fatal(err)
	}
	if chatStatus != runStatusInterrupted || chatError == "" || chatFinished != 1 {
		t.Fatalf("recovered chat = status %q error %q finished %d", chatStatus, chatError, chatFinished)
	}
	var providerStatus, providerError string
	var providerFinished int
	if err := store.db.QueryRow(`
SELECT status, error, finished_at IS NOT NULL FROM provider_exchanges WHERE run_id = ?`, st.ID).
		Scan(&providerStatus, &providerError, &providerFinished); err != nil {
		t.Fatal(err)
	}
	if providerStatus != runStatusInterrupted || providerError == "" || providerFinished != 1 {
		t.Fatalf("recovered provider = status %q error %q finished %d", providerStatus, providerError, providerFinished)
	}
	var attemptStatus, failureStage, errorCategory, attemptError string
	var attemptFinished int
	if err := store.db.QueryRow(`
SELECT status, failure_stage, error_category, error, finished_at IS NOT NULL
FROM run_attempts WHERE run_id = ?`, st.ID).
		Scan(&attemptStatus, &failureStage, &errorCategory, &attemptError, &attemptFinished); err != nil {
		t.Fatal(err)
	}
	if attemptStatus != runStatusInterrupted || failureStage != "gateway_restart" ||
		errorCategory != "interrupted" || attemptError == "" || attemptFinished != 1 {
		t.Fatalf("recovered attempt = status %q stage %q category %q error %q finished %d",
			attemptStatus, failureStage, errorCategory, attemptError, attemptFinished)
	}
	checkpoint, err := store.LoadLatestRunCheckpoint(context.Background(), st.ID)
	if err != nil || checkpoint.Turn != 1 {
		t.Fatalf("checkpoint = %+v, %v", checkpoint, err)
	}
	if recovered, err := store.RecoverInterruptedRuns(context.Background()); err != nil || recovered != 0 {
		t.Fatalf("second recovery = %d, %v", recovered, err)
	}
	reloaded, err := store.LoadRun(context.Background(), st.ID)
	if err != nil || reloaded.LastSeq != 1 {
		t.Fatalf("idempotent recovery events = %d, %v", reloaded.LastSeq, err)
	}
	if _, err := store.db.Exec(`UPDATE chat_exchanges SET status = 'running' WHERE run_id = ?`, st.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec(`UPDATE provider_exchanges SET status = 'running' WHERE run_id = ?`, st.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec(`UPDATE run_attempts SET status = 'running' WHERE run_id = ?`, st.ID); err != nil {
		t.Fatal(err)
	}
	if recovered, err := store.RecoverInterruptedRuns(context.Background()); err != nil || recovered != 1 {
		t.Fatalf("reconcile legacy children = %d, %v", recovered, err)
	}
	var runningChildren int
	if err := store.db.QueryRow(`
SELECT (SELECT COUNT(*) FROM chat_exchanges WHERE run_id = ? AND status = 'running') +
       (SELECT COUNT(*) FROM provider_exchanges WHERE run_id = ? AND status = 'running') +
       (SELECT COUNT(*) FROM run_attempts WHERE run_id = ? AND status = 'running')`,
		st.ID, st.ID, st.ID).Scan(&runningChildren); err != nil {
		t.Fatal(err)
	}
	if runningChildren != 0 {
		t.Fatalf("legacy running children = %d", runningChildren)
	}
	reloaded, err = store.LoadRun(context.Background(), st.ID)
	if err != nil || reloaded.LastSeq != 1 {
		t.Fatalf("legacy reconciliation duplicated events = %d, %v", reloaded.LastSeq, err)
	}
	if recovered, err := store.RecoverInterruptedRuns(context.Background()); err != nil || recovered != 0 {
		t.Fatalf("post-reconciliation recovery = %d, %v", recovered, err)
	}
}

func TestRunManagerCancelAndTimeoutStates(t *testing.T) {
	m := NewRunManager()
	ctx, cancel := context.WithCancel(context.Background())
	st, err := m.Register("cancel-session", "m", "", cancel)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.EnqueueMessage("queued", agentcore.UserMessage{}, false); err != nil {
		t.Fatal(err)
	}
	if !m.Cancel(st.ID) || !m.Cancel(st.ID) {
		t.Fatal("cancel must be idempotent")
	}
	if err := st.EnqueueMessage("late", agentcore.UserMessage{}, false); err == nil {
		t.Fatal("cancelled run accepted a queued message")
	}
	if queued := st.DrainMessages(); len(queued) != 0 {
		t.Fatalf("cancelled run retained %d queued messages", len(queued))
	}
	<-ctx.Done()
	st.Finish(ctx.Err())
	if st.Info().Status != runStatusCancelled {
		t.Fatalf("cancel status = %q", st.Info().Status)
	}

	timed, err := m.Register("timeout-session", "m", "", func() {})
	if err != nil {
		t.Fatal(err)
	}
	timed.Finish(context.DeadlineExceeded)
	if timed.Info().Status != runStatusTimedOut {
		t.Fatalf("timeout status = %q", timed.Info().Status)
	}
}
