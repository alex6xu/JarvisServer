package gateway

import (
	"archive/zip"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// WorkspaceInfo matches the web Coder/AgentTasks workspace row.
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

func (s *Service) listWorkspaces() ([]WorkspaceInfo, error) {
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
		info, err := s.workspaceInfo(e.Name())
		if err != nil {
			continue
		}
		out = append(out, info)
	}
	return out, nil
}

func (s *Service) workspaceDir(id string) string {
	return filepath.Join(s.workspacesRoot(), filepath.Clean(id))
}

func (s *Service) workspaceInfo(id string) (WorkspaceInfo, error) {
	dir := s.workspaceDir(id)
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
		if !info.IsDir() {
			files++
			size += info.Size()
		}
		return nil
	})
	name := id
	if meta, err := os.ReadFile(filepath.Join(dir, ".workspace.json")); err == nil {
		var m map[string]string
		if json.Unmarshal(meta, &m) == nil && m["name"] != "" {
			name = m["name"]
		}
	}
	return WorkspaceInfo{
		ID:        id,
		Name:      name,
		FileCount: files,
		SizeBytes: size,
		CreatedAt: st.ModTime().UTC().Format(time.RFC3339),
		UpdatedAt: st.ModTime().UTC().Format(time.RFC3339),
		Source:    "local",
	}, nil
}

func (s *Service) createWorkspaceFromZip(name string, r io.ReaderAt, size int64) (WorkspaceInfo, error) {
	if err := s.ensureWorkspacesRoot(); err != nil {
		return WorkspaceInfo{}, err
	}
	id := newID("ws")
	dir := s.workspaceDir(id)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return WorkspaceInfo{}, err
	}
	zr, err := zip.NewReader(r, size)
	if err != nil {
		_ = os.RemoveAll(dir)
		return WorkspaceInfo{}, fmt.Errorf("invalid zip: %w", err)
	}
	for _, f := range zr.File {
		if err := extractZipFile(dir, f); err != nil {
			_ = os.RemoveAll(dir)
			return WorkspaceInfo{}, err
		}
	}
	if name == "" {
		name = id
	}
	_ = os.WriteFile(filepath.Join(dir, ".workspace.json"), []byte(fmt.Sprintf(`{"name":%q}`, name)), 0o644)
	return s.workspaceInfo(id)
}

func extractZipFile(root string, f *zip.File) error {
	name := filepath.Clean(f.Name)
	if strings.HasPrefix(name, "..") {
		return fmt.Errorf("invalid path in zip")
	}
	target := filepath.Join(root, name)
	if !strings.HasPrefix(target, root) {
		return fmt.Errorf("invalid path in zip")
	}
	if f.FileInfo().IsDir() {
		return os.MkdirAll(target, 0o755)
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return err
	}
	rc, err := f.Open()
	if err != nil {
		return err
	}
	defer rc.Close()
	out, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, rc)
	return err
}

func (s *Service) deleteWorkspace(id string) error {
	dir := s.workspaceDir(id)
	if _, err := os.Stat(dir); err != nil {
		return fmt.Errorf("workspace not found")
	}
	return os.RemoveAll(dir)
}

func (s *Service) zipWorkspace(id string, w io.Writer) error {
	dir := s.workspaceDir(id)
	if _, err := os.Stat(dir); err != nil {
		return fmt.Errorf("workspace not found")
	}
	zw := zip.NewWriter(w)
	defer zw.Close()
	return filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info == nil || info.IsDir() {
			return err
		}
		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return err
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
