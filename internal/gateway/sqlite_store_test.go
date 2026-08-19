package gateway

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/alex6xu/jarvisserver/internal/agentcore"
	"github.com/alex6xu/jarvisserver/internal/provider"
)

func newTestGatewayStore(t *testing.T) *GatewayStore {
	t.Helper()
	store, err := OpenGatewayStore(filepath.Join(t.TempDir(), "gateway.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func TestGatewayStoreRecordsChatAndProviderExchange(t *testing.T) {
	store := newTestGatewayStore(t)
	ctx := context.Background()
	now := time.Now().UTC()
	if err := store.CreateChat(ctx, ChatExchange{
		ID: "chat_1", RunID: "run_1", SessionID: "session_1", WorkspaceID: "workspace_1",
		Mode: "coder", Model: "model_1", RequestText: "hello", CreatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.FinishChat(ctx, "run_1", "world", "done", "", now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	var request, response, status string
	if err := store.db.QueryRow(`SELECT request_text, response_text, status FROM chat_exchanges WHERE run_id = ?`, "run_1").Scan(&request, &response, &status); err != nil {
		t.Fatal(err)
	}
	if request != "hello" || response != "world" || status != "done" {
		t.Fatalf("chat row = %q %q %q", request, response, status)
	}

	if err := store.CreateProviderExchange(ctx, ProviderExchange{
		ID: "attempt_1", RunID: "run_1", SessionID: "session_1", ProviderID: 7,
		ProviderName: "provider_1", Model: "model_1", RequestBody: `{"model":"model_1"}`, CreatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.FinishProviderExchange(ctx, "attempt_1", `{"answer":"ok"}`, "done", "", 200, 10, 5, 0, now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	logs, err := store.ListRequestLogs(ctx, 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(logs) != 1 || logs[0].RunID != "run_1" || logs[0].PromptTokens != 10 || logs[0].CompletionTokens != 5 {
		t.Fatalf("logs = %#v", logs)
	}
}

func TestGatewayStoreFiltersAndAggregatesRequestLogs(t *testing.T) {
	store := newTestGatewayStore(t)
	ctx := context.Background()
	now := time.Now().UTC()
	for i, item := range []struct {
		id, model, status        string
		code, prompt, completion int
	}{{"ok", "model-a", "done", 200, 10, 5}, {"failed", "model-b", "error", 429, 7, 0}} {
		if err := store.CreateProviderExchange(ctx, ProviderExchange{
			ID: item.id, RunID: "run", SessionID: "session", Model: item.model, CreatedAt: now.Add(time.Duration(i) * time.Second),
		}); err != nil {
			t.Fatal(err)
		}
		if err := store.FinishProviderExchange(ctx, item.id, "{}", item.status, "", item.code,
			item.prompt, item.completion, 0, now.Add(time.Duration(i+1)*time.Second)); err != nil {
			t.Fatal(err)
		}
	}
	logs, err := store.ListRequestLogsFiltered(ctx, RequestLogFilter{Limit: 10, Model: "model-b", StatusCode: 429})
	if err != nil {
		t.Fatal(err)
	}
	if len(logs) != 1 || logs[0].ID != "failed" {
		t.Fatalf("filtered logs = %#v", logs)
	}
	stats, err := store.Stats(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if stats.TotalRequests != 2 || stats.FailedRequests != 1 || stats.TotalTokens != 22 {
		t.Fatalf("stats = %#v", stats)
	}
}

func TestAuditJSONBodyIsBounded(t *testing.T) {
	store := newTestGatewayStore(t)
	store.maxAuditBodyBytes = 10
	raw := store.marshalAuditJSON(map[string]string{"content": strings.Repeat("x", 100)})
	if !strings.Contains(raw, `"truncated":true`) || !strings.Contains(raw, `"original_bytes"`) {
		t.Fatalf("bounded audit body = %s", raw)
	}
}

func TestFinalAssistantResultReturnsTerminalStatus(t *testing.T) {
	messages := agentcore.MessageList{
		agentcore.UserMessage{RoleField: agentcore.RoleUser, Content: agentcore.ContentList{agentcore.NewTextContent("question")}},
		agentcore.AssistantMessage{RoleField: agentcore.RoleAssistant, Content: agentcore.ContentList{agentcore.NewTextContent("partial")}, StopReason: agentcore.StopReasonError, ErrorMessage: "upstream failed"},
	}
	text, reason, errorMessage := finalAssistantResult(messages, 0)
	if text != "partial" || reason != agentcore.StopReasonError || errorMessage != "upstream failed" {
		t.Fatalf("result = %q %q %q", text, reason, errorMessage)
	}
}

type auditFakeProvider struct{}

func (auditFakeProvider) Name() string             { return "fake" }
func (auditFakeProvider) Models() []provider.Model { return nil }
func (auditFakeProvider) StreamCompletion(ctx context.Context, req provider.CompletionRequest) (*provider.AssistantMessageEventStream, error) {
	stream := provider.NewAssistantMessageEventStream(0)
	go func() {
		defer stream.Close()
		msg := agentcore.AssistantMessage{
			RoleField:  agentcore.RoleAssistant,
			Content:    agentcore.ContentList{agentcore.NewTextContent("answer")},
			StopReason: agentcore.StopReasonEndTurn,
			Usage:      &agentcore.Usage{InputTokens: 12, OutputTokens: 4},
		}
		_ = stream.Emit(ctx, provider.StreamDoneEvent{Message: msg})
	}()
	return stream, nil
}

func TestRecordingProviderPersistsTurnWithoutAPIKey(t *testing.T) {
	store := newTestGatewayStore(t)
	recorder := &recordingProvider{
		inner: auditFakeProvider{}, store: store, runID: "run_1", sessionID: "session_1",
		providerID: 9, providerName: "fake",
	}
	stream, err := recorder.StreamCompletion(context.Background(), provider.CompletionRequest{
		Model: "model_1",
		Context: provider.LlmContext{
			SystemPrompt: "system",
			Messages: agentcore.MessageList{agentcore.UserMessage{
				RoleField: agentcore.RoleUser,
				Content:   agentcore.ContentList{agentcore.NewTextContent("question")},
			}},
		},
		Config: provider.StreamConfig{APIKey: "sk-must-not-be-stored"},
	})
	if err != nil {
		t.Fatal(err)
	}
	for range stream.Events() {
	}
	if _, err := stream.Result(context.Background()); err != nil {
		t.Fatal(err)
	}
	logs, err := store.ListRequestLogs(context.Background(), 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(logs) != 1 {
		t.Fatalf("logs = %#v", logs)
	}
	log := logs[0]
	if strings.Contains(log.RequestBody, "sk-must-not-be-stored") {
		t.Fatalf("API key leaked into request audit: %s", log.RequestBody)
	}
	if !strings.Contains(log.RequestBody, "question") || !strings.Contains(log.ResponseBody, "answer") {
		t.Fatalf("request/response missing: %#v", log)
	}
	if log.PromptTokens != 12 || log.CompletionTokens != 4 || log.StatusCode != 200 {
		t.Fatalf("usage/status = %#v", log)
	}
}
