package gateway

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, ErrorBody{Error: msg})
}

func (s *Service) handleHealthz(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusOK)
	_, _ = io.WriteString(w, "ok\n")
}

func (s *Service) handleModels(w http.ResponseWriter, _ *http.Request) {
	seen := map[string]bool{}
	var ids []string
	add := func(id string) {
		id = strings.TrimSpace(id)
		if id == "" || seen[id] {
			return
		}
		seen[id] = true
		ids = append(ids, id)
	}
	add("auto")
	for _, p := range s.Mem.listProviders() {
		if p.Status == 0 {
			continue
		}
		for _, m := range parseProviderModels(p.Models) {
			add(m)
		}
	}
	add(s.Opts.Model)
	data := make([]map[string]string, 0, len(ids))
	models := make([]map[string]string, 0, len(ids))
	for _, id := range ids {
		data = append(data, map[string]string{"id": id})
		models = append(models, map[string]string{"id": id, "name": id})
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"data":    data,
		"models":  models,
		"default": "auto",
	})
}

func (s *Service) handleChat(w http.ResponseWriter, r *http.Request) {
	var req ChatRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json body")
		return
	}
	accountID, ok := s.requestAccountID(r)
	if !ok {
		writeErr(w, http.StatusUnauthorized, "account context is required")
		return
	}
	req.AccountID = accountID
	resp, err := s.StartChat(r.Context(), req)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) || isNotFound(err) {
			writeErr(w, http.StatusNotFound, err.Error())
			return
		}
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Service) handleRunEvents(w http.ResponseWriter, r *http.Request) {
	runID := pathParam(r, "runId")
	st, ok := s.Runs.Get(runID)
	if !ok {
		writeErr(w, http.StatusNotFound, "run not found")
		return
	}
	afterSeq := parseAfterSeq(r.URL.Query().Get("after_seq"))

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeErr(w, http.StatusInternalServerError, "streaming unsupported")
		return
	}

	ctx := r.Context()
	ch := st.SubscribeContext(ctx, afterSeq)
	for {
		select {
		case <-ctx.Done():
			return
		case ev, open := <-ch:
			if !open {
				fmt.Fprintf(w, "data: [DONE]\n\n")
				flusher.Flush()
				return
			}
			b, err := json.Marshal(ev.Payload)
			if err != nil {
				continue
			}
			fmt.Fprintf(w, "data: %s\n\n", b)
			flusher.Flush()
		}
	}
}

func (s *Service) handleCancelRun(w http.ResponseWriter, r *http.Request) {
	runID := pathParam(r, "runId")
	if !s.Runs.Cancel(runID) {
		writeErr(w, http.StatusNotFound, "run not found")
		return
	}
	st, _ := s.Runs.Get(runID)
	writeJSON(w, http.StatusAccepted, map[string]any{"run": st.Info()})
}

func (s *Service) handleListRunAttempts(w http.ResponseWriter, r *http.Request) {
	runID := pathParam(r, "runId")
	if _, ok := s.Runs.Get(runID); !ok {
		writeErr(w, http.StatusNotFound, "run not found")
		return
	}
	attempts, err := s.Audit.ListRunAttempts(r.Context(), runID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"attempts": attempts})
}

func (s *Service) handleGetSession(w http.ResponseWriter, r *http.Request) {
	id := pathParam(r, "sessionId")
	resp, err := s.GetSession(id)
	if err != nil {
		if isNotFound(err) {
			writeErr(w, http.StatusNotFound, err.Error())
			return
		}
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Service) handleListSessions(w http.ResponseWriter, _ *http.Request) {
	resp, err := s.ListSessions()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Service) handleAuthMe(w http.ResponseWriter, r *http.Request) {
	acct, ok := requestAccount(r)
	if !ok {
		writeErr(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"account": acct})
}

func (s *Service) handleAuthLogin(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json body")
		return
	}
	acct, err := s.Audit.Authenticate(r.Context(), body.Username, body.Password)
	if err != nil {
		writeErr(w, http.StatusUnauthorized, err.Error())
		return
	}
	_, token, err := s.Audit.IssueToken(r.Context(), acct.ID, "web-session", "sess_", 24*time.Hour)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"token":   token,
		"account": acct,
	})
}

func (s *Service) handleAuthConfig(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]bool{
		"registration_enabled": s.Opts.AllowRegistration,
	})
}

func (s *Service) handleAuthRegister(w http.ResponseWriter, r *http.Request) {
	if !s.Opts.AllowRegistration {
		writeErr(w, http.StatusForbidden, "public registration is disabled")
		return
	}
	var body struct {
		Username string `json:"username"`
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json body")
		return
	}
	acct, err := s.Audit.CreateAccount(r.Context(), body.Username, body.Email, "user", body.Password)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	_, token, err := s.Audit.IssueToken(r.Context(), acct.ID, "web-session", "sess_", 24*time.Hour)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"token": token, "account": acct})
}

func (s *Service) handleAuthLogout(w http.ResponseWriter, r *http.Request) {
	if raw := bearerToken(r); raw != "" {
		_ = s.Audit.RevokeToken(r.Context(), raw)
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Service) authOK(r *http.Request) bool {
	_, err := s.authenticateRequest(r)
	return err == nil
}

func isNotFound(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, os.ErrNotExist) {
		return true
	}
	msg := err.Error()
	return strings.Contains(msg, "not found") || strings.Contains(msg, "no such")
}
