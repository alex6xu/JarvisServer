package gateway

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
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
	return out, rows.Err()
}

func (s *GatewayStore) ReplaceProviders(ctx context.Context, providers []Provider) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
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
		for _, model := range parseProviderModels(p.Models) {
			if _, err := tx.ExecContext(ctx, `
INSERT INTO provider_models(endpoint_id, model_id, capabilities_json, context_window, enabled)
VALUES (?, ?, ?, ?, 1)`, endpointID, model, capabilities, p.ContextWindow); err != nil {
				return err
			}
		}
	}
	return tx.Commit()
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
