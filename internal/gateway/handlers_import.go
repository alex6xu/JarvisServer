package gateway

import (
	"encoding/json"
	"net/http"
	"strings"
)

func (s *Service) handleImportPreview(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Content string `json:"content"`
		Title   string `json:"title"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json body")
		return
	}
	if strings.TrimSpace(body.Content) == "" {
		writeErr(w, http.StatusBadRequest, "content is required")
		return
	}
	title, msgs := ImportPreview(body.Content, body.Title)
	writeJSON(w, http.StatusOK, map[string]any{"title": title, "messages": msgs})
}

func (s *Service) handleImportSession(w http.ResponseWriter, r *http.Request) {
	accountID, ok := s.requestAccountID(r)
	if !ok {
		writeErr(w, http.StatusUnauthorized, "account context is required")
		return
	}
	var body struct {
		Content string `json:"content"`
		Title   string `json:"title"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json body")
		return
	}
	meta, err := s.importSession(body.Content, body.Title, accountID)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"session": meta})
}
