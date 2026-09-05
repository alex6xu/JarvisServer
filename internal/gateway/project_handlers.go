package gateway

import (
	"encoding/json"
	"net/http"
	"strings"
)

type createProjectBody struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

type assignSessionProjectBody struct {
	ProjectID string `json:"project_id"`
	Pinned    bool   `json:"pinned"`
}

func (s *Service) handleListProjects(w http.ResponseWriter, r *http.Request) {
	accountID, ok := s.requestAccountID(r)
	if !ok {
		writeErr(w, http.StatusUnauthorized, "account context is required")
		return
	}
	_, _ = s.Audit.ReconcileWorkspaceProjects(r.Context(), accountID)
	projects, err := s.Audit.ListProjects(r.Context(), accountID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"projects": projects})
}

func (s *Service) handleCreateProject(w http.ResponseWriter, r *http.Request) {
	accountID, ok := s.requestAccountID(r)
	if !ok {
		writeErr(w, http.StatusUnauthorized, "account context is required")
		return
	}
	var body createProjectBody
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json body")
		return
	}
	project, err := s.Audit.CreateProject(r.Context(), accountID, body.Name, body.Description)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"project": project})
}

func (s *Service) handleGetProject(w http.ResponseWriter, r *http.Request) {
	accountID, ok := s.requestAccountID(r)
	if !ok {
		writeErr(w, http.StatusUnauthorized, "account context is required")
		return
	}
	project, err := s.Audit.ProjectByID(r.Context(), accountID, pathParam(r, "id"))
	if err != nil {
		writeErr(w, http.StatusNotFound, "project not found")
		return
	}
	ids, err := s.Audit.ProjectSessionIDs(r.Context(), accountID, project.ID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	sessions := make([]SessionMeta, 0, len(ids))
	for _, id := range ids {
		header, _, err := s.Store.LoadEntries(id)
		if err != nil || !sessionOwnedByAccount(header, accountID) {
			continue
		}
		sessions = append(sessions, s.sessionMeta(header))
	}
	tags, err := s.Audit.ProjectTags(r.Context(), accountID, project.ID, 12)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, ProjectDetail{Project: project, Sessions: sessions, Tags: tags})
}

func (s *Service) handleGetSessionProject(w http.ResponseWriter, r *http.Request) {
	accountID, ok := s.requestAccountID(r)
	if !ok {
		writeErr(w, http.StatusUnauthorized, "account context is required")
		return
	}
	sessionID := strings.TrimSpace(pathParam(r, "sessionId"))
	header, _, err := s.Store.LoadEntries(sessionID)
	if err != nil || !sessionOwnedByAccount(header, accountID) {
		writeErr(w, http.StatusNotFound, "session not found")
		return
	}
	assignment, err := s.Audit.SessionProject(r.Context(), accountID, sessionID)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"assignment": nil})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"assignment": assignment})
}

func (s *Service) handleSetSessionProject(w http.ResponseWriter, r *http.Request) {
	accountID, ok := s.requestAccountID(r)
	if !ok {
		writeErr(w, http.StatusUnauthorized, "account context is required")
		return
	}
	var body assignSessionProjectBody
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json body")
		return
	}
	sessionID := strings.TrimSpace(pathParam(r, "sessionId"))
	if err := s.Audit.AssignSessionProject(r.Context(), accountID, sessionID, strings.TrimSpace(body.ProjectID), "user", 1, body.Pinned); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	assignment, _ := s.Audit.SessionProject(r.Context(), accountID, sessionID)
	writeJSON(w, http.StatusOK, map[string]any{"assignment": assignment})
}

func (s *Service) handleDeleteSessionProject(w http.ResponseWriter, r *http.Request) {
	accountID, ok := s.requestAccountID(r)
	if !ok {
		writeErr(w, http.StatusUnauthorized, "account context is required")
		return
	}
	if err := s.Audit.RemoveSessionProject(r.Context(), accountID, strings.TrimSpace(pathParam(r, "sessionId"))); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}
