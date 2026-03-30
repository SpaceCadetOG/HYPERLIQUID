package hyperliquid

import (
	"math"
	"testing"

	"hyperliquid/internal/market"
)

func TestRoundHelpers(t *testing.T) {
	c := New("")
	c.meta["BTC"] = market.Market{
		Symbol:        "BTC",
		PriceDecimals: 1,
		SizeDecimals:  3,
		TickSize:      0.1,
	}
	c.metaLoaded = true

	price, err := c.RoundPrice("BTC", 101.27)
	if err != nil {
		t.Fatalf("round price: %v", err)
	}
	if math.Abs(price-101.3) > 1e-9 {
		t.Fatalf("unexpected rounded price: %.4f", price)
	}

	size, err := c.RoundSize("BTC", 0.0199)
	if err != nil {
		t.Fatalf("round size: %v", err)
	}
	if size != 0.02 {
		t.Fatalf("unexpected rounded size: %.6f", size)
	}
}

func TestValidateOrderRequest(t *testing.T) {
	c := New("")
	c.meta["ETH"] = market.Market{
		Symbol:       "ETH",
		SizeDecimals: 3,
		TickSize:     0.1,
	}
	c.metaLoaded = true

	if err := c.ValidateOrderRequest(market.OrderRequest{
		Symbol: "ETH",
		Side:   market.SideLong,
		Size:   0.1254,
		Price:  2000.05,
		Type:   "limit",
	}); err == nil {
		t.Fatal("expected precision validation error")
	}

	if err := c.ValidateOrderRequest(market.OrderRequest{
		Symbol: "ETH",
		Side:   market.SideLong,
		Size:   0.125,
		Price:  2000.1,
		Type:   "limit",
	}); err != nil {
		t.Fatalf("unexpected validation error: %v", err)
	}
}

func TestAssetIDLookup(t *testing.T) {
	c := New("")
	c.assetIDs["SOL"] = 5
	c.metaLoaded = true
	id, ok, err := c.AssetID("SOL")
	if err != nil {
		t.Fatalf("asset id err: %v", err)
	}
	if !ok || id != 5 {
		t.Fatalf("unexpected asset id: %v %v", id, ok)
	}
}
