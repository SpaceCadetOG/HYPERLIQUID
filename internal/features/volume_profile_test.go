package features

import (
	"testing"
	"time"

	"hyperliquid/internal/market"
)

func TestBuildSnapshotDirectionalBias(t *testing.T) {
	mkt := market.Market{Symbol: "BTC", MarkPrice: 160, FundingRate: -0.01}
	candles := []market.Candle{
		{Time: time.Now(), Open: 100, High: 120, Low: 90, Close: 100, Volume: 10},
		{Time: time.Now(), Open: 100, High: 140, Low: 95, Close: 130, Volume: 12},
		{Time: time.Now(), Open: 130, High: 165, Low: 120, Close: 160, Volume: 15},
	}
	book := market.OrderBook{
		Bids: []market.BookLevel{{Price: 159, Size: 10}, {Price: 158, Size: 10}},
		Asks: []market.BookLevel{{Price: 161, Size: 2}, {Price: 162, Size: 2}},
	}
	snap := BuildSnapshot(mkt, candles, book)
	if snap.LongScore <= snap.ShortScore {
		t.Fatalf("expected long bias, got long=%.1f short=%.1f", snap.LongScore, snap.ShortScore)
	}
	if snap.AVWAP <= 0 {
		t.Fatalf("expected avwap to be populated, got %.4f", snap.AVWAP)
	}
	if snap.SpreadBps <= 0 {
		t.Fatalf("expected spread bps to be populated, got %.4f", snap.SpreadBps)
	}
	if snap.ADX <= 0 {
		t.Fatalf("expected adx to be populated, got %.4f", snap.ADX)
	}
	if snap.LongTargetPrice <= snap.Last {
		t.Fatalf("expected long target above last, got target=%.4f last=%.4f", snap.LongTargetPrice, snap.Last)
	}
}

func TestBuildSnapshotShortBiasBelowValue(t *testing.T) {
	mkt := market.Market{Symbol: "ETH", MarkPrice: 91, FundingRate: 0.01, TickSize: 0.5}
	candles := []market.Candle{
		{Time: time.Now(), Open: 120, High: 122, Low: 116, Close: 118, Volume: 8},
		{Time: time.Now(), Open: 118, High: 119, Low: 108, Close: 110, Volume: 10},
		{Time: time.Now(), Open: 110, High: 111, Low: 90, Close: 92, Volume: 12},
	}
	book := market.OrderBook{
		Bids: []market.BookLevel{{Price: 90.5, Size: 2}, {Price: 90, Size: 1}},
		Asks: []market.BookLevel{{Price: 91.5, Size: 12}, {Price: 92, Size: 10}},
	}

	snap := BuildSnapshot(mkt, candles, book)
	if snap.ShortScore <= snap.LongScore {
		t.Fatalf("expected short bias, got long=%.1f short=%.1f", snap.LongScore, snap.ShortScore)
	}
	if snap.ShortStopPrice <= snap.Last {
		t.Fatalf("expected short stop above last, got stop=%.4f last=%.4f", snap.ShortStopPrice, snap.Last)
	}
}
