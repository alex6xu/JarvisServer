package gateway

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

func documentRelativeDir(accountID int, projectID, documentID string) (string, error) {
	if accountID <= 0 || !safeServerID(projectID) || !safeServerID(documentID) {
		return "", errors.New("invalid document storage identifier")
	}
	return filepath.Join(strconv.Itoa(accountID), projectID, documentID), nil
}

func safeServerID(id string) bool {
	if id == "" || len(id) > 128 {
		return false
	}
	for _, r := range id {
		if !(r >= 'a' && r <= 'z') && !(r >= 'A' && r <= 'Z') && !(r >= '0' && r <= '9') && r != '_' && r != '-' {
			return false
		}
	}
	return true
}

func resolveDocumentPath(root, relative string) (string, error) {
	if root == "" || filepath.IsAbs(relative) || relative == "" {
		return "", errors.New("invalid document path")
	}
	clean := filepath.Clean(relative)
	if clean == "." || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", errors.New("invalid document path")
	}
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	full := filepath.Join(rootAbs, clean)
	if full != rootAbs && !strings.HasPrefix(full, rootAbs+string(filepath.Separator)) {
		return "", errors.New("document path escapes root")
	}
	return full, nil
}

func createDocumentDir(root, relativeDir string) (string, error) {
	path, err := resolveDocumentPath(root, relativeDir)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(path, 0o700); err != nil {
		return "", err
	}
	if err := os.Chmod(path, 0o700); err != nil {
		return "", err
	}
	return path, nil
}

func writeDocumentAtomic(path string, source io.Reader, max int64) (int64, error) {
	if max <= 0 {
		return 0, errors.New("invalid size limit")
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".upload-*")
	if err != nil {
		return 0, err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return 0, err
	}
	n, copyErr := io.Copy(tmp, io.LimitReader(source, max+1))
	if copyErr == nil && n > max {
		copyErr = fmt.Errorf("document exceeds %d byte limit", max)
	}
	if copyErr == nil {
		copyErr = tmp.Sync()
	}
	if closeErr := tmp.Close(); copyErr == nil {
		copyErr = closeErr
	}
	if copyErr != nil {
		return n, copyErr
	}
	if err := os.Rename(tmpName, path); err != nil {
		return n, err
	}
	return n, os.Chmod(path, 0o600)
}

func openDocumentFile(path string) (*os.File, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return nil, errors.New("document storage entry is not a regular file")
	}
	return os.Open(path)
}
