package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"

	"hyperliquid/adapters/hyperliquid"
	"hyperliquid/internal/features"
	"hyperliquid/internal/inplay"
	"hyperliquid/internal/market"
	"hyperliquid/internal/sessions"
	"hyperliquid/internal/status"
	"hyperliquid/internal/strategies"
)

func main() {
	baseURL := flag.String("base-url", hyperliquid.MainnetURL, "Hyperliquid API base URL")
	tf := flag.String("tf", "5m", "candle timeframe")
	candlesN := flag.Int("candles", 72, "number of candles")
	poll := flag.Duration("poll", 30*time.Second, "scan interval")
	topN := flag.Int("top", 3, "rows to print")
	port := flag.String("port", "8081", "status server port")
	engineURL := flag.String("engine-url", "http://localhost:8090", "live-lite engine url")
	flag.Parse()

	client := hyperliquid.New(*baseURL)
	trk := inplay.NewTracker("short", inplay.Config{MinGrade: "C", MinVolumeUSD: 1_000_000})
	store := status.NewStore()
	registerHTTP(store)
	go serveHTTP(*port)

	for {
		runOnce(client, trk, store, *tf, *candlesN, *topN, strings.TrimRight(strings.TrimSpace(*engineURL), "/"))
		time.Sleep(*poll)
	}
}

func runOnce(client *hyperliquid.Client, trk *inplay.Tracker, store *status.Store, tf string, candlesN int, topN int, engineURL string) {
	now := time.Now().UTC()
	fmt.Printf("🔧 HYPERLIQUID SHORT adapter - live fetch @ %s\n", now.Format(time.RFC3339))
	rows, grades := sideRows(client, tf, candlesN, "short", now)
	trk.Update(now, rows, grades)
	inplayEntries := trk.Entries()
	store.SetSnap(status.Snapshot{
		Generated: now,
		Exchange:  "hyperliquid (SHORTS)",
		Active:    sessions.ActiveSessionLabels(now),
		Rows:      rows,
		Conf:      grades,
		InPlay:    inplayEntries,
		Engine:    fetchEngineSnapshot(engineURL),
	})
	fmt.Println(market.FormatHeader("hyperliquid (SHORTS)", sessions.ActiveSessionLabels(now)))
	fmt.Println("Symbol       | Score  | Δ%(24h) | DayUTC% | Vol($)  | OI($)   | Funding(%) | Open24h  | Last     | G")
	fmt.Println("------------------------------------------------------------------------------------------------------------")
	printRows(rows, grades, topN)
	printInPlay("SHORT", inplayEntries, topN)
}

func sideRows(client *hyperliquid.Client, tf string, candlesN int, side string, now time.Time) ([]market.Scored, map[string]string) {
	mkts, err := client.FetchAllMarkets()
	if err != nil {
		return nil, nil
	}
	snaps := make([]features.Snapshot, 0, len(mkts))
	for _, mkt := range mkts {
		candles, err := client.LoadCandles(mkt.Symbol, tf, candlesN)
		if err != nil || len(candles) == 0 {
			continue
		}
		book, err := client.FetchOrderBook(mkt.Symbol, 10)
		if err != nil {
			continue
		}
		snaps = append(snaps, features.BuildSnapshot(mkt, candles, book))
	}
	longs, shorts := strategies.Rank(snaps, now)
	ranked := longs
	if side == "short" {
		ranked = shorts
	}
	rows := toScoredRows(mkts, ranked, side)
	return buildEligible(rows, side)
}

func toScoredRows(markets []market.Market, ranked []market.RankedMarket, side string) []market.Scored {
	bySymbol := make(map[string]market.Market, len(markets))
	for _, m := range markets {
		bySymbol[m.Symbol] = m
	}
	out := make([]market.Scored, 0, len(ranked))
	for _, r := range ranked {
		m := bySymbol[r.Symbol]
		funding := m.FundingRate
		oi := m.OpenInterest
		out = append(out, market.Scored{
			Symbol:            r.Symbol,
			Change24h:         r.ChangePct,
			DayUTC24h:         ptr(r.DayUTCChangePct),
			VolumeUSD:         m.Volume24hUSD,
			OIUSD:             ptr(oi),
			FundingRate:       ptr(funding),
			OpenPrice:         r.WindowOpenPrice,
			LastPrice:         r.Last,
			Grade:             gradeLabelForRanked(r, side),
			Score:             r.Score,
			RawScore:          r.RawScore,
			NormalizedScore:   r.NormalizedScore,
			Reason:            r.Reason,
			WindowDerived:     true,
			Signals:           r.Eligibility,
			Displayable:       m.Volume24hUSD > 0,
			Completeness:      r.Completeness,
			IntegrityPenalty:  r.IntegrityPenalty,
			ExecutionPenalty:  r.ExecutionPenalty,
			SpreadPenaltyBps:  r.SpreadBps,
			EstSlippageBps:    r.EstSlippageBps,
			TopBookUSD:        r.TopBookUSD,
			Momentum5m:        r.Momentum5m,
			Momentum30m:       r.Momentum30m,
			Momentum4h:        r.Momentum4h,
			Momentum24h:       r.Momentum24h,
			MomentumAgreement: r.MomentumAgreement,
			RegimeTag:         r.RegimeTag,
			Confidence:        r.Confidence,
			Uncertainty:       r.Uncertainty,
			DataFlags:         append([]string{}, r.DataFlags...),
			ReliabilityAdj:    r.ReliabilityAdj,
			TradePriority:     r.TradePriority,
		})
	}
	return out
}

func buildEligible(rows []market.Scored, side string) ([]market.Scored, map[string]string) {
	out := make([]market.Scored, 0, len(rows))
	grades := make(map[string]string, len(rows))
	for _, row := range rows {
		var ok bool
		if side == "short" {
			ok, row.EligibilityReasons = market.EligibleShort(row)
		} else {
			ok, row.EligibilityReasons = market.EligibleLong(row)
		}
		row.Eligible = ok
		if !ok {
			continue
		}
		if row.Grade == "N/A" {
			continue
		}
		out = append(out, row)
		grades[row.Symbol] = row.Grade
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Score > out[j].Score })
	return out, grades
}

func printRows(rows []market.Scored, grades map[string]string, topN int) {
	if len(rows) == 0 {
		fmt.Println("none")
		return
	}
	if topN <= 0 {
		topN = 1
	}
	limit := topN
	if rem := len(rows) - topN; rem > 0 && rem <= 3 {
		limit = len(rows)
	}
	for i, row := range rows {
		if i >= limit {
			break
		}
		row.Grade = grades[row.Symbol]
		fmt.Println(market.FormatRow(row))
	}
	if len(rows) > limit {
		fmt.Printf("+%d more\n", len(rows)-limit)
	}
}

func printInPlay(side string, entries []inplay.Entry, topN int) {
	fmt.Printf("🔥 IN-PLAY (%s)\n", side)
	if len(entries) == 0 {
		fmt.Println("none")
		return
	}
	if topN <= 0 {
		topN = 1
	}
	for i, e := range entries {
		if i >= topN {
			break
		}
		fmt.Printf("%-12s grade=%s score=%5.2f slope=%6.3f state=%-10s momentum=%-5v\n",
			market.DisplaySymbol(e.Symbol), market.ColorGrade(e.CurrentGrade), e.CurrentScore, e.ScoreSlope, e.State, e.Momentum)
	}
}

func registerHTTP(store *status.Store) {
	http.Handle("/", store.Handler())
	http.Handle("/status", store.Handler())
	http.Handle("/api/status", store.APISnapshotHandler())
	http.Handle("/api/inplay", store.InPlayHandler())
	http.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
	})
}

func serveHTTP(port string) {
	if port == "" {
		port = "8081"
	}
	if err := http.ListenAndServe(":"+port, nil); err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "address already in use") {
			fmt.Printf("status server disabled: port %s already in use\n", port)
			return
		}
		fmt.Println("http server error:", err)
	}
}

func gradeLabelForRanked(r market.RankedMarket, side string) string {
	lbl := strings.TrimSpace(r.ConfluenceLabel)
	if lbl != "" && lbl != "_" && !strings.EqualFold(lbl, "C") {
		return lbl
	}
	return market.FallbackGradeDirectional(r.Score, r.ChangePct, side)
}

func ptr(v float64) *float64 { return &v }

func fetchEngineSnapshot(base string) *status.EngineSnapshot {
	if strings.TrimSpace(base) == "" {
		return nil
	}
	resp, err := http.Get(base + "/api/engine/status")
	if err != nil {
		return nil
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil
	}
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil
	}
	var snap status.EngineSnapshot
	if err := json.Unmarshal(raw, &snap); err != nil {
		return nil
	}
	return &snap
}
