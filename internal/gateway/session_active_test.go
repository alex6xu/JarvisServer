package gateway

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/alex6xu/jarvisserver/internal/agentcore"
	"github.com/alex6xu/jarvisserver/internal/session"
)

func saveScopedSession(t *testing.T, svc *Service, id string, accountID int, workspaceID string, updatedAt time.Time) {
	t.Helper()
	header := session.SessionHeader{
		ID: id, AccountID: accountID, WorkspaceID: workspaceID, Cwd: svc.Opts.Cwd,
		CreatedAt: updatedAt.Add(-time.Minute), UpdatedAt: updatedAt,
	}
	entries := []session.Entry{{
		ID: id + "-message", Timestamp: updatedAt,
		Message: agentcore.UserMessage{
			RoleField: agentcore.RoleUser,
			Content:   agentcore.ContentList{agentcore.NewTextContent("message " + id)},
		},
	}}
	if err := svc.Store.SaveEntries(header, entries); err != nil {
		t.Fatal(err)
	}
}

func TestActiveSessionIsScopedByAccountModeAndWorkspace(t *testing.T) {
	root := t.TempDir()
	svc, err := NewService(Options{Cwd: root, DatabasePath: root + "/gateway.db", AdminPassword: "test-password", NoTools: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = svc.Close() })
	ctx := context.Background()
	other, err := svc.Audit.CreateAccount(ctx, "other", "other@example.test", "user", "test-password")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	saveScopedSession(t, svc, "chat-owner", legacyWorkspaceAccountID, "", now)
	saveScopedSession(t, svc, "chat-other", other.ID, "", now)
	saveScopedSession(t, svc, "coder-one", legacyWorkspaceAccountID, "workspace-one", now)
	saveScopedSession(t, svc, "coder-two", legacyWorkspaceAccountID, "workspace-two", now)

	if err := svc.setActiveSession(ctx, legacyWorkspaceAccountID, "chat", "", "chat-owner"); err != nil {
		t.Fatal(err)
	}
	if err := svc.setActiveSession(ctx, legacyWorkspaceAccountID, "coder", "workspace-one", "coder-one"); err != nil {
		t.Fatal(err)
	}
	if err := svc.setActiveSession(ctx, legacyWorkspaceAccountID, "coder", "workspace-two", "coder-two"); err != nil {
		t.Fatal(err)
	}

	chat, found, err := svc.getActiveSession(ctx, legacyWorkspaceAccountID, "chat", "")
	if err != nil || !found || chat.SessionID != "chat-owner" {
		t.Fatalf("chat active = %+v, found=%v, err=%v", chat, found, err)
	}
	for workspaceID, want := range map[string]string{"workspace-one": "coder-one", "workspace-two": "coder-two"} {
		got, ok, getErr := svc.getActiveSession(ctx, legacyWorkspaceAccountID, "coder", workspaceID)
		if getErr != nil || !ok || got.SessionID != want {
			t.Fatalf("coder %s active = %+v, found=%v, err=%v", workspaceID, got, ok, getErr)
		}
	}
	latestCoder, found, err := svc.getActiveSession(ctx, legacyWorkspaceAccountID, "coder", "")
	if err != nil || !found || latestCoder.SessionID != "coder-two" || latestCoder.WorkspaceID != "workspace-two" {
		t.Fatalf("latest coder active = %+v, found=%v, err=%v", latestCoder, found, err)
	}
	if _, found, err := svc.getActiveSession(ctx, other.ID, "chat", ""); err != nil || found {
		t.Fatalf("other account found=%v err=%v", found, err)
	}
	if err := svc.setActiveSession(ctx, legacyWorkspaceAccountID, "chat", "", "chat-other"); err == nil {
		t.Fatal("another account's session was accepted")
	}
	if err := svc.setActiveSession(ctx, legacyWorkspaceAccountID, "coder", "workspace-two", "coder-one"); err == nil {
		t.Fatal("a session from another workspace was accepted")
	}
	if err := svc.clearActiveSession(ctx, legacyWorkspaceAccountID, "chat", "", "another-session"); err != nil {
		t.Fatal(err)
	}
	if _, found, err := svc.getActiveSession(ctx, legacyWorkspaceAccountID, "chat", ""); err != nil || !found {
		t.Fatalf("conditional clear removed a newer session: found=%v err=%v", found, err)
	}
	if err := svc.clearActiveSession(ctx, legacyWorkspaceAccountID, "chat", "", "chat-owner"); err != nil {
		t.Fatal(err)
	}
	if _, found, err := svc.getActiveSession(ctx, legacyWorkspaceAccountID, "chat", ""); err != nil || found {
		t.Fatalf("active chat remained after clear: found=%v err=%v", found, err)
	}
}

func TestActiveSessionRemovesInvalidPointer(t *testing.T) {
	root := t.TempDir()
	svc, err := NewService(Options{Cwd: root, DatabasePath: root + "/gateway.db", AdminPassword: "test-password", NoTools: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = svc.Close() })
	now := time.Now().UTC()
	saveScopedSession(t, svc, "coder-session", legacyWorkspaceAccountID, "workspace-one", now)
	if _, err := svc.Audit.db.Exec(`
INSERT INTO account_active_sessions(account_id, mode, workspace_id, session_id, updated_at)
VALUES (?, 'chat', '', ?, ?)`, legacyWorkspaceAccountID, "coder-session", now.Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}

	if _, found, err := svc.getActiveSession(context.Background(), legacyWorkspaceAccountID, "chat", ""); err != nil || found {
		t.Fatalf("invalid pointer found=%v err=%v", found, err)
	}
	var count int
	if err := svc.Audit.db.QueryRow(`SELECT COUNT(*) FROM account_active_sessions`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("invalid pointer was not removed: count=%d", count)
	}
}

func TestRecentSessionsAreFilteredSortedAndCapped(t *testing.T) {
	root := t.TempDir()
	svc, err := NewService(Options{Cwd: root, DatabasePath: root + "/gateway.db", AdminPassword: "test-password", NoTools: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = svc.Close() })
	base := time.Now().UTC().Add(-time.Hour)
	for i := 0; i < 12; i++ {
		saveScopedSession(t, svc, "chat-"+string(rune('a'+i)), legacyWorkspaceAccountID, "", base.Add(time.Duration(i)*time.Minute))
	}
	saveScopedSession(t, svc, "coder-newest", legacyWorkspaceAccountID, "workspace-one", base.Add(2*time.Hour))

	resp, err := svc.recentSessionsForAccount(legacyWorkspaceAccountID, "chat", "", 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Sessions) != 10 {
		t.Fatalf("recent count=%d, want 10", len(resp.Sessions))
	}
	if resp.Sessions[0].ID != "chat-l" || resp.Sessions[9].ID != "chat-c" {
		t.Fatalf("unexpected order: first=%s last=%s", resp.Sessions[0].ID, resp.Sessions[9].ID)
	}
	for _, item := range resp.Sessions {
		if item.Platform != "chat" || item.WorkspaceID != "" {
			t.Fatalf("scope leaked into recent sessions: %+v", item)
		}
	}
}

func TestActiveAndRecentRoutesPrecedeSessionParameter(t *testing.T) {
	svc := &Service{}
	routes := apiRoutes(svc)
	indices := map[string]int{}
	for i, route := range routes {
		if route.Method == http.MethodGet {
			indices[route.Path] = i
		}
	}
	param := indices["/v1/agent/sessions/:sessionId"]
	for _, path := range []string{"/v1/agent/sessions/active", "/v1/agent/sessions/recent"} {
		if index, ok := indices[path]; !ok || index >= param {
			t.Fatalf("static route %q index=%d must precede session parameter index=%d", path, index, param)
		}
	}
}

func TestSessionTypeFilteringSupportsExplicitAndLegacySessions(t *testing.T) {
	root := t.TempDir()
	svc, err := NewService(Options{Cwd: root, DatabasePath: root + "/gateway.db", AdminPassword: "test-password", NoTools: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = svc.Close() })
	now := time.Now().UTC()
	saveScopedSession(t, svc, "legacy-chat", legacyWorkspaceAccountID, "", now)
	saveScopedSession(t, svc, "legacy-code", legacyWorkspaceAccountID, "workspace-one", now.Add(time.Minute))
	typedCode := session.SessionHeader{
		ID: "typed-code-no-workspace", AccountID: legacyWorkspaceAccountID, Type: sessionTypeCode,
		Cwd: root, CreatedAt: now, UpdatedAt: now.Add(2 * time.Minute),
	}
	if err := svc.Store.SaveEntries(typedCode, nil); err != nil {
		t.Fatal(err)
	}

	chat, err := svc.listSessionsForAccount(legacyWorkspaceAccountID, sessionTypeChat)
	if err != nil || len(chat.Sessions) != 1 || chat.Sessions[0].ID != "legacy-chat" {
		t.Fatalf("chat sessions = %+v, err=%v", chat.Sessions, err)
	}
	code, err := svc.listSessionsForAccount(legacyWorkspaceAccountID, sessionTypeCode)
	if err != nil || len(code.Sessions) != 2 {
		t.Fatalf("code sessions = %+v, err=%v", code.Sessions, err)
	}
	for _, item := range code.Sessions {
		if item.Type != sessionTypeCode || item.Platform != "coder" {
			t.Fatalf("code metadata = %+v", item)
		}
	}
	if _, err := svc.listSessionsForAccount(legacyWorkspaceAccountID, "invalid"); err == nil {
		t.Fatal("invalid session type filter was accepted")
	}
	if _, _, _, err := svc.openSession(typedCode.ID, "", root, "", sessionTypeChat, legacyWorkspaceAccountID); err == nil {
		t.Fatal("chat page resumed an explicit code session")
	}
	if _, handle, _, err := svc.openSession(typedCode.ID, "", root, "", sessionTypeCode, legacyWorkspaceAccountID); err != nil || handle.header.Type != sessionTypeCode {
		t.Fatalf("code page resume = %+v, err=%v", handle.header, err)
	}
}
