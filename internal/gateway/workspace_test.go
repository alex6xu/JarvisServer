package gateway

import (
	"archive/zip"
	"bytes"
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
	if _, err := svc.createWorkspaceFromZip("bad", bytes.NewReader(data.Bytes()), int64(data.Len())); err == nil {
		t.Fatal("traversal zip must be rejected")
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(root), "outside.txt")); !os.IsNotExist(err) {
		t.Fatalf("outside file was created: %v", err)
	}
}

func TestEnsurePathWithinRejectsSiblingPrefix(t *testing.T) {
	root := filepath.Join(t.TempDir(), "workspaces")
	if err := ensurePathWithin(root, root+"-evil"); err == nil {
		t.Fatal("sibling prefix must be rejected")
	}
}
