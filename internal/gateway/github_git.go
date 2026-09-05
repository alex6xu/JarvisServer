package gateway

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

type GitHubSyncResult struct {
	Message string `json:"message"`
	Head    string `json:"head,omitempty"`
}

type gitOutput struct {
	b   strings.Builder
	max int
}

func (w *gitOutput) Write(p []byte) (int, error) {
	remaining := w.max - w.b.Len()
	if remaining > 0 {
		if len(p) < remaining {
			remaining = len(p)
		}
		_, _ = w.b.Write(p[:remaining])
	}
	return len(p), nil
}

func (w *gitOutput) String() string { return strings.TrimSpace(w.b.String()) }

func (g *GitHubService) remoteURL(fullName string) (string, error) {
	parts := strings.Split(fullName, "/")
	if len(parts) != 2 || !validGitHubName(parts[0]) || !validGitHubName(parts[1]) {
		return "", errors.New("invalid github repository name")
	}
	return g.webBaseURL + "/" + parts[0] + "/" + parts[1] + ".git", nil
}

func validGitHubName(value string) bool {
	if value == "" || len(value) > 100 || value == "." || value == ".." || strings.HasPrefix(value, "-") {
		return false
	}
	for _, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' || r == '.' {
			continue
		}
		return false
	}
	return true
}

func validGitBranch(branch string) bool {
	if branch == "" || len(branch) > 255 || strings.HasPrefix(branch, "-") || strings.HasPrefix(branch, ".") ||
		strings.HasSuffix(branch, ".") || strings.HasSuffix(branch, ".lock") || strings.Contains(branch, "..") ||
		strings.ContainsAny(branch, " ~^:?*[\\") || strings.Contains(branch, "@{") || strings.Contains(branch, "//") {
		return false
	}
	for _, part := range strings.Split(branch, "/") {
		if part == "" || strings.HasPrefix(part, ".") || strings.HasSuffix(part, ".lock") {
			return false
		}
	}
	return true
}

func cleanGitEnvironment() []string {
	out := make([]string, 0, len(os.Environ()))
	for _, item := range os.Environ() {
		name := item
		if at := strings.IndexByte(item, '='); at >= 0 {
			name = item[:at]
		}
		upper := strings.ToUpper(name)
		if strings.HasPrefix(upper, "GIT_") || strings.HasPrefix(upper, "GCM_") || upper == "GH_TOKEN" || upper == "GITHUB_TOKEN" {
			continue
		}
		out = append(out, item)
	}
	return out
}

func (g *GitHubService) runGit(ctx context.Context, dir, token string, args ...string) (string, error) {
	authDir, err := os.MkdirTemp("", "jarvis-git-auth-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(authDir)

	askPass := filepath.Join(authDir, "askpass.sh")
	script := "#!/bin/sh\ncase \"$1\" in *Username*) printf '%s\\n' 'x-access-token' ;; *) printf '%s\\n' \"$JARVIS_GITHUB_TOKEN\" ;; esac\n"
	if runtime.GOOS == "windows" {
		askPass = filepath.Join(authDir, "askpass.cmd")
		script = "@echo off\r\necho %~1 | findstr /I Username >nul\r\nif not errorlevel 1 (echo x-access-token) else (echo %JARVIS_GITHUB_TOKEN%)\r\n"
	}
	if err := os.WriteFile(askPass, []byte(script), 0o700); err != nil {
		return "", err
	}

	gitArgs := []string{"-c", "credential.helper=", "-c", "core.hooksPath=" + authDir}
	gitArgs = append(gitArgs, args...)
	cmd := exec.CommandContext(ctx, "git", gitArgs...)
	cmd.Dir = dir
	nullConfig := "/dev/null"
	if runtime.GOOS == "windows" {
		nullConfig = "NUL"
	}
	cmd.Env = append(cleanGitEnvironment(),
		"GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL="+nullConfig, "GIT_TERMINAL_PROMPT=0",
		"GCM_INTERACTIVE=Never", "GIT_ASKPASS="+askPass, "JARVIS_GITHUB_TOKEN="+token)
	output := &gitOutput{max: 64 << 10}
	cmd.Stdout, cmd.Stderr = output, output
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return output.String(), ctx.Err()
		}
		message := output.String()
		if message == "" {
			message = err.Error()
		}
		return message, fmt.Errorf("git %s: %s", args[0], message)
	}
	return output.String(), nil
}

func (g *GitHubService) gitContext(parent context.Context) (context.Context, context.CancelFunc) {
	timeout := g.gitTimeout
	if timeout <= 0 {
		timeout = 5 * time.Minute
	}
	return context.WithTimeout(parent, timeout)
}

func (s *Service) importGitHubRepository(ctx context.Context, accountID int, owner, repo, branch, name string) (WorkspaceInfo, error) {
	if !validGitHubName(owner) || !validGitHubName(repo) {
		return WorkspaceInfo{}, errors.New("invalid owner or repository name")
	}
	repository, token, err := s.GitHub.repository(ctx, accountID, owner, repo)
	if err != nil {
		return WorkspaceInfo{}, err
	}
	if branch == "" {
		branch = repository.DefaultBranch
	}
	if !validGitBranch(branch) {
		return WorkspaceInfo{}, errors.New("invalid branch name")
	}
	if name = strings.TrimSpace(name); name == "" {
		name = repository.Name
	}
	if len(name) > 120 {
		return WorkspaceInfo{}, errors.New("workspace name is too long")
	}
	remote, err := s.GitHub.remoteURL(repository.FullName)
	if err != nil {
		return WorkspaceInfo{}, err
	}

	s.GitHub.gitMu.Lock()
	defer s.GitHub.gitMu.Unlock()
	if err := s.ensureWorkspacesRoot(); err != nil {
		return WorkspaceInfo{}, err
	}
	id := newID("ws")
	dir, err := s.workspaceDir(id)
	if err != nil {
		return WorkspaceInfo{}, err
	}
	gitCtx, cancel := s.GitHub.gitContext(ctx)
	defer cancel()
	if _, err := s.GitHub.runGit(gitCtx, s.workspacesRoot(), token, "clone", "--depth=1", "--single-branch", "--branch", branch, "--", remote, dir); err != nil {
		_ = os.RemoveAll(dir)
		return WorkspaceInfo{}, err
	}
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.RemoveAll(dir)
		}
	}()
	metadata := workspaceMetadataFile{Name: name, Source: "github", GitHubFullName: repository.FullName, GitHubDefaultBranch: branch}
	if err := writeWorkspaceMetadata(dir, metadata); err != nil {
		return WorkspaceInfo{}, err
	}
	if err := excludeWorkspaceMetadata(dir); err != nil {
		return WorkspaceInfo{}, err
	}
	if err := validateWorkspaceDirectory(dir, s.workspaceUploadLimits()); err != nil {
		return WorkspaceInfo{}, err
	}
	info, err := s.workspaceInfo(id)
	if err != nil {
		return WorkspaceInfo{}, err
	}
	info.AccountID = accountID
	if err := s.Control.UpsertWorkspace(ctx, info); err != nil {
		return WorkspaceInfo{}, err
	}
	if _, err := s.Audit.EnsureWorkspaceProject(ctx, accountID, info.ID); err != nil {
		_ = s.Control.DeleteWorkspace(ctx, info.ID)
		return WorkspaceInfo{}, err
	}
	cleanup = false
	return info, nil
}

func excludeWorkspaceMetadata(dir string) error {
	infoDir := filepath.Join(dir, ".git", "info")
	if err := os.MkdirAll(infoDir, 0o700); err != nil {
		return err
	}
	excludePath := filepath.Join(infoDir, "exclude")
	if raw, err := os.ReadFile(excludePath); err == nil && strings.Contains("\n"+string(raw)+"\n", "\n.workspace.json\n") {
		return nil
	}
	file, err := os.OpenFile(excludePath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer file.Close()
	_, err = file.WriteString("\n.workspace.json\n")
	return err
}

func validateWorkspaceDirectory(root string, limits workspaceUploadLimits) error {
	var total int64
	files := 0
	return filepath.Walk(root, func(current string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			if err := validateWorkspaceSymlink(root, current); err != nil {
				return err
			}
		}
		if info.IsDir() {
			return nil
		}
		total += info.Size()
		if total > limits.uncompressedBytes {
			return fmt.Errorf("workspace exceeds %d MB limit", limits.uncompressedBytes>>20)
		}
		rel, err := filepath.Rel(root, current)
		if err != nil {
			return err
		}
		if rel == ".workspace.json" || rel == ".git" || strings.HasPrefix(rel, ".git"+string(filepath.Separator)) {
			return nil
		}
		files++
		if files > maxWorkspaceFiles {
			return fmt.Errorf("workspace exceeds %d file limit", maxWorkspaceFiles)
		}
		if info.Size() > limits.fileBytes {
			return fmt.Errorf("file %s exceeds %d MB limit", rel, limits.fileBytes>>20)
		}
		return nil
	})
}

func validateWorkspaceSymlink(root, current string) error {
	rel, relErr := filepath.Rel(root, current)
	if relErr != nil {
		return relErr
	}
	target, err := os.Readlink(current)
	if err != nil {
		return fmt.Errorf("read symbolic link %s: %w", rel, err)
	}

	// Check the direct target before resolving it so an escaping or dangling
	// link is rejected without ever treating an out-of-workspace path as valid.
	if _, err := workspaceSymlinkTarget(root, current, target); err != nil {
		return fmt.Errorf("%w: %s", err, rel)
	}
	resolved, err := filepath.EvalSymlinks(current)
	if err != nil {
		// Git repositories may intentionally contain dangling relative links (for
		// example, a crate reusing an optional root license file). Walking the
		// workspace never follows links, so a missing in-workspace target is safe
		// to retain. Other failures such as a symlink loop remain invalid.
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("symbolic link target cannot be resolved: %s: %w", rel, err)
	}
	if err := ensurePathWithin(root, resolved); err != nil {
		return fmt.Errorf("symbolic link points outside workspace: %s", rel)
	}
	return nil
}

func workspaceSymlinkTarget(root, current, target string) (string, error) {
	if filepath.IsAbs(target) || filepath.VolumeName(target) != "" ||
		strings.HasPrefix(target, "/") || strings.HasPrefix(target, `\`) {
		return "", errors.New("absolute symbolic links are not allowed in workspace")
	}
	directTarget := filepath.Join(filepath.Dir(current), target)
	if err := ensurePathWithin(root, directTarget); err != nil {
		return "", errors.New("symbolic link points outside workspace")
	}
	return directTarget, nil
}

func (s *Service) pullGitHubWorkspace(ctx context.Context, accountID int, workspaceID string) (GitHubSyncResult, error) {
	info, dir, remote, token, err := s.githubWorkspaceContext(ctx, accountID, workspaceID)
	if err != nil {
		return GitHubSyncResult{}, err
	}
	s.GitHub.gitMu.Lock()
	defer s.GitHub.gitMu.Unlock()
	gitCtx, cancel := s.GitHub.gitContext(ctx)
	defer cancel()
	status, err := s.GitHub.runGit(gitCtx, dir, "", "status", "--porcelain")
	if err != nil {
		return GitHubSyncResult{}, err
	}
	if status != "" {
		return GitHubSyncResult{}, errors.New("workspace has uncommitted changes; push them before pulling")
	}
	oldHead, err := s.GitHub.runGit(gitCtx, dir, "", "rev-parse", "HEAD")
	if err != nil {
		return GitHubSyncResult{}, err
	}
	if _, err := s.GitHub.runGit(gitCtx, dir, token, "fetch", "--depth=1", remote, info.GitHubDefaultBranch); err != nil {
		return GitHubSyncResult{}, err
	}
	if _, err := s.GitHub.runGit(gitCtx, dir, "", "merge", "--ff-only", "FETCH_HEAD"); err != nil {
		return GitHubSyncResult{}, err
	}
	if err := validateWorkspaceDirectory(dir, s.workspaceUploadLimits()); err != nil {
		_, _ = s.GitHub.runGit(gitCtx, dir, "", "reset", "--hard", oldHead)
		return GitHubSyncResult{}, err
	}
	updated, err := s.workspaceInfo(workspaceID)
	if err == nil {
		updated.AccountID = accountID
		updated.Name = info.Name
		updated.Source = info.Source
		updated.GitHubFullName = info.GitHubFullName
		updated.GitHubDefaultBranch = info.GitHubDefaultBranch
		_ = s.Control.UpsertWorkspace(ctx, updated)
	}
	head, _ := s.GitHub.runGit(gitCtx, dir, "", "rev-parse", "--short", "HEAD")
	return GitHubSyncResult{Message: "Pulled latest changes", Head: head}, nil
}

func (s *Service) pushGitHubWorkspace(ctx context.Context, accountID int, workspaceID, branch, message string) (GitHubSyncResult, error) {
	info, dir, remote, token, err := s.githubWorkspaceContext(ctx, accountID, workspaceID)
	if err != nil {
		return GitHubSyncResult{}, err
	}
	if branch == "" {
		branch = info.GitHubDefaultBranch
	}
	if !validGitBranch(branch) {
		return GitHubSyncResult{}, errors.New("invalid branch name")
	}
	message = strings.TrimSpace(message)
	if message == "" {
		message = "Update from JarvisServer"
	}
	if len(message) > 500 {
		return GitHubSyncResult{}, errors.New("commit message is too long")
	}
	credential, _, err := s.GitHub.credential(ctx, accountID)
	if err != nil {
		return GitHubSyncResult{}, err
	}
	s.GitHub.gitMu.Lock()
	defer s.GitHub.gitMu.Unlock()
	if err := excludeWorkspaceMetadata(dir); err != nil {
		return GitHubSyncResult{}, err
	}
	if err := validateWorkspaceDirectory(dir, s.workspaceUploadLimits()); err != nil {
		return GitHubSyncResult{}, err
	}
	gitCtx, cancel := s.GitHub.gitContext(ctx)
	defer cancel()
	status, err := s.GitHub.runGit(gitCtx, dir, "", "status", "--porcelain")
	if err != nil {
		return GitHubSyncResult{}, err
	}
	if status != "" {
		if _, err := s.GitHub.runGit(gitCtx, dir, "", "add", "-A"); err != nil {
			return GitHubSyncResult{}, err
		}
		email := credential.Login + "@users.noreply.github.com"
		if _, err := s.GitHub.runGit(gitCtx, dir, "", "-c", "user.name="+credential.Login, "-c", "user.email="+email, "commit", "-m", message); err != nil {
			return GitHubSyncResult{}, err
		}
	}
	refspec := "HEAD:refs/heads/" + branch
	if _, err := s.GitHub.runGit(gitCtx, dir, token, "push", remote, refspec); err != nil {
		return GitHubSyncResult{}, fmt.Errorf("%w (the local commit was kept)", err)
	}
	head, _ := s.GitHub.runGit(gitCtx, dir, "", "rev-parse", "--short", "HEAD")
	return GitHubSyncResult{Message: "Pushed changes to GitHub", Head: head}, nil
}

func (s *Service) githubWorkspaceContext(ctx context.Context, accountID int, workspaceID string) (WorkspaceInfo, string, string, string, error) {
	info, err := s.workspaceInfoForAccount(workspaceID, accountID)
	if err != nil || info.Source != "github" || info.GitHubFullName == "" {
		return WorkspaceInfo{}, "", "", "", errors.New("github workspace not found")
	}
	dir, err := s.workspaceDir(workspaceID)
	if err != nil {
		return WorkspaceInfo{}, "", "", "", err
	}
	remote, err := s.GitHub.remoteURL(info.GitHubFullName)
	if err != nil {
		return WorkspaceInfo{}, "", "", "", err
	}
	_, token, err := s.GitHub.credential(ctx, accountID)
	return info, dir, remote, token, err
}

func parsePositiveInt(value string, fallback int) int {
	n, err := strconv.Atoi(value)
	if err != nil || n <= 0 {
		return fallback
	}
	return n
}
