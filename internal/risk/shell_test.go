package risk

import "testing"

func TestApproveRejectsWideSpread(t *testing.T) {
	cfg := DefaultConfig()
	dec := Approve(cfg, Input{
		Side:              "LONG",
		Entry:             100,
		Stop:              99,
		Leverage:          3,
		NotionalUSD:       300,
		SpreadBps:         40,
		BookImbalance:     1.2,
		RecentSlippageBps: 2,
		VenueHealthy:      true,
	})
	if dec.Approved || dec.RejectReason != "spread_too_wide" {
		t.Fatalf("expected spread_too_wide, got %+v", dec)
	}
}

func TestApprovePassesHealthyTrade(t *testing.T) {
	cfg := DefaultConfig()
	dec := Approve(cfg, Input{
		Side:              "LONG",
		Entry:             100,
		Stop:              98,
		Leverage:          3,
		NotionalUSD:       300,
		FundingRate:       -0.0001,
		SpreadBps:         4,
		BookImbalance:     1.2,
		RecentSlippageBps: 2,
		VenueHealthy:      true,
	})
	if !dec.Approved {
		t.Fatalf("expected approval, got %+v", dec)
	}
}
