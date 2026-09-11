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
	rows, err := s.db.QueryContext(ctx, `
SELECT id FROM runs AS r
WHERE r.status = ? OR (
    r.status = ? AND (
        EXISTS (SELECT 1 FROM chat_exchanges AS c WHERE c.run_id = r.id AND c.status = ?) OR
        EXISTS (SELECT 1 FROM provider_exchanges AS p WHERE p.run_id = r.id AND p.status = ?) OR
        EXISTS (SELECT 1 FROM run_attempts AS a WHERE a.run_id = r.id AND a.status = ?)
    )
)`, runStatusRunning, runStatusInterrupted, runStatusRunning, runStatusRunning, runStatusRunning)
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
	finishedAt := now.Format(time.RFC3339Nano)
	result, err := tx.ExecContext(ctx, `
UPDATE runs SET status = ?, error = ?, finished_at = ? WHERE id = ? AND status = ?`,
		runStatusInterrupted, message, finishedAt, runID, runStatusRunning)
	if err != nil {
		return err
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return err
	}
	newlyInterrupted := changed > 0
	if !newlyInterrupted {
		var status, existingError string
		var existingFinished sql.NullString
		if err := tx.QueryRowContext(ctx, `
SELECT status, error, finished_at FROM runs WHERE id = ?`, runID).
			Scan(&status, &existingError, &existingFinished); err != nil {
			return err
		}
		if status != runStatusInterrupted {
			return tx.Commit()
		}
		if existingError != "" {
			message = existingError
		}
		if existingFinished.Valid {
			finishedAt = existingFinished.String
		}
	}
	if _, err := tx.ExecContext(ctx, `
UPDATE chat_exchanges
SET status = ?, error = ?, finished_at = ?,
    latency_ms = CAST((julianday(?) - julianday(created_at)) * 86400000 AS INTEGER)
WHERE run_id = ? AND status = ?`, runStatusInterrupted, message, finishedAt,
		finishedAt, runID, runStatusRunning); err != nil {
		return fmt.Errorf("interrupt chat exchange: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
UPDATE provider_exchanges
SET status = ?, error = ?, status_code = 0, finished_at = ?,
    latency_ms = CAST((julianday(?) - julianday(created_at)) * 86400000 AS INTEGER)
WHERE run_id = ? AND status = ?`, runStatusInterrupted, message, finishedAt,
		finishedAt, runID, runStatusRunning); err != nil {
		return fmt.Errorf("interrupt provider exchange: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
UPDATE run_attempts
SET status = ?, failure_stage = 'gateway_restart', error_category = 'interrupted',
    error = ?, finished_at = ?,
    latency_ms = CAST((julianday(?) - julianday(created_at)) * 86400000 AS INTEGER)
WHERE run_id = ? AND status = ?`, runStatusInterrupted, message, finishedAt,
		finishedAt, runID, runStatusRunning); err != nil {
		return fmt.Errorf("interrupt run attempt: %w", err)
	}
	queueResult, err := tx.ExecContext(ctx, `
UPDATE run_message_queue_items
SET status = ?, updated_at = ?
WHERE run_id = ? AND status IN (?, ?)`, queueStatusDropped, finishedAt, runID,
		queueStatusPending, queueStatusInjecting)
	if err != nil {
		return fmt.Errorf("drop interrupted run queue: %w", err)
	}
	if changedQueueItems, rowsErr := queueResult.RowsAffected(); rowsErr != nil {
		return rowsErr
	} else if changedQueueItems > 0 {
		if _, err := tx.ExecContext(ctx, `
UPDATE run_message_queues SET version = version + 1, updated_at = ? WHERE run_id = ?`,
			finishedAt, runID); err != nil {
			return fmt.Errorf("version interrupted run queue: %w", err)
		}
	}
	if !newlyInterrupted {
		return tx.Commit()
	}
	payload := s.marshalAuditJSON(StreamEvent{
		Type: "run.interrupted", Content: message, Seq: seq + 1,
		Timestamp: now.Format(time.RFC3339Nano),
	})
	if _, err := tx.ExecContext(ctx, `
INSERT INTO run_events(run_id, seq, payload, created_at) VALUES (?, ?, ?, ?)`,
		runID, seq+1, payload, finishedAt); err != nil {
		return fmt.Errorf("append interrupted event: %w", err)
	}
	return tx.Commit()
}
