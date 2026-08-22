package gateway

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
)

type skillContentBody struct {
	Content  string `json:"content"`
	Revision int64  `json:"revision"`
}

type skillStatusBody struct {
	Enabled bool `json:"enabled"`
}

func (s *Service) handleAdminListSkills(w http.ResponseWriter, r *http.Request) {
	if s.Skills == nil {
		writeErr(w, http.StatusServiceUnavailable, "skills are disabled")
		return
	}
	accountID, _ := s.requestAccountID(r)
	skills, err := s.Skills.List(r.Context(), accountID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "cannot list skills")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"skills": skills, "directory": s.Skills.Dir()})
}

func (s *Service) handleAdminGetSkill(w http.ResponseWriter, r *http.Request) {
	if s.Skills == nil {
		writeErr(w, http.StatusServiceUnavailable, "skills are disabled")
		return
	}
	accountID, _ := s.requestAccountID(r)
	skill, content, err := s.Skills.Get(r.Context(), accountID, pathParam(r, "name"))
	if err != nil {
		writeSkillError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"skill": skill, "content": content})
}

func (s *Service) handleAdminValidateSkill(w http.ResponseWriter, r *http.Request) {
	if s.Skills == nil {
		writeErr(w, http.StatusServiceUnavailable, "skills are disabled")
		return
	}
	var body skillContentBody
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxSkillFileBytes+1024)).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json body")
		return
	}
	validation := s.Skills.Validate(r.Context(), []byte(body.Content))
	status := http.StatusOK
	if !validation.Valid {
		status = http.StatusBadRequest
	}
	writeJSON(w, status, validation)
}

func (s *Service) handleAdminCreateSkill(w http.ResponseWriter, r *http.Request) {
	if s.Skills == nil {
		writeErr(w, http.StatusServiceUnavailable, "skills are disabled")
		return
	}
	actor, ok := requestAccount(r)
	if !ok || actor.ID <= 0 {
		writeErr(w, http.StatusUnauthorized, "account context is required")
		return
	}
	var body skillContentBody
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxSkillFileBytes+1024)).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json body")
		return
	}
	skill, err := s.Skills.Create(r.Context(), actor.ID, []byte(body.Content))
	if err != nil {
		writeSkillError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"skill": skill})
}

func (s *Service) handleAdminUpdateSkill(w http.ResponseWriter, r *http.Request) {
	if s.Skills == nil {
		writeErr(w, http.StatusServiceUnavailable, "skills are disabled")
		return
	}
	accountID, ok := s.requestAccountID(r)
	if !ok {
		writeErr(w, http.StatusUnauthorized, "account context is required")
		return
	}
	var body skillContentBody
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxSkillFileBytes+1024)).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json body")
		return
	}
	skill, err := s.Skills.Update(r.Context(), accountID, pathParam(r, "name"), body.Revision, []byte(body.Content))
	if err != nil {
		writeSkillError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"skill": skill})
}

func (s *Service) handleAdminSkillStatus(w http.ResponseWriter, r *http.Request) {
	if s.Skills == nil {
		writeErr(w, http.StatusServiceUnavailable, "skills are disabled")
		return
	}
	var body skillStatusBody
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json body")
		return
	}
	if err := s.Skills.SetGlobalEnabled(r.Context(), pathParam(r, "name"), body.Enabled); err != nil {
		writeSkillError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "enabled": body.Enabled})
}

func (s *Service) handleAdminDeleteSkill(w http.ResponseWriter, r *http.Request) {
	if s.Skills == nil {
		writeErr(w, http.StatusServiceUnavailable, "skills are disabled")
		return
	}
	revision, err := strconv.ParseInt(r.URL.Query().Get("revision"), 10, 64)
	if err != nil || revision <= 0 {
		writeErr(w, http.StatusBadRequest, "revision is required")
		return
	}
	if err := s.Skills.Delete(r.Context(), pathParam(r, "name"), revision); err != nil {
		writeSkillError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Service) handleAdminReloadSkills(w http.ResponseWriter, r *http.Request) {
	if s.Skills == nil {
		writeErr(w, http.StatusServiceUnavailable, "skills are disabled")
		return
	}
	result, err := s.Skills.Reload(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Service) handleListAccountSkills(w http.ResponseWriter, r *http.Request) {
	if s.Skills == nil {
		writeJSON(w, http.StatusOK, map[string]any{"skills": []SkillSummary{}})
		return
	}
	accountID, ok := s.requestAccountID(r)
	if !ok {
		writeErr(w, http.StatusUnauthorized, "account context is required")
		return
	}
	skills, err := s.Skills.List(r.Context(), accountID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "cannot list skills")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"skills": skills})
}

func (s *Service) handleAccountSkillStatus(w http.ResponseWriter, r *http.Request) {
	if s.Skills == nil {
		writeErr(w, http.StatusServiceUnavailable, "skills are disabled")
		return
	}
	accountID, ok := s.requestAccountID(r)
	if !ok {
		writeErr(w, http.StatusUnauthorized, "account context is required")
		return
	}
	var body skillStatusBody
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json body")
		return
	}
	if err := s.Skills.SetAccountEnabled(r.Context(), accountID, pathParam(r, "name"), body.Enabled); err != nil {
		writeSkillError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "enabled": body.Enabled})
}

func writeSkillError(w http.ResponseWriter, err error) {
	status := http.StatusBadRequest
	switch {
	case errors.Is(err, errSkillNotFound):
		status = http.StatusNotFound
	case errors.Is(err, errSkillConflict):
		status = http.StatusConflict
	}
	writeErr(w, status, err.Error())
}
