package gateway

import (
	"context"
	"testing"
	"time"

	"github.com/alex6xu/jarvisserver/internal/agentcore"
	"github.com/alex6xu/jarvisserver/internal/session"
)

func TestWorkspaceSessionAutomaticallyCreatesProject(t *testing.T) {
	store := newTestGatewayStore(t)
	account, err := store.CreateAccount(context.Background(), "project-user", "", "user", "project-password")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := store.db.Exec(`INSERT INTO workspace_metadata(id,name,source,github_full_name,github_default_branch,created_at,updated_at,account_id) VALUES('ws-one','JarvisServer','github','alex/JarvisServer','main',?,?,?)`, now, now, account.ID); err != nil {
		t.Fatal(err)
	}
	header := session.SessionHeader{ID: "project-code-session", AccountID: account.ID, WorkspaceID: "ws-one", Type: sessionTypeCode, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}
	if _, err := store.AppendSessionEntry(header, "", agentcore.UserMessage{RoleField: agentcore.RoleUser, Content: agentcore.ContentList{agentcore.NewTextContent("修复 GitHub push")}}); err != nil {
		t.Fatal(err)
	}
	projects, err := store.ListProjects(context.Background(), account.ID)
	if err != nil || len(projects) != 1 {
		t.Fatalf("projects=%+v err=%v", projects, err)
	}
	if projects[0].Source != "workspace" || projects[0].LinkedWorkspaceID != "ws-one" || projects[0].Name != "alex/JarvisServer" {
		t.Fatalf("workspace project=%+v", projects[0])
	}
	assignment, err := store.SessionProject(context.Background(), account.ID, header.ID)
	if err != nil || assignment.Project.ID != projects[0].ID || assignment.Source != "workspace" || assignment.Pinned {
		t.Fatalf("assignment=%+v err=%v", assignment, err)
	}
}

func TestManualPinnedProjectOverridesWorkspaceReconciliation(t *testing.T) {
	store := newTestGatewayStore(t)
	account, err := store.CreateAccount(context.Background(), "manual-project-user", "", "user", "project-password")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := store.db.Exec(`INSERT INTO workspace_metadata(id,name,source,github_full_name,github_default_branch,created_at,updated_at,account_id) VALUES('ws-two','Workspace','local','','',?,?,?)`, now, now, account.ID); err != nil {
		t.Fatal(err)
	}
	header := session.SessionHeader{ID: "manual-project-session", AccountID: account.ID, WorkspaceID: "ws-two", Type: sessionTypeCode, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}
	if err := store.SaveEntries(header, nil); err != nil {
		t.Fatal(err)
	}
	manual, err := store.CreateProject(context.Background(), account.ID, "Custom Project", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.AssignSessionProject(context.Background(), account.ID, header.ID, manual.ID, "user", 1, true); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ReconcileWorkspaceProjects(context.Background(), account.ID); err != nil {
		t.Fatal(err)
	}
	assignment, err := store.SessionProject(context.Background(), account.ID, header.ID)
	if err != nil || assignment.Project.ID != manual.ID || !assignment.Pinned {
		t.Fatalf("assignment=%+v err=%v", assignment, err)
	}
}

func TestProjectTagsReturnsEmptySliceWhenProjectHasNoTags(t *testing.T) {
	store := newTestGatewayStore(t)
	account, err := store.CreateAccount(context.Background(), "empty-project-tags", "", "user", "project-password")
	if err != nil {
		t.Fatal(err)
	}
	project, err := store.CreateProject(context.Background(), account.ID, "No Tags", "")
	if err != nil {
		t.Fatal(err)
	}
	tags, err := store.ProjectTags(context.Background(), account.ID, project.ID, 12)
	if err != nil {
		t.Fatal(err)
	}
	if tags == nil || len(tags) != 0 {
		t.Fatalf("expected a non-nil empty tag slice, got %#v", tags)
	}
}

func TestProjectsAreAccountIsolatedAndAggregateTags(t *testing.T) {
	store := newTestGatewayStore(t)
	first, _ := store.CreateAccount(context.Background(), "project-first", "", "user", "password-one")
	second, _ := store.CreateAccount(context.Background(), "project-second", "", "user", "password-two")
	firstProject, err := store.CreateProject(context.Background(), first.ID, "Backend", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateProject(context.Background(), second.ID, "Private", ""); err != nil {
		t.Fatal(err)
	}
	header := session.SessionHeader{ID: "project-chat", AccountID: first.ID, Type: sessionTypeChat, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}
	if _, err := store.AppendSessionEntry(header, "", agentcore.UserMessage{RoleField: agentcore.RoleUser, Content: agentcore.ContentList{agentcore.NewTextContent("Go 后端 SQLite API 开发")}}); err != nil {
		t.Fatal(err)
	}
	if err := store.AssignSessionProject(context.Background(), first.ID, header.ID, firstProject.ID, "user", 1, true); err != nil {
		t.Fatal(err)
	}
	projects, err := store.ListProjects(context.Background(), first.ID)
	if err != nil || len(projects) != 1 || projects[0].SessionCount != 1 || projects[0].MessageCount != 1 {
		t.Fatalf("projects=%+v err=%v", projects, err)
	}
	tags, err := store.ProjectTags(context.Background(), first.ID, firstProject.ID, 20)
	if err != nil || len(tags) == 0 {
		t.Fatalf("tags=%+v err=%v", tags, err)
	}
	if _, err := store.ProjectByID(context.Background(), second.ID, firstProject.ID); err == nil {
		t.Fatal("second account read first project")
	}
	if err := store.AssignSessionProject(context.Background(), second.ID, header.ID, firstProject.ID, "user", 1, true); err == nil {
		t.Fatal("second account assigned foreign session/project")
	}
}
