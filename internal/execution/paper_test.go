package execution

import (
	"strings"
	"testing"

	"hyperliquid/internal/market"
)

func TestPaperReservesAndReleasesCapital(t *testing.T) {
	p := NewPaper(1000)
	_, err := p.PlaceOrder(market.OrderRequest{
		Symbol:      "BTC",
		Side:        market.SideLong,
		Size:        1,
		Price:       100,
		StopPrice:   95,
		TargetPrice: 110,
		Leverage:    5,
	})
	if err != nil {
		t.Fatalf("place order: %v", err)
	}
	snap := p.Snapshot()
	if snap.Available != 980 || snap.Reserved != 20 {
		t.Fatalf("unexpected capital state after entry: %+v", snap)
	}
	receipts := p.Manage(map[string]ManageInput{
		"BTC": {Symbol: "BTC", MarkPrice: 111},
	})
	if len(receipts) == 0 {
		t.Fatal("expected target receipt")
	}
	snap = p.Snapshot()
	if snap.Reserved != 0 {
		t.Fatalf("expected released capital, got %+v", snap)
	}
	if snap.Available <= 1000 {
		t.Fatalf("expected realized gain reflected in available, got %+v", snap)
	}
}

func TestPaperRejectsOverAllocation(t *testing.T) {
	p := NewPaper(10)
	_, err := p.PlaceOrder(market.OrderRequest{
		Symbol:      "BTC",
		Side:        market.SideLong,
		Size:        1,
		Price:       100,
		StopPrice:   95,
		TargetPrice: 110,
		Leverage:    2,
	})
	if err == nil || !strings.Contains(err.Error(), "insufficient free capital") {
		t.Fatalf("expected insufficient capital error, got %v", err)
	}
}

func TestPaperForceExitsOnStaleManageInput(t *testing.T) {
	p := NewPaper(1000)
	_, err := p.PlaceOrder(market.OrderRequest{
		Symbol:      "BTC",
		Side:        market.SideLong,
		Size:        2,
		Price:       100,
		StopPrice:   95,
		TargetPrice: 110,
		Leverage:    5,
	})
	if err != nil {
		t.Fatalf("place order: %v", err)
	}
	receipts := p.Manage(map[string]ManageInput{
		"BTC": {Symbol: "BTC", MarkPrice: 99, Stale: true, ForceExit: true, ExitReason: "STALE_DATA_EXIT"},
	})
	if len(receipts) != 1 || receipts[0].Reason != "STALE_DATA_EXIT" {
		t.Fatalf("expected stale exit receipt, got %+v", receipts)
	}
	if snap := p.Snapshot(); snap.OpenCount != 0 {
		t.Fatalf("expected flat after stale exit, got %+v", snap)
	}
}
