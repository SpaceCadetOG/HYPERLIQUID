package inplay

import (
	"testing"
	"time"

	"hyperliquid/internal/market"
)

func TestBalancedImpulseClassifier(t *testing.T) {
	if !balancedImpulse(0.05, true, false, false) {
		t.Fatal("expected low-energy sequence to classify as balanced")
	}
	if balancedImpulse(0.45, true, true, false) {
		t.Fatal("expected strong impulse not to classify as balanced")
	}
}

func TestTrackerPumpingStateOnStrongRise(t *testing.T) {
	tr := NewTracker("short", Config{MinGrade: "C", MinVolumeUSD: 1000, HistoryN: 5, RiseN: 3})
	now := time.Now().UTC()
	tr.Update(now, []market.Scored{{Symbol: "ETH", Grade: "B", Score: 60, VolumeUSD: 2000, LastPrice: 100, Change24h: -2}}, map[string]string{"ETH": "B"})
	tr.Update(now.Add(time.Minute), []market.Scored{{Symbol: "ETH", Grade: "B", Score: 68, VolumeUSD: 2200, LastPrice: 99, Change24h: -3}}, map[string]string{"ETH": "B"})
	tr.Update(now.Add(2*time.Minute), []market.Scored{{Symbol: "ETH", Grade: "B", Score: 78, VolumeUSD: 2500, LastPrice: 98, Change24h: -4}}, map[string]string{"ETH": "B"})
	entries := tr.Entries()
	if len(entries) != 1 {
		t.Fatalf("expected one entry, got %d", len(entries))
	}
	if entries[0].State != StatePumping && entries[0].State != StateInPlay {
		t.Fatalf("expected strong state, got %s", entries[0].State)
	}
}

func TestTrackerUsesExplicitGradeMap(t *testing.T) {
	tr := NewTracker("long", Config{MinGrade: "C", MinVolumeUSD: 1000, HistoryN: 5, RiseN: 3})
	now := time.Now().UTC()
	tr.Update(now, []market.Scored{{Symbol: "SOL", Grade: "N/A", Score: 72, VolumeUSD: 5000, LastPrice: 10, Change24h: 2}}, map[string]string{"SOL": "B"})
	entries := tr.Entries()
	if len(entries) != 1 {
		t.Fatalf("expected one entry, got %d", len(entries))
	}
	if entries[0].CurrentGrade != "B" {
		t.Fatalf("expected grade map to win, got %s", entries[0].CurrentGrade)
	}
}
