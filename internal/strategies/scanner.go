package strategies

import (
	"sort"
	"time"

	"hyperliquid/internal/features"
	"hyperliquid/internal/market"
)

func Rank(snapshots []features.Snapshot, now time.Time) ([]market.RankedMarket, []market.RankedMarket) {
	longs := make([]market.RankedMarket, 0, len(snapshots))
	shorts := make([]market.RankedMarket, 0, len(snapshots))
	cfg := currentRankConfig()
	for _, s := range snapshots {
		longRanked := scoreSnapshot(s, market.SideLong, cfg)
		longRanked.Generated = now
		shortRanked := scoreSnapshot(s, market.SideShort, cfg)
		shortRanked.Generated = now
		longs = append(longs, longRanked)
		shorts = append(shorts, shortRanked)
	}
	sort.Slice(longs, func(i, j int) bool { return longs[i].TradePriority > longs[j].TradePriority })
	sort.Slice(shorts, func(i, j int) bool { return shorts[i].TradePriority > shorts[j].TradePriority })
	return longs, shorts
}
