package gateway

import (
	"context"
	"testing"
	"time"

	"github.com/alex6xu/jarvisserver/internal/agentcore"
	"github.com/alex6xu/jarvisserver/internal/session"
)

func TestLiveSessionWriterPersistsAndFinalizesStreamingConversation(t *testing.T) {
	store := newTestGatewayStore(t)
	now := time.Now().UTC()
	header := session.SessionHeader{
		ID: "session_live", CreatedAt: now, UpdatedAt: now, Model: "model-a", Type: sessionTypeChat,
	}
	if err := store.SaveEntries(header, nil); err != nil {
		t.Fatal(err)
	}
	if err := store.CreateChat(context.Background(), ChatExchange{
		ID: "chat_live", RunID: "run_live", SessionID: header.ID, Model: "model-a",
		RequestText: "question", CreatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	writer, err := newLiveSessionWriter(sessionHandle{store: store, header: header}, "model-a", "provider-a", "run_live", store)
	if err != nil {
		t.Fatal(err)
	}
	// Make the throttle deterministic: only the first update and forced boundary
	// writes should reach SQLite during this test.
	writer.interval = time.Hour

	user := agentcore.UserMessage{RoleField: agentcore.RoleUser, Content: agentcore.ContentList{agentcore.NewTextContent("question")}}
	if err := writer.PersistInitial(user); err != nil {
		t.Fatal(err)
	}
	_, entries, err := store.LoadEntries(header.ID)
	if err != nil || len(entries) != 1 || entries[0].Message.Role() != agentcore.RoleUser {
		t.Fatalf("initial entries=%+v err=%v", entries, err)
	}
	userEntryID := entries[0].ID

	empty := assistantMessage("")
	partial := assistantMessage("hel")
	complete := assistantMessage("hello")
	if err := writer.HandleEvent(agentcore.MessageStartEvent{Message: empty}); err != nil {
		t.Fatal(err)
	}
	if err := writer.HandleEvent(agentcore.MessageUpdateEvent{Message: partial}); err != nil {
		t.Fatal(err)
	}
	if err := writer.HandleEvent(agentcore.MessageUpdateEvent{Message: complete}); err != nil {
		t.Fatal(err)
	}
	_, entries, err = store.LoadEntries(header.ID)
	if err != nil || len(entries) != 2 {
		t.Fatalf("partial entries=%+v err=%v", entries, err)
	}
	assistantEntryID := entries[1].ID
	if got := assistantText(entries[1]); got != "hel" {
		t.Fatalf("throttled assistant=%q, want hel", got)
	}
	var response string
	if err := store.db.QueryRow(`SELECT response_text FROM chat_exchanges WHERE run_id = ?`, "run_live").Scan(&response); err != nil {
		t.Fatal(err)
	}
	if response != "hel" {
		t.Fatalf("checkpointed response=%q, want hel", response)
	}

	if err := writer.HandleEvent(agentcore.MessageEndEvent{Message: complete}); err != nil {
		t.Fatal(err)
	}
	toolResult := agentcore.ToolResultMessage{
		RoleField: agentcore.RoleToolResult, ToolCallID: "tool-1", ToolName: "read",
		Content: agentcore.ContentList{agentcore.NewTextContent("result")},
	}
	if err := writer.HandleEvent(agentcore.TurnEndEvent{Message: complete, ToolResults: []agentcore.ToolResultMessage{toolResult}}); err != nil {
		t.Fatal(err)
	}
	followUp := agentcore.UserMessage{RoleField: agentcore.RoleUser, Content: agentcore.ContentList{agentcore.NewTextContent("continue")}}
	writer.QueueMessages([]agentcore.AgentMessage{followUp})
	second := assistantMessage("done")
	if err := writer.HandleEvent(agentcore.MessageStartEvent{Message: assistantMessage("")}); err != nil {
		t.Fatal(err)
	}
	if err := writer.HandleEvent(agentcore.MessageEndEvent{Message: second}); err != nil {
		t.Fatal(err)
	}
	if err := writer.HandleEvent(agentcore.TurnEndEvent{Message: second}); err != nil {
		t.Fatal(err)
	}

	finalMessages := agentcore.MessageList{user, complete, toolResult, followUp, second}
	if err := writer.Finalize(finalMessages); err != nil {
		t.Fatal(err)
	}
	loaded, entries, err := store.LoadEntries(header.ID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Provider != "provider-a" || len(entries) != len(finalMessages) {
		t.Fatalf("loaded=%+v entries=%+v", loaded, entries)
	}
	if entries[0].ID != userEntryID || entries[1].ID != assistantEntryID {
		t.Fatalf("live entry ids changed after finalization: %q/%q -> %q/%q",
			userEntryID, assistantEntryID, entries[0].ID, entries[1].ID)
	}
	wantRoles := []string{agentcore.RoleUser, agentcore.RoleAssistant, agentcore.RoleToolResult, agentcore.RoleUser, agentcore.RoleAssistant}
	for i, want := range wantRoles {
		if got := entries[i].Message.Role(); got != want {
			t.Fatalf("entry[%d] role=%q, want %q", i, got, want)
		}
		if i > 0 && entries[i].ParentID != entries[i-1].ID {
			t.Fatalf("entry[%d] parent=%q, want %q", i, entries[i].ParentID, entries[i-1].ID)
		}
	}
	if got := assistantText(entries[1]); got != "hello" {
		t.Fatalf("final first assistant=%q", got)
	}
	if got := assistantText(entries[4]); got != "done" {
		t.Fatalf("final second assistant=%q", got)
	}
}

func assistantMessage(text string) agentcore.AssistantMessage {
	return agentcore.AssistantMessage{
		RoleField: agentcore.RoleAssistant,
		Content:   agentcore.ContentList{agentcore.NewTextContent(text)},
	}
}

func assistantText(entry session.Entry) string {
	message, _ := entry.Message.(agentcore.AssistantMessage)
	return agentcore.ContentToText(message.Content)
}
