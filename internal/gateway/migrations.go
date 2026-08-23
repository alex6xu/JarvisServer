package gateway

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

type gatewayMigration struct {
	version int
	name    string
	schema  string
}

const controlPlaneSchema = `
CREATE TABLE IF NOT EXISTS sessions (
    id TEXT PRIMARY KEY,
    header_json TEXT NOT NULL,
    model TEXT NOT NULL DEFAULT '',
    provider TEXT NOT NULL DEFAULT '',
    cwd TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_sessions_updated ON sessions(updated_at DESC);

CREATE TABLE IF NOT EXISTS session_entries (
    session_id TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
    entry_id TEXT NOT NULL,
    parent_id TEXT NOT NULL DEFAULT '',
    seq INTEGER NOT NULL,
    payload TEXT NOT NULL,
    created_at TEXT NOT NULL,
    PRIMARY KEY(session_id, entry_id),
    UNIQUE(session_id, seq)
);

CREATE TABLE IF NOT EXISTS route_profiles (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL UNIQUE COLLATE NOCASE,
    purpose TEXT NOT NULL DEFAULT '',
    models_json TEXT NOT NULL DEFAULT '[]',
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS agent_tasks (
    id TEXT PRIMARY KEY,
    workspace_id TEXT NOT NULL DEFAULT '',
    route_profile_id TEXT NOT NULL DEFAULT '',
    type TEXT NOT NULL DEFAULT '',
    prompt TEXT NOT NULL,
    status TEXT NOT NULL,
    result TEXT NOT NULL DEFAULT '',
    error TEXT NOT NULL DEFAULT '',
    tool_steps_json TEXT NOT NULL DEFAULT '[]',
    created_at TEXT NOT NULL,
    finished_at TEXT
);
CREATE INDEX IF NOT EXISTS idx_agent_tasks_created ON agent_tasks(created_at DESC);

CREATE TABLE IF NOT EXISTS tags (
    id TEXT PRIMARY KEY,
    slug TEXT NOT NULL UNIQUE COLLATE NOCASE,
    name TEXT NOT NULL,
    kind TEXT NOT NULL DEFAULT 'topic',
    use_count INTEGER NOT NULL DEFAULT 0,
    updated_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS workspace_metadata (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL,
    source TEXT NOT NULL DEFAULT 'local',
    github_full_name TEXT NOT NULL DEFAULT '',
    github_default_branch TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS agent_profiles (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL UNIQUE COLLATE NOCASE,
    mode TEXT NOT NULL,
    system_prompt TEXT NOT NULL DEFAULT '',
    tools_json TEXT NOT NULL DEFAULT '[]',
    config_json TEXT NOT NULL DEFAULT '{}',
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS channel_bindings (
    id TEXT PRIMARY KEY,
    channel TEXT NOT NULL,
    account_id INTEGER REFERENCES accounts(id) ON DELETE CASCADE,
    conversation TEXT NOT NULL DEFAULT '',
    agent_profile_id TEXT NOT NULL REFERENCES agent_profiles(id),
    workspace_id TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    UNIQUE(channel, account_id, conversation)
);
`

const routerSchema = `
CREATE TABLE IF NOT EXISTS provider_endpoints (
    id TEXT PRIMARY KEY,
    provider_id INTEGER NOT NULL REFERENCES providers(id) ON DELETE CASCADE,
    name TEXT NOT NULL,
    base_url TEXT NOT NULL DEFAULT '',
    protocol TEXT NOT NULL DEFAULT '',
    enabled INTEGER NOT NULL DEFAULT 1,
    priority INTEGER NOT NULL DEFAULT 0,
    weight INTEGER NOT NULL DEFAULT 1,
    is_default INTEGER NOT NULL DEFAULT 0,
    capabilities_json TEXT NOT NULL DEFAULT '{}',
    cost_per_mtok REAL NOT NULL DEFAULT 0,
    updated_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_provider_endpoints_provider ON provider_endpoints(provider_id);

CREATE TABLE IF NOT EXISTS provider_models (
    endpoint_id TEXT NOT NULL REFERENCES provider_endpoints(id) ON DELETE CASCADE,
    model_id TEXT NOT NULL,
    capabilities_json TEXT NOT NULL DEFAULT '{}',
    context_window INTEGER NOT NULL DEFAULT 0,
    enabled INTEGER NOT NULL DEFAULT 1,
    PRIMARY KEY(endpoint_id, model_id)
);

CREATE TABLE IF NOT EXISTS route_policies (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL UNIQUE COLLATE NOCASE,
    mode TEXT NOT NULL,
    current_revision INTEGER NOT NULL,
    config_json TEXT NOT NULL,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS route_policy_versions (
    policy_id TEXT NOT NULL REFERENCES route_policies(id) ON DELETE CASCADE,
    revision INTEGER NOT NULL,
    config_json TEXT NOT NULL,
    created_at TEXT NOT NULL,
    PRIMARY KEY(policy_id, revision)
);

CREATE TABLE IF NOT EXISTS health_samples (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    endpoint_id TEXT NOT NULL,
    run_id TEXT NOT NULL DEFAULT '',
    attempt_id TEXT NOT NULL DEFAULT '',
    success INTEGER NOT NULL,
    error_category TEXT NOT NULL DEFAULT '',
    error_text TEXT NOT NULL DEFAULT '',
    latency_ms INTEGER NOT NULL DEFAULT 0,
    first_token_ms INTEGER NOT NULL DEFAULT 0,
    health_json TEXT NOT NULL,
    created_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_health_samples_endpoint_created ON health_samples(endpoint_id, created_at DESC);
`

const runtimeRoutingSchema = `
ALTER TABLE runs ADD COLUMN deadline_at TEXT;

CREATE TABLE IF NOT EXISTS run_attempts (
    id TEXT PRIMARY KEY,
    run_id TEXT NOT NULL REFERENCES runs(id) ON DELETE CASCADE,
    endpoint_id TEXT NOT NULL,
    provider_id INTEGER NOT NULL DEFAULT 0,
    model TEXT NOT NULL,
    ordinal INTEGER NOT NULL,
    turn INTEGER NOT NULL,
    status TEXT NOT NULL,
    failure_stage TEXT NOT NULL DEFAULT '',
    route_reason TEXT NOT NULL DEFAULT '',
    policy_revision INTEGER NOT NULL DEFAULT 0,
    error_category TEXT NOT NULL DEFAULT '',
    error TEXT NOT NULL DEFAULT '',
    latency_ms INTEGER NOT NULL DEFAULT 0,
    first_token_ms INTEGER NOT NULL DEFAULT 0,
	input_tokens INTEGER NOT NULL DEFAULT 0,
	output_tokens INTEGER NOT NULL DEFAULT 0,
    produced_output INTEGER NOT NULL DEFAULT 0,
    produced_tool_call INTEGER NOT NULL DEFAULT 0,
    side_effects INTEGER NOT NULL DEFAULT 0,
    created_at TEXT NOT NULL,
    finished_at TEXT
);
CREATE INDEX IF NOT EXISTS idx_run_attempts_run_turn ON run_attempts(run_id, turn, ordinal);

CREATE TABLE IF NOT EXISTS run_checkpoints (
    run_id TEXT NOT NULL REFERENCES runs(id) ON DELETE CASCADE,
    turn INTEGER NOT NULL,
    session_id TEXT NOT NULL,
    workspace_id TEXT NOT NULL DEFAULT '',
    mode TEXT NOT NULL DEFAULT '',
    model TEXT NOT NULL,
    system_prompt TEXT NOT NULL DEFAULT '',
    messages_json TEXT NOT NULL,
    created_at TEXT NOT NULL,
    PRIMARY KEY(run_id, turn)
);
`

const workspaceOwnershipSchema = `
ALTER TABLE workspace_metadata ADD COLUMN account_id INTEGER NOT NULL DEFAULT 1;
CREATE INDEX IF NOT EXISTS idx_workspace_metadata_account ON workspace_metadata(account_id, updated_at DESC);
`

const adaptiveProviderRoutingSchema = `
ALTER TABLE providers ADD COLUMN capabilities_json TEXT NOT NULL DEFAULT '{}';
ALTER TABLE providers ADD COLUMN context_window INTEGER NOT NULL DEFAULT 32768;
ALTER TABLE providers ADD COLUMN quality_tier INTEGER NOT NULL DEFAULT 3;
ALTER TABLE providers ADD COLUMN cost_per_mtok REAL NOT NULL DEFAULT 0;
ALTER TABLE run_attempts ADD COLUMN purpose TEXT NOT NULL DEFAULT '';
`

const providerOwnedDefaultModelSchema = `
UPDATE route_profiles
SET models_json = '["auto"]', updated_at = CURRENT_TIMESTAMP
WHERE id = 'rp_1' AND name = 'default' AND models_json = '["openrouter/free"]';

UPDATE agent_profiles
SET config_json = '{"model":"auto"}', updated_at = CURRENT_TIMESTAMP
WHERE id IN ('profile_chat', 'profile_code') AND config_json = '{"model":"openrouter/free"}';
`

const chatRetrievalToolsSchema = `
UPDATE agent_profiles
SET tools_json = '["memory_search","websearch","webfetch"]', updated_at = CURRENT_TIMESTAMP
WHERE id = 'profile_chat' AND tools_json = '[]';
`

const githubIntegrationSchema = `
CREATE TABLE IF NOT EXISTS github_credentials (
    account_id INTEGER PRIMARY KEY REFERENCES accounts(id) ON DELETE CASCADE,
    token_cipher TEXT NOT NULL,
    github_login TEXT NOT NULL,
    auth_method TEXT NOT NULL,
    updated_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS github_oauth_states (
    state_hash TEXT PRIMARY KEY,
    account_id INTEGER NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    return_path TEXT NOT NULL DEFAULT '/code',
    expires_at TEXT NOT NULL,
    created_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_github_oauth_states_expires ON github_oauth_states(expires_at);
`

// Agent tasks duplicated coder runs without providing a durable scheduler.
// Their underlying sessions remain in the sessions tables.
const removeLegacyAgentTasksSchema = `
DROP TABLE IF EXISTS agent_tasks;
`

const notificationChannelsSchema = `
CREATE TABLE IF NOT EXISTS notification_channels (
    account_id INTEGER NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    kind TEXT NOT NULL,
    name TEXT NOT NULL,
    enabled INTEGER NOT NULL DEFAULT 1,
    events_json TEXT NOT NULL,
    config_cipher TEXT NOT NULL,
    target_hint TEXT NOT NULL DEFAULT '',
    last_error TEXT NOT NULL DEFAULT '',
    last_test_at TEXT,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    PRIMARY KEY(account_id, kind)
);
`

const stockSentimentSchema = `
CREATE TABLE IF NOT EXISTS stock_sentiment_snapshots (
    ticker TEXT PRIMARY KEY,
    score REAL,
    label TEXT NOT NULL DEFAULT '数据不足',
    buzz_score REAL,
    mentions INTEGER NOT NULL DEFAULT 0,
    sources_json TEXT NOT NULL DEFAULT '[]',
    diagnostics_json TEXT NOT NULL DEFAULT '[]',
    analysis_context TEXT NOT NULL DEFAULT '',
    fetched_at TEXT NOT NULL,
    expires_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_stock_sentiment_expires
    ON stock_sentiment_snapshots(expires_at);
`

const stockNewsSentimentSchema = `
CREATE TABLE IF NOT EXISTS stock_news_sentiment_cache (
    cache_key TEXT PRIMARY KEY,
    symbol TEXT NOT NULL,
    name TEXT NOT NULL DEFAULT '',
    query TEXT NOT NULL,
    sentiment_score REAL,
    sentiment_label TEXT NOT NULL DEFAULT '数据不足',
    sentiment_method TEXT NOT NULL DEFAULT 'keyword_v1',
    status TEXT NOT NULL,
    message TEXT NOT NULL DEFAULT '',
    items_json TEXT NOT NULL DEFAULT '[]',
    diagnostics_json TEXT NOT NULL DEFAULT '[]',
    analysis_context TEXT NOT NULL DEFAULT '',
    fetched_at TEXT NOT NULL,
    expires_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_stock_news_sentiment_symbol
    ON stock_news_sentiment_cache(symbol, fetched_at DESC);
CREATE INDEX IF NOT EXISTS idx_stock_news_sentiment_expires
    ON stock_news_sentiment_cache(expires_at);
`

const skillRegistrySchema = `
CREATE TABLE IF NOT EXISTS skills (
    name TEXT PRIMARY KEY,
    relative_path TEXT NOT NULL UNIQUE,
    description TEXT NOT NULL,
    allowed_tools_json TEXT NOT NULL DEFAULT '[]',
    source TEXT NOT NULL DEFAULT 'custom',
    enabled INTEGER NOT NULL DEFAULT 1,
    revision INTEGER NOT NULL DEFAULT 1,
    content_sha256 TEXT NOT NULL,
    validation_error TEXT NOT NULL DEFAULT '',
    created_by INTEGER REFERENCES accounts(id) ON DELETE SET NULL,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS account_skills (
    account_id INTEGER NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    skill_name TEXT NOT NULL REFERENCES skills(name) ON DELETE CASCADE,
    enabled INTEGER NOT NULL DEFAULT 1,
    updated_at TEXT NOT NULL,
    PRIMARY KEY(account_id, skill_name)
);
`

const watchlistSchema = `
CREATE TABLE IF NOT EXISTS watchlist_items (
    account_id INTEGER NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    symbol TEXT NOT NULL,
    code TEXT NOT NULL DEFAULT '',
    name TEXT NOT NULL DEFAULT '',
    market TEXT NOT NULL DEFAULT '',
    asset_type TEXT NOT NULL,
    sort_order INTEGER NOT NULL DEFAULT 0,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    PRIMARY KEY(account_id, symbol)
);
CREATE INDEX IF NOT EXISTS idx_watchlist_account_order
    ON watchlist_items(account_id, sort_order, created_at);
`

const notificationDeliveriesSchema = `
CREATE TABLE IF NOT EXISTS notification_deliveries (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    account_id INTEGER NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    event TEXT NOT NULL,
    idempotency_key TEXT NOT NULL,
    channel_kind TEXT NOT NULL,
    status TEXT NOT NULL,
    error TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    UNIQUE(account_id, idempotency_key, channel_kind)
);
CREATE INDEX IF NOT EXISTS idx_notification_deliveries_account_created
    ON notification_deliveries(account_id, created_at DESC);
`

const activeSessionsSchema = `
CREATE TABLE IF NOT EXISTS account_active_sessions (
    account_id INTEGER NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    mode TEXT NOT NULL CHECK(mode IN ('chat', 'coder')),
    workspace_id TEXT NOT NULL DEFAULT '',
    session_id TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
    updated_at TEXT NOT NULL,
    PRIMARY KEY(account_id, mode, workspace_id)
);
CREATE INDEX IF NOT EXISTS idx_account_active_sessions_updated
    ON account_active_sessions(account_id, updated_at DESC);
`

const providerModelMetadataSchema = `
ALTER TABLE provider_models ADD COLUMN max_input_tokens INTEGER NOT NULL DEFAULT 0;
ALTER TABLE provider_models ADD COLUMN max_output_tokens INTEGER NOT NULL DEFAULT 0;
ALTER TABLE provider_models ADD COLUMN metadata_source TEXT NOT NULL DEFAULT 'provider_default';
ALTER TABLE provider_models ADD COLUMN manual_context_window INTEGER NOT NULL DEFAULT 0;
ALTER TABLE provider_models ADD COLUMN detected_at TEXT;
`

var gatewayMigrations = []gatewayMigration{
	{version: 1, name: "gateway_base", schema: gatewaySchema},
	{version: 2, name: "control_plane_repositories", schema: controlPlaneSchema},
	{version: 3, name: "provider_router", schema: routerSchema},
	{version: 4, name: "runtime_routing", schema: runtimeRoutingSchema},
	{version: 5, name: "workspace_ownership", schema: workspaceOwnershipSchema},
	{version: 6, name: "adaptive_provider_routing", schema: adaptiveProviderRoutingSchema},
	{version: 7, name: "provider_owned_default_model", schema: providerOwnedDefaultModelSchema},
	{version: 8, name: "chat_retrieval_tools", schema: chatRetrievalToolsSchema},
	{version: 9, name: "github_integration", schema: githubIntegrationSchema},
	{version: 10, name: "remove_legacy_agent_tasks", schema: removeLegacyAgentTasksSchema},
	{version: 11, name: "notification_channels", schema: notificationChannelsSchema},
	{version: 12, name: "stock_sentiment", schema: stockSentimentSchema},
	{version: 13, name: "stock_news_sentiment", schema: stockNewsSentimentSchema},
	{version: 14, name: "skill_registry", schema: skillRegistrySchema},
	{version: 15, name: "watchlist", schema: watchlistSchema},
	{version: 16, name: "notification_deliveries", schema: notificationDeliveriesSchema},
	{version: 17, name: "account_active_sessions", schema: activeSessionsSchema},
	{version: 18, name: "provider_model_metadata", schema: providerModelMetadataSchema},
}

func applyGatewayMigrations(db *sql.DB) error {
	ctx := context.Background()
	if _, err := db.ExecContext(ctx, `
CREATE TABLE IF NOT EXISTS schema_migrations (
    version INTEGER PRIMARY KEY,
    name TEXT NOT NULL,
    applied_at TEXT NOT NULL
)`); err != nil {
		return err
	}
	for _, migration := range gatewayMigrations {
		var exists int
		err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM schema_migrations WHERE version = ?`, migration.version).Scan(&exists)
		if err != nil {
			return err
		}
		if exists != 0 {
			continue
		}
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, migration.schema); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("migration %d (%s): %w", migration.version, migration.name, err)
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO schema_migrations(version, name, applied_at) VALUES (?, ?, ?)`,
			migration.version, migration.name, time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
			_ = tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	return nil
}
