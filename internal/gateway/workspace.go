package gateway

import (
	"archive/zip"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"
)

const (
	defaultWorkspaceArchiveBytes      int64 = 100 << 20
	defaultWorkspaceUncompressedBytes int64 = 100 << 20
	defaultWorkspaceFileBytes         int64 = 10 << 20
	maxWorkspaceFiles                       = 5000
	maxWorkspaceEntries                     = 10000
	legacyWorkspaceAccountID                = 1
)

type workspaceUploadLimits struct {
	archiveBytes      int64
	uncompressedBytes int64
	fileBytes         int64
}

func (s *Service) workspaceUploadLimits() workspaceUploadLimits {
	limits := workspaceUploadLimits{
		archiveBytes: s.Opts.WorkspaceUploadMaxBytes, uncompressedBytes: s.Opts.WorkspaceMaxBytes,
		fileBytes: s.Opts.WorkspaceMaxFileBytes,
	}
	if limits.archiveBytes <= 0 {
		limits.archiveBytes = defaultWorkspaceArchiveBytes
	}
	if limits.uncompressedBytes <= 0 {
		limits.uncompressedBytes = defaultWorkspaceUncompressedBytes
	}
	if limits.fileBytes <= 0 {
		limits.fileBytes = defaultWorkspaceFileBytes
	}
	return limits
}

// WorkspaceInfo matches the web Coder workspace row.
type WorkspaceInfo struct {
	ID                  string `json:"id"`
	Name                string `json:"name"`
	FileCount           int    `json:"file_count"`
	SizeBytes           int64  `json:"size_bytes"`
	CreatedAt           string `json:"created_at"`
	UpdatedAt           string `json:"updated_at"`
	Source              string `json:"source,omitempty"`
	GitHubFullName      string `json:"github_full_name,omitempty"`
	GitHubDefaultBranch string `json:"github_default_branch,omitempty"`
	AccountID           int    `json:"-"`
}

type workspaceMetadataFile struct {
	Name                string `json:"name"`
	Source              string `json:"source,omitempty"`
	GitHubFullName      string `json:"github_full_name,omitempty"`
	GitHubDefaultBranch string `json:"github_default_branch,omitempty"`
}

func (s *Service) workspacesRoot() string {
	if s.Opts.WorkspacesRoot != "" {
		return s.Opts.WorkspacesRoot
	}
	return filepath.Join(s.Opts.Cwd, "workspaces")
}

func (s *Service) ensureWorkspacesRoot() error {
	return os.MkdirAll(s.workspacesRoot(), 0o755)
}

func (s *Service) listWorkspaces(accountID int) ([]WorkspaceInfo, error) {
	if err := s.ensureWorkspacesRoot(); err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(s.workspacesRoot())
	if err != nil {
		return nil, err
	}
	out := make([]WorkspaceInfo, 0)
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		info, err := s.workspaceInfoWithOwnership(e.Name())
		if err != nil {
			continue
		}
		_ = s.Control.UpsertWorkspace(context.Background(), info)
		if info.AccountID == accountID {
			out = append(out, info)
		}
	}
	return out, nil
}

func (s *Service) workspaceDir(id string) (string, error) {
	if id == "" || id == "." || filepath.IsAbs(id) || strings.ContainsAny(id, `/\\`) {
		return "", fmt.Errorf("invalid workspace id")
	}
	root, err := filepath.Abs(s.workspacesRoot())
	if err != nil {
		return "", err
	}
	target := filepath.Join(root, id)
	if err := ensurePathWithin(root, target); err != nil {
		return "", fmt.Errorf("invalid workspace id: %w", err)
	}
	return target, nil
}

func (s *Service) workspaceInfo(id string) (WorkspaceInfo, error) {
	dir, err := s.workspaceDir(id)
	if err != nil {
		return WorkspaceInfo{}, err
	}
	st, err := os.Stat(dir)
	if err != nil {
		return WorkspaceInfo{}, err
	}
	var files int
	var size int64
	_ = filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info == nil {
			return nil
		}
		if info.IsDir() && info.Name() == ".git" {
			return filepath.SkipDir
		}
		if !info.IsDir() && info.Name() != ".workspace.json" {
			files++
			size += info.Size()
		}
		return nil
	})
	name, source, githubFullName, githubDefaultBranch := id, "local", "", ""
	if meta, err := os.ReadFile(filepath.Join(dir, ".workspace.json")); err == nil {
		var m workspaceMetadataFile
		if json.Unmarshal(meta, &m) == nil {
			if m.Name != "" {
				name = m.Name
			}
			if m.Source != "" {
				source = m.Source
			}
			githubFullName = m.GitHubFullName
			githubDefaultBranch = m.GitHubDefaultBranch
		}
	}
	return WorkspaceInfo{
		ID:                  id,
		Name:                name,
		FileCount:           files,
		SizeBytes:           size,
		CreatedAt:           st.ModTime().UTC().Format(time.RFC3339),
		UpdatedAt:           st.ModTime().UTC().Format(time.RFC3339),
		Source:              source,
		GitHubFullName:      githubFullName,
		GitHubDefaultBranch: githubDefaultBranch,
	}, nil
}

func (s *Service) workspaceInfoWithOwnership(id string) (WorkspaceInfo, error) {
	info, err := s.workspaceInfo(id)
	if err != nil {
		return WorkspaceInfo{}, err
	}
	stored, err := s.Audit.WorkspaceMetadata(context.Background(), id)
	if err == nil {
		info.AccountID = stored.AccountID
		if stored.Name != "" {
			info.Name = stored.Name
		}
		if stored.Source != "" {
			info.Source = stored.Source
		}
		info.GitHubFullName = stored.GitHubFullName
		info.GitHubDefaultBranch = stored.GitHubDefaultBranch
		return info, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return WorkspaceInfo{}, err
	}
	info.AccountID = legacyWorkspaceAccountID
	if err := s.Control.UpsertWorkspace(context.Background(), info); err != nil {
		return WorkspaceInfo{}, err
	}
	return info, nil
}

func (s *Service) workspaceInfoForAccount(id string, accountID int) (WorkspaceInfo, error) {
	if accountID <= 0 {
		return WorkspaceInfo{}, fmt.Errorf("workspace not found: %w", os.ErrNotExist)
	}
	info, err := s.workspaceInfoWithOwnership(id)
	if err != nil {
		return WorkspaceInfo{}, err
	}
	if info.AccountID != accountID {
		return WorkspaceInfo{}, fmt.Errorf("workspace not found: %w", os.ErrNotExist)
	}
	return info, nil
}

func (s *Service) createWorkspaceFromZip(name string, accountID int, r io.ReaderAt, size int64) (WorkspaceInfo, error) {
	if accountID <= 0 {
		return WorkspaceInfo{}, fmt.Errorf("account is required")
	}
	limits := s.workspaceUploadLimits()
	if size <= 0 || size > limits.archiveBytes {
		return WorkspaceInfo{}, fmt.Errorf("workspace archive must be between 1 byte and %d MB", limits.archiveBytes>>20)
	}
	if err := s.ensureWorkspacesRoot(); err != nil {
		return WorkspaceInfo{}, err
	}
	id := newID("ws")
	dir, err := s.workspaceDir(id)
	if err != nil {
		return WorkspaceInfo{}, err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return WorkspaceInfo{}, err
	}
	zr, err := zip.NewReader(r, size)
	if err != nil {
		_ = os.RemoveAll(dir)
		return WorkspaceInfo{}, fmt.Errorf("invalid zip: %w", err)
	}
	if err := validateWorkspaceZip(zr, limits); err != nil {
		_ = os.RemoveAll(dir)
		return WorkspaceInfo{}, err
	}
	var extractedBytes int64
	for _, f := range zr.File {
		written, err := extractZipFile(dir, f, limits.fileBytes)
		if err != nil {
			_ = os.RemoveAll(dir)
			return WorkspaceInfo{}, err
		}
		extractedBytes += written
		if extractedBytes > limits.uncompressedBytes {
			_ = os.RemoveAll(dir)
			return WorkspaceInfo{}, fmt.Errorf("workspace exceeds %d MB uncompressed limit", limits.uncompressedBytes>>20)
		}
	}
	if name == "" {
		name = id
	}
	if err := writeWorkspaceMetadata(dir, workspaceMetadataFile{Name: name, Source: "local"}); err != nil {
		_ = os.RemoveAll(dir)
		return WorkspaceInfo{}, err
	}
	info, err := s.workspaceInfo(id)
	if err != nil {
		return WorkspaceInfo{}, err
	}
	info.AccountID = accountID
	if err := s.Control.UpsertWorkspace(context.Background(), info); err != nil {
		_ = os.RemoveAll(dir)
		return WorkspaceInfo{}, err
	}
	return info, nil
}

func writeWorkspaceMetadata(dir string, metadata workspaceMetadataFile) error {
	raw, err := json.Marshal(metadata)
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, ".workspace.json"), raw, 0o600)
}

func validateWorkspaceZip(zr *zip.Reader, limits workspaceUploadLimits) error {
	if len(zr.File) > maxWorkspaceEntries {
		return fmt.Errorf("workspace zip exceeds %d entry limit", maxWorkspaceEntries)
	}
	files := 0
	var total int64
	seen := make(map[string]struct{}, len(zr.File))
	for _, f := range zr.File {
		name, err := normalizedZipPath(f.Name)
		if err != nil {
			return err
		}
		key := strings.ToLower(name)
		if _, ok := seen[key]; ok {
			return fmt.Errorf("duplicate path in zip: %s", name)
		}
		seen[key] = struct{}{}
		if f.FileInfo().Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("symbolic links are not allowed in workspace zip: %s", name)
		}
		if f.FileInfo().IsDir() {
			continue
		}
		if strings.EqualFold(path.Base(name), ".workspace.json") {
			return fmt.Errorf("reserved workspace metadata path is not allowed")
		}
		files++
		if files > maxWorkspaceFiles {
			return fmt.Errorf("workspace exceeds %d file limit", maxWorkspaceFiles)
		}
		fileSize := int64(f.UncompressedSize64)
		if fileSize > limits.fileBytes {
			return fmt.Errorf("file %s exceeds %d MB limit", name, limits.fileBytes>>20)
		}
		if fileSize > limits.uncompressedBytes-total {
			return fmt.Errorf("workspace exceeds %d MB uncompressed limit", limits.uncompressedBytes>>20)
		}
		total += fileSize
	}
	if files == 0 {
		return fmt.Errorf("workspace zip contains no files")
	}
	return nil
}

func normalizedZipPath(raw string) (string, error) {
	norm := strings.ReplaceAll(raw, `\`, "/")
	if norm == "" || strings.ContainsRune(norm, '\x00') || strings.HasPrefix(norm, "/") {
		return "", fmt.Errorf("invalid path in zip")
	}
	clean := path.Clean(norm)
	if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") {
		return "", fmt.Errorf("invalid path in zip")
	}
	for _, part := range strings.Split(clean, "/") {
		if part == "" || strings.Contains(part, ":") {
			return "", fmt.Errorf("invalid path in zip: %s", raw)
		}
	}
	return clean, nil
}

func extractZipFile(root string, f *zip.File, maxFileBytes int64) (int64, error) {
	name, err := normalizedZipPath(f.Name)
	if err != nil {
		return 0, err
	}
	name = filepath.FromSlash(name)
	target := filepath.Join(root, name)
	if err := ensurePathWithin(root, target); err != nil {
		return 0, fmt.Errorf("invalid path in zip: %w", err)
	}
	if f.FileInfo().IsDir() {
		return 0, os.MkdirAll(target, 0o755)
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return 0, err
	}
	rc, err := f.Open()
	if err != nil {
		return 0, err
	}
	defer rc.Close()
	out, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return 0, err
	}
	written, copyErr := io.Copy(out, io.LimitReader(rc, maxFileBytes+1))
	closeErr := out.Close()
	if copyErr != nil {
		_ = os.Remove(target)
		return 0, copyErr
	}
	if closeErr != nil {
		_ = os.Remove(target)
		return 0, closeErr
	}
	if written > maxFileBytes {
		_ = os.Remove(target)
		return 0, fmt.Errorf("file %s exceeds %d MB limit", f.Name, maxFileBytes>>20)
	}
	return written, nil
}

func (s *Service) deleteWorkspace(id string, accountID int) error {
	if _, err := s.workspaceInfoForAccount(id, accountID); err != nil {
		return err
	}
	dir, err := s.workspaceDir(id)
	if err != nil {
		return err
	}
	if _, err := os.Stat(dir); err != nil {
		return fmt.Errorf("workspace not found")
	}
	if err := os.RemoveAll(dir); err != nil {
		return err
	}
	return s.Control.DeleteWorkspace(context.Background(), id)
}

func (s *Service) zipWorkspace(id string, accountID int, w io.Writer) error {
	if _, err := s.workspaceInfoForAccount(id, accountID); err != nil {
		return err
	}
	dir, err := s.workspaceDir(id)
	if err != nil {
		return err
	}
	if _, err := os.Stat(dir); err != nil {
		return fmt.Errorf("workspace not found")
	}
	zw := zip.NewWriter(w)
	defer zw.Close()
	return filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info == nil {
			return err
		}
		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}
		if info.IsDir() {
			if rel == ".git" || strings.HasPrefix(rel, ".git"+string(filepath.Separator)) {
				return filepath.SkipDir
			}
			return nil
		}
		if rel == ".workspace.json" {
			return nil
		}
		fw, err := zw.Create(filepath.ToSlash(rel))
		if err != nil {
			return err
		}
		f, err := os.Open(path)
		if err != nil {
			return err
		}
		defer f.Close()
		_, err = io.Copy(fw, f)
		return err
	})
}

func ensurePathWithin(root, target string) error {
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return err
	}
	absTarget, err := filepath.Abs(target)
	if err != nil {
		return err
	}
	rel, err := filepath.Rel(absRoot, absTarget)
	if err != nil {
		return err
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return fmt.Errorf("path escapes workspace root")
	}
	return nil
}
