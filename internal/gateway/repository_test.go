package gateway

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/alex6xu/jarvisserver/internal/agentcore"
	"github.com/alex6xu/jarvisserver/internal/session"
)

func TestGatewayMigrationsApplied(t *testing.T) {
	store := newTestGatewayStore(t)
	var count, latest int
	if err := store.db.QueryRow(`SELECT COUNT(*), MAX(version) FROM schema_migrations`).Scan(&count, &latest); err != nil {
		t.Fatal(err)
	}
	if count != len(gatewayMigrations) || latest != gatewayMigrations[len(gatewayMigrations)-1].version {
		t.Fatalf("migrations count=%d latest=%d", count, latest)
	}
	for _, table := range []string{"sessions", "session_entries", "route_profiles", "agent_tasks", "tags", "workspace_metadata", "agent_profiles", "channel_bindings", "provider_endpoints", "provider_models", "route_policies", "route_policy_versions", "health_samples"} {
		var found int
		if err := store.db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?`, table).Scan(&found); err != nil {
			t.Fatal(err)
		}
		if found != 1 {
			t.Errorf("table %s not created", table)
		}
	}
}

func TestSQLiteSessionRepositoryRoundTrip(t *testing.T) {
	store := newTestGatewayStore(t)
	now := time.Now().UTC()
	header := session.SessionHeader{ID: "session_sqlite", CreatedAt: now, UpdatedAt: now, Model: "m", Cwd: t.TempDir()}
	messages := agentcore.MessageList{
		agentcore.UserMessage{RoleField: agentcore.RoleUser, Content: agentcore.ContentList{agentcore.NewTextContent("hello")}},
		agentcore.AssistantMessage{RoleField: agentcore.RoleAssistant, Content: agentcore.ContentList{agentcore.NewTextContent("world")}},
	}
	leaf, err := store.AppendBranch(header, "", messages)
	if err != nil {
		t.Fatal(err)
	}
	if leaf == "" {
		t.Fatal("empty leaf")
	}
	loaded, entries, err := store.LoadEntries(header.ID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Model != "m" || len(entries) != 2 || entries[1].ParentID != entries[0].ID {
		t.Fatalf("loaded=%+v entries=%+v", loaded, entries)
	}
	if got := agentcore.ContentToText(entries[1].Message.(agentcore.AssistantMessage).Content); got != "world" {
		t.Fatalf("assistant content = %q", got)
	}
}

func TestNewServiceImportsLegacyJSONLSessions(t *testing.T) {
	cwd := t.TempDir()
	legacy, err := session.NewStore(filepath.Join(cwd, ".jarvis", "sessions"))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	header := session.SessionHeader{ID: "legacy_session", CreatedAt: now, UpdatedAt: now, Model: "legacy", Cwd: cwd}
	if err := legacy.Save(header, agentcore.MessageList{
		agentcore.UserMessage{RoleField: agentcore.RoleUser, Content: agentcore.ContentList{agentcore.NewTextContent("import me")}},
	}); err != nil {
		t.Fatal(err)
	}
	svc, err := NewService(Options{Cwd: cwd, DatabasePath: filepath.Join(cwd, "gateway.db"), AdminPassword: "test-password", NoTools: true})
	if err != nil {
		t.Fatal(err)
	}
	defer svc.Close()
	_, entries, err := svc.Store.LoadEntries(header.ID)
	if err != nil || len(entries) != 1 {
		t.Fatalf("imported entries=%d err=%v", len(entries), err)
	}
}

func TestControlPlaneReloadsAfterRestart(t *testing.T) {
	cwd := t.TempDir()
	dbPath := filepath.Join(cwd, "gateway.db")
	opts := Options{Cwd: cwd, DatabasePath: dbPath, AdminPassword: "test-password", NoTools: true}
	svc, err := NewService(opts)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	profile := RouteProfile{ID: "rp_persist", Name: "persistent", Purpose: "code", Models: []string{"model-a"}}
	if err := svc.Control.UpsertRouteProfile(ctx, profile); err != nil {
		t.Fatal(err)
	}
	task := AgentTask{ID: "task_persist", WorkspaceID: "ws", Prompt: "do it", Status: "queued", ToolSteps: []ToolStep{}, CreatedAt: time.Now().UTC().Format(time.RFC3339Nano)}
	if err := svc.Control.UpsertAgentTask(ctx, task); err != nil {
		t.Fatal(err)
	}
	if err := svc.Close(); err != nil {
		t.Fatal(err)
	}

	restarted, err := NewService(opts)
	if err != nil {
		t.Fatal(err)
	}
	defer restarted.Close()
	if got := restarted.Mem.findProfileByName("persistent"); got == nil || got.Models[0] != "model-a" {
		t.Fatalf("reloaded profile = %#v", got)
	}
	tasks := restarted.Mem.listTasks()
	if len(tasks) != 1 || tasks[0].ID != task.ID {
		t.Fatalf("reloaded tasks = %#v", tasks)
	}
	agentProfiles, err := restarted.Control.ListAgentProfiles(ctx)
	if err != nil || len(agentProfiles) != 2 {
		t.Fatalf("agent profiles=%#v err=%v", agentProfiles, err)
	}
}
