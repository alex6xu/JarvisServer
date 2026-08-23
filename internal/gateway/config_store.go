package gateway

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

func (s *GatewayStore) ListProviders(ctx context.Context) ([]Provider, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT id, name, type, api_key, base_url, models, status, weight, priority, is_default, auth_mode,
       capabilities_json, context_window, quality_tier, cost_per_mtok
FROM providers ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Provider
	for rows.Next() {
		var p Provider
		var capabilities string
		if err := rows.Scan(&p.ID, &p.Name, &p.Type, &p.Key, &p.BaseURL, &p.Models,
			&p.Status, &p.Weight, &p.Priority, &p.IsDefault, &p.AuthMode, &capabilities,
			&p.ContextWindow, &p.QualityTier, &p.CostPerMTok); err != nil {
			return nil, err
		}
		_ = json.Unmarshal([]byte(capabilities), &p.Capabilities)
		out = append(out, normalizeProviderConfig(p))
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	allMetadata, err := loadAllProviderModelMetadata(ctx, s.db)
	if err != nil {
		return nil, err
	}
	for i := range out {
		models := allMetadata[fmt.Sprintf("provider_%d", out[i].ID)]
		for j := range models {
			resolveProviderModelMetadata(&models[j], out[i].ContextWindow)
		}
		out[i].ModelMetadata = models
	}
	return out, nil
}

func (s *GatewayStore) ReplaceProviders(ctx context.Context, providers []Provider) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	existing, err := loadAllProviderModelMetadata(ctx, tx)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM providers`); err != nil {
		return err
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	for _, raw := range providers {
		p := normalizeProviderConfig(raw)
		if _, err := tx.ExecContext(ctx, `
INSERT INTO providers(id, name, type, api_key, base_url, models, status, weight, priority, is_default,
                      auth_mode, updated_at, capabilities_json, context_window, quality_tier, cost_per_mtok)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, p.ID, p.Name, p.Type, p.Key, p.BaseURL,
			p.Models, p.Status, p.Weight, p.Priority, p.IsDefault, p.AuthMode, now,
			encodeJSON(p.Capabilities, "{}"), p.ContextWindow, p.QualityTier, p.CostPerMTok); err != nil {
			return err
		}
		endpointID := fmt.Sprintf("provider_%d", p.ID)
		protocol, _ := mapChannelType(p.Type, p.BaseURL)
		capabilities := encodeJSON(map[string]any{
			"chat": p.Capabilities.Chat, "reasoning": p.Capabilities.Reasoning,
			"coding": p.Capabilities.Coding, "tools": p.Capabilities.Tools,
			"images": p.Capabilities.Images, "thinking": p.Capabilities.Thinking,
			"quality_tier": p.QualityTier, "context_window": p.ContextWindow,
		}, "{}")
		if _, err := tx.ExecContext(ctx, `
INSERT INTO provider_endpoints(id, provider_id, name, base_url, protocol, enabled,
                               priority, weight, is_default, capabilities_json, cost_per_mtok, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, endpointID, p.ID, p.Name, p.BaseURL, protocol,
			p.Status, p.Priority, max(p.Weight, 1), p.IsDefault, capabilities, p.CostPerMTok, now); err != nil {
			return err
		}
		metadata := mergeProviderModelMetadata(p, existing[endpointID], p.ModelMetadata)
		for _, model := range metadata {
			if _, err := tx.ExecContext(ctx, `
INSERT INTO provider_models(endpoint_id, model_id, capabilities_json, context_window, enabled,
                            max_input_tokens, max_output_tokens, metadata_source,
                            manual_context_window, detected_at)
VALUES (?, ?, ?, ?, 1, ?, ?, ?, ?, NULLIF(?, ''))`, endpointID, model.ID, capabilities,
				model.ContextWindow, model.MaxInputTokens, model.MaxOutputTokens, model.MetadataSource,
				model.ManualContextWindow, model.DetectedAt); err != nil {
				return err
			}
		}
	}
	return tx.Commit()
}

type modelMetadataQuerier interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

func loadAllProviderModelMetadata(ctx context.Context, q modelMetadataQuerier) (map[string][]ProviderModelMetadata, error) {
	rows, err := q.QueryContext(ctx, `
SELECT endpoint_id, model_id, context_window, max_input_tokens, max_output_tokens,
       metadata_source, manual_context_window, COALESCE(detected_at, '')
FROM provider_models`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make(map[string][]ProviderModelMetadata)
	for rows.Next() {
		var endpointID string
		var model ProviderModelMetadata
		if err := rows.Scan(&endpointID, &model.ID, &model.ContextWindow, &model.MaxInputTokens,
			&model.MaxOutputTokens, &model.MetadataSource, &model.ManualContextWindow, &model.DetectedAt); err != nil {
			return nil, err
		}
		out[endpointID] = append(out[endpointID], model)
	}
	return out, rows.Err()
}

func mergeProviderModelMetadata(p Provider, existing, incoming []ProviderModelMetadata) []ProviderModelMetadata {
	byID := make(map[string]ProviderModelMetadata)
	for _, model := range existing {
		if id := strings.TrimSpace(model.ID); id != "" {
			byID[strings.ToLower(id)] = model
		}
	}
	for _, model := range incoming {
		id := strings.TrimSpace(model.ID)
		if id == "" {
			continue
		}
		key := strings.ToLower(id)
		old := byID[key]
		// A zero manual value explicitly clears a prior manual override. Auto
		// metadata only replaces old auto metadata when it is present.
		old.ID = id
		old.ManualContextWindow = model.ManualContextWindow
		if model.ContextWindow > 0 || model.MaxInputTokens > 0 || model.MaxOutputTokens > 0 {
			old.ContextWindow = model.ContextWindow
			old.MaxInputTokens = model.MaxInputTokens
			old.MaxOutputTokens = model.MaxOutputTokens
			old.MetadataSource = model.MetadataSource
			old.DetectedAt = model.DetectedAt
		}
		byID[key] = old
	}
	configured := parseProviderModels(p.Models)
	for _, id := range configured {
		key := strings.ToLower(id)
		model := byID[key]
		model.ID = id
		if model.MetadataSource == "" {
			model.MetadataSource = "provider_default"
		}
		byID[key] = model
	}
	out := make([]ProviderModelMetadata, 0, len(configured))
	for _, id := range configured {
		model := byID[strings.ToLower(id)]
		resolveProviderModelMetadata(&model, p.ContextWindow)
		out = append(out, model)
	}
	return out
}

// mergeDiscoveredProviderModelMetadata refreshes auto metadata without
// overwriting a user's manual model window.
func mergeDiscoveredProviderModelMetadata(p Provider, existing, discovered []ProviderModelMetadata) []ProviderModelMetadata {
	discovered = decorateDiscoveredProviderModelMetadata(p, existing, discovered)
	return mergeProviderModelMetadata(p, existing, discovered)
}

func decorateDiscoveredProviderModelMetadata(p Provider, existing, discovered []ProviderModelMetadata) []ProviderModelMetadata {
	manualByID := make(map[string]int, len(existing))
	for _, model := range existing {
		manualByID[strings.ToLower(strings.TrimSpace(model.ID))] = model.ManualContextWindow
	}
	for i := range discovered {
		discovered[i].ManualContextWindow = manualByID[strings.ToLower(strings.TrimSpace(discovered[i].ID))]
		resolveProviderModelMetadata(&discovered[i], p.ContextWindow)
	}
	return discovered
}

func (s *GatewayStore) CreateRun(ctx context.Context, st *RunState) error {
	deadline := ""
	if !st.Deadline.IsZero() {
		deadline = st.Deadline.UTC().Format(time.RFC3339Nano)
	}
	_, err := s.db.ExecContext(ctx, `
INSERT INTO runs(id, session_id, model, workspace_id, status, created_at, deadline_at)
VALUES (?, ?, ?, ?, ?, ?, NULLIF(?, ''))`, st.ID, st.SessionID, st.Model, st.WorkspaceID, st.Status,
		time.Now().UTC().Format(time.RFC3339Nano), deadline)
	return err
}

func (s *GatewayStore) AppendRunEvent(ctx context.Context, runID string, event StoredEvent) error {
	payload := s.marshalAuditJSON(event.Payload)
	_, err := s.db.ExecContext(ctx, `
INSERT INTO run_events(run_id, seq, payload, created_at) VALUES (?, ?, ?, ?)`, runID,
		event.Seq, payload, time.Now().UTC().Format(time.RFC3339Nano))
	return err
}

func (s *GatewayStore) FinishRun(ctx context.Context, runID, status, errorText string) error {
	_, err := s.db.ExecContext(ctx, `
UPDATE runs SET status = ?, error = ?, finished_at = ? WHERE id = ?`, status, errorText,
		time.Now().UTC().Format(time.RFC3339Nano), runID)
	return err
}

func (s *GatewayStore) LoadRun(ctx context.Context, id string) (*RunState, error) {
	st := &RunState{ID: id, subs: make(map[chan StoredEvent]struct{}), done: make(chan struct{})}
	var errorText string
	var deadline sql.NullString
	err := s.db.QueryRowContext(ctx, `
SELECT session_id, model, workspace_id, status, error, deadline_at FROM runs WHERE id = ?`, id).Scan(
		&st.SessionID, &st.Model, &st.WorkspaceID, &st.Status, &errorText, &deadline)
	if err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT seq, payload FROM run_events WHERE run_id = ? ORDER BY seq`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var event StoredEvent
		var payload string
		if err := rows.Scan(&event.Seq, &payload); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(payload), &event.Payload); err != nil {
			return nil, err
		}
		st.Events = append(st.Events, event)
		st.LastSeq = event.Seq
	}
	if deadline.Valid {
		st.Deadline, _ = time.Parse(time.RFC3339Nano, deadline.String)
	}
	if st.Status == runStatusRunning {
		st.Status = runStatusInterrupted
		errorText = "gateway restarted before run completion"
	}
	close(st.done)
	if errorText != "" {
		st.Err = errors.New(errorText)
	}
	return st, rows.Err()
}
