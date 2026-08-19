package gateway

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// RecoverInterruptedRuns turns stale running rows into replayable terminal
// states. Checkpoints are retained, but model/tool execution is never replayed
// automatically because the last tool may already have caused side effects.
func (s *GatewayStore) RecoverInterruptedRuns(ctx context.Context) (int, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id FROM runs WHERE status = ?`, runStatusRunning)
	if err != nil {
		return 0, err
	}
	var runIDs []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			_ = rows.Close()
			return 0, err
		}
		runIDs = append(runIDs, id)
	}
	if err := rows.Close(); err != nil {
		return 0, err
	}

	for _, runID := range runIDs {
		if err := s.markRunInterrupted(ctx, runID); err != nil {
			return 0, err
		}
	}
	return len(runIDs), nil
}

func (s *GatewayStore) markRunInterrupted(ctx context.Context, runID string) error {
	message := "gateway restarted; automatic replay suppressed to avoid duplicate tool side effects"
	if _, err := s.LoadLatestRunCheckpoint(ctx, runID); errors.Is(err, sql.ErrNoRows) {
		message = "gateway restarted before a recoverable checkpoint was saved"
	} else if err != nil {
		return err
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var seq int64
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(seq), 0) FROM run_events WHERE run_id = ?`, runID).Scan(&seq); err != nil {
		return err
	}
	now := time.Now().UTC()
	result, err := tx.ExecContext(ctx, `
UPDATE runs SET status = ?, error = ?, finished_at = ? WHERE id = ? AND status = ?`,
		runStatusInterrupted, message, now.Format(time.RFC3339Nano), runID, runStatusRunning)
	if err != nil {
		return err
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if changed == 0 {
		return tx.Commit()
	}
	payload := s.marshalAuditJSON(StreamEvent{
		Type: "run.interrupted", Content: message, Seq: seq + 1,
		Timestamp: now.Format(time.RFC3339Nano),
	})
	if _, err := tx.ExecContext(ctx, `
INSERT INTO run_events(run_id, seq, payload, created_at) VALUES (?, ?, ?, ?)`,
		runID, seq+1, payload, now.Format(time.RFC3339Nano)); err != nil {
		return fmt.Errorf("append interrupted event: %w", err)
	}
	return tx.Commit()
}
