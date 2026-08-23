package gateway

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/alex6xu/jarvisserver/internal/agentcore"
	"github.com/alex6xu/jarvisserver/internal/session"
	"github.com/zeromicro/go-zero/rest/pathvar"
)

func TestRunManagerConcurrentRegisterAllowsOneRunPerSession(t *testing.T) {
	manager := NewRunManager()
	const workers = 32
	start := make(chan struct{})
	results := make(chan *RunState, workers)
	var wg sync.WaitGroup
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			state, err := manager.Register("shared-session", "m", "", func() {})
			if err == nil {
				results <- state
			}
		}()
	}
	close(start)
	wg.Wait()
	close(results)

	var winners []*RunState
	for state := range results {
		winners = append(winners, state)
	}
	if len(winners) != 1 {
		t.Fatalf("successful registrations = %d, want 1", len(winners))
	}
	winners[0].Finish(nil)
}

func TestRunStateQueueDrainsInSubmissionOrder(t *testing.T) {
	manager := NewRunManager()
	state, err := manager.Register("session", "m", "ws", func() {})
	if err != nil {
		t.Fatal(err)
	}
	for _, content := range []string{"first", "second"} {
		message := agentcore.UserMessage{RoleField: agentcore.RoleUser, Content: agentcore.ContentList{agentcore.NewTextContent(content)}}
		if err := state.EnqueueMessage(content, message, false); err != nil {
			t.Fatal(err)
		}
	}
	messages := state.DrainMessages()
	if len(messages) != 2 {
		t.Fatalf("drained messages = %d, want 2", len(messages))
	}
	for i, want := range []string{"first", "second"} {
		message, ok := messages[i].(agentcore.UserMessage)
		if !ok || agentcore.ContentToText(message.Content) != want {
			t.Fatalf("message %d = %#v, want %q", i, messages[i], want)
		}
	}
	if again := state.DrainMessages(); len(again) != 0 {
		t.Fatalf("second drain = %d messages, want 0", len(again))
	}
	state.Finish(nil)
}

func TestRunStateQueueDrainsPinnedMessagesFirst(t *testing.T) {
	manager := NewRunManager()
	state, err := manager.Register("session", "m", "ws", func() {})
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range []struct {
		content string
		pinned  bool
	}{
		{content: "normal-1"},
		{content: "pinned-1", pinned: true},
		{content: "normal-2"},
		{content: "pinned-2", pinned: true},
	} {
		message := agentcore.UserMessage{RoleField: agentcore.RoleUser, Content: agentcore.ContentList{agentcore.NewTextContent(item.content)}}
		if err := state.EnqueueMessage(item.content, message, item.pinned); err != nil {
			t.Fatal(err)
		}
	}
	messages := state.DrainMessages()
	for i, want := range []string{"pinned-1", "pinned-2", "normal-1", "normal-2"} {
		message, ok := messages[i].(agentcore.UserMessage)
		if !ok || agentcore.ContentToText(message.Content) != want {
			t.Fatalf("message %d = %#v, want %q", i, messages[i], want)
		}
	}
	var injected []StreamEvent
	for _, event := range state.Events {
		if event.Payload.Type == "user_injected" {
			injected = append(injected, event.Payload)
		}
	}
	if len(injected) != 4 || !injected[0].Pinned || !injected[1].Pinned || injected[2].Pinned || injected[3].Pinned {
		t.Fatalf("injected events = %#v", injected)
	}
	state.Finish(nil)
}

func TestQueuedUserMessageMarksPinnedIntent(t *testing.T) {
	original := agentcore.ContentList{agentcore.NewTextContent("finish migration before stopping")}
	message := queuedUserMessage(original, true)
	text := agentcore.ContentToText(message.Content)
	if !strings.Contains(text, "highest-priority current user intent") || !strings.Contains(text, "finish migration before stopping") {
		t.Fatalf("pinned message = %q", text)
	}
	if got := agentcore.ContentToText(original); got != "finish migration before stopping" {
		t.Fatalf("input content was mutated: %q", got)
	}
}

func TestRunStateQueueRejectsTerminalAndFullRuns(t *testing.T) {
	manager := NewRunManager()
	state, err := manager.Register("session", "m", "", func() {})
	if err != nil {
		t.Fatal(err)
	}
	message := agentcore.UserMessage{RoleField: agentcore.RoleUser}
	for i := 0; i < maxQueuedMessages; i++ {
		if err := state.EnqueueMessage(fmt.Sprintf("message-%d", i), message, false); err != nil {
			t.Fatal(err)
		}
	}
	if err := state.EnqueueMessage("overflow", message, false); err == nil {
		t.Fatal("expected full queue to reject a message")
	}
	state.Finish(nil)
	if err := state.EnqueueMessage("late", message, false); err == nil {
		t.Fatal("expected terminal run to reject a message")
	}
}

func TestRunStateFinalDrainSealsEmptyQueue(t *testing.T) {
	manager := NewRunManager()
	state, err := manager.Register("session", "m", "", func() {})
	if err != nil {
		t.Fatal(err)
	}
	if messages := state.DrainFinalMessages(); len(messages) != 0 {
		t.Fatalf("final drain = %d messages, want 0", len(messages))
	}
	message := agentcore.UserMessage{RoleField: agentcore.RoleUser}
	if err := state.EnqueueMessage("late", message, false); err == nil {
		t.Fatal("expected a sealed inbox to reject late messages")
	}
	state.Finish(nil)
}

func TestRunStateSubscribeReplaysBacklogLargerThanLiveBuffer(t *testing.T) {
	manager := NewRunManager()
	state, err := manager.Register("session", "m", "", func() {})
	if err != nil {
		t.Fatal(err)
	}
	const events = 256
	for i := 1; i <= events; i++ {
		state.Publish(StreamEvent{Type: "delta", Content: fmt.Sprintf("event-%d", i)})
	}
	state.Finish(nil)

	done := make(chan []StoredEvent, 1)
	go func() {
		var replay []StoredEvent
		for event := range state.Subscribe(0) {
			replay = append(replay, event)
		}
		done <- replay
	}()
	select {
	case replay := <-done:
		if len(replay) != events || replay[0].Seq != 1 || replay[len(replay)-1].Seq != events {
			t.Fatalf("replay length/sequence = %d/%d/%d", len(replay), replay[0].Seq, replay[len(replay)-1].Seq)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("historical event replay deadlocked")
	}
}

func TestRunStateSubscribeContextRemovesDisconnectedSubscriber(t *testing.T) {
	manager := NewRunManager()
	state, err := manager.Register("session", "m", "", func() {})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	stream := state.SubscribeContext(ctx, 0)
	cancel()
	for range stream {
	}
	state.mu.Lock()
	subscribers := len(state.subs)
	state.mu.Unlock()
	if subscribers != 0 {
		t.Fatalf("subscribers after disconnect = %d", subscribers)
	}
	state.Finish(context.Canceled)
}

func TestRunEventsHandlerRespectsAfterSeqAndTerminates(t *testing.T) {
	manager := NewRunManager()
	state, err := manager.Register("session", "m", "", func() {})
	if err != nil {
		t.Fatal(err)
	}
	for _, content := range []string{"one", "two", "three"} {
		state.Publish(StreamEvent{Type: "delta", Content: content})
	}
	state.Finish(nil)

	service := &Service{Runs: manager}
	req := httptest.NewRequest(http.MethodGet, "/v1/agent/runs/"+state.ID+"/events?after_seq=1", nil)
	req = pathvar.WithVars(req, map[string]string{"runId": state.ID})
	res := httptest.NewRecorder()
	service.handleRunEvents(res, req)

	body := res.Body.String()
	if strings.Contains(body, `"content":"one"`) || !strings.Contains(body, `"content":"two"`) || !strings.Contains(body, `"content":"three"`) {
		t.Fatalf("unexpected SSE replay: %s", body)
	}
	if !strings.HasSuffix(body, "data: [DONE]\n\n") {
		t.Fatalf("SSE stream did not terminate cleanly: %q", body)
	}
}

func TestCancelRunHandlerIsIdempotent(t *testing.T) {
	manager := NewRunManager()
	ctx, cancel := context.WithCancel(context.Background())
	state, err := manager.Register("session", "m", "", cancel)
	if err != nil {
		t.Fatal(err)
	}
	service := &Service{Runs: manager}
	for attempt := 0; attempt < 2; attempt++ {
		req := httptest.NewRequest(http.MethodPost, "/v1/agent/runs/"+state.ID+"/cancel", nil)
		req = req.WithContext(context.WithValue(req.Context(), accountContextKey{}, Account{ID: legacyWorkspaceAccountID}))
		req = pathvar.WithVars(req, map[string]string{"runId": state.ID})
		res := httptest.NewRecorder()
		service.handleCancelRun(res, req)
		if res.Code != http.StatusAccepted {
			t.Fatalf("cancel %d status = %d, body = %s", attempt+1, res.Code, res.Body.String())
		}
	}
	select {
	case <-ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("cancel handler did not cancel run context")
	}
	state.Finish(ctx.Err())
}

func TestCancelRunHandlerEnforcesSessionOwnership(t *testing.T) {
	store := newTestGatewayStore(t)
	now := time.Now().UTC()
	header := session.SessionHeader{
		ID: "owned-session", AccountID: 11, Type: sessionTypeChat,
		CreatedAt: now, UpdatedAt: now,
	}
	if err := store.SaveEntries(header, nil); err != nil {
		t.Fatal(err)
	}
	manager := NewRunManager()
	runCtx, cancel := context.WithCancel(context.Background())
	state, err := manager.Register(header.ID, "m", "", cancel)
	if err != nil {
		t.Fatal(err)
	}
	service := &Service{Runs: manager, Store: store}

	req := httptest.NewRequest(http.MethodPost, "/v1/agent/runs/"+state.ID+"/cancel", nil)
	req = req.WithContext(context.WithValue(req.Context(), accountContextKey{}, Account{ID: 12}))
	req = pathvar.WithVars(req, map[string]string{"runId": state.ID})
	res := httptest.NewRecorder()
	service.handleCancelRun(res, req)
	if res.Code != http.StatusNotFound {
		t.Fatalf("other account cancel status = %d, body = %s", res.Code, res.Body.String())
	}
	select {
	case <-runCtx.Done():
		t.Fatal("another account cancelled the run")
	default:
	}

	req = httptest.NewRequest(http.MethodPost, "/v1/agent/runs/"+state.ID+"/cancel", nil)
	req = req.WithContext(context.WithValue(req.Context(), accountContextKey{}, Account{ID: 11}))
	req = pathvar.WithVars(req, map[string]string{"runId": state.ID})
	res = httptest.NewRecorder()
	service.handleCancelRun(res, req)
	if res.Code != http.StatusAccepted {
		t.Fatalf("owner cancel status = %d, body = %s", res.Code, res.Body.String())
	}
	select {
	case <-runCtx.Done():
	case <-time.After(time.Second):
		t.Fatal("owner cancellation did not reach the run")
	}
	state.Finish(runCtx.Err())
}

func TestGatewayMigrationsUpgradeVersionOneAndRemainIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "gateway.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(gatewaySchema); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
CREATE TABLE schema_migrations (
    version INTEGER PRIMARY KEY,
    name TEXT NOT NULL,
    applied_at TEXT NOT NULL
);
INSERT INTO schema_migrations(version, name, applied_at) VALUES (1, 'gateway_base', '2026-01-01T00:00:00Z');
INSERT INTO runs(id, session_id, model, workspace_id, status, created_at)
VALUES ('legacy-run', 'legacy-session', 'm', '', 'done', '2026-01-01T00:00:00Z');`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	for open := 0; open < 2; open++ {
		store, err := OpenGatewayStore(path)
		if err != nil {
			t.Fatalf("open after migration %d: %v", open+1, err)
		}
		var versions int
		if err := store.db.QueryRow(`SELECT COUNT(*) FROM schema_migrations`).Scan(&versions); err != nil {
			_ = store.Close()
			t.Fatal(err)
		}
		if versions != len(gatewayMigrations) {
			_ = store.Close()
			t.Fatalf("migration versions = %d, want %d", versions, len(gatewayMigrations))
		}
		loaded, err := store.LoadRun(context.Background(), "legacy-run")
		if err != nil || loaded.Status != runStatusDone {
			_ = store.Close()
			t.Fatalf("legacy run = %+v, %v", loaded, err)
		}
		if err := store.Close(); err != nil {
			t.Fatal(err)
		}
	}
}

func TestRunAttemptsEndpointNeverContainsProviderCredential(t *testing.T) {
	store := newTestGatewayStore(t)
	if err := store.ReplaceProviders(context.Background(), []Provider{{
		ID: 1, Name: "secret-provider", Type: 1, Key: "secret-value", BaseURL: "https://provider.test/v1", Models: "m", Status: 1,
	}}); err != nil {
		t.Fatal(err)
	}
	manager := NewRunManager(store)
	state, err := manager.Register("session", "m", "", func() {})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.CreateRunAttempt(context.Background(), RunAttempt{
		ID: "attempt", RunID: state.ID, EndpointID: "provider_1", ProviderID: 1,
		Model: "m", Ordinal: 1, Turn: 1, Status: "done", CreatedAt: time.Now().UTC().Format(time.RFC3339Nano),
	}); err != nil {
		t.Fatal(err)
	}
	service := &Service{Runs: manager, Audit: store}
	req := httptest.NewRequest(http.MethodGet, "/v1/admin/runs/"+state.ID+"/attempts", nil)
	req = pathvar.WithVars(req, map[string]string{"runId": state.ID})
	res := httptest.NewRecorder()
	service.handleListRunAttempts(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", res.Code, res.Body.String())
	}
	var payload map[string]json.RawMessage
	if err := json.Unmarshal(res.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(res.Body.String(), "secret-value") || strings.Contains(strings.ToLower(res.Body.String()), "api_key") {
		t.Fatalf("attempt response exposes provider credential: %s", res.Body.String())
	}
	state.Finish(nil)
}

func FuzzParseAfterSeq(f *testing.F) {
	for _, seed := range []string{"", "0", "1", "-1", "9223372036854775807", "not-a-number"} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, raw string) {
		if sequence := parseAfterSeq(raw); sequence < 0 {
			t.Fatalf("parseAfterSeq(%q) = %d", raw, sequence)
		}
	})
}
