package gate

import (
	"testing"

	"hyperliquid/internal/market"
)

func TestFilter(t *testing.T) {
	in := []market.RankedMarket{{Symbol: "BTC", Score: 50}, {Symbol: "ETH", Score: 20}}
	out := Filter(in, Config{MinScore: 30})
	if len(out) != 1 || out[0].Symbol != "BTC" {
		t.Fatalf("unexpected filter result: %+v", out)
	}
}

func TestEvaluateRejectsLowGrade(t *testing.T) {
	cfg := DefaultConfig()
	cfg.RequireMTF = false
	dec := Evaluate(Input{Symbol: "BTC", Side: "LONG", Grade: "C", Score: 80, Slope: 0.2, VolumeRatio: 2.0}, cfg)
	if dec.Allow {
		t.Fatal("expected reject on low grade")
	}
}
