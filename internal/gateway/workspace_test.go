package gateway

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestWorkspaceDirRejectsTraversal(t *testing.T) {
	svc := &Service{Opts: Options{WorkspacesRoot: t.TempDir()}}
	for _, id := range []string{"", ".", "..", "../outside", `..\outside`, "/absolute", `C:\absolute`} {
		if dir, err := svc.workspaceDir(id); err == nil {
			t.Errorf("workspaceDir(%q) = %q, want error", id, dir)
		}
	}
	dir, err := svc.workspaceDir("ws_safe")
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(dir) != "ws_safe" {
		t.Fatalf("safe dir = %q", dir)
	}
}

func TestCreateWorkspaceRejectsZipTraversal(t *testing.T) {
	var data bytes.Buffer
	zw := zip.NewWriter(&data)
	w, err := zw.Create("../outside.txt")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = w.Write([]byte("escape"))
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	svc := &Service{Opts: Options{WorkspacesRoot: root}}
	if _, err := svc.createWorkspaceFromZip("bad", legacyWorkspaceAccountID, bytes.NewReader(data.Bytes()), int64(data.Len())); err == nil {
		t.Fatal("traversal zip must be rejected")
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(root), "outside.txt")); !os.IsNotExist(err) {
		t.Fatalf("outside file was created: %v", err)
	}
}

func TestValidateWorkspaceZipLimitsAndConflicts(t *testing.T) {
	limits := workspaceUploadLimits{
		archiveBytes: defaultWorkspaceArchiveBytes, uncompressedBytes: defaultWorkspaceUncompressedBytes,
		fileBytes: defaultWorkspaceFileBytes,
	}
	t.Run("oversized file", func(t *testing.T) {
		zr := &zip.Reader{File: []*zip.File{{FileHeader: zip.FileHeader{
			Name: "large.bin", UncompressedSize64: uint64(limits.fileBytes + 1),
		}}}}
		if err := validateWorkspaceZip(zr, limits); err == nil {
			t.Fatal("oversized file must be rejected")
		}
	})

	t.Run("case insensitive duplicate", func(t *testing.T) {
		zr := &zip.Reader{File: []*zip.File{
			{FileHeader: zip.FileHeader{Name: "src/Main.go"}},
			{FileHeader: zip.FileHeader{Name: "src/main.go"}},
		}}
		if err := validateWorkspaceZip(zr, limits); err == nil {
			t.Fatal("case-insensitive duplicate must be rejected")
		}
	})

	t.Run("reserved metadata", func(t *testing.T) {
		zr := &zip.Reader{File: []*zip.File{{FileHeader: zip.FileHeader{Name: ".workspace.json"}}}}
		if err := validateWorkspaceZip(zr, limits); err == nil {
			t.Fatal("reserved metadata file must be rejected")
		}
	})

	t.Run("too many entries", func(t *testing.T) {
		zr := &zip.Reader{File: make([]*zip.File, maxWorkspaceEntries+1)}
		if err := validateWorkspaceZip(zr, limits); err == nil {
			t.Fatal("entry limit must be enforced")
		}
	})

	t.Run("too many files", func(t *testing.T) {
		files := make([]*zip.File, 0, maxWorkspaceFiles+1)
		for i := 0; i <= maxWorkspaceFiles; i++ {
			files = append(files, &zip.File{FileHeader: zip.FileHeader{Name: fmt.Sprintf("file-%d", i)}})
		}
		if err := validateWorkspaceZip(&zip.Reader{File: files}, limits); err == nil {
			t.Fatal("file limit must be enforced")
		}
	})
}

func TestWorkspaceUploadRejectsGeneratedBinaryAndPrivatePaths(t *testing.T) {
	tests := []struct {
		path      string
		directory bool
		allowed   bool
	}{
		{path: ".github/workflows/ci.yml"},
		{path: "node_modules/pkg/index.js"},
		{path: "dist/", directory: true},
		{path: "bin/server.exe"},
		{path: "release.zip"},
		{path: "gateway.test"},
		{path: "web/tsconfig.tsbuildinfo"},
		{path: "config.yaml"},
		{path: "etc/gateway.yaml"},
		{path: "appsettings.Production.json"},
		{path: "server.pem"},
		{path: ".env.production"},
		{path: "package.json", allowed: true},
		{path: "tsconfig.json", allowed: true},
		{path: ".gitignore", allowed: true},
		{path: ".env.example", allowed: true},
		{path: "src/main.go", allowed: true},
	}
	for _, test := range tests {
		t.Run(test.path, func(t *testing.T) {
			reason := disallowedWorkspacePath(test.path, test.directory)
			if test.allowed && reason != "" {
				t.Fatalf("allowed path rejected: %s", reason)
			}
			if !test.allowed && reason == "" {
				t.Fatal("disallowed path was accepted")
			}
		})
	}
}

func TestValidateWorkspaceZipRejectsExtensionlessExecutable(t *testing.T) {
	var data bytes.Buffer
	zw := zip.NewWriter(&data)
	w, err := zw.Create("server")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = w.Write([]byte{0x7f, 'E', 'L', 'F', 0x01})
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	zr, err := zip.NewReader(bytes.NewReader(data.Bytes()), int64(data.Len()))
	if err != nil {
		t.Fatal(err)
	}
	limits := workspaceUploadLimits{
		archiveBytes: defaultWorkspaceArchiveBytes, uncompressedBytes: defaultWorkspaceUncompressedBytes,
		fileBytes: defaultWorkspaceFileBytes,
	}
	if err := validateWorkspaceZip(zr, limits); err == nil || !strings.Contains(err.Error(), "executable") {
		t.Fatalf("executable validation error = %v", err)
	}
}

func TestWorkspaceUploadLimitsUseConfiguredValues(t *testing.T) {
	svc := &Service{Opts: Options{
		WorkspaceUploadMaxBytes: 700 << 20,
		WorkspaceMaxBytes:       900 << 20,
		WorkspaceMaxFileBytes:   64 << 20,
	}}
	got := svc.workspaceUploadLimits()
	if got.archiveBytes != 700<<20 || got.uncompressedBytes != 900<<20 || got.fileBytes != 64<<20 {
		t.Fatalf("limits = %+v", got)
	}
}

func TestWorkspaceOwnershipAndUserFileCount(t *testing.T) {
	dir := t.TempDir()
	svc, err := NewService(Options{
		Cwd: dir, DatabasePath: filepath.Join(dir, "gateway.db"), WorkspacesRoot: filepath.Join(dir, "workspaces"),
		AdminPassword: "admin-password", NoTools: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = svc.Close() })

	owner, err := svc.Audit.CreateAccount(context.Background(), "workspace-owner", "", "user", "owner-password")
	if err != nil {
		t.Fatal(err)
	}
	var data bytes.Buffer
	zw := zip.NewWriter(&data)
	w, err := zw.Create("README.md")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = w.Write([]byte("hello"))
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}

	info, err := svc.createWorkspaceFromZip("owned", owner.ID, bytes.NewReader(data.Bytes()), int64(data.Len()))
	if err != nil {
		t.Fatal(err)
	}
	if info.FileCount != 1 || info.SizeBytes != 5 {
		t.Fatalf("workspace counts internal metadata: %+v", info)
	}
	workspaceDir, err := svc.workspaceDir(info.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspaceDir, ".workspace.json"), []byte(`{"name":"changed","account_id":1}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.workspaceInfoForAccount(info.ID, owner.ID); err != nil {
		t.Fatalf("database owner cannot access workspace after metadata edit: %v", err)
	}
	if _, err := svc.workspaceInfoForAccount(info.ID, legacyWorkspaceAccountID); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("other account error = %v, want not found", err)
	}
	owned, err := svc.listWorkspaces(owner.ID)
	if err != nil || len(owned) != 1 || owned[0].ID != info.ID {
		t.Fatalf("owner list = %+v, %v", owned, err)
	}
	other, err := svc.listWorkspaces(legacyWorkspaceAccountID)
	if err != nil || len(other) != 0 {
		t.Fatalf("other account list = %+v, %v", other, err)
	}
}

func TestEnsurePathWithinRejectsSiblingPrefix(t *testing.T) {
	root := filepath.Join(t.TempDir(), "workspaces")
	if err := ensurePathWithin(root, root+"-evil"); err == nil {
		t.Fatal("sibling prefix must be rejected")
	}
}

func TestGitHubWorkspaceMetadataAndDownloadExcludeGitDirectory(t *testing.T) {
	root := t.TempDir()
	svc, err := NewService(Options{
		Cwd: root, DatabasePath: filepath.Join(root, "gateway.db"), WorkspacesRoot: filepath.Join(root, "workspaces"),
		AdminPassword: "admin-password", NoTools: true, GitHubTokenKey: "test-key",
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = svc.Close() })

	id := "ws_github"
	dir, err := svc.workspaceDir(id)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, ".git", "objects"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".git", "config"), []byte("secret git data"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := writeWorkspaceMetadata(dir, workspaceMetadataFile{
		Name: "repository", Source: "github", GitHubFullName: "octocat/hello", GitHubDefaultBranch: "main",
	}); err != nil {
		t.Fatal(err)
	}
	info, err := svc.workspaceInfo(id)
	if err != nil {
		t.Fatal(err)
	}
	info.AccountID = 1
	if err := svc.Control.UpsertWorkspace(context.Background(), info); err != nil {
		t.Fatal(err)
	}

	listed, err := svc.listWorkspaces(1)
	if err != nil || len(listed) != 1 || listed[0].Source != "github" || listed[0].GitHubFullName != "octocat/hello" {
		t.Fatalf("listed workspaces=%+v err=%v", listed, err)
	}
	if listed[0].FileCount != 1 {
		t.Fatalf("git internals counted as workspace files: %+v", listed[0])
	}

	var archive bytes.Buffer
	if err := svc.zipWorkspace(id, 1, &archive); err != nil {
		t.Fatal(err)
	}
	zr, err := zip.NewReader(bytes.NewReader(archive.Bytes()), int64(archive.Len()))
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, file := range zr.File {
		names = append(names, file.Name)
	}
	if !slices.Equal(names, []string{"main.go"}) {
		t.Fatalf("downloaded files=%v", names)
	}
}
