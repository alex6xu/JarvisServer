package gateway

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	corerouter "github.com/alex6xu/jarvisserver/internal/router"
)

func (s *Service) handleListTokens(w http.ResponseWriter, r *http.Request) {
	tokens, err := s.Audit.ListAPITokens(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"tokens": tokens})
}

func (s *Service) handleCreateToken(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name string `json:"name"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	if body.Name == "" {
		body.Name = "api-key"
	}
	account, ok := requestAccount(r)
	if !ok {
		writeErr(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	t, raw, err := s.Audit.IssueToken(r.Context(), account.ID, body.Name, "sk-", 0)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"key": raw, "token": t})
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
	if err := s.Audit.SetTokenStatus(r.Context(), id, body.Status); err != nil {
		writeErr(w, http.StatusNotFound, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Service) handleDeleteToken(w http.ResponseWriter, r *http.Request) {
	if err := s.Audit.DeleteToken(r.Context(), pathParam(r, "id")); err != nil {
		writeErr(w, http.StatusNotFound, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Service) handleListProviders(w http.ResponseWriter, _ *http.Request) {
	providers := s.Mem.listProviders()
	for i := range providers {
		providers[i].Key = ""
	}
	writeJSON(w, http.StatusOK, map[string]any{"providers": providers})
}

func (s *Service) handleCreateProvider(w http.ResponseWriter, r *http.Request) {
	var p Provider
	if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json body")
		return
	}
	if err := validateProviderInput(p); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	p = normalizeProviderConfig(p)
	out := s.Mem.upsertProvider(0, p)
	if err := s.persistProviders(r.Context()); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	out.Key = ""
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
	if err := validateProviderInput(p); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	p = normalizeProviderConfig(p)
	out := s.Mem.upsertProvider(id, p)
	if err := s.persistProviders(r.Context()); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	out.Key = ""
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
	if err := s.persistProviders(r.Context()); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
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
	if err := s.persistProviders(r.Context()); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Service) handleSetProviderStatus(w http.ResponseWriter, r *http.Request) {
	id, err := parseProviderID(pathParam(r, "id"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	var body struct {
		Status int `json:"status"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json body")
		return
	}
	if err := s.Mem.setProviderStatus(id, body.Status); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.persistProviders(r.Context()); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Service) persistProviders(ctx context.Context) error {
	return s.Audit.ReplaceProviders(ctx, s.Mem.listProviders())
}

func (s *Service) handleFetchProviderModels(w http.ResponseWriter, r *http.Request) {
	id, err := parseProviderID(pathParam(r, "id"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if p, ok := s.Mem.getProvider(id); ok {
		models, err := fetchProviderModels(r.Context(), *p, s.Opts.AllowPrivateProviderURLs)
		if err != nil {
			writeErr(w, http.StatusBadGateway, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"models": models})
		return
	}
	writeErr(w, http.StatusNotFound, "provider not found")
}

func (s *Service) handleFetchModelsBody(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Type     int    `json:"type"`
		Key      string `json:"key"`
		BaseURL  string `json:"base_url"`
		AuthMode string `json:"auth_mode"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	models, err := fetchProviderModels(r.Context(), Provider{
		Type: body.Type, Key: body.Key, BaseURL: body.BaseURL, AuthMode: body.AuthMode,
	}, s.Opts.AllowPrivateProviderURLs)
	if err != nil {
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"models": models})
}

func (s *Service) handleProbeProvider(w http.ResponseWriter, r *http.Request) {
	id, err := parseProviderID(pathParam(r, "id"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	configured, ok := s.Mem.getProvider(id)
	if !ok {
		writeErr(w, http.StatusNotFound, "provider not found")
		return
	}
	started := time.Now()
	models, probeErr := fetchProviderModels(r.Context(), *configured, s.Opts.AllowPrivateProviderURLs)
	result := corerouter.AttemptResult{
		EndpointID: "provider_" + strconv.Itoa(id), AttemptID: newID("probe"),
		Success: probeErr == nil, Latency: time.Since(started), OccurredAt: time.Now().UTC(),
	}
	if probeErr != nil {
		result.ErrorText = probeErr.Error()
		result.ErrorCategory = classifyProviderError(probeErr)
	}
	if err := s.Router.ObserveResult(result); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if probeErr != nil {
		writeErr(w, http.StatusBadGateway, probeErr.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "models": models, "latency_ms": result.Latency.Milliseconds()})
}

func (s *Service) handleListRoutePolicies(w http.ResponseWriter, r *http.Request) {
	policies, err := s.Audit.ListRoutePolicies(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"route_policies": policies})
}

func (s *Service) handlePublishRoutePolicy(w http.ResponseWriter, r *http.Request) {
	var policy corerouter.Policy
	if err := json.NewDecoder(r.Body).Decode(&policy); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json body")
		return
	}
	if pathID := pathParam(r, "id"); pathID != "" {
		policy.ID = pathID
	}
	policy.ID = strings.TrimSpace(policy.ID)
	policy.Name = strings.TrimSpace(policy.Name)
	policy.Mode = strings.ToLower(strings.TrimSpace(policy.Mode))
	if policy.ID == "" || policy.Name == "" {
		writeErr(w, http.StatusBadRequest, "id and name are required")
		return
	}
	validMode := map[string]bool{"balanced": true, "quality-first": true, "cost-first": true, "latency-first": true, "fixed-provider": true}
	if !validMode[policy.Mode] {
		writeErr(w, http.StatusBadRequest, "invalid route policy mode")
		return
	}
	published, err := s.Audit.PublishRoutePolicy(r.Context(), policy)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if published.ID == "balanced" {
		s.Router.SetPolicy(published)
	}
	writeJSON(w, http.StatusOK, published)
}

func (s *Service) handleRoutePreview(w http.ResponseWriter, r *http.Request) {
	purpose := normalizeRoutePurpose(RoutePurpose(r.URL.Query().Get("purpose")))
	plan, err := s.resolveLLMPlanForPurpose(r.URL.Query().Get("model"), purpose, 0)
	if err != nil {
		writeErr(w, http.StatusNotFound, err.Error())
		return
	}
	type previewCandidate struct {
		Order         int            `json:"order"`
		ProviderID    int            `json:"provider_id,omitempty"`
		ProviderName  string         `json:"provider_name"`
		Model         string         `json:"model"`
		BaseURL       string         `json:"base_url,omitempty"`
		Protocol      string         `json:"protocol,omitempty"`
		Priority      int            `json:"priority"`
		Weight        int            `json:"weight"`
		Default       bool           `json:"is_default"`
		ContextWindow int            `json:"context_window"`
		QualityTier   int            `json:"quality_tier"`
		CostPerMTok   float64        `json:"cost_per_mtok"`
		Health        ProviderHealth `json:"health"`
	}
	out := make([]previewCandidate, 0, len(plan.Candidates))
	for i, candidate := range plan.Candidates {
		name := candidate.ProviderLabel
		if name == "" {
			name = candidate.ProviderName
		}
		out = append(out, previewCandidate{
			Order: i + 1, ProviderID: candidate.ProviderID, ProviderName: name,
			Model: candidate.Model, BaseURL: candidate.BaseURL, Protocol: candidate.Protocol,
			Priority: candidate.Priority, Weight: candidate.Weight, Default: candidate.IsDefault,
			ContextWindow: candidate.ContextWindow, QualityTier: candidate.QualityTier,
			CostPerMTok: candidate.CostPerMTok,
			Health:      s.Router.Health(candidate.ProviderID),
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"requested_model": plan.RequestedModel, "purpose": plan.Purpose, "candidates": out})
}

func (s *Service) handleAdminStats(w http.ResponseWriter, r *http.Request) {
	stats, err := s.Audit.Stats(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"totalSessions":   stats.TotalSessions,
		"totalMessages":   stats.TotalMessages,
		"totalTokens":     stats.TotalTokens,
		"totalCost":       0.0,
		"totalRequests":   stats.TotalRequests,
		"failedRequests":  stats.FailedRequests,
		"activeProviders": s.Mem.activeProviderCount(),
	})
}

func (s *Service) handleListRequestLogs(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
	status, _ := strconv.Atoi(r.URL.Query().Get("status"))
	logs, err := s.Audit.ListRequestLogsFiltered(r.Context(), RequestLogFilter{
		Limit: limit, Offset: offset, Model: r.URL.Query().Get("model"), StatusCode: status,
	})
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"logs": logs})
}

func (s *Service) handleGetRequestLog(w http.ResponseWriter, r *http.Request) {
	id := pathParam(r, "id")
	log, err := s.Audit.GetRequestLog(r.Context(), id)
	if err == sql.ErrNoRows {
		writeErr(w, http.StatusNotFound, "log not found")
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
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
	if err := s.Control.UpsertRouteProfile(r.Context(), p); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, p)
}
