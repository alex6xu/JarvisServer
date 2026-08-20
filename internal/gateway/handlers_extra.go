package gateway

import (
	"errors"
	"fmt"
	"io"
	"net/http"
)

func (s *Service) handleListWorkspaces(w http.ResponseWriter, r *http.Request) {
	accountID, ok := s.requestAccountID(r)
	if !ok {
		writeErr(w, http.StatusUnauthorized, "account context is required")
		return
	}
	list, err := s.listWorkspaces(accountID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"workspaces": list})
}

func (s *Service) handleWorkspaceUploadLimits(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requestAccountID(r); !ok {
		writeErr(w, http.StatusUnauthorized, "account context is required")
		return
	}
	limits := s.workspaceUploadLimits()
	writeJSON(w, http.StatusOK, map[string]any{
		"archive_bytes":      limits.archiveBytes,
		"uncompressed_bytes": limits.uncompressedBytes,
		"file_bytes":         limits.fileBytes,
		"max_files":          maxWorkspaceFiles,
	})
}

func (s *Service) handleUploadWorkspace(w http.ResponseWriter, r *http.Request) {
	accountID, ok := s.requestAccountID(r)
	if !ok {
		writeErr(w, http.StatusUnauthorized, "account context is required")
		return
	}
	limits := s.workspaceUploadLimits()
	r.Body = http.MaxBytesReader(w, r.Body, limits.archiveBytes+(1<<20))
	if err := r.ParseMultipartForm(8 << 20); err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			writeErr(w, http.StatusRequestEntityTooLarge, fmt.Sprintf("workspace archive exceeds %d MB limit", limits.archiveBytes>>20))
			return
		}
		writeErr(w, http.StatusBadRequest, "invalid multipart form")
		return
	}
	if r.MultipartForm != nil {
		defer r.MultipartForm.RemoveAll()
	}
	name := r.FormValue("name")
	file, _, err := r.FormFile("archive")
	if err != nil {
		writeErr(w, http.StatusBadRequest, "archive is required")
		return
	}
	defer file.Close()
	size, err := file.Seek(0, io.SeekEnd)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "cannot inspect workspace archive")
		return
	}
	if size <= 0 || size > limits.archiveBytes {
		writeErr(w, http.StatusRequestEntityTooLarge, fmt.Sprintf("workspace archive exceeds %d MB limit", limits.archiveBytes>>20))
		return
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		writeErr(w, http.StatusBadRequest, "cannot read workspace archive")
		return
	}
	info, err := s.createWorkspaceFromZip(name, accountID, file, size)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"workspace": info})
}

func (s *Service) handleDownloadWorkspace(w http.ResponseWriter, r *http.Request) {
	accountID, ok := s.requestAccountID(r)
	if !ok {
		writeErr(w, http.StatusUnauthorized, "account context is required")
		return
	}
	id := pathParam(r, "id")
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", "attachment; filename="+id+".zip")
	if err := s.zipWorkspace(id, accountID, w); err != nil {
		writeErr(w, http.StatusNotFound, err.Error())
	}
}

func (s *Service) handleDeleteWorkspace(w http.ResponseWriter, r *http.Request) {
	accountID, ok := s.requestAccountID(r)
	if !ok {
		writeErr(w, http.StatusUnauthorized, "account context is required")
		return
	}
	if err := s.deleteWorkspace(pathParam(r, "id"), accountID); err != nil {
		writeErr(w, http.StatusNotFound, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Service) handleClaudeOAuthStatus(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"configured": false, "connected": false, "email": "",
	})
}

func (s *Service) handleClaudeOAuthAuthorize(w http.ResponseWriter, _ *http.Request) {
	writeErr(w, http.StatusBadRequest, "claude_oauth is not configured")
}

func (s *Service) handleClaudeOAuthExchange(w http.ResponseWriter, _ *http.Request) {
	writeErr(w, http.StatusBadRequest, "claude_oauth is not configured")
}

func (s *Service) handleClaudeOAuthDisconnect(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Service) handleASRStatus(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"enabled": false})
}

func (s *Service) handleASR(w http.ResponseWriter, _ *http.Request) {
	writeErr(w, http.StatusBadRequest, "asr is not configured")
}

func (s *Service) handleListTags(w http.ResponseWriter, _ *http.Request) {
	s.Mem.ensureDemoTag()
	writeJSON(w, http.StatusOK, map[string]any{"tags": s.Mem.listTags()})
}

func (s *Service) handleTagsOverview(w http.ResponseWriter, _ *http.Request) {
	s.Mem.ensureDemoTag()
	writeJSON(w, http.StatusOK, map[string]any{"groups": []any{}})
}

func (s *Service) handleGetTag(w http.ResponseWriter, r *http.Request) {
	slug := pathParam(r, "slug")
	tag, ok := s.Mem.getTag(slug)
	if !ok {
		s.Mem.ensureDemoTag()
		tag, ok = s.Mem.getTag(slug)
	}
	if !ok {
		writeErr(w, http.StatusNotFound, "tag not found")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"tag": tag, "messages": []any{}})
}

func (s *Service) handleRetag(w http.ResponseWriter, _ *http.Request) {
	s.Mem.ensureDemoTag()
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}
