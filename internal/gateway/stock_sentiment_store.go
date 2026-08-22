package gateway

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"
)

func (s *GatewayStore) SaveStockSentimentSnapshot(ctx context.Context, snapshot StockSentimentSnapshot) error {
	if s == nil || s.db == nil {
		return nil
	}
	sources, err := json.Marshal(snapshot.Sources)
	if err != nil {
		return fmt.Errorf("encode stock sentiment sources: %w", err)
	}
	diagnostics, err := json.Marshal(snapshot.Diagnostics)
	if err != nil {
		return fmt.Errorf("encode stock sentiment diagnostics: %w", err)
	}
	_, err = s.db.ExecContext(ctx, `
INSERT INTO stock_sentiment_snapshots
    (ticker, score, label, buzz_score, mentions, sources_json, diagnostics_json,
     analysis_context, fetched_at, expires_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(ticker) DO UPDATE SET
    score=excluded.score,
    label=excluded.label,
    buzz_score=excluded.buzz_score,
    mentions=excluded.mentions,
    sources_json=excluded.sources_json,
    diagnostics_json=excluded.diagnostics_json,
    analysis_context=excluded.analysis_context,
    fetched_at=excluded.fetched_at,
    expires_at=excluded.expires_at`,
		snapshot.Ticker, nullableFloat(snapshot.Score), snapshot.Label,
		nullableFloat(snapshot.BuzzScore), snapshot.Mentions, string(sources),
		string(diagnostics), snapshot.AnalysisContext,
		snapshot.FetchedAt.UTC().Format(time.RFC3339Nano),
		snapshot.ExpiresAt.UTC().Format(time.RFC3339Nano))
	return err
}

func (s *GatewayStore) LatestStockSentimentSnapshot(ctx context.Context, ticker string) (*StockSentimentSnapshot, error) {
	if s == nil || s.db == nil {
		return nil, nil
	}
	var (
		snapshot        StockSentimentSnapshot
		score           sql.NullFloat64
		buzz            sql.NullFloat64
		sourcesJSON     string
		diagnosticsJSON string
		fetchedAt       string
		expiresAt       string
	)
	err := s.db.QueryRowContext(ctx, `
SELECT ticker, score, label, buzz_score, mentions, sources_json,
       diagnostics_json, analysis_context, fetched_at, expires_at
FROM stock_sentiment_snapshots
WHERE ticker = ?`, ticker).Scan(
		&snapshot.Ticker, &score, &snapshot.Label, &buzz, &snapshot.Mentions,
		&sourcesJSON, &diagnosticsJSON, &snapshot.AnalysisContext, &fetchedAt, &expiresAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if score.Valid {
		snapshot.Score = floatPtr(score.Float64)
	}
	if buzz.Valid {
		snapshot.BuzzScore = floatPtr(buzz.Float64)
	}
	if err := json.Unmarshal([]byte(sourcesJSON), &snapshot.Sources); err != nil {
		return nil, fmt.Errorf("decode stock sentiment sources: %w", err)
	}
	if err := json.Unmarshal([]byte(diagnosticsJSON), &snapshot.Diagnostics); err != nil {
		return nil, fmt.Errorf("decode stock sentiment diagnostics: %w", err)
	}
	if snapshot.FetchedAt, err = time.Parse(time.RFC3339Nano, fetchedAt); err != nil {
		return nil, fmt.Errorf("decode stock sentiment fetched time: %w", err)
	}
	if snapshot.ExpiresAt, err = time.Parse(time.RFC3339Nano, expiresAt); err != nil {
		return nil, fmt.Errorf("decode stock sentiment expiry: %w", err)
	}
	return &snapshot, nil
}

func nullableFloat(value *float64) any {
	if value == nil {
		return nil
	}
	return *value
}
