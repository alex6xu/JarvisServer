package gateway

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"
)

func (s *GatewayStore) SaveStockNewsSentiment(ctx context.Context, result StockNewsSentimentResult) error {
	if s == nil || s.db == nil {
		return nil
	}
	items, err := json.Marshal(result.Items)
	if err != nil {
		return fmt.Errorf("encode stock news items: %w", err)
	}
	diagnostics, err := json.Marshal(result.Diagnostics)
	if err != nil {
		return fmt.Errorf("encode stock news diagnostics: %w", err)
	}
	_, err = s.db.ExecContext(ctx, `
INSERT INTO stock_news_sentiment_cache
    (cache_key, symbol, name, query, sentiment_score, sentiment_label,
     sentiment_method, status, message, items_json, diagnostics_json,
     analysis_context, fetched_at, expires_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(cache_key) DO UPDATE SET
    symbol=excluded.symbol,
    name=excluded.name,
    query=excluded.query,
    sentiment_score=excluded.sentiment_score,
    sentiment_label=excluded.sentiment_label,
    sentiment_method=excluded.sentiment_method,
    status=excluded.status,
    message=excluded.message,
    items_json=excluded.items_json,
    diagnostics_json=excluded.diagnostics_json,
    analysis_context=excluded.analysis_context,
    fetched_at=excluded.fetched_at,
    expires_at=excluded.expires_at`,
		result.cacheKey, result.Symbol, result.Name, result.Query,
		nullableFloat(result.SentimentScore), result.SentimentLabel,
		result.SentimentMethod, result.Status, result.Message, string(items),
		string(diagnostics), result.AnalysisContext,
		result.FetchedAt.UTC().Format(time.RFC3339Nano),
		result.ExpiresAt.UTC().Format(time.RFC3339Nano))
	return err
}

func (s *GatewayStore) StockNewsSentimentByKey(ctx context.Context, cacheKey string) (*StockNewsSentimentResult, error) {
	if s == nil || s.db == nil {
		return nil, nil
	}
	var (
		result          StockNewsSentimentResult
		score           sql.NullFloat64
		itemsJSON       string
		diagnosticsJSON string
		fetchedAt       string
		expiresAt       string
	)
	err := s.db.QueryRowContext(ctx, `
SELECT symbol, name, query, sentiment_score, sentiment_label, sentiment_method,
       status, message, items_json, diagnostics_json, analysis_context,
       fetched_at, expires_at
FROM stock_news_sentiment_cache
WHERE cache_key = ?`, cacheKey).Scan(
		&result.Symbol, &result.Name, &result.Query, &score, &result.SentimentLabel,
		&result.SentimentMethod, &result.Status, &result.Message, &itemsJSON,
		&diagnosticsJSON, &result.AnalysisContext, &fetchedAt, &expiresAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	result.Enabled = true
	result.cacheKey = cacheKey
	if score.Valid {
		result.SentimentScore = floatPtr(score.Float64)
	}
	if err := json.Unmarshal([]byte(itemsJSON), &result.Items); err != nil {
		return nil, fmt.Errorf("decode stock news items: %w", err)
	}
	if err := json.Unmarshal([]byte(diagnosticsJSON), &result.Diagnostics); err != nil {
		return nil, fmt.Errorf("decode stock news diagnostics: %w", err)
	}
	if result.FetchedAt, err = time.Parse(time.RFC3339Nano, fetchedAt); err != nil {
		return nil, fmt.Errorf("decode stock news fetched time: %w", err)
	}
	if result.ExpiresAt, err = time.Parse(time.RFC3339Nano, expiresAt); err != nil {
		return nil, fmt.Errorf("decode stock news expiry: %w", err)
	}
	return &result, nil
}
