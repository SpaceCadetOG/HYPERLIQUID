package execution

import (
	"path/filepath"
	"testing"

	"hyperliquid/internal/market"
)

func TestLiveStateStoreRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "live_state.json")
	store := NewLiveStateStore(path)
	err := store.UpsertEntry(market.OrderRequest{
		Symbol:      "ETH",
		Side:        market.SideLong,
		Size:        0.1,
		Price:       2000,
		StopPrice:   1900,
		TargetPrice: 2100,
	}, market.OrderResult{
		OrderID:       "1",
		StopOrderID:   "2",
		TargetOrderID: "3",
	})
	if err != nil {
		t.Fatalf("upsert entry: %v", err)
	}
	reloaded := NewLiveStateStore(path)
	snap := reloaded.Snapshot()
	pos, ok := snap.Positions["ETH"]
	if !ok {
		t.Fatalf("expected ETH state")
	}
	if pos.StopOrderID != "2" || pos.TargetOrderID != "3" {
		t.Fatalf("unexpected protective ids: %+v", pos)
	}
	if err := reloaded.Remove("ETH"); err != nil {
		t.Fatalf("remove: %v", err)
	}
	if _, ok := reloaded.Snapshot().Positions["ETH"]; ok {
		t.Fatalf("expected state removed")
	}
}
