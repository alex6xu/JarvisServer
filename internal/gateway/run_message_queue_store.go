package gateway

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

func (s *GatewayStore) SaveRunMessageQueue(ctx context.Context, snapshot RunMessageQueueSnapshot) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := tx.ExecContext(ctx, `
INSERT INTO run_message_queues(run_id, version, updated_at) VALUES (?, ?, ?)
ON CONFLICT(run_id) DO UPDATE SET version = excluded.version, updated_at = excluded.updated_at`,
		snapshot.RunID, snapshot.Version, now); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM run_message_queue_items WHERE run_id = ?`, snapshot.RunID); err != nil {
		return err
	}
	for _, item := range snapshot.Items {
		if _, err := tx.ExecContext(ctx, `
INSERT INTO run_message_queue_items(
    id, run_id, session_id, account_id, content, event_type, position, status,
    idempotency_key, created_at, updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, item.ID, snapshot.RunID, item.SessionID,
			item.AccountID, item.Content, item.EventType, item.Position, item.Status,
			item.IdempotencyKey, item.CreatedAt, item.UpdatedAt); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *GatewayStore) LoadRunMessageQueue(ctx context.Context, runID string) (RunMessageQueueSnapshot, error) {
	snapshot := RunMessageQueueSnapshot{RunID: runID, Items: []RunMessageQueueItem{}}
	err := s.db.QueryRowContext(ctx, `SELECT version FROM run_message_queues WHERE run_id = ?`, runID).
		Scan(&snapshot.Version)
	if errors.Is(err, sql.ErrNoRows) {
		return snapshot, nil
	}
	if err != nil {
		return RunMessageQueueSnapshot{}, err
	}
	rows, err := s.db.QueryContext(ctx, `
SELECT id, session_id, account_id, content, event_type, position, status,
       idempotency_key, created_at, updated_at
FROM run_message_queue_items WHERE run_id = ? ORDER BY position`, runID)
	if err != nil {
		return RunMessageQueueSnapshot{}, err
	}
	defer rows.Close()
	for rows.Next() {
		var item RunMessageQueueItem
		item.RunID = runID
		if err := rows.Scan(&item.ID, &item.SessionID, &item.AccountID, &item.Content,
			&item.EventType, &item.Position, &item.Status, &item.IdempotencyKey,
			&item.CreatedAt, &item.UpdatedAt); err != nil {
			return RunMessageQueueSnapshot{}, err
		}
		snapshot.Items = append(snapshot.Items, item)
	}
	return snapshot, rows.Err()
}
