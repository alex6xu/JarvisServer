package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/alex6xu/jarvisserver/internal/agentcore"
	"github.com/alex6xu/jarvisserver/internal/session"
	"github.com/zeromicro/go-zero/rest/pathvar"
)

func TestRunMessageQueuePersistsFIFOAndPinnedOrder(t *testing.T) {
	store := newTestGatewayStore(t)
	manager := NewRunManager(store)
	state, err := manager.Register("queue-session", "m", "", func() {})
	if err != nil {
		t.Fatal(err)
	}
	for _, input := range []QueueMessageInput{
		{Content: "A", EventType: queueEventEnqueue, IdempotencyKey: "a"},
		{Content: "B", EventType: queueEventEnqueue, IdempotencyKey: "b"},
		{Content: "C", EventType: queueEventPin, IdempotencyKey: "c"},
	} {
		if _, _, err := state.QueueMessage(input); err != nil {
			t.Fatal(err)
		}
	}
	loaded, err := store.LoadRunMessageQueue(context.Background(), state.ID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Version != 3 || len(loaded.Items) != 3 {
		t.Fatalf("loaded queue = %+v", loaded)
	}
	for index, want := range []string{"C", "A", "B"} {
		if got := loaded.Items[index].Content; got != want {
			t.Fatalf("item %d = %q, want %q", index, got, want)
		}
		if loaded.Items[index].Position != index {
			t.Fatalf("item %d position = %d", index, loaded.Items[index].Position)
		}
	}
	state.Finish(nil)
}

func TestRunMessageQueueSteerAndFollowUpUseSeparateBoundaries(t *testing.T) {
	state, err := NewRunManager().Register("queue-session", "m", "", func() {})
	if err != nil {
		t.Fatal(err)
	}
	for _, input := range []QueueMessageInput{
		{Content: "A", EventType: queueEventEnqueue},
		{Content: "change direction", EventType: queueEventSteer},
		{Content: "B", EventType: queueEventEnqueue},
	} {
		if _, _, err := state.QueueMessage(input); err != nil {
			t.Fatal(err)
		}
	}
	assertAgentMessageTexts(t, state.DrainSteeringMessages(), []string{"change direction"})
	assertAgentMessageTexts(t, state.DrainFollowUpMessages(), []string{"A", "B"})
	state.Finish(nil)

	for _, item := range state.QueueSnapshot().Items {
		if item.Status != queueStatusCompleted {
			t.Fatalf("item %q status = %q, want completed", item.Content, item.Status)
		}
	}
}

func TestRunMessageQueueIdempotencyAndVersionedReorder(t *testing.T) {
	state, err := NewRunManager().Register("queue-session", "m", "", func() {})
	if err != nil {
		t.Fatal(err)
	}
	first, snapshot, err := state.QueueMessage(QueueMessageInput{
		Content: "A", EventType: queueEventEnqueue, IdempotencyKey: "same-request",
	})
	if err != nil {
		t.Fatal(err)
	}
	repeated, repeatedSnapshot, err := state.QueueMessage(QueueMessageInput{
		Content: "A", EventType: queueEventEnqueue, IdempotencyKey: "same-request",
	})
	if err != nil {
		t.Fatal(err)
	}
	if repeated.ID != first.ID || repeatedSnapshot.Version != snapshot.Version || len(repeatedSnapshot.Items) != 1 {
		t.Fatalf("idempotent result = %+v / %+v", repeated, repeatedSnapshot)
	}
	second, snapshot, err := state.QueueMessage(QueueMessageInput{Content: "B", EventType: queueEventEnqueue})
	if err != nil {
		t.Fatal(err)
	}
	reordered, err := state.ReorderQueueMessages(snapshot.Version, []string{second.ID, first.ID})
	if err != nil {
		t.Fatal(err)
	}
	if reordered.Items[0].ID != second.ID || reordered.Version != snapshot.Version+1 {
		t.Fatalf("reordered queue = %+v", reordered)
	}
	if _, err := state.ReorderQueueMessages(snapshot.Version, []string{first.ID, second.ID}); !errors.Is(err, ErrRunQueueVersionConflict) {
		t.Fatalf("stale reorder error = %v", err)
	}
	state.Finish(nil)
}

func TestRunMessageQueueCancelDropsPendingItems(t *testing.T) {
	manager := NewRunManager()
	runCtx, cancel := context.WithCancel(context.Background())
	state, err := manager.Register("queue-session", "m", "", cancel)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := state.QueueMessage(QueueMessageInput{Content: "A"}); err != nil {
		t.Fatal(err)
	}
	if !manager.Cancel(state.ID) {
		t.Fatal("cancel failed")
	}
	if got := state.QueueSnapshot().Items[0].Status; got != queueStatusDropped {
		t.Fatalf("cancelled run queue status = %q", got)
	}
	if _, _, err := state.QueueMessage(QueueMessageInput{Content: "late"}); !errors.Is(err, ErrRunQueueClosed) {
		t.Fatalf("late enqueue error = %v", err)
	}
	state.Finish(runCtx.Err())
}

func TestRunMessageQueueHandlersEnforceOwnershipAndConflicts(t *testing.T) {
	store := newTestGatewayStore(t)
	now := time.Now().UTC()
	header := session.SessionHeader{ID: "owned-queue-session", AccountID: 21, Type: sessionTypeCode, CreatedAt: now, UpdatedAt: now}
	if err := store.SaveEntries(header, nil); err != nil {
		t.Fatal(err)
	}
	manager := NewRunManager(store)
	state, err := manager.Register(header.ID, "m", "", func() {})
	if err != nil {
		t.Fatal(err)
	}
	service := &Service{Runs: manager, Store: store}

	foreign := queueHandlerRequest(http.MethodGet, state.ID, "", nil, 22)
	foreignRes := httptest.NewRecorder()
	service.handleGetRunMessageQueue(foreignRes, foreign)
	if foreignRes.Code != http.StatusNotFound {
		t.Fatalf("foreign queue status = %d", foreignRes.Code)
	}

	body, _ := json.Marshal(queueMessageRequest{Content: "queued", EventType: queueEventEnqueue, IdempotencyKey: "api-request"})
	owner := queueHandlerRequest(http.MethodPost, state.ID, "", body, 21)
	ownerRes := httptest.NewRecorder()
	service.handlePostRunMessageQueue(ownerRes, owner)
	if ownerRes.Code != http.StatusCreated {
		t.Fatalf("owner enqueue status = %d, body = %s", ownerRes.Code, ownerRes.Body.String())
	}

	staleBody, _ := json.Marshal(reorderQueueRequest{Version: 0, Order: []string{state.QueueSnapshot().Items[0].ID}})
	stale := queueHandlerRequest(http.MethodPut, state.ID, "", staleBody, 21)
	staleRes := httptest.NewRecorder()
	service.handleReorderRunMessageQueue(staleRes, stale)
	if staleRes.Code != http.StatusConflict {
		t.Fatalf("stale reorder status = %d, body = %s", staleRes.Code, staleRes.Body.String())
	}
	state.Finish(nil)
}

func assertAgentMessageTexts(t *testing.T, messages []agentcore.AgentMessage, wants []string) {
	t.Helper()
	if len(messages) != len(wants) {
		t.Fatalf("message count = %d, want %d", len(messages), len(wants))
	}
	for index, want := range wants {
		message, ok := messages[index].(agentcore.UserMessage)
		if !ok || agentcore.ContentToText(message.Content) != want {
			t.Fatalf("message %d = %#v, want %q", index, messages[index], want)
		}
	}
}

func queueHandlerRequest(method, runID, messageID string, body []byte, accountID int) *http.Request {
	request := httptest.NewRequest(method, "/v1/agent/runs/"+runID+"/messages/queue", bytes.NewReader(body))
	request = request.WithContext(context.WithValue(request.Context(), accountContextKey{}, Account{ID: accountID}))
	vars := map[string]string{"runId": runID}
	if messageID != "" {
		vars["messageId"] = messageID
	}
	return pathvar.WithVars(request, vars)
}
