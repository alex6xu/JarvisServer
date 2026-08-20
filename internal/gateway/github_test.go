package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func newGitHubTestService(t *testing.T, upstream *httptest.Server, oauth bool) *Service {
	t.Helper()
	root := t.TempDir()
	opts := Options{
		Cwd: root, DatabasePath: filepath.Join(root, "gateway.db"), AdminPassword: "test-password", NoTools: true,
		GitHubAPIBaseURL: upstream.URL, GitHubWebBaseURL: upstream.URL, GitHubTokenKey: "test-encryption-key",
	}
	if oauth {
		opts.GitHubClientID = "client-id"
		opts.GitHubClientSecret = "client-secret"
	}
	svc, err := NewService(opts)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = svc.Close() })
	return svc
}

func TestGitHubPATIsValidatedAndEncrypted(t *testing.T) {
	const token = "github_pat_plaintext_must_not_be_stored"
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+token {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		switch r.URL.Path {
		case "/user":
			_ = json.NewEncoder(w).Encode(map[string]any{"id": 7, "login": "octocat"})
		case "/user/repos":
			_ = json.NewEncoder(w).Encode([]map[string]any{{
				"id": 9, "name": "hello", "full_name": "octocat/hello",
				"owner": map[string]string{"login": "octocat"}, "default_branch": "main",
			}})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(upstream.Close)
	svc := newGitHubTestService(t, upstream, false)

	user, err := svc.GitHub.connectToken(context.Background(), 1, token)
	if err != nil || user.Login != "octocat" {
		t.Fatalf("connectToken user=%+v err=%v", user, err)
	}
	credential, err := svc.Audit.GitHubCredential(context.Background(), 1)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(credential.TokenCipher, token) || credential.TokenCipher == token {
		t.Fatal("github token was stored in plaintext")
	}
	_, decrypted, err := svc.GitHub.credential(context.Background(), 1)
	if err != nil || decrypted != token {
		t.Fatalf("decrypted token=%q err=%v", decrypted, err)
	}
	repos, err := svc.GitHub.repositories(context.Background(), 1, 50)
	if err != nil || len(repos) != 1 || repos[0].Owner != "octocat" {
		t.Fatalf("repos=%+v err=%v", repos, err)
	}
}

func TestGitHubOAuthStateIsAccountBoundAndSingleUse(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/login/oauth/access_token":
			if err := r.ParseForm(); err != nil || r.Form.Get("client_secret") != "client-secret" {
				http.Error(w, "bad request", http.StatusBadRequest)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]string{"access_token": "oauth-token"})
		case "/user":
			_ = json.NewEncoder(w).Encode(map[string]any{"id": 8, "login": "hubot"})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(upstream.Close)
	svc := newGitHubTestService(t, upstream, true)

	authorizeRaw, err := svc.GitHub.authorizeURL(context.Background(), 1, "/settings")
	if err != nil {
		t.Fatal(err)
	}
	authorizeURL, err := url.Parse(authorizeRaw)
	if err != nil {
		t.Fatal(err)
	}
	state := authorizeURL.Query().Get("state")
	if state == "" || authorizeURL.Query().Get("client_id") != "client-id" {
		t.Fatalf("authorize URL=%s", authorizeRaw)
	}
	accountID, returnPath, user, err := svc.GitHub.exchangeOAuth(context.Background(), "code", state)
	if err != nil || accountID != 1 || returnPath != "/settings" || user.Login != "hubot" {
		t.Fatalf("exchange account=%d return=%q user=%+v err=%v", accountID, returnPath, user, err)
	}
	if _, _, _, err := svc.GitHub.exchangeOAuth(context.Background(), "code", state); err == nil {
		t.Fatal("oauth state replay unexpectedly succeeded")
	}
}

func TestGitHubOAuthStateExpiry(t *testing.T) {
	store := newTestGatewayStore(t)
	account, err := store.CreateAccount(context.Background(), "oauth-user", "", "user", "oauth-password")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.CreateGitHubOAuthState(context.Background(), "expired", account.ID, "/code", time.Now().Add(-time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.ConsumeGitHubOAuthState(context.Background(), "expired", time.Now()); err == nil {
		t.Fatal("expired state unexpectedly succeeded")
	}
}

func TestGitHubNamesAndBranchesRejectGitOptionInjection(t *testing.T) {
	for _, value := range []string{"owner", "repo.name", "under_score"} {
		if !validGitHubName(value) {
			t.Errorf("validGitHubName(%q)=false", value)
		}
	}
	for _, value := range []string{"-upload-pack=evil", "owner/repo", ".."} {
		if validGitHubName(value) {
			t.Errorf("validGitHubName(%q)=true", value)
		}
	}
	for _, branch := range []string{"main", "feature/github-settings"} {
		if !validGitBranch(branch) {
			t.Errorf("validGitBranch(%q)=false", branch)
		}
	}
	for _, branch := range []string{"-main", "../escape", "feature..bad", "bad:ref"} {
		if validGitBranch(branch) {
			t.Errorf("validGitBranch(%q)=true", branch)
		}
	}
}

func TestRunGitWithSanitizedConfiguration(t *testing.T) {
	dir := t.TempDir()
	github := &GitHubService{gitTimeout: time.Minute}
	if _, err := github.runGit(context.Background(), dir, "", "init", "--initial-branch=main"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, ".git")); err != nil {
		t.Fatalf("git repository was not initialized: %v", err)
	}
}

func TestGitHubImportAndPushAgainstLocalRemote(t *testing.T) {
	remoteRoot := t.TempDir()
	bare := filepath.Join(remoteRoot, "octocat", "hello.git")
	if err := os.MkdirAll(filepath.Dir(bare), 0o755); err != nil {
		t.Fatal(err)
	}
	runTestGit(t, "", "init", "--bare", "--initial-branch=main", bare)
	seed := t.TempDir()
	runTestGit(t, seed, "init", "--initial-branch=main")
	if err := os.WriteFile(filepath.Join(seed, "README.md"), []byte("initial\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runTestGit(t, seed, "add", "README.md")
	runTestGit(t, seed, "-c", "user.name=seed", "-c", "user.email=seed@example.com", "commit", "-m", "initial")
	runTestGit(t, seed, "remote", "add", "origin", bare)
	runTestGit(t, seed, "push", "origin", "main")

	const token = "github_pat_local_remote"
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+token {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		switch r.URL.Path {
		case "/user":
			_ = json.NewEncoder(w).Encode(map[string]any{"id": 7, "login": "octocat"})
		case "/repos/octocat/hello":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id": 9, "name": "hello", "full_name": "octocat/hello",
				"owner": map[string]string{"login": "octocat"}, "default_branch": "main",
			})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(upstream.Close)

	filePath := filepath.ToSlash(remoteRoot)
	if filepath.VolumeName(remoteRoot) != "" {
		filePath = "/" + filePath
	}
	webBase := (&url.URL{Scheme: "file", Path: filePath}).String()
	root := t.TempDir()
	svc, err := NewService(Options{
		Cwd: root, DatabasePath: filepath.Join(root, "gateway.db"), WorkspacesRoot: filepath.Join(root, "workspaces"),
		AdminPassword: "test-password", NoTools: true, GitHubTokenKey: "test-key",
		GitHubAPIBaseURL: upstream.URL, GitHubWebBaseURL: webBase,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = svc.Close() })
	if _, err := svc.GitHub.connectToken(context.Background(), 1, token); err != nil {
		t.Fatal(err)
	}
	workspace, err := svc.importGitHubRepository(context.Background(), 1, "octocat", "hello", "main", "hello")
	if err != nil {
		t.Fatal(err)
	}
	workspaceDir, err := svc.workspaceDir(workspace.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspaceDir, "README.md"), []byte("updated\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	result, err := svc.pushGitHubWorkspace(context.Background(), 1, workspace.ID, "main", "update readme")
	if err != nil || result.Head == "" {
		t.Fatalf("push result=%+v err=%v", result, err)
	}
	got := runTestGit(t, "", "--git-dir", bare, "show", "main:README.md")
	if got != "updated" {
		t.Fatalf("remote README=%q", got)
	}
}

func runTestGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	if dir != "" {
		cmd.Dir = dir
	}
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, output)
	}
	return strings.TrimSpace(string(output))
}
