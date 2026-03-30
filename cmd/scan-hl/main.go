package main

import (
	"flag"
	"fmt"
	"log"
	"strings"
	"time"

	"hyperliquid/adapters/hyperliquid"
	"hyperliquid/internal/features"
	"hyperliquid/internal/gate"
	"hyperliquid/internal/market"
	"hyperliquid/internal/strategies"
)

func main() {
	baseURL := flag.String("base-url", hyperliquid.MainnetURL, "Hyperliquid API base URL")
	tf := flag.String("tf", "5m", "candle timeframe")
	limit := flag.Int("limit", 20, "number of rows per side")
	candlesN := flag.Int("candles", 72, "number of candles to load for profile and AVWAP")
	minScore := flag.Float64("min-score", 40, "minimum scanner score")
	flag.Parse()

	client := hyperliquid.New(*baseURL)
	markets, err := client.FetchAllMarkets()
	if err != nil {
		log.Fatal(err)
	}

	snapshots := make([]features.Snapshot, 0, len(markets))
	for _, mkt := range markets {
		candles, err := client.LoadCandles(mkt.Symbol, *tf, *candlesN)
		if err != nil || len(candles) == 0 {
			continue
		}
		book, err := client.FetchOrderBook(mkt.Symbol, 10)
		if err != nil {
			continue
		}
		snapshots = append(snapshots, features.BuildSnapshot(mkt, candles, book))
	}

	now := time.Now().UTC()
	longs, shorts := strategies.Rank(snapshots, now)
	longs = gate.Filter(longs, gate.Config{MinScore: *minScore})
	shorts = gate.Filter(shorts, gate.Config{MinScore: *minScore})
	printSide("LONGS", longs, *limit)
	printSide("SHORTS", shorts, *limit)
}

func printSide(title string, rows []market.RankedMarket, limit int) {
	fmt.Println()
	fmt.Println(title)
	fmt.Println(strings.Repeat("-", 100))
	fmt.Printf("%-10s %-6s %-8s %-9s %-7s %-7s %-8s %-8s %-8s %s\n", "symbol", "side", "score", "last", "adx", "atr%", "whale", "spread", "funding", "reason")
	for i, row := range rows {
		if limit > 0 && i >= limit {
			break
		}
		fmt.Printf("%-10s %-6s %-8.1f %-9.4f %-7.1f %-7.2f %-8.2f %-8.2f %-8.4f %s\n",
			row.Symbol, row.Side, row.Score, row.Last, row.ADX, row.ATRPct, row.WhaleDistanceBps, row.SpreadBps, row.Funding, row.Reason)
	}
}
