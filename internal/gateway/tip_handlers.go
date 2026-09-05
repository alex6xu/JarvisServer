package gateway

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
)

const maxTipRequestBytes = 128 << 10

type createProjectTipBody struct {
	Type     string `json:"type"`
	Title    string `json:"title"`
	Content  string `json:"content"`
	Priority int    `json:"priority"`
	DueAt    string `json:"due_at"`
}

type updateProjectTipBody struct {
	Version  int     `json:"version"`
	Type     *string `json:"type"`
	Status   *string `json:"status"`
	Title    *string `json:"title"`
	Content  *string `json:"content"`
	Priority *int    `json:"priority"`
	DueAt    *string `json:"due_at"`
}

func (s *Service) handleListProjectTips(w http.ResponseWriter, r *http.Request) {
	accountID, ok := s.requestAccountID(r)
	if !ok {
		writeErr(w, http.StatusUnauthorized, "account context is required")
		return
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	tips, err := s.Audit.ListProjectTips(r.Context(), accountID, pathParam(r, "projectId"), ProjectTipListOptions{
		Status: strings.TrimSpace(r.URL.Query().Get("status")),
		Type:   strings.TrimSpace(r.URL.Query().Get("type")),
		Query:  strings.TrimSpace(r.URL.Query().Get("q")),
		Limit:  limit,
	})
	if errors.Is(err, sql.ErrNoRows) {
		writeErr(w, http.StatusNotFound, "project not found")
		return
	}
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"tips": tips})
}

func (s *Service) handleCreateProjectTip(w http.ResponseWriter, r *http.Request) {
	accountID, ok := s.requestAccountID(r)
	if !ok {
		writeErr(w, http.StatusUnauthorized, "account context is required")
		return
	}
	var body createProjectTipBody
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxTipRequestBytes))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json body")
		return
	}
	tip, err := s.Audit.CreateProjectTip(r.Context(), accountID, CreateProjectTipInput{
		ProjectID: pathParam(r, "projectId"), Type: body.Type, Title: body.Title,
		Content: body.Content, Priority: body.Priority, DueAt: body.DueAt,
	})
	if errors.Is(err, sql.ErrNoRows) {
		writeErr(w, http.StatusNotFound, "project not found")
		return
	}
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"tip": tip})
}

func (s *Service) handleGetProjectTip(w http.ResponseWriter, r *http.Request) {
	accountID, ok := s.requestAccountID(r)
	if !ok {
		writeErr(w, http.StatusUnauthorized, "account context is required")
		return
	}
	tip, err := s.Audit.ProjectTipByID(r.Context(), accountID, pathParam(r, "projectId"), pathParam(r, "tipId"))
	if errors.Is(err, sql.ErrNoRows) {
		writeErr(w, http.StatusNotFound, "tip not found")
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"tip": tip})
}

func (s *Service) handleUpdateProjectTip(w http.ResponseWriter, r *http.Request) {
	accountID, ok := s.requestAccountID(r)
	if !ok {
		writeErr(w, http.StatusUnauthorized, "account context is required")
		return
	}
	var body updateProjectTipBody
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxTipRequestBytes))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json body")
		return
	}
	tip, err := s.Audit.UpdateProjectTip(r.Context(), accountID, UpdateProjectTipInput{
		ProjectID: pathParam(r, "projectId"), ID: pathParam(r, "tipId"), Version: body.Version,
		Type: body.Type, Status: body.Status, Title: body.Title, Content: body.Content,
		Priority: body.Priority, DueAt: body.DueAt,
	})
	switch {
	case errors.Is(err, sql.ErrNoRows):
		writeErr(w, http.StatusNotFound, "tip not found")
	case errors.Is(err, ErrTipVersionConflict):
		writeErr(w, http.StatusConflict, err.Error())
	case err != nil:
		writeErr(w, http.StatusBadRequest, err.Error())
	default:
		writeJSON(w, http.StatusOK, map[string]any{"tip": tip})
	}
}

func (s *Service) handleDeleteProjectTip(w http.ResponseWriter, r *http.Request) {
	accountID, ok := s.requestAccountID(r)
	if !ok {
		writeErr(w, http.StatusUnauthorized, "account context is required")
		return
	}
	err := s.Audit.DeleteProjectTip(r.Context(), accountID, pathParam(r, "projectId"), pathParam(r, "tipId"))
	if errors.Is(err, sql.ErrNoRows) {
		writeErr(w, http.StatusNotFound, "tip not found")
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}
