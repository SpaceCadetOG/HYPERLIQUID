package execution

import (
	"testing"

	"hyperliquid/internal/market"
)

func TestEvaluateProtectWeakFlowBE(t *testing.T) {
	m := NewManager(Config{})
	dec := m.EvaluateProtect(ProtectInput{
		Side: "BUY", Entry: 100, Stop: 98, Mark: 101, MFER: 0.6, MAER: 0.2, WeakFlow: true,
	})
	if !dec.MoveStopToBE {
		t.Fatalf("expected BE arm on weak flow")
	}
}

func TestEvaluateProtectNoFollowThrough(t *testing.T) {
	m := NewManager(Config{NoFollowThroughBars: 6, NoFollowThroughMinMFER: 0.3, NoFollowThroughMinMAER: 0.8})
	dec := m.EvaluateProtect(ProtectInput{
		Side: "BUY", Entry: 100, Stop: 98, Mark: 99.6, BarsHeld: 8, MFER: 0.1, MAER: 1.0,
	})
	if !dec.FullExit || dec.Reason != "NO_FOLLOW_THROUGH" {
		t.Fatalf("expected no follow through exit, got %+v", dec)
	}
}

func TestPaperManageClosesAtTarget(t *testing.T) {
	p := NewPaper(1000)
	_, err := p.PlaceOrder(market.OrderRequest{
		Symbol:      "BTC",
		Side:        market.SideLong,
		Size:        1,
		Price:       100,
		StopPrice:   95,
		TargetPrice: 110,
	})
	if err != nil {
		t.Fatalf("place order: %v", err)
	}
	receipts := p.Manage(map[string]ManageInput{
		"BTC": {Symbol: "BTC", MarkPrice: 111},
	})
	if len(receipts) != 1 {
		t.Fatalf("expected 1 receipt, got %d", len(receipts))
	}
	if receipts[0].Reason != "TARGET_HIT" {
		t.Fatalf("expected TARGET_HIT, got %+v", receipts[0])
	}
	positions, _ := p.Positions()
	if len(positions) != 0 {
		t.Fatalf("expected position closed, got %+v", positions)
	}
}
