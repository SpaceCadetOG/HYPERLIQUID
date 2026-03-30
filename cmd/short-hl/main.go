package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"sort"
	"strconv"
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
	topN := flag.Int("top", 5, "rows to print")
	port := flag.String("port", "8081", "status server port")
	engineURL := flag.String("engine-url", "http://localhost:8090", "live-lite engine url")
	flag.Parse()

	client := hyperliquid.New(*baseURL)
	trk := inplay.NewTracker("short", inplay.Config{
		MinGrade:       envStr("INPLAY_MIN_GRADE", "C"),
		MinVolumeUSD:   envFloat("INPLAY_MIN_VOL_USD", 1_000_000),
		HistoryN:       envInt("INPLAY_HISTORY_N", 5),
		RiseN:          envInt("INPLAY_RISE_N", 3),
		DropGradeScans: envInt("INPLAY_DROP_SCANS", 2),
		FallScans:      envInt("INPLAY_FALL_SCANS", 2),
		TTL:            time.Duration(envInt("INPLAY_TTL_MIN", 30)) * time.Minute,
	})
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
	rows, grades := sideRows(client, trk, tf, candlesN, "short", now)
	entries := filterDisplayInPlay(trk.Entries())
	store.SetSnap(status.Snapshot{Generated: now, Exchange: "hyperliquid (SHORTS)", Active: sessions.ActiveSessionLabels(now), Rows: rows, Conf: grades, InPlay: entries, Engine: fetchEngineSnapshot(engineURL)})

	fmt.Println(market.FormatHeader("hyperliquid (SHORTS)", sessions.ActiveSessionLabels(now)))
	fmt.Println("Symbol       | Score  | DayUTC% | UTC4h%  | UTC1h%  | Δ%(24h) | Vol($)  | OI($)   | Funding(%) | OpenUTC |     Last")
	fmt.Println("-------------+--------+---------+---------+---------+----------+---------+---------+------------+---------+---------")
	printRows(rows, grades, topN)
	printInPlay("SHORT", entries)
}

func sideRows(client *hyperliquid.Client, trk *inplay.Tracker, tf string, candlesN int, side string, now time.Time) ([]market.Scored, map[string]string) {
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
	rows := toScoredRows(mkts, ranked, side, sessions.ScannerScoreMultiplier(now))
	confMap := initialGradeMap(rows)
	trk.Update(now, rows, confMap)
	eligible, grades := buildEligible(rows, side, entryMap(trk.Entries()))
	trk.Update(now, rows, grades)
	return eligible, grades
}

func toScoredRows(markets []market.Market, ranked []market.RankedMarket, side string, scanMult float64) []market.Scored {
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
			UTC4hPct:          ptr(r.UTC4hPct),
			UTC1hPct:          ptr(r.UTC1hPct),
			Change24h:         r.ChangePct,
			DayUTC24h:         ptr(r.DayUTCChangePct),
			VolumeUSD:         m.Volume24hUSD,
			OIUSD:             ptr(oi),
			FundingRate:       ptr(funding),
			OpenPrice:         r.WindowOpenPrice,
			LastPrice:         r.Last,
			Grade:             gradeLabelForRanked(r, side),
			Score:             round2(r.Score * scanMult),
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

func buildEligible(rows []market.Scored, side string, entryBySymbol map[string]inplay.Entry) ([]market.Scored, map[string]string) {
	out := make([]market.Scored, 0, len(rows))
	grades := make(map[string]string, len(rows))
	earlyShortMin := envFloat("SHORT_EARLY_ADMISSION_MIN_REVERSAL", 5.0)
	for _, row := range rows {
		ok, reasons := market.EligibleShort(row)
		row.Eligible = ok
		row.EligibilityReasons = reasons
		entry, hasEntry := entryBySymbol[row.Symbol]
		if !ok && !(hasEntry && inplay.EarlyShortAdmission(entry, earlyShortMin)) {
			continue
		}
		if hasEntry && entry.ShortDemotionFlag {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(row.Grade), "N/A") {
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
		grade := grades[row.Symbol]
		fmt.Printf("%s | %s\n", market.FormatRow(row), market.ColorGrade(grade))
	}
	if len(rows) > limit {
		fmt.Printf("+%d more\n", len(rows)-limit)
	}
}

func printInPlay(side string, entries []inplay.Entry) {
	fmt.Printf("🔥 IN-PLAY (%s)\n", side)
	n := 0
	for _, e := range entries {
		if n >= 8 {
			break
		}
		fmt.Printf("%-12s grade=%-2s score=%6.2f slope=%6.3f state=%-8s dd=%6.1f up=%6.1f bear=%4.1f bull=%4.1f style=%s\n",
			market.DisplaySymbol(e.Symbol), e.CurrentGrade, e.CurrentScore, e.ScoreSlope, e.State, e.DrawdownFromPeakPct, e.DrawupFromTroughPct, e.BearReversalScore, e.BullReversalScore, e.EntryStyle)
		n++
	}
	if n == 0 {
		fmt.Println("(none)")
	}
}

func filterDisplayInPlay(entries []inplay.Entry) []inplay.Entry {
	minAbsSlope := envFloat("INPLAY_DISPLAY_MIN_ABS_SLOPE", 0.05)
	out := make([]inplay.Entry, 0, len(entries))
	for _, e := range entries {
		switch e.State {
		case inplay.StateHeating, inplay.StateInPlay, inplay.StatePumping, inplay.StateCooling, inplay.StateDumping, inplay.StateExhausted:
		default:
			continue
		}
		if e.Momentum || math.Abs(e.ScoreSlope) >= minAbsSlope {
			out = append(out, e)
		}
	}
	return out
}

func initialGradeMap(rows []market.Scored) map[string]string {
	out := make(map[string]string, len(rows))
	for _, row := range rows {
		out[row.Symbol] = row.Grade
	}
	return out
}

func entryMap(entries []inplay.Entry) map[string]inplay.Entry {
	out := make(map[string]inplay.Entry, len(entries))
	for _, e := range entries {
		out[e.Symbol] = e
	}
	return out
}

func gradeLabelForRanked(r market.RankedMarket, side string) string {
	lbl := strings.TrimSpace(r.ConfluenceLabel)
	if lbl != "" && lbl != "_" && !strings.EqualFold(lbl, "C") {
		return lbl
	}
	return market.FallbackGradeDirectionalView(r.Score, r.DayUTCChangePct, r.ChangePct, side)
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
		port = "8080"
	}
	if err := http.ListenAndServe(":"+port, nil); err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "address already in use") {
			fmt.Printf("status server disabled: port %s already in use\n", port)
			return
		}
		fmt.Println("http server error:", err)
	}
}

func ptr(v float64) *float64 { return &v }

func round2(x float64) float64 { return math.Round(x*100) / 100 }

func envStr(k, def string) string {
	v := strings.TrimSpace(os.Getenv(k))
	if v == "" {
		return def
	}
	return v
}

func envFloat(k string, def float64) float64 {
	v := strings.TrimSpace(os.Getenv(k))
	if v == "" {
		return def
	}
	f, err := strconv.ParseFloat(v, 64)
	if err != nil {
		return def
	}
	return f
}

func envInt(k string, def int) int {
	v := strings.TrimSpace(os.Getenv(k))
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return def
	}
	return n
}

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
