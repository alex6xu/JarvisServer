package gateway

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/alex6xu/jarvisserver/internal/agentcore"
	"github.com/alex6xu/jarvisserver/internal/session"
)

func newWorktreeTestService(t *testing.T) (*Service, string) {
	t.Helper()
	root := t.TempDir()
	svc, err := NewService(Options{
		Approve: true, NoTools: true, Cwd: root, WorkspacesRoot: filepath.Join(root, "workspaces"),
		AdminPassword: "test-password",
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = svc.Close() })
	workspaceID := "ws_test"
	dir, err := svc.workspaceDir(workspaceID)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "main.txt"), []byte("base\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := writeWorkspaceMetadata(dir, workspaceMetadataFile{Name: "test"}); err != nil {
		t.Fatal(err)
	}
	info, err := svc.workspaceInfo(workspaceID)
	if err != nil {
		t.Fatal(err)
	}
	info.AccountID = legacyWorkspaceAccountID
	if err := svc.Control.UpsertWorkspace(context.Background(), info); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	header := session.SessionHeader{
		ID: session.NewID(now), CreatedAt: now, UpdatedAt: now, Cwd: dir,
		AccountID: legacyWorkspaceAccountID, WorkspaceID: workspaceID,
	}
	entries := []session.Entry{{
		ID: "message-1", Timestamp: now,
		Message: agentcore.UserMessage{RoleField: agentcore.RoleUser, Content: agentcore.ContentList{agentcore.NewTextContent("change it")}},
	}}
	if err := svc.Store.SaveEntries(header, entries); err != nil {
		t.Fatal(err)
	}
	return svc, header.ID
}

func TestForkSessionCreatesIsolatedWorktreeAndMerges(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is unavailable")
	}
	svc, sourceID := newWorktreeTestService(t)
	resp, err := svc.ForkSession(context.Background(), sourceID, ForkSessionRequest{}, legacyWorkspaceAccountID)
	if err != nil {
		t.Fatal(err)
	}
	header, entries, err := svc.Store.LoadEntries(resp.Session.ID)
	if err != nil {
		t.Fatal(err)
	}
	if header.ParentSession != sourceID || header.WorktreeBranch == "" || header.WorktreeBaseCommit == "" || len(entries) != 1 {
		t.Fatalf("unexpected fork header/entries: %+v, %d", header, len(entries))
	}
	if err := os.WriteFile(filepath.Join(header.Cwd, "main.txt"), []byte("from branch\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	primary, _ := svc.workspaceDir(resp.WorkspaceID)
	before, _ := os.ReadFile(filepath.Join(primary, "main.txt"))
	if string(before) != "base\n" {
		t.Fatalf("branch write leaked into primary: %q", before)
	}
	diff, err := svc.SessionDiff(context.Background(), header.ID, legacyWorkspaceAccountID)
	if err != nil || !strings.Contains(diff, "main.txt") {
		t.Fatalf("diff = %q, err = %v", diff, err)
	}
	merged, err := svc.MergeSession(context.Background(), header.ID, legacyWorkspaceAccountID)
	if err != nil {
		t.Fatal(err)
	}
	if merged.ChangedFiles != 1 {
		t.Fatalf("changed files = %d, want 1", merged.ChangedFiles)
	}
	after, _ := os.ReadFile(filepath.Join(primary, "main.txt"))
	if string(after) != "from branch\n" {
		t.Fatalf("merged primary = %q", after)
	}
}

func TestMergeSessionRejectsConflictWithoutChangingPrimary(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is unavailable")
	}
	svc, sourceID := newWorktreeTestService(t)
	resp, err := svc.ForkSession(context.Background(), sourceID, ForkSessionRequest{}, legacyWorkspaceAccountID)
	if err != nil {
		t.Fatal(err)
	}
	header, _, _ := svc.Store.LoadEntries(resp.Session.ID)
	if err := os.WriteFile(filepath.Join(header.Cwd, "main.txt"), []byte("branch\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	primary, _ := svc.workspaceDir(resp.WorkspaceID)
	if err := os.WriteFile(filepath.Join(primary, "main.txt"), []byte("primary\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.MergeSession(context.Background(), header.ID, legacyWorkspaceAccountID); err == nil {
		t.Fatal("expected merge conflict")
	}
	after, _ := os.ReadFile(filepath.Join(primary, "main.txt"))
	if string(after) != "primary\n" {
		t.Fatalf("conflict changed primary to %q", after)
	}
}

func TestForkOfForkSnapshotsParentWorktree(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is unavailable")
	}
	svc, sourceID := newWorktreeTestService(t)
	parent, err := svc.ForkSession(context.Background(), sourceID, ForkSessionRequest{}, legacyWorkspaceAccountID)
	if err != nil {
		t.Fatal(err)
	}
	parentHeader, _, _ := svc.Store.LoadEntries(parent.Session.ID)
	if err := os.WriteFile(filepath.Join(parentHeader.Cwd, "main.txt"), []byte("parent branch\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	child, err := svc.ForkSession(context.Background(), parent.Session.ID, ForkSessionRequest{}, legacyWorkspaceAccountID)
	if err != nil {
		t.Fatal(err)
	}
	childHeader, _, _ := svc.Store.LoadEntries(child.Session.ID)
	content, err := os.ReadFile(filepath.Join(childHeader.Cwd, "main.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "parent branch\n" {
		t.Fatalf("child did not inherit parent worktree: %q", content)
	}
	if childHeader.WorktreeBaseCommit != parentHeader.WorktreeBaseCommit {
		t.Fatalf("child merge base = %q, want %q", childHeader.WorktreeBaseCommit, parentHeader.WorktreeBaseCommit)
	}
}

func TestSessionOwnershipAppliesToReadAndFork(t *testing.T) {
	svc, sourceID := newWorktreeTestService(t)
	if _, err := svc.getSessionForAccount(sourceID, 2); err == nil {
		t.Fatal("another account read the session")
	}
	listed, err := svc.listSessionsForAccount(2)
	if err != nil {
		t.Fatal(err)
	}
	if len(listed.Sessions) != 0 {
		t.Fatalf("another account listed %d sessions", len(listed.Sessions))
	}
	if _, err := svc.ForkSession(context.Background(), sourceID, ForkSessionRequest{}, 2); err == nil {
		t.Fatal("another account forked the session")
	}
}
