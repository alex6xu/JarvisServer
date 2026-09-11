package gateway

import (
	"context"
	"path/filepath"
	"slices"
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
	for _, table := range []string{"sessions", "session_entries", "route_profiles", "tags", "account_tags", "message_tags", "projects", "session_projects", "workspace_metadata", "agent_profiles", "channel_bindings", "provider_endpoints", "provider_models", "route_policies", "route_policy_versions", "health_samples", "github_credentials", "github_oauth_states", "stock_sentiment_snapshots", "stock_news_sentiment_cache", "account_active_sessions"} {
		var found int
		if err := store.db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?`, table).Scan(&found); err != nil {
			t.Fatal(err)
		}
		if found != 1 {
			t.Errorf("table %s not created", table)
		}
	}
	var legacyTasks int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='agent_tasks'`).Scan(&legacyTasks); err != nil {
		t.Fatal(err)
	}
	if legacyTasks != 0 {
		t.Fatal("legacy agent_tasks table still exists")
	}
}

func TestMigrationReplacesLegacyDefaultRouteProfileModel(t *testing.T) {
	path := filepath.Join(t.TempDir(), "gateway.db")
	store, err := OpenGatewayStore(path)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := store.UpsertRouteProfile(ctx, RouteProfile{
		ID: "rp_1", Name: "default", Purpose: "general", Models: []string{"openrouter/free"},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec(`DELETE FROM schema_migrations WHERE version = 7`); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	store, err = OpenGatewayStore(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	profiles, err := store.ListRouteProfiles(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, profile := range profiles {
		if profile.ID != "rp_1" {
			continue
		}
		if len(profile.Models) != 1 || profile.Models[0] != "auto" {
			t.Fatalf("default route profile models = %v", profile.Models)
		}
		return
	}
	t.Fatal("default route profile not found")
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
	agentProfiles, err := restarted.Control.ListAgentProfiles(ctx)
	if err != nil || len(agentProfiles) != 2 {
		t.Fatalf("agent profiles=%#v err=%v", agentProfiles, err)
	}
	for _, agentProfile := range agentProfiles {
		if agentProfile.ID == "profile_chat" && !slices.Equal(agentProfile.Tools, []string{"memory_search", "websearch", "webfetch"}) {
			t.Fatalf("chat profile tools=%v", agentProfile.Tools)
		}
	}
}
