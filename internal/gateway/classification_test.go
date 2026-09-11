package gateway

import (
	"context"
	"testing"
	"time"

	"github.com/alex6xu/jarvisserver/internal/agentcore"
	"github.com/alex6xu/jarvisserver/internal/session"
)

func TestLocalClassifierMatchesTechnicalTopics(t *testing.T) {
	matches := classifyTextLocally("GitHub git push 权限失败，如何配置 SSH key 并修复部署？")
	seen := map[string]bool{}
	for _, match := range matches {
		seen[match.Rule.Slug] = true
		if match.Confidence < 0.60 || len(match.Evidence) == 0 {
			t.Fatalf("invalid match: %+v", match)
		}
	}
	for _, want := range []string{"github", "authentication", "debugging", "deployment"} {
		if !seen[want] {
			t.Fatalf("missing %q in %+v", want, matches)
		}
	}
}

func TestClassificationPersistsAndIsAccountIsolated(t *testing.T) {
	store := newTestGatewayStore(t)
	now := time.Now().UTC()
	firstAccount, err := store.CreateAccount(context.Background(), "classifier-one", "", "user", "classifier-password")
	if err != nil {
		t.Fatal(err)
	}
	first := session.SessionHeader{ID: "classify-first", AccountID: firstAccount.ID, Type: sessionTypeChat, CreatedAt: now, UpdatedAt: now}
	secondAccount, err := store.CreateAccount(context.Background(), "classifier-two", "", "user", "classifier-password")
	if err != nil {
		t.Fatal(err)
	}
	second := session.SessionHeader{ID: "classify-second", AccountID: secondAccount.ID, Type: sessionTypeChat, CreatedAt: now, UpdatedAt: now}

	message := func(text string) agentcore.UserMessage {
		return agentcore.UserMessage{RoleField: agentcore.RoleUser, Content: agentcore.ContentList{agentcore.NewTextContent(text)}}
	}
	firstEntry, err := store.AppendSessionEntry(first, "", message("GitHub OAuth 登录失败，需要排查 token 权限"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.AppendSessionEntry(second, "", message("Docker 容器部署失败")); err != nil {
		t.Fatal(err)
	}

	firstTags, err := store.ListAccountTags(context.Background(), firstAccount.ID, "", 80)
	if err != nil || len(firstTags) == 0 {
		t.Fatalf("first tags=%+v err=%v", firstTags, err)
	}
	secondTags, err := store.ListAccountTags(context.Background(), secondAccount.ID, "", 80)
	if err != nil || len(secondTags) == 0 {
		t.Fatalf("second tags=%+v err=%v", secondTags, err)
	}
	for _, tag := range secondTags {
		if tag.Slug == "github" {
			t.Fatalf("second account leaked first account tag: %+v", secondTags)
		}
	}

	github, err := store.AccountTagBySlug(context.Background(), firstAccount.ID, "github")
	if err != nil || github.UseCount != 1 {
		t.Fatalf("github tag=%+v err=%v", github, err)
	}
	messages, err := store.TaggedMessages(context.Background(), firstAccount.ID, "github", 10)
	if err != nil || len(messages) != 1 || messages[0].MessageID != firstEntry.ID || messages[0].SessionID != first.ID {
		t.Fatalf("tagged messages=%+v err=%v", messages, err)
	}
	foreign, err := store.TaggedMessages(context.Background(), secondAccount.ID, "github", 10)
	if err != nil || len(foreign) != 0 {
		t.Fatalf("foreign messages=%+v err=%v", foreign, err)
	}
}

func TestRetagIsIdempotent(t *testing.T) {
	store := newTestGatewayStore(t)
	account, err := store.CreateAccount(context.Background(), "retag-user", "", "user", "retag-password")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	header := session.SessionHeader{ID: "retag-session", AccountID: account.ID, Type: sessionTypeCode, WorkspaceID: "ws", CreatedAt: now, UpdatedAt: now}
	entries := []session.Entry{{
		ID: "user-entry", Timestamp: now,
		Message: agentcore.UserMessage{RoleField: agentcore.RoleUser, Content: agentcore.ContentList{agentcore.NewTextContent("Go 后端 SQLite migration 测试")}},
	}}
	if err := store.SaveEntries(header, entries); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		classified, err := store.RetagAccountMessages(context.Background(), account.ID, 200)
		if err != nil || classified != 1 {
			t.Fatalf("run %d classified=%d err=%v", i, classified, err)
		}
	}
	var associations, distinct int
	if err := store.db.QueryRow(`SELECT COUNT(*), COUNT(DISTINCT tag_id) FROM message_tags WHERE session_id=? AND entry_id=?`, header.ID, "user-entry").Scan(&associations, &distinct); err != nil {
		t.Fatal(err)
	}
	if associations == 0 || associations != distinct {
		t.Fatalf("associations=%d distinct=%d", associations, distinct)
	}
}

func TestLocalClassificationMigrationExists(t *testing.T) {
	store := newTestGatewayStore(t)
	for _, table := range []string{"account_tags", "message_tags"} {
		var name string
		if err := store.db.QueryRow(`SELECT name FROM sqlite_master WHERE type='table' AND name=?`, table).Scan(&name); err != nil {
			t.Fatalf("missing table %s: %v", table, err)
		}
	}
}
