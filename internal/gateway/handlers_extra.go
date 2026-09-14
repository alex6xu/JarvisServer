package gateway

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/alex6xu/jarvisserver/internal/distributedlog"
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
	started := time.Now()
	accountID, ok := s.requestAccountID(r)
	if !ok {
		s.logWorkspaceUploadFailure(r, 0, "account", http.StatusUnauthorized, errors.New("account context is required"), started)
		writeErr(w, http.StatusUnauthorized, "account context is required")
		return
	}
	limits := s.workspaceUploadLimits()
	r.Body = http.MaxBytesReader(w, r.Body, workspaceUploadRequestMaxBytes(limits))
	if err := r.ParseMultipartForm(8 << 20); err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			s.logWorkspaceUploadFailure(r, accountID, "multipart_parse", http.StatusRequestEntityTooLarge, err, started)
			writeErr(w, http.StatusRequestEntityTooLarge, fmt.Sprintf("workspace archive exceeds %d MB limit", limits.archiveBytes>>20))
			return
		}
		s.logWorkspaceUploadFailure(r, accountID, "multipart_parse", http.StatusBadRequest, err, started)
		writeErr(w, http.StatusBadRequest, "invalid multipart form")
		return
	}
	if r.MultipartForm != nil {
		defer r.MultipartForm.RemoveAll()
	}
	name := r.FormValue("name")
	file, _, err := r.FormFile("archive")
	if err != nil {
		s.logWorkspaceUploadFailure(r, accountID, "archive_lookup", http.StatusBadRequest, err, started)
		writeErr(w, http.StatusBadRequest, "archive is required")
		return
	}
	defer file.Close()
	size, err := file.Seek(0, io.SeekEnd)
	if err != nil {
		s.logWorkspaceUploadFailure(r, accountID, "archive_inspect", http.StatusBadRequest, err, started)
		writeErr(w, http.StatusBadRequest, "cannot inspect workspace archive")
		return
	}
	if size <= 0 || size > limits.archiveBytes {
		s.logWorkspaceUploadFailure(r, accountID, "archive_size", http.StatusRequestEntityTooLarge,
			fmt.Errorf("archive size %d is outside allowed range 1..%d", size, limits.archiveBytes), started)
		writeErr(w, http.StatusRequestEntityTooLarge, fmt.Sprintf("workspace archive exceeds %d MB limit", limits.archiveBytes>>20))
		return
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		s.logWorkspaceUploadFailure(r, accountID, "archive_rewind", http.StatusBadRequest, err, started)
		writeErr(w, http.StatusBadRequest, "cannot read workspace archive")
		return
	}
	info, err := s.createWorkspaceFromZip(name, accountID, file, size)
	if err != nil {
		s.logWorkspaceUploadFailure(r, accountID, "workspace_create", http.StatusBadRequest, err, started)
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	s.Logger.Info(r.Context(), "workspace upload completed",
		distributedlog.F("account_id", accountID),
		distributedlog.F("workspace_id", info.ID),
		distributedlog.F("archive_bytes", size),
		distributedlog.F("file_count", info.FileCount),
		distributedlog.F("workspace_bytes", info.SizeBytes),
		distributedlog.F("duration_ms", time.Since(started).Milliseconds()),
	)
	writeJSON(w, http.StatusOK, map[string]any{"workspace": info})
}

func (s *Service) logWorkspaceUploadFailure(r *http.Request, accountID int, stage string, status int, err error, started time.Time) {
	s.Logger.Error(r.Context(), "workspace upload failed",
		distributedlog.F("account_id", accountID),
		distributedlog.F("stage", stage),
		distributedlog.F("status", status),
		distributedlog.F("content_length", r.ContentLength),
		distributedlog.F("duration_ms", time.Since(started).Milliseconds()),
		distributedlog.Err(err),
	)
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

func (s *Service) handleListTags(w http.ResponseWriter, r *http.Request) {
	accountID, ok := s.requestAccountID(r)
	if !ok {
		writeErr(w, http.StatusUnauthorized, "account context is required")
		return
	}
	limit := parseTagInt(r.URL.Query().Get("limit"), 80)
	tags, err := s.Audit.ListAccountTags(r.Context(), accountID, strings.TrimSpace(r.URL.Query().Get("kind")), limit)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"tags": tags})
}

func (s *Service) handleTagsOverview(w http.ResponseWriter, r *http.Request) {
	accountID, ok := s.requestAccountID(r)
	if !ok {
		writeErr(w, http.StatusUnauthorized, "account context is required")
		return
	}
	groups, err := s.Audit.TagsOverview(r.Context(), accountID,
		parseTagInt(r.URL.Query().Get("top"), 12), parseTagInt(r.URL.Query().Get("per_tag"), 5))
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"groups": groups})
}

func (s *Service) handleGetTag(w http.ResponseWriter, r *http.Request) {
	accountID, ok := s.requestAccountID(r)
	if !ok {
		writeErr(w, http.StatusUnauthorized, "account context is required")
		return
	}
	slug := strings.ToLower(strings.TrimSpace(pathParam(r, "slug")))
	tag, err := s.Audit.AccountTagBySlug(r.Context(), accountID, slug)
	if err != nil {
		if isMissingAccountTag(err) {
			writeErr(w, http.StatusNotFound, "tag not found")
			return
		}
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	messages, err := s.Audit.TaggedMessages(r.Context(), accountID, slug, parseTagInt(r.URL.Query().Get("limit"), 80))
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"tag": tag, "messages": messages})
}

func (s *Service) handleRetag(w http.ResponseWriter, r *http.Request) {
	accountID, ok := s.requestAccountID(r)
	if !ok {
		writeErr(w, http.StatusUnauthorized, "account context is required")
		return
	}
	classified, err := s.Audit.RetagAccountMessages(r.Context(), accountID, parseTagInt(r.URL.Query().Get("limit"), 200))
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "classified": classified})
}

func parseTagInt(value string, fallback int) int {
	n, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil || n <= 0 {
		return fallback
	}
	return n
}
