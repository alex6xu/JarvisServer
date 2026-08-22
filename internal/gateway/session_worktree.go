package gateway

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/alex6xu/jarvisserver/internal/agentcore"
	"github.com/alex6xu/jarvisserver/internal/session"
)

const maxSessionDiffBytes = 20 << 20

type cappedBytes struct {
	bytes.Buffer
	max      int
	overflow bool
}

func (w *cappedBytes) Write(p []byte) (int, error) {
	original := len(p)
	remaining := w.max - w.Len()
	if remaining < len(p) {
		w.overflow = true
		if remaining < 0 {
			remaining = 0
		}
		p = p[:remaining]
	}
	_, _ = w.Buffer.Write(p)
	return original, nil
}

func runLocalGit(ctx context.Context, dir string, extraEnv []string, input []byte, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	cmd.Env = append(cleanGitEnvironment(), "GIT_TERMINAL_PROMPT=0")
	cmd.Env = append(cmd.Env, extraEnv...)
	if input != nil {
		cmd.Stdin = bytes.NewReader(input)
	}
	output := &gitOutput{max: maxSessionDiffBytes}
	cmd.Stdout, cmd.Stderr = output, output
	if err := cmd.Run(); err != nil {
		message := output.String()
		if message == "" {
			message = err.Error()
		}
		return message, fmt.Errorf("git %s: %s", args[0], message)
	}
	return output.String(), nil
}

func runLocalGitBytes(ctx context.Context, dir string, input []byte, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	cmd.Env = append(cleanGitEnvironment(), "GIT_TERMINAL_PROMPT=0")
	if input != nil {
		cmd.Stdin = bytes.NewReader(input)
	}
	output := &cappedBytes{max: maxSessionDiffBytes}
	cmd.Stdout, cmd.Stderr = output, output
	if err := cmd.Run(); err != nil {
		message := strings.TrimSpace(output.String())
		if message == "" {
			message = err.Error()
		}
		return nil, fmt.Errorf("git %s: %s", args[0], message)
	}
	if output.overflow {
		return nil, fmt.Errorf("git %s output exceeds %d MB", args[0], maxSessionDiffBytes>>20)
	}
	return output.Bytes(), nil
}

func (s *Service) sessionWorkspaceID(header session.SessionHeader) (string, error) {
	if header.WorkspaceID != "" {
		return header.WorkspaceID, nil
	}
	if header.Cwd == "" {
		return "", errors.New("session is not attached to a Coder workspace")
	}
	root, err := filepath.Abs(s.workspacesRoot())
	if err != nil {
		return "", err
	}
	cwd, err := filepath.Abs(header.Cwd)
	if err != nil || filepath.Dir(cwd) != root {
		return "", errors.New("legacy session workspace cannot be resolved")
	}
	return filepath.Base(cwd), nil
}

func (s *Service) lockWorkspaceGit() func() {
	if s.GitHub == nil {
		return func() {}
	}
	s.GitHub.gitMu.Lock()
	return s.GitHub.gitMu.Unlock
}

func (s *Service) createSessionWorktree(ctx context.Context, workspaceID, sourceDir, sessionID string) (string, string, string, error) {
	primary, err := s.workspaceDir(workspaceID)
	if err != nil {
		return "", "", "", err
	}
	unlock := s.lockWorkspaceGit()
	defer unlock()

	if sourceDir == "" {
		sourceDir = primary
	}
	top, topErr := runLocalGit(ctx, sourceDir, nil, nil, "rev-parse", "--show-toplevel")
	if topErr != nil {
		if filepath.Clean(sourceDir) != filepath.Clean(primary) {
			return "", "", "", errors.New("source worktree is not a Git repository")
		}
		if _, err := runLocalGit(ctx, primary, nil, nil, "init"); err != nil {
			return "", "", "", fmt.Errorf("initialize workspace repository: %w", err)
		}
		if err := excludeWorkspaceMetadata(primary); err != nil {
			return "", "", "", err
		}
	} else {
		resolvedTop, err := filepath.EvalSymlinks(strings.TrimSpace(top))
		resolvedSource, sourceErr := filepath.EvalSymlinks(sourceDir)
		if err != nil || sourceErr != nil || filepath.Clean(resolvedTop) != filepath.Clean(resolvedSource) {
			return "", "", "", errors.New("session directory must be the root of its Git worktree")
		}
	}

	indexFile, err := os.CreateTemp("", "jarvis-session-index-")
	if err != nil {
		return "", "", "", err
	}
	indexPath := indexFile.Name()
	_ = indexFile.Close()
	_ = os.Remove(indexPath)
	defer os.Remove(indexPath)
	indexEnv := []string{"GIT_INDEX_FILE=" + indexPath}

	head, headErr := runLocalGit(ctx, sourceDir, nil, nil, "rev-parse", "--verify", "HEAD")
	if headErr == nil {
		if _, err := runLocalGit(ctx, sourceDir, indexEnv, nil, "read-tree", strings.TrimSpace(head)); err != nil {
			return "", "", "", err
		}
	}
	if _, err := runLocalGit(ctx, sourceDir, indexEnv, nil, "add", "-A", "--", "."); err != nil {
		return "", "", "", fmt.Errorf("snapshot workspace: %w", err)
	}
	tree, err := runLocalGit(ctx, sourceDir, indexEnv, nil, "write-tree")
	if err != nil {
		return "", "", "", err
	}
	commitArgs := []string{"-c", "user.name=Jarvis", "-c", "user.email=jarvis@localhost", "commit-tree", strings.TrimSpace(tree)}
	if headErr == nil {
		commitArgs = append(commitArgs, "-p", strings.TrimSpace(head))
	}
	baseCommit, err := runLocalGit(ctx, sourceDir, nil, []byte("Jarvis session snapshot\n"), commitArgs...)
	if err != nil {
		return "", "", "", err
	}
	baseCommit = strings.TrimSpace(baseCommit)
	branch := "jarvis/session-" + sessionID
	if !validGitBranch(branch) {
		return "", "", "", errors.New("generated session branch is invalid")
	}
	if _, err := runLocalGit(ctx, primary, nil, nil, "branch", branch, baseCommit); err != nil {
		return "", "", "", err
	}

	worktreeRoot, err := filepath.Abs(filepath.Join(s.sessionWorktreesRoot(), workspaceID))
	if err != nil {
		return "", "", "", err
	}
	worktree := filepath.Join(worktreeRoot, sessionID)
	if err := ensurePathWithin(worktreeRoot, worktree); err != nil {
		return "", "", "", err
	}
	if err := os.MkdirAll(worktreeRoot, 0o700); err != nil {
		return "", "", "", err
	}
	if _, err := runLocalGit(ctx, primary, nil, nil, "worktree", "add", worktree, branch); err != nil {
		_, _ = runLocalGit(ctx, primary, nil, nil, "branch", "-D", branch)
		return "", "", "", err
	}
	return worktree, branch, baseCommit, nil
}

func (s *Service) removeSessionWorktree(ctx context.Context, workspaceID, worktree, branch string) {
	primary, err := s.workspaceDir(workspaceID)
	if err != nil {
		return
	}
	unlock := s.lockWorkspaceGit()
	defer unlock()
	_, _ = runLocalGit(ctx, primary, nil, nil, "worktree", "remove", "--force", worktree)
	_, _ = runLocalGit(ctx, primary, nil, nil, "branch", "-D", branch)
}

func (s *Service) ForkSession(ctx context.Context, sourceID string, req ForkSessionRequest, accountID int) (ForkSessionResponse, error) {
	if active := s.Runs.ActiveForSession(sourceID); active != nil {
		return ForkSessionResponse{}, errors.New("wait for the active run to finish before forking")
	}
	header, entries, err := s.Store.LoadEntries(sourceID)
	if err != nil {
		return ForkSessionResponse{}, err
	}
	if !sessionOwnedByAccount(header, accountID) {
		return ForkSessionResponse{}, errors.New("session does not belong to this account")
	}
	workspaceID, err := s.sessionWorkspaceID(header)
	if err != nil {
		return ForkSessionResponse{}, err
	}
	if _, err := s.workspaceInfoForAccount(workspaceID, accountID); err != nil {
		return ForkSessionResponse{}, err
	}
	leafID := req.EntryID
	if leafID == "" && len(entries) > 0 {
		leafID = entries[len(entries)-1].ID
	}
	if req.EntryID != "" {
		found := false
		for index, entry := range entries {
			if entry.ID != req.EntryID {
				continue
			}
			found = true
			if req.Position == "before" {
				leafID = entry.ParentID
			} else if entry.Message.Role() == agentcore.RoleAssistant {
				// Keep assistant tool calls and their results together. Restored web
				// messages collapse those entries into one visible assistant turn.
				for next := index + 1; next < len(entries); next++ {
					if entries[next].Message.Role() == agentcore.RoleUser {
						break
					}
					leafID = entries[next].ID
				}
			}
			break
		}
		if !found {
			return ForkSessionResponse{}, errors.New("fork entry was not found")
		}
	}
	if req.Position != "" && req.Position != "at" && req.Position != "before" {
		return ForkSessionResponse{}, errors.New("position must be at or before")
	}

	now := time.Now().UTC()
	newHeader := session.SessionHeader{
		ID:            session.NewID(now),
		CreatedAt:     now,
		UpdatedAt:     now,
		Model:         header.Model,
		Provider:      header.Provider,
		SystemPrompt:  header.SystemPrompt,
		ParentSession: sourceID,
		AccountID:     accountID,
		WorkspaceID:   workspaceID,
	}
	worktree, branch, snapshotCommit, err := s.createSessionWorktree(ctx, workspaceID, header.Cwd, newHeader.ID)
	if err != nil {
		return ForkSessionResponse{}, err
	}
	newHeader.Cwd = worktree
	newHeader.WorktreeBranch = branch
	newHeader.WorktreeBaseCommit = snapshotCommit
	if header.WorktreeBaseCommit != "" {
		newHeader.WorktreeBaseCommit = header.WorktreeBaseCommit
	}
	path := session.PathToLeaf(entries, leafID)
	if err := s.Store.SaveEntries(newHeader, path); err != nil {
		s.removeSessionWorktree(context.Background(), workspaceID, worktree, branch)
		return ForkSessionResponse{}, err
	}
	meta := sessionMetaFromHeader(newHeader, len(path))
	return ForkSessionResponse{Session: meta, WorkspaceID: workspaceID}, nil
}

func (s *Service) SessionDiff(ctx context.Context, id string, accountID int) (string, error) {
	header, _, err := s.Store.LoadEntries(id)
	if err != nil {
		return "", err
	}
	if !sessionOwnedByAccount(header, accountID) {
		return "", errors.New("session does not belong to this account")
	}
	if header.WorktreeBranch == "" || header.WorktreeBaseCommit == "" {
		return "", errors.New("session does not use an isolated worktree")
	}
	diff, err := runLocalGit(ctx, header.Cwd, nil, nil, "diff", "--stat", header.WorktreeBaseCommit)
	if err != nil {
		return "", err
	}
	status, err := runLocalGit(ctx, header.Cwd, nil, nil, "status", "--short")
	if err != nil {
		return "", err
	}
	if diff == "" && status == "" {
		return "No changes", nil
	}
	return strings.TrimSpace(diff + "\n" + status), nil
}

func (s *Service) MergeSession(ctx context.Context, id string, accountID int) (MergeSessionResponse, error) {
	if active := s.Runs.ActiveForSession(id); active != nil {
		return MergeSessionResponse{}, errors.New("wait for the active run to finish before merging")
	}
	header, entries, err := s.Store.LoadEntries(id)
	if err != nil {
		return MergeSessionResponse{}, err
	}
	if !sessionOwnedByAccount(header, accountID) {
		return MergeSessionResponse{}, errors.New("session does not belong to this account")
	}
	if header.WorktreeBranch == "" || header.WorktreeBaseCommit == "" {
		return MergeSessionResponse{}, errors.New("session does not use an isolated worktree")
	}
	workspaceID, err := s.sessionWorkspaceID(header)
	if err != nil {
		return MergeSessionResponse{}, err
	}
	primary, err := s.workspaceDir(workspaceID)
	if err != nil {
		return MergeSessionResponse{}, err
	}
	unlock := s.lockWorkspaceGit()
	defer unlock()
	status, err := runLocalGit(ctx, header.Cwd, nil, nil, "status", "--porcelain")
	if err != nil {
		return MergeSessionResponse{}, err
	}
	if status != "" {
		if _, err := runLocalGit(ctx, header.Cwd, nil, nil, "add", "-A", "--", "."); err != nil {
			return MergeSessionResponse{}, err
		}
		if _, err := runLocalGit(ctx, header.Cwd, nil, []byte(nil), "-c", "user.name=Jarvis", "-c", "user.email=jarvis@localhost", "commit", "-m", "Changes from session "+id); err != nil {
			return MergeSessionResponse{}, err
		}
	}
	head, err := runLocalGit(ctx, header.Cwd, nil, nil, "rev-parse", "HEAD")
	if err != nil {
		return MergeSessionResponse{}, err
	}
	head = strings.TrimSpace(head)
	names, err := runLocalGit(ctx, header.Cwd, nil, nil, "diff", "--name-only", header.WorktreeBaseCommit+".."+head)
	if err != nil {
		return MergeSessionResponse{}, err
	}
	if strings.TrimSpace(names) == "" {
		return MergeSessionResponse{Message: "No changes to merge"}, nil
	}
	patchBytes, err := runLocalGitBytes(ctx, header.Cwd, nil, "diff", "--binary", header.WorktreeBaseCommit+".."+head)
	if err != nil {
		return MergeSessionResponse{}, err
	}
	if _, err := runLocalGit(ctx, primary, nil, patchBytes, "apply", "--check", "--whitespace=nowarn", "-"); err != nil {
		return MergeSessionResponse{}, fmt.Errorf("branch conflicts with the main workspace: %w", err)
	}
	if _, err := runLocalGit(ctx, primary, nil, patchBytes, "apply", "--whitespace=nowarn", "-"); err != nil {
		return MergeSessionResponse{}, err
	}
	header.WorktreeBaseCommit = head
	header.UpdatedAt = time.Now().UTC()
	if err := s.Store.SaveEntries(header, entries); err != nil {
		return MergeSessionResponse{}, fmt.Errorf("changes applied but session baseline update failed: %w", err)
	}
	return MergeSessionResponse{Message: "Branch changes merged into the workspace", ChangedFiles: len(strings.Split(strings.TrimSpace(names), "\n"))}, nil
}
