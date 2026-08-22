package gateway

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

const maxWatchlistItems = 26

var errInvalidWatchlist = errors.New("invalid watchlist request")

type WatchlistItem struct {
	Symbol    string `json:"symbol"`
	Code      string `json:"code"`
	Name      string `json:"name"`
	Market    string `json:"market"`
	AssetType string `json:"asset_type"`
	SortOrder int    `json:"sort_order"`
	CreatedAt string `json:"created_at,omitempty"`
	UpdatedAt string `json:"updated_at,omitempty"`
}

func (s *GatewayStore) ListWatchlist(ctx context.Context, accountID int) ([]WatchlistItem, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT symbol, code, name, market, asset_type, sort_order, created_at, updated_at
FROM watchlist_items WHERE account_id = ? ORDER BY sort_order, created_at, symbol`, accountID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]WatchlistItem, 0)
	for rows.Next() {
		var item WatchlistItem
		if err := rows.Scan(&item.Symbol, &item.Code, &item.Name, &item.Market, &item.AssetType,
			&item.SortOrder, &item.CreatedAt, &item.UpdatedAt); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *GatewayStore) UpsertWatchlist(ctx context.Context, accountID int, items []WatchlistItem) ([]WatchlistItem, error) {
	if len(items) == 0 {
		return nil, fmt.Errorf("%w: at least one item is required", errInvalidWatchlist)
	}
	existing, err := s.ListWatchlist(ctx, accountID)
	if err != nil {
		return nil, err
	}
	existingSymbols := make(map[string]bool, len(existing))
	for _, item := range existing {
		existingSymbols[item.Symbol] = true
	}
	seen := make(map[string]bool, len(items))
	normalized := make([]WatchlistItem, 0, len(items))
	newCount := 0
	for _, item := range items {
		item, err = normalizeWatchlistItem(item)
		if err != nil {
			return nil, err
		}
		if seen[item.Symbol] {
			continue
		}
		seen[item.Symbol] = true
		if !existingSymbols[item.Symbol] {
			newCount++
		}
		normalized = append(normalized, item)
	}
	if len(existing)+newCount > maxWatchlistItems {
		return nil, fmt.Errorf("%w: watchlist cannot exceed %d items", errInvalidWatchlist, maxWatchlistItems)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	now := time.Now().UTC().Format(time.RFC3339Nano)
	for _, item := range normalized {
		if _, err := tx.ExecContext(ctx, `
INSERT INTO watchlist_items(account_id, symbol, code, name, market, asset_type, sort_order, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(account_id, symbol) DO UPDATE SET
 code=excluded.code, name=excluded.name, market=excluded.market,
 asset_type=excluded.asset_type, sort_order=excluded.sort_order, updated_at=excluded.updated_at`,
			accountID, item.Symbol, item.Code, item.Name, item.Market, item.AssetType,
			item.SortOrder, now, now); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return s.ListWatchlist(ctx, accountID)
}

func (s *GatewayStore) DeleteWatchlistItem(ctx context.Context, accountID int, symbol string) error {
	symbol = strings.ToUpper(strings.TrimSpace(symbol))
	if symbol == "" {
		return fmt.Errorf("%w: symbol is required", errInvalidWatchlist)
	}
	_, err := s.db.ExecContext(ctx, `DELETE FROM watchlist_items WHERE account_id = ? AND symbol = ?`, accountID, symbol)
	return err
}

func (s *GatewayStore) ReorderWatchlist(ctx context.Context, accountID int, symbols []string) ([]WatchlistItem, error) {
	if len(symbols) == 0 || len(symbols) > maxWatchlistItems {
		return nil, fmt.Errorf("%w: invalid order", errInvalidWatchlist)
	}
	existing, err := s.ListWatchlist(ctx, accountID)
	if err != nil {
		return nil, err
	}
	if len(symbols) != len(existing) {
		return nil, fmt.Errorf("%w: order must contain every watchlist item", errInvalidWatchlist)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	seen := make(map[string]bool, len(symbols))
	now := time.Now().UTC().Format(time.RFC3339Nano)
	for index, symbol := range symbols {
		symbol = strings.ToUpper(strings.TrimSpace(symbol))
		if symbol == "" || seen[symbol] {
			return nil, fmt.Errorf("%w: duplicate or empty symbol", errInvalidWatchlist)
		}
		seen[symbol] = true
		result, err := tx.ExecContext(ctx, `UPDATE watchlist_items SET sort_order=?, updated_at=? WHERE account_id=? AND symbol=?`, index, now, accountID, symbol)
		if err != nil {
			return nil, err
		}
		if affected, _ := result.RowsAffected(); affected == 0 {
			return nil, fmt.Errorf("%w: symbol %s is not in the watchlist", errInvalidWatchlist, symbol)
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return s.ListWatchlist(ctx, accountID)
}

func normalizeWatchlistItem(item WatchlistItem) (WatchlistItem, error) {
	item.Symbol = strings.ToUpper(strings.TrimSpace(item.Symbol))
	item.Code = strings.TrimSpace(item.Code)
	item.Name = strings.TrimSpace(item.Name)
	item.Market = strings.TrimSpace(item.Market)
	item.AssetType = strings.ToLower(strings.TrimSpace(item.AssetType))
	if item.AssetType == "" {
		if cryptoSymbolPattern.MatchString(item.Symbol) {
			item.AssetType = "crypto"
		} else {
			item.AssetType = "stock"
		}
	}
	switch item.AssetType {
	case "stock":
		if !stockSymbolPattern.MatchString(item.Symbol) {
			return WatchlistItem{}, fmt.Errorf("%w: invalid stock symbol %q", errInvalidWatchlist, item.Symbol)
		}
	case "crypto":
		if !cryptoSymbolPattern.MatchString(item.Symbol) {
			return WatchlistItem{}, fmt.Errorf("%w: invalid crypto symbol %q", errInvalidWatchlist, item.Symbol)
		}
	default:
		return WatchlistItem{}, fmt.Errorf("%w: unsupported asset type %q", errInvalidWatchlist, item.AssetType)
	}
	if len([]rune(item.Name)) > 80 || len([]rune(item.Code)) > 32 || len([]rune(item.Market)) > 40 {
		return WatchlistItem{}, fmt.Errorf("%w: item metadata is too long", errInvalidWatchlist)
	}
	if item.Code == "" {
		if item.AssetType == "crypto" {
			item.Code = strings.Split(item.Symbol, "-")[0]
		} else if parts := strings.SplitN(item.Symbol, ".", 2); len(parts) == 2 {
			item.Code = parts[1]
		}
	}
	if item.Name == "" {
		item.Name = item.Code
	}
	return item, nil
}
