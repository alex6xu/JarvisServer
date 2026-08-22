package gateway

import (
	"context"
	"errors"
	"testing"
)

func TestWatchlistCRUDAndAccountIsolation(t *testing.T) {
	store, err := OpenGatewayStore(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	first, err := store.CreateAccount(context.Background(), "first", "", "user", "password-123")
	if err != nil {
		t.Fatal(err)
	}
	second, err := store.CreateAccount(context.Background(), "second", "", "user", "password-123")
	if err != nil {
		t.Fatal(err)
	}
	items, err := store.UpsertWatchlist(context.Background(), first.ID, []WatchlistItem{
		{Symbol: "105.aapl", Code: "AAPL", Name: "Apple", Market: "US"},
		{Symbol: "btc-usdt", Name: "Bitcoin"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 || items[0].Symbol != "105.AAPL" || items[1].AssetType != "crypto" {
		t.Fatalf("items=%+v", items)
	}
	other, err := store.ListWatchlist(context.Background(), second.ID)
	if err != nil || len(other) != 0 {
		t.Fatalf("other=%+v err=%v", other, err)
	}
	items, err = store.ReorderWatchlist(context.Background(), first.ID, []string{"BTC-USDT", "105.AAPL"})
	if err != nil || items[0].Symbol != "BTC-USDT" {
		t.Fatalf("reordered=%+v err=%v", items, err)
	}
	if err := store.DeleteWatchlistItem(context.Background(), first.ID, "BTC-USDT"); err != nil {
		t.Fatal(err)
	}
	items, _ = store.ListWatchlist(context.Background(), first.ID)
	if len(items) != 1 {
		t.Fatalf("after delete=%+v", items)
	}
}

func TestWatchlistValidation(t *testing.T) {
	if _, err := normalizeWatchlistItem(WatchlistItem{Symbol: "../../secret", AssetType: "stock"}); !errors.Is(err, errInvalidWatchlist) {
		t.Fatalf("err=%v", err)
	}
	if item, err := normalizeWatchlistItem(WatchlistItem{Symbol: "ETH-USDT"}); err != nil || item.Code != "ETH" || item.AssetType != "crypto" {
		t.Fatalf("item=%+v err=%v", item, err)
	}
}
