package gateway

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/alex6xu/jarvisserver/internal/router"
)

func (s *GatewayStore) LoadRouterHealth(ctx context.Context) (map[string]router.Health, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT h.endpoint_id, h.health_json
FROM health_samples h
JOIN (SELECT endpoint_id, MAX(id) id FROM health_samples GROUP BY endpoint_id) latest ON latest.id = h.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	health := make(map[string]router.Health)
	for rows.Next() {
		var endpointID, raw string
		if err := rows.Scan(&endpointID, &raw); err != nil {
			return nil, err
		}
		var item router.Health
		if err := json.Unmarshal([]byte(raw), &item); err != nil {
			return nil, err
		}
		health[endpointID] = item
	}
	return health, rows.Err()
}

func (s *GatewayStore) SaveHealthSample(ctx context.Context, result router.AttemptResult, health router.Health) error {
	_, err := s.db.ExecContext(ctx, `
INSERT INTO health_samples(endpoint_id, run_id, attempt_id, success, error_category, error_text,
                           latency_ms, first_token_ms, health_json, created_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, result.EndpointID, result.RunID, result.AttemptID,
		result.Success, result.ErrorCategory, result.ErrorText, result.Latency.Milliseconds(),
		result.FirstToken.Milliseconds(), encodeJSON(health, "{}"), result.OccurredAt.UTC().Format(time.RFC3339Nano))
	return err
}

func (s *GatewayStore) ListRoutePolicies(ctx context.Context) ([]router.Policy, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT config_json FROM route_policies ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var policies []router.Policy
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		var policy router.Policy
		if err := json.Unmarshal([]byte(raw), &policy); err != nil {
			return nil, err
		}
		policies = append(policies, policy)
	}
	return policies, rows.Err()
}

func (s *GatewayStore) GetRoutePolicy(ctx context.Context, id string) (router.Policy, error) {
	var raw string
	if err := s.db.QueryRowContext(ctx, `SELECT config_json FROM route_policies WHERE id = ?`, id).Scan(&raw); err != nil {
		return router.Policy{}, err
	}
	var policy router.Policy
	if err := json.Unmarshal([]byte(raw), &policy); err != nil {
		return router.Policy{}, err
	}
	return policy, nil
}

func (s *GatewayStore) PublishRoutePolicy(ctx context.Context, policy router.Policy) (router.Policy, error) {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	current, err := s.GetRoutePolicy(ctx, policy.ID)
	if err == nil {
		policy.Revision = current.Revision + 1
	} else if errors.Is(err, sql.ErrNoRows) {
		policy.Revision = 1
	} else {
		return router.Policy{}, err
	}
	if policy.MaxAttempts <= 0 {
		policy.MaxAttempts = 3
	}
	raw := encodeJSON(policy, "{}")
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return router.Policy{}, err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `
INSERT INTO route_policies(id, name, mode, current_revision, config_json, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(id) DO UPDATE SET name=excluded.name, mode=excluded.mode,
current_revision=excluded.current_revision, config_json=excluded.config_json, updated_at=excluded.updated_at`,
		policy.ID, policy.Name, policy.Mode, policy.Revision, raw, now, now); err != nil {
		return router.Policy{}, err
	}
	if _, err := tx.ExecContext(ctx, `
INSERT INTO route_policy_versions(policy_id, revision, config_json, created_at) VALUES (?, ?, ?, ?)`,
		policy.ID, policy.Revision, raw, now); err != nil {
		return router.Policy{}, err
	}
	if err := tx.Commit(); err != nil {
		return router.Policy{}, err
	}
	return policy, nil
}

func ensureDefaultRoutePolicy(ctx context.Context, store *GatewayStore) (router.Policy, error) {
	policy, err := store.GetRoutePolicy(ctx, "balanced")
	if err == nil {
		return policy, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return router.Policy{}, err
	}
	return store.PublishRoutePolicy(ctx, router.DefaultPolicy())
}
