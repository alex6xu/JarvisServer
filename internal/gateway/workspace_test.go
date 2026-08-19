package gateway

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
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
	t.Run("oversized file", func(t *testing.T) {
		zr := &zip.Reader{File: []*zip.File{{FileHeader: zip.FileHeader{
			Name: "large.bin", UncompressedSize64: uint64(maxWorkspaceFileBytes + 1),
		}}}}
		if err := validateWorkspaceZip(zr); err == nil {
			t.Fatal("oversized file must be rejected")
		}
	})

	t.Run("case insensitive duplicate", func(t *testing.T) {
		zr := &zip.Reader{File: []*zip.File{
			{FileHeader: zip.FileHeader{Name: "src/Main.go"}},
			{FileHeader: zip.FileHeader{Name: "src/main.go"}},
		}}
		if err := validateWorkspaceZip(zr); err == nil {
			t.Fatal("case-insensitive duplicate must be rejected")
		}
	})

	t.Run("reserved metadata", func(t *testing.T) {
		zr := &zip.Reader{File: []*zip.File{{FileHeader: zip.FileHeader{Name: ".workspace.json"}}}}
		if err := validateWorkspaceZip(zr); err == nil {
			t.Fatal("reserved metadata file must be rejected")
		}
	})

	t.Run("too many entries", func(t *testing.T) {
		zr := &zip.Reader{File: make([]*zip.File, maxWorkspaceEntries+1)}
		if err := validateWorkspaceZip(zr); err == nil {
			t.Fatal("entry limit must be enforced")
		}
	})

	t.Run("too many files", func(t *testing.T) {
		files := make([]*zip.File, 0, maxWorkspaceFiles+1)
		for i := 0; i <= maxWorkspaceFiles; i++ {
			files = append(files, &zip.File{FileHeader: zip.FileHeader{Name: fmt.Sprintf("file-%d", i)}})
		}
		if err := validateWorkspaceZip(&zip.Reader{File: files}); err == nil {
			t.Fatal("file limit must be enforced")
		}
	})
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
