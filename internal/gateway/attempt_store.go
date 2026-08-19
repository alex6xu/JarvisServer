package gateway

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"

	"github.com/alex6xu/jarvisserver/internal/agentcore"
)

type RunAttempt struct {
	ID               string `json:"id"`
	RunID            string `json:"run_id"`
	EndpointID       string `json:"endpoint_id"`
	ProviderID       int    `json:"provider_id"`
	Model            string `json:"model"`
	Ordinal          int    `json:"ordinal"`
	Turn             int    `json:"turn"`
	Purpose          string `json:"purpose,omitempty"`
	Status           string `json:"status"`
	FailureStage     string `json:"failure_stage,omitempty"`
	RouteReason      string `json:"route_reason,omitempty"`
	PolicyRevision   int64  `json:"policy_revision"`
	ErrorCategory    string `json:"error_category,omitempty"`
	Error            string `json:"error,omitempty"`
	LatencyMs        int64  `json:"latency_ms"`
	FirstTokenMs     int64  `json:"first_token_ms"`
	InputTokens      int    `json:"input_tokens"`
	OutputTokens     int    `json:"output_tokens"`
	ProducedOutput   bool   `json:"produced_output"`
	ProducedToolCall bool   `json:"produced_tool_call"`
	SideEffects      bool   `json:"side_effects"`
	CreatedAt        string `json:"created_at"`
	FinishedAt       string `json:"finished_at,omitempty"`
}

type RunCheckpoint struct {
	RunID        string
	Turn         int
	SessionID    string
	WorkspaceID  string
	Mode         string
	Model        string
	SystemPrompt string
	Messages     agentcore.MessageList
	CreatedAt    time.Time
}

func (s *GatewayStore) CreateRunAttempt(ctx context.Context, attempt RunAttempt) error {
	_, err := s.db.ExecContext(ctx, `
INSERT INTO run_attempts(id, run_id, endpoint_id, provider_id, model, ordinal, turn, status,
                         route_reason, policy_revision, purpose, created_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, attempt.ID, attempt.RunID, attempt.EndpointID,
		attempt.ProviderID, attempt.Model, attempt.Ordinal, attempt.Turn, attempt.Status,
		attempt.RouteReason, attempt.PolicyRevision, attempt.Purpose, attempt.CreatedAt)
	return err
}

func (s *GatewayStore) FinishRunAttempt(ctx context.Context, attempt RunAttempt) error {
	_, err := s.db.ExecContext(ctx, `
UPDATE run_attempts SET status=?, failure_stage=?, error_category=?, error=?, latency_ms=?,
first_token_ms=?, input_tokens=?, output_tokens=?, produced_output=?, produced_tool_call=?, side_effects=?, finished_at=? WHERE id=?`,
		attempt.Status, attempt.FailureStage, attempt.ErrorCategory, attempt.Error, attempt.LatencyMs,
		attempt.FirstTokenMs, attempt.InputTokens, attempt.OutputTokens, attempt.ProducedOutput, attempt.ProducedToolCall, attempt.SideEffects,
		attempt.FinishedAt, attempt.ID)
	return err
}

func (s *GatewayStore) ListRunAttempts(ctx context.Context, runID string) ([]RunAttempt, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT id, run_id, endpoint_id, provider_id, model, ordinal, turn, status, failure_stage,
route_reason, policy_revision, purpose, error_category, error, latency_ms, first_token_ms,
input_tokens, output_tokens, produced_output, produced_tool_call, side_effects, created_at, finished_at
FROM run_attempts WHERE run_id=? ORDER BY turn, ordinal`, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	attempts := make([]RunAttempt, 0)
	for rows.Next() {
		var attempt RunAttempt
		var finished sql.NullString
		if err := rows.Scan(&attempt.ID, &attempt.RunID, &attempt.EndpointID, &attempt.ProviderID,
			&attempt.Model, &attempt.Ordinal, &attempt.Turn, &attempt.Status, &attempt.FailureStage,
			&attempt.RouteReason, &attempt.PolicyRevision, &attempt.Purpose, &attempt.ErrorCategory, &attempt.Error,
			&attempt.LatencyMs, &attempt.FirstTokenMs, &attempt.InputTokens, &attempt.OutputTokens, &attempt.ProducedOutput,
			&attempt.ProducedToolCall, &attempt.SideEffects, &attempt.CreatedAt, &finished); err != nil {
			return nil, err
		}
		if finished.Valid {
			attempt.FinishedAt = finished.String
		}
		attempts = append(attempts, attempt)
	}
	return attempts, rows.Err()
}

func (s *GatewayStore) SaveRunCheckpoint(ctx context.Context, checkpoint RunCheckpoint) error {
	raw, err := json.Marshal(checkpoint.Messages)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `
INSERT INTO run_checkpoints(run_id, turn, session_id, workspace_id, mode, model, system_prompt, messages_json, created_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(run_id, turn) DO UPDATE SET messages_json=excluded.messages_json,
system_prompt=excluded.system_prompt, created_at=excluded.created_at`, checkpoint.RunID, checkpoint.Turn,
		checkpoint.SessionID, checkpoint.WorkspaceID, checkpoint.Mode, checkpoint.Model,
		checkpoint.SystemPrompt, string(raw), checkpoint.CreatedAt.UTC().Format(time.RFC3339Nano))
	return err
}

func (s *GatewayStore) LoadLatestRunCheckpoint(ctx context.Context, runID string) (RunCheckpoint, error) {
	var checkpoint RunCheckpoint
	var raw, created string
	err := s.db.QueryRowContext(ctx, `
SELECT run_id, turn, session_id, workspace_id, mode, model, system_prompt, messages_json, created_at
FROM run_checkpoints WHERE run_id=? ORDER BY turn DESC LIMIT 1`, runID).Scan(&checkpoint.RunID,
		&checkpoint.Turn, &checkpoint.SessionID, &checkpoint.WorkspaceID, &checkpoint.Mode,
		&checkpoint.Model, &checkpoint.SystemPrompt, &raw, &created)
	if err != nil {
		return RunCheckpoint{}, err
	}
	if err := json.Unmarshal([]byte(raw), &checkpoint.Messages); err != nil {
		return RunCheckpoint{}, err
	}
	checkpoint.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
	return checkpoint, nil
}
