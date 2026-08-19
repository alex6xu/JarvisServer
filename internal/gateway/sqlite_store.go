package gateway

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"
)

const gatewaySchema = `
CREATE TABLE IF NOT EXISTS accounts (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    username TEXT NOT NULL UNIQUE COLLATE NOCASE,
    email TEXT NOT NULL DEFAULT '',
    role TEXT NOT NULL DEFAULT 'user',
    password_hash TEXT NOT NULL,
    quota INTEGER NOT NULL DEFAULT 0,
    used_quota INTEGER NOT NULL DEFAULT 0,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS auth_tokens (
    id TEXT PRIMARY KEY,
    account_id INTEGER NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    name TEXT NOT NULL DEFAULT '',
    token_hash TEXT NOT NULL UNIQUE,
    key_prefix TEXT NOT NULL DEFAULT '',
    status INTEGER NOT NULL DEFAULT 1,
    unlimited_quota INTEGER NOT NULL DEFAULT 1,
    expires_at TEXT,
    created_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_auth_tokens_hash ON auth_tokens(token_hash);

CREATE TABLE IF NOT EXISTS providers (
    id INTEGER PRIMARY KEY,
    name TEXT NOT NULL,
    type INTEGER NOT NULL,
    api_key TEXT NOT NULL DEFAULT '',
    base_url TEXT NOT NULL DEFAULT '',
    models TEXT NOT NULL DEFAULT '',
    status INTEGER NOT NULL DEFAULT 1,
    weight INTEGER NOT NULL DEFAULT 1,
    priority INTEGER NOT NULL DEFAULT 0,
    is_default INTEGER NOT NULL DEFAULT 0,
    auth_mode TEXT NOT NULL DEFAULT 'api_key',
    updated_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS runs (
    id TEXT PRIMARY KEY,
    session_id TEXT NOT NULL,
    model TEXT NOT NULL DEFAULT '',
    workspace_id TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL,
    error TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL,
    finished_at TEXT
);
CREATE INDEX IF NOT EXISTS idx_runs_session_created ON runs(session_id, created_at DESC);

CREATE TABLE IF NOT EXISTS run_events (
    run_id TEXT NOT NULL REFERENCES runs(id) ON DELETE CASCADE,
    seq INTEGER NOT NULL,
    payload TEXT NOT NULL,
    created_at TEXT NOT NULL,
    PRIMARY KEY(run_id, seq)
);

CREATE TABLE IF NOT EXISTS chat_exchanges (
    id TEXT PRIMARY KEY,
    run_id TEXT NOT NULL UNIQUE,
    session_id TEXT NOT NULL,
    workspace_id TEXT NOT NULL DEFAULT '',
    mode TEXT NOT NULL DEFAULT '',
    model TEXT NOT NULL DEFAULT '',
    request_text TEXT NOT NULL,
    response_text TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL,
    error TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL,
    finished_at TEXT,
    latency_ms INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS idx_chat_exchanges_session_created
    ON chat_exchanges(session_id, created_at DESC);

CREATE TABLE IF NOT EXISTS provider_exchanges (
    id TEXT PRIMARY KEY,
    run_id TEXT NOT NULL,
    session_id TEXT NOT NULL,
    provider_id INTEGER NOT NULL DEFAULT 0,
    provider_name TEXT NOT NULL DEFAULT '',
    model TEXT NOT NULL,
    stream INTEGER NOT NULL DEFAULT 1,
    status TEXT NOT NULL,
    status_code INTEGER NOT NULL DEFAULT 0,
    error TEXT NOT NULL DEFAULT '',
    prompt_tokens INTEGER NOT NULL DEFAULT 0,
    completion_tokens INTEGER NOT NULL DEFAULT 0,
    cached_tokens INTEGER NOT NULL DEFAULT 0,
    latency_ms INTEGER NOT NULL DEFAULT 0,
    request_body TEXT NOT NULL DEFAULT '',
    response_body TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL,
    finished_at TEXT
);
CREATE INDEX IF NOT EXISTS idx_provider_exchanges_run_created
    ON provider_exchanges(run_id, created_at);
CREATE INDEX IF NOT EXISTS idx_provider_exchanges_created
    ON provider_exchanges(created_at DESC);
`

// GatewayStore persists chat-level exchanges and each provider attempt.
type GatewayStore struct {
	db                *sql.DB
	maxAuditBodyBytes int
}

type RequestLogFilter struct {
	Limit      int
	Offset     int
	Model      string
	StatusCode int
}

type AuditStats struct {
	TotalSessions  int
	TotalMessages  int
	TotalTokens    int
	TotalRequests  int
	FailedRequests int
}

type ChatExchange struct {
	ID          string
	RunID       string
	SessionID   string
	WorkspaceID string
	Mode        string
	Model       string
	RequestText string
	CreatedAt   time.Time
}

type ProviderExchange struct {
	ID           string
	RunID        string
	SessionID    string
	ProviderID   int
	ProviderName string
	Model        string
	RequestBody  string
	CreatedAt    time.Time
}

func defaultGatewayDBPath() (string, error) {
	if home := os.Getenv("JARVIS_HOME"); home != "" {
		return filepath.Join(home, "gateway.db"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".jarvis", "gateway.db"), nil
}

func OpenGatewayStore(path string) (*GatewayStore, error) {
	if path == "" {
		var err error
		path, err = defaultGatewayDBPath()
		if err != nil {
			return nil, fmt.Errorf("resolve gateway database: %w", err)
		}
	}
	if path != ":memory:" {
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			return nil, fmt.Errorf("create gateway database directory: %w", err)
		}
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open gateway database: %w", err)
	}
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(`PRAGMA journal_mode = WAL; PRAGMA foreign_keys = ON; PRAGMA busy_timeout = 5000;`); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("configure gateway database: %w", err)
	}
	if err := applyGatewayMigrations(db); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("migrate gateway database: %w", err)
	}
	return &GatewayStore{db: db}, nil
}

func (s *GatewayStore) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

func (s *GatewayStore) CreateChat(ctx context.Context, ex ChatExchange) error {
	_, err := s.db.ExecContext(ctx, `
INSERT INTO chat_exchanges
    (id, run_id, session_id, workspace_id, mode, model, request_text, status, created_at)
VALUES (?, ?, ?, ?, ?, ?, ?, 'running', ?)`,
		ex.ID, ex.RunID, ex.SessionID, ex.WorkspaceID, ex.Mode, ex.Model,
		ex.RequestText, ex.CreatedAt.UTC().Format(time.RFC3339Nano))
	return err
}

func (s *GatewayStore) FinishChat(ctx context.Context, runID, response, status, errorText string, finished time.Time) error {
	res, err := s.db.ExecContext(ctx, `
UPDATE chat_exchanges
SET response_text = ?, status = ?, error = ?, finished_at = ?,
    latency_ms = CAST((julianday(?) - julianday(created_at)) * 86400000 AS INTEGER)
WHERE run_id = ?`, response, status, errorText, finished.UTC().Format(time.RFC3339Nano),
		finished.UTC().Format(time.RFC3339Nano), runID)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err == nil && n == 0 {
		return fmt.Errorf("chat exchange for run %s not found", runID)
	}
	return err
}

func (s *GatewayStore) CreateProviderExchange(ctx context.Context, ex ProviderExchange) error {
	_, err := s.db.ExecContext(ctx, `
INSERT INTO provider_exchanges
    (id, run_id, session_id, provider_id, provider_name, model, stream, status, request_body, created_at)
VALUES (?, ?, ?, ?, ?, ?, 1, 'running', ?, ?)`, ex.ID, ex.RunID, ex.SessionID,
		ex.ProviderID, ex.ProviderName, ex.Model, ex.RequestBody,
		ex.CreatedAt.UTC().Format(time.RFC3339Nano))
	return err
}

func (s *GatewayStore) FinishProviderExchange(ctx context.Context, id, response, status, errorText string, statusCode, promptTokens, completionTokens, cachedTokens int, finished time.Time) error {
	_, err := s.db.ExecContext(ctx, `
UPDATE provider_exchanges
SET response_body = ?, status = ?, error = ?, status_code = ?, prompt_tokens = ?,
    completion_tokens = ?, cached_tokens = ?, finished_at = ?,
    latency_ms = CAST((julianday(?) - julianday(created_at)) * 86400000 AS INTEGER)
WHERE id = ?`, response, status, errorText, statusCode, promptTokens, completionTokens,
		cachedTokens, finished.UTC().Format(time.RFC3339Nano), finished.UTC().Format(time.RFC3339Nano), id)
	return err
}

func (s *GatewayStore) ListRequestLogs(ctx context.Context, limit, offset int) ([]RequestLog, error) {
	return s.ListRequestLogsFiltered(ctx, RequestLogFilter{Limit: limit, Offset: offset})
}

func (s *GatewayStore) ListRequestLogsFiltered(ctx context.Context, filter RequestLogFilter) ([]RequestLog, error) {
	if filter.Limit <= 0 || filter.Limit > 500 {
		filter.Limit = 50
	}
	if filter.Offset < 0 {
		filter.Offset = 0
	}
	query := `
SELECT id, run_id, session_id, provider_id, provider_name, model, stream, status_code,
       error, prompt_tokens, completion_tokens, cached_tokens, latency_ms, created_at,
       request_body, response_body
FROM provider_exchanges WHERE 1=1`
	args := make([]any, 0, 4)
	if filter.Model != "" {
		query += ` AND model = ?`
		args = append(args, filter.Model)
	}
	if filter.StatusCode != 0 {
		query += ` AND status_code = ?`
		args = append(args, filter.StatusCode)
	}
	query += ` ORDER BY created_at DESC LIMIT ? OFFSET ?`
	args = append(args, filter.Limit, filter.Offset)
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []RequestLog
	for rows.Next() {
		var log RequestLog
		var providerID int
		if err := rows.Scan(&log.ID, &log.RunID, &log.SessionID, &providerID, &log.ProviderName,
			&log.Model, &log.Stream, &log.StatusCode, &log.Error, &log.PromptTokens,
			&log.CompletionTokens, &log.CachedTokens, &log.LatencyMs, &log.CreatedAt,
			&log.RequestBody, &log.ResponseBody); err != nil {
			return nil, err
		}
		if providerID != 0 {
			log.ProviderID = fmt.Sprint(providerID)
		}
		out = append(out, log)
	}
	return out, rows.Err()
}

func (s *GatewayStore) Stats(ctx context.Context) (AuditStats, error) {
	var stats AuditStats
	err := s.db.QueryRowContext(ctx, `
SELECT (SELECT COUNT(DISTINCT session_id) FROM chat_exchanges),
       (SELECT COUNT(*) FROM chat_exchanges),
       (SELECT COALESCE(SUM(prompt_tokens + completion_tokens), 0) FROM provider_exchanges),
       (SELECT COUNT(*) FROM provider_exchanges),
       (SELECT COUNT(*) FROM provider_exchanges WHERE status = 'error')`).Scan(
		&stats.TotalSessions, &stats.TotalMessages, &stats.TotalTokens,
		&stats.TotalRequests, &stats.FailedRequests)
	return stats, err
}

func (s *GatewayStore) PruneAudit(ctx context.Context, before time.Time) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	cutoff := before.UTC().Format(time.RFC3339Nano)
	queries := []string{
		`DELETE FROM provider_exchanges WHERE created_at < ?`,
		`DELETE FROM chat_exchanges WHERE created_at < ?`,
		`DELETE FROM runs WHERE created_at < ? AND status <> 'running'`,
		`DELETE FROM auth_tokens WHERE expires_at IS NOT NULL AND expires_at < ?`,
	}
	for _, query := range queries {
		if _, err := tx.ExecContext(ctx, query, cutoff); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *GatewayStore) GetRequestLog(ctx context.Context, id string) (*RequestLog, error) {
	logs, err := s.queryRequestLog(ctx, id)
	if err != nil {
		return nil, err
	}
	if len(logs) == 0 {
		return nil, sql.ErrNoRows
	}
	return &logs[0], nil
}

func (s *GatewayStore) queryRequestLog(ctx context.Context, id string) ([]RequestLog, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT id, run_id, session_id, provider_id, provider_name, model, stream, status_code,
       error, prompt_tokens, completion_tokens, cached_tokens, latency_ms, created_at,
       request_body, response_body
FROM provider_exchanges WHERE id = ?`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []RequestLog
	for rows.Next() {
		var log RequestLog
		var providerID int
		if err := rows.Scan(&log.ID, &log.RunID, &log.SessionID, &providerID, &log.ProviderName,
			&log.Model, &log.Stream, &log.StatusCode, &log.Error, &log.PromptTokens,
			&log.CompletionTokens, &log.CachedTokens, &log.LatencyMs, &log.CreatedAt,
			&log.RequestBody, &log.ResponseBody); err != nil {
			return nil, err
		}
		if providerID != 0 {
			log.ProviderID = fmt.Sprint(providerID)
		}
		out = append(out, log)
	}
	return out, rows.Err()
}

func marshalAuditJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprintf(`{"marshal_error":%q}`, err.Error())
	}
	return string(b)
}

func (s *GatewayStore) marshalAuditJSON(v any) string {
	raw := marshalAuditJSON(v)
	limit := s.maxAuditBodyBytes
	if limit <= 0 || len(raw) <= limit {
		return raw
	}
	return marshalAuditJSON(map[string]any{
		"truncated":      true,
		"original_bytes": len(raw),
		"preview":        raw[:limit],
	})
}
