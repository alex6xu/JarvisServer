package gateway

import (
	"encoding/json"
	"net/http"
	"strconv"
)

func (s *Service) handleListTokens(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"tokens": s.Mem.listTokens()})
}

func (s *Service) handleCreateToken(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name string `json:"name"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	if body.Name == "" {
		body.Name = "api-key"
	}
	t := s.Mem.createToken(body.Name)
	writeJSON(w, http.StatusOK, map[string]any{"key": t.Key, "token": t})
}

func (s *Service) handleUpdateToken(w http.ResponseWriter, r *http.Request) {
	id := pathParam(r, "id")
	var body struct {
		Status int `json:"status"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json body")
		return
	}
	if err := s.Mem.updateTokenStatus(id, body.Status); err != nil {
		writeErr(w, http.StatusNotFound, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Service) handleDeleteToken(w http.ResponseWriter, r *http.Request) {
	if err := s.Mem.deleteToken(pathParam(r, "id")); err != nil {
		writeErr(w, http.StatusNotFound, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Service) handleListProviders(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"providers": s.Mem.listProviders()})
}

func (s *Service) handleCreateProvider(w http.ResponseWriter, r *http.Request) {
	var p Provider
	if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json body")
		return
	}
	out := s.Mem.upsertProvider(0, p)
	_ = s.Mem.saveProvidersToDisk()
	writeJSON(w, http.StatusOK, out)
}

func (s *Service) handleUpdateProvider(w http.ResponseWriter, r *http.Request) {
	id, err := parseProviderID(pathParam(r, "id"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	var p Provider
	if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json body")
		return
	}
	out := s.Mem.upsertProvider(id, p)
	_ = s.Mem.saveProvidersToDisk()
	writeJSON(w, http.StatusOK, out)
}

func (s *Service) handleDeleteProvider(w http.ResponseWriter, r *http.Request) {
	id, err := parseProviderID(pathParam(r, "id"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.Mem.deleteProvider(id); err != nil {
		writeErr(w, http.StatusNotFound, err.Error())
		return
	}
	_ = s.Mem.saveProvidersToDisk()
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Service) handleSetDefaultProvider(w http.ResponseWriter, r *http.Request) {
	id, err := parseProviderID(pathParam(r, "id"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.Mem.setDefaultProvider(id); err != nil {
		writeErr(w, http.StatusNotFound, err.Error())
		return
	}
	_ = s.Mem.saveProvidersToDisk()
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Service) handleFetchProviderModels(w http.ResponseWriter, r *http.Request) {
	id, err := parseProviderID(pathParam(r, "id"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if p, ok := s.Mem.getProvider(id); ok {
		models := parseProviderModels(p.Models)
		if len(models) > 0 {
			writeJSON(w, http.StatusOK, map[string]any{"models": models})
			return
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"models": []string{s.Opts.Model}})
}

func (s *Service) handleFetchModelsBody(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Type     json.RawMessage `json:"type"`
		Key      string          `json:"key"`
		BaseURL  string          `json:"base_url"`
		AuthMode string          `json:"auth_mode"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	_ = body
	// Real upstream model listing can be added later; return configured default for now.
	writeJSON(w, http.StatusOK, map[string]any{"models": []string{s.Opts.Model}})
}

func (s *Service) handleAdminStats(w http.ResponseWriter, _ *http.Request) {
	sessions, _ := s.Store.List()
	totalMsgs := 0
	for _, h := range sessions {
		if _, entries, err := s.Store.LoadEntries(h.ID); err == nil {
			totalMsgs += len(entries)
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"totalSessions":   len(sessions),
		"totalMessages":   totalMsgs,
		"totalTokens":     0,
		"totalCost":       0.0,
		"activeProviders": s.Mem.activeProviderCount(),
	})
}

func (s *Service) handleListRequestLogs(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
	writeJSON(w, http.StatusOK, map[string]any{"logs": s.Mem.listLogs(limit, offset)})
}

func (s *Service) handleGetRequestLog(w http.ResponseWriter, r *http.Request) {
	id := pathParam(r, "id")
	log, ok := s.Mem.getLog(id)
	if !ok {
		writeErr(w, http.StatusNotFound, "log not found")
		return
	}
	writeJSON(w, http.StatusOK, log)
}

func (s *Service) handleListRouteProfiles(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"route_profiles": s.Mem.listProfiles()})
}

func (s *Service) handleCreateRouteProfile(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name    string   `json:"name"`
		Purpose string   `json:"purpose"`
		Models  []string `json:"models"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json body")
		return
	}
	if body.Name == "" || len(body.Models) == 0 {
		writeErr(w, http.StatusBadRequest, "name and models are required")
		return
	}
	p := s.Mem.createProfile(body.Name, body.Purpose, body.Models)
	writeJSON(w, http.StatusOK, p)
}
