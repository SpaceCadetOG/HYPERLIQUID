package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"math"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"hyperliquid/adapters/hyperliquid"
	"hyperliquid/internal/data"
	"hyperliquid/internal/execution"
	"hyperliquid/internal/features"
	"hyperliquid/internal/gate"
	"hyperliquid/internal/inplay"
	"hyperliquid/internal/market"
	"hyperliquid/internal/notify"
	"hyperliquid/internal/risk"
	"hyperliquid/internal/sessions"
	"hyperliquid/internal/status"
	"hyperliquid/internal/strategies"
)

func main() {
	baseURL := flag.String("base-url", hyperliquid.MainnetURL, "Hyperliquid API base URL")
	user := flag.String("user", "", "Hyperliquid user address for live account snapshot")
	mode := flag.String("mode", "paper", "execution mode: paper|live")
	minScore := flag.Float64("min-score", 45, "minimum score to enter paper positions")
	marginUSD := flag.Float64("margin-usd", 100, "max paper margin per trade")
	leverage := flag.Float64("leverage", 5, "paper leverage")
	riskPct := flag.Float64("risk-pct", 3.6, "risk percent per trade")
	tf := flag.String("tf", "5m", "candle timeframe")
	candlesN := flag.Int("candles", 72, "number of candles to load for profile and AVWAP")
	poll := flag.Duration("poll", 15*time.Second, "market polling interval")
	topN := flag.Int("top", 3, "number of candidates to print per side")
	clear := flag.Bool("clear", false, "clear terminal each cycle")
	staleExitAfter := flag.Int("stale-exit-after", 3, "force-exit paper positions after N stale management cycles")
	liveStatePath := flag.String("live-state", "out/live_state.json", "path to persisted live TP/SL state")
	httpPort := flag.String("http-port", "8090", "live-lite status/control port")
	flag.Parse()

	client := hyperliquid.NewWithUser(*baseURL, *user)
	paper := execution.NewPaper(1000)
	logger := notify.Logger{}
	tg := notify.NewTelegramFromEnv()
	defer tg.Stop()
	liveState := execution.NewLiveStateStore(*liveStatePath)
	ctrl := newRuntimeControl()
	longTracker := inplay.NewTracker("long", inplay.Config{MinGrade: "C", MinVolumeUSD: 1_000_000})
	shortTracker := inplay.NewTracker("short", inplay.Config{MinGrade: "C", MinVolumeUSD: 1_000_000})
	gateCfg := gate.DefaultConfig()
	gateCfg.RequireMTF = false
	riskCfg := risk.DefaultConfig()
	riskCfg.MarginUSD = *marginUSD
	riskCfg.Leverage = *leverage
	riskCfg.RiskPct = *riskPct
	if tg.Enabled() {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		go tg.Listen(ctx, func(chatID string, text string) string {
			return handleTelegram(text, ctrl, paper, client, strings.ToLower(strings.TrimSpace(*mode)))
		})
	}
	if strings.TrimSpace(*httpPort) != "" {
		go serveLiveHTTP(*httpPort, ctrl, paper, client, liveState, func() string {
			return strings.ToLower(strings.TrimSpace(*mode))
		})
	}

	for {
		markets, err := client.FetchAllMarkets()
		if err != nil {
			logger.Infof("market fetch failed: %v", err)
			time.Sleep(*poll)
			continue
		}
		snapshots := make([]features.Snapshot, 0, len(markets))
		candleCache := make(map[string][]market.Candle, len(markets))
		bookCache := make(map[string]market.OrderBook, len(markets))
		marketBySymbol := make(map[string]market.Market, len(markets))
		for _, mkt := range markets {
			marketBySymbol[mkt.Symbol] = mkt
			candles, err := client.LoadCandles(mkt.Symbol, *tf, *candlesN)
			if err != nil || len(candles) == 0 {
				continue
			}
			candleCache[mkt.Symbol] = candles
			book, err := client.FetchOrderBook(mkt.Symbol, 10)
			if err != nil {
				continue
			}
			bookCache[mkt.Symbol] = book
			snapshots = append(snapshots, features.BuildSnapshot(mkt, candles, book))
		}
		longs, shorts := strategies.Rank(snapshots, time.Now().UTC())
		longs = gate.Filter(longs, gate.Config{MinScore: *minScore})
		shorts = gate.Filter(shorts, gate.Config{MinScore: *minScore})
		snapshotMap := make(map[string]features.Snapshot, len(snapshots))
		for _, snap := range snapshots {
			snapshotMap[snap.Symbol] = snap
		}
		nowUTC := time.Now().UTC()
		longRows := toScoredRows(markets, longs, "long")
		shortRows := toScoredRows(markets, shorts, "short")
		longRows, longGrades := buildEligible(longRows, "long")
		shortRows, shortGrades := buildEligible(shortRows, "short")
		longs = restrictRankedToRows(longs, longRows)
		shorts = restrictRankedToRows(shorts, shortRows)
		longTracker.Update(nowUTC, longRows, longGrades)
		shortTracker.Update(nowUTC, shortRows, shortGrades)
		longInPlay := longTracker.Entries()
		shortInPlay := shortTracker.Entries()
		ctrl.UpdateScans(longRows, shortRows, longGrades, shortGrades, longInPlay, shortInPlay, nowUTC, sessionTag(nowUTC))
		longEntryMap := inPlayMap(longInPlay)
		shortEntryMap := inPlayMap(shortInPlay)
		modeName := strings.ToLower(strings.TrimSpace(*mode))
		priceMap := make(map[string]float64, len(markets))
		for _, mkt := range markets {
			priceMap[mkt.Symbol] = mkt.MarkPrice
		}
		if modeName != "live" {
			paper.MarkPrices(priceMap)
		}
		balance, positions, receipts := venueState(modeName, paper, client)
		if modeName != "live" {
			manageInputs, manageWarnings := buildManageInputsFromPositions(client, positions, snapshotMap, marketBySymbol, candleCache, bookCache, *tf, *candlesN, *staleExitAfter)
			for _, warning := range manageWarnings {
				logger.Infof("manage warning: %s", warning)
			}
			receipts = paper.Manage(manageInputs)
		} else {
			for _, warning := range reconcileLiveOrders(client, liveState, positions) {
				logger.Infof("live reconcile warning: %s", warning)
			}
		}
		open := map[string]bool{}
		for _, pos := range positions {
			open[pos.Symbol] = true
		}
		signal := "none"
		for _, receipt := range receipts {
			if receipt.Qty <= 0 {
				continue
			}
			logger.Infof("%s exit %s %s qty=%.4f fill=%.4f realized=%+.2f realized_pct=%+.2f reason=%s",
				modeName,
				receipt.Symbol, receipt.Side, receipt.Qty, receipt.FillPrice, receipt.Realized, receipt.RealizedPct, receipt.Reason)
			if tg.Enabled() {
				tg.SendTrade(notify.TradeEvent{
					Symbol: receipt.Symbol,
					Side:   string(receipt.Side),
					Price:  receipt.FillPrice,
					Reason: receipt.Reason,
				}, false)
			}
		}
		applyManualCloses(ctrl, paper, client, positions, tg, modeName)
		longRanked := selectCandidate(longs, snapshotMap, longEntryMap, longGrades, gateCfg, riskCfg, balance.Equity, open)
		if !ctrl.IsPaused() && longRanked != nil {
			size := risk.SizeForRisk(*longRanked, balance.Equity, riskCfg)
			if size > 0 {
				req := market.OrderRequest{
					Symbol:      longRanked.Symbol,
					Side:        longRanked.Side,
					Size:        size,
					Price:       longRanked.Last,
					StopPrice:   longRanked.StopPrice,
					TargetPrice: longRanked.TargetPrice,
					Leverage:    riskCfg.Leverage,
					Type:        "market",
				}
				orderResult, placeErr := placeOrder(modeName, paper, client, req)
				if placeErr != nil {
					logger.Infof("%s entry rejected %s %s: %v", modeName, longRanked.Symbol, longRanked.Side, placeErr)
					goto shortSide
				}
				if modeName == "live" {
					_ = liveState.UpsertEntry(req, orderResult)
				}
				signal = fmt.Sprintf("%s %s %.1f", longRanked.Symbol, longRanked.Side, longRanked.Score)
				logger.Infof("%s entry %s %s size=%.4f score=%.1f adx=%.1f whale_bps=%.2f stop=%.4f target=%.4f reason=%s",
					modeName, longRanked.Symbol, longRanked.Side, size, longRanked.Score, longRanked.ADX, longRanked.WhaleDistanceBps, longRanked.StopPrice, longRanked.TargetPrice, longRanked.Reason)
				if tg.Enabled() {
					tg.SendTrade(notify.TradeEvent{
						Symbol:     longRanked.Symbol,
						Side:       string(longRanked.Side),
						Price:      longRanked.Last,
						Confluence: scoreToConfluence(longRanked.Score),
						Setup:      longRanked.RegimeTag,
						Reason:     longRanked.Reason,
					}, true)
				}
				open[longRanked.Symbol] = true
			}
		}
	shortSide:
		shortRanked := selectCandidate(shorts, snapshotMap, shortEntryMap, shortGrades, gateCfg, riskCfg, balance.Equity, open)
		if !ctrl.IsPaused() && shortRanked != nil {
			size := risk.SizeForRisk(*shortRanked, balance.Equity, riskCfg)
			if size > 0 {
				req := market.OrderRequest{
					Symbol:      shortRanked.Symbol,
					Side:        shortRanked.Side,
					Size:        size,
					Price:       shortRanked.Last,
					StopPrice:   shortRanked.StopPrice,
					TargetPrice: shortRanked.TargetPrice,
					Leverage:    riskCfg.Leverage,
					Type:        "market",
				}
				orderResult, placeErr := placeOrder(modeName, paper, client, req)
				if placeErr != nil {
					logger.Infof("%s entry rejected %s %s: %v", modeName, shortRanked.Symbol, shortRanked.Side, placeErr)
					goto render
				}
				if modeName == "live" {
					_ = liveState.UpsertEntry(req, orderResult)
				}
				if signal == "none" {
					signal = fmt.Sprintf("%s %s %.1f", shortRanked.Symbol, shortRanked.Side, shortRanked.Score)
				}
				logger.Infof("%s entry %s %s size=%.4f score=%.1f adx=%.1f whale_bps=%.2f stop=%.4f target=%.4f reason=%s",
					modeName, shortRanked.Symbol, shortRanked.Side, size, shortRanked.Score, shortRanked.ADX, shortRanked.WhaleDistanceBps, shortRanked.StopPrice, shortRanked.TargetPrice, shortRanked.Reason)
				if tg.Enabled() {
					tg.SendTrade(notify.TradeEvent{
						Symbol:     shortRanked.Symbol,
						Side:       string(shortRanked.Side),
						Price:      shortRanked.Last,
						Confluence: scoreToConfluence(shortRanked.Score),
						Setup:      shortRanked.RegimeTag,
						Reason:     shortRanked.Reason,
					}, true)
				}
			}
		}
	render:
		ctrl.UpdateSignal(signal)
		renderCycle(*clear, longRows, shortRows, longInPlay, shortInPlay, longGrades, shortGrades, client, paper, signal, *topN, modeName)
		time.Sleep(*poll)
	}
}

func renderCycle(clear bool, longRows, shortRows []market.Scored, longInPlay, shortInPlay []inplay.Entry, longGrades, shortGrades map[string]string, client *hyperliquid.Client, paper *execution.Paper, signal string, topN int, mode string) {
	if clear {
		fmt.Print("\033[H\033[2J")
	}
	now := time.Now().In(chicago())
	nowUTC := now.UTC()
	fmt.Printf("🔧 HYPERLIQUID LONG adapter - live fetch @ %s\n", nowUTC.Format(time.RFC3339))
	fmt.Println(market.FormatHeader("hyperliquid (LONGS)", sessions.ActiveSessionLabels(nowUTC)))
	fmt.Println("Symbol       | Score  | Δ%(24h) | DayUTC% | Vol($)  | OI($)   | Funding(%) | Open24h  | Last     | G")
	fmt.Println("------------------------------------------------------------------------------------------------------------")
	printScoredTable(longRows, longGrades, topN)
	printInPlay("LONG", prioritizeInPlay(longInPlay, longRows, topN), topN)
	fmt.Println()
	fmt.Printf("🔧 HYPERLIQUID SHORT adapter - live fetch @ %s\n", nowUTC.Format(time.RFC3339))
	fmt.Println(market.FormatHeader("hyperliquid (SHORTS)", sessions.ActiveSessionLabels(nowUTC)))
	fmt.Println("Symbol       | Score  | Δ%(24h) | DayUTC% | Vol($)  | OI($)   | Funding(%) | Open24h  | Last     | G")
	fmt.Println("------------------------------------------------------------------------------------------------------------")
	printScoredTable(shortRows, shortGrades, topN)
	printInPlay("SHORT", prioritizeInPlay(shortInPlay, shortRows, topN), topN)
	if acct, err := client.Balance(); err == nil {
		if mode == "live" {
			pos, _ := client.Positions()
			fmt.Printf("LIVE    eq=%.2f avail=%.2f open=%d\n", acct.Equity, acct.AvailableUSD, len(pos))
		} else {
			fmt.Printf("ACCOUNT avail=%.2f eq=%.2f\n", acct.AvailableUSD, acct.Equity)
		}
	} else {
		fmt.Printf("ACCOUNT live=unconfigured\n")
	}
	if mode == "live" {
		fmt.Printf("PAPER   disabled\n")
	} else {
		ps := paper.Snapshot()
		fmt.Printf("PAPER   eq=%.2f bal=%.2f avail=%.2f res=%.2f pnl=%+.2f day=%+.2f open=%d\n", ps.Equity, ps.Balance, ps.Available, ps.Reserved, ps.OpenPnL, ps.DayRealized, ps.OpenCount)
	}
	fmt.Printf("signal: %s\n\n", signal)
}

func printScoredTable(rows []market.Scored, grades map[string]string, topN int) {
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
	fmt.Printf("🔥 IN-PLAY (%s)\n", strings.ToUpper(side))
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
		fmt.Printf("%-12s grade=%s score=%5.2f slope=%6.3f state=%-10s momentum=%-5v age=%4.0fm\n",
			market.DisplaySymbol(e.Symbol), market.ColorGrade(e.CurrentGrade), e.CurrentScore, e.ScoreSlope, e.State, e.Momentum, e.AgeMinutes)
	}
}

func prioritizeInPlay(entries []inplay.Entry, rows []market.Scored, topN int) []inplay.Entry {
	if len(entries) == 0 || len(rows) == 0 || topN <= 0 {
		return entries
	}
	visibleOrder := make(map[string]int, len(rows))
	for i, row := range rows {
		visibleOrder[row.Symbol] = i
	}
	visible := make([]inplay.Entry, 0, topN)
	remainder := make([]inplay.Entry, 0, len(entries))
	seen := make(map[string]struct{}, len(entries))
	for _, entry := range entries {
		if _, ok := seen[entry.Symbol]; ok {
			continue
		}
		seen[entry.Symbol] = struct{}{}
		if _, ok := visibleOrder[entry.Symbol]; ok {
			visible = append(visible, entry)
			continue
		}
		remainder = append(remainder, entry)
	}
	if len(visible) > 1 {
		sort.SliceStable(visible, func(i, j int) bool {
			return visibleOrder[visible[i].Symbol] < visibleOrder[visible[j].Symbol]
		})
	}
	out := make([]inplay.Entry, 0, minInt(topN, len(entries)))
	for _, entry := range visible {
		if len(out) >= topN {
			return out
		}
		out = append(out, entry)
	}
	for _, entry := range remainder {
		if len(out) >= topN {
			break
		}
		out = append(out, entry)
	}
	return out
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func chicago() *time.Location {
	loc, err := time.LoadLocation("America/Chicago")
	if err != nil {
		return time.FixedZone("CST", -6*3600)
	}
	return loc
}

func sessionTag(ts time.Time) string {
	return string(data.CurrentRegimeCT(ts))
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
		change := r.ChangePct
		out = append(out, market.Scored{
			Symbol:            r.Symbol,
			Change24h:         change,
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

func gradeLabelForRanked(r market.RankedMarket, side string) string {
	lbl := strings.TrimSpace(r.ConfluenceLabel)
	if lbl != "" && lbl != "_" && !strings.EqualFold(lbl, "C") {
		return lbl
	}
	return market.FallbackGradeDirectional(r.Score, r.ChangePct, side)
}

func gradeMap(rows []market.Scored) map[string]string {
	out := make(map[string]string, len(rows))
	for _, r := range rows {
		out[r.Symbol] = r.Grade
	}
	return out
}

func open24(last float64, changePct float64) float64 {
	if last <= 0 {
		return 0
	}
	return last / (1 + (changePct / 100))
}

func ptr(v float64) *float64 {
	return &v
}

func buildEligible(rows []market.Scored, side string) ([]market.Scored, map[string]string) {
	out := make([]market.Scored, 0, len(rows))
	grades := make(map[string]string, len(rows))
	for _, row := range rows {
		ok := false
		reasons := []string{}
		if side == "short" {
			ok, reasons = market.EligibleShort(row)
		} else {
			ok, reasons = market.EligibleLong(row)
		}
		row.Eligible = ok
		row.EligibilityReasons = reasons
		if !ok {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(row.Grade), "N/A") {
			continue
		}
		row.EntryReadyHint = gradeHint(row.Grade) >= gradeHint("C")
		out = append(out, row)
		grades[row.Symbol] = row.Grade
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Score > out[j].Score })
	return out, grades
}

func restrictRankedToRows(items []market.RankedMarket, rows []market.Scored) []market.RankedMarket {
	keep := make(map[string]struct{}, len(rows))
	for _, row := range rows {
		keep[row.Symbol] = struct{}{}
	}
	out := make([]market.RankedMarket, 0, len(items))
	for _, item := range items {
		if _, ok := keep[item.Symbol]; ok {
			out = append(out, item)
		}
	}
	return out
}

func inPlayMap(entries []inplay.Entry) map[string]inplay.Entry {
	out := make(map[string]inplay.Entry, len(entries))
	for _, entry := range entries {
		out[entry.Symbol] = entry
	}
	return out
}

func buildManageInputs(longs, shorts []market.RankedMarket, snaps map[string]features.Snapshot) map[string]execution.ManageInput {
	out := make(map[string]execution.ManageInput, len(longs)+len(shorts))
	for _, item := range longs {
		snap, ok := snaps[item.Symbol]
		if !ok {
			continue
		}
		out[item.Symbol] = execution.ManageInput{
			Symbol:       item.Symbol,
			MarkPrice:    item.Last,
			WeakFlow:     snap.BookSkew < 0 || snap.VolumeRatio < 1.0,
			NearFriction: nearTarget(item.Last, item.TargetPrice),
			LiqSpike:     item.SpreadBps > 10,
		}
	}
	for _, item := range shorts {
		snap, ok := snaps[item.Symbol]
		if !ok {
			continue
		}
		out[item.Symbol] = execution.ManageInput{
			Symbol:       item.Symbol,
			MarkPrice:    item.Last,
			WeakFlow:     snap.BookSkew > 0 || snap.VolumeRatio < 1.0,
			NearFriction: nearTarget(item.Last, item.TargetPrice),
			LiqSpike:     item.SpreadBps > 10,
		}
	}
	return out
}

func buildManageInputsFromPositions(client *hyperliquid.Client, positions []market.Position, snapshots map[string]features.Snapshot, markets map[string]market.Market, candles map[string][]market.Candle, books map[string]market.OrderBook, tf string, candleN int, staleExitAfter int) (map[string]execution.ManageInput, []string) {
	inputs := make(map[string]execution.ManageInput, len(positions))
	warnings := make([]string, 0)
	for _, pos := range positions {
		snap, ok := snapshots[pos.Symbol]
		if !ok {
			refreshed, warning := refreshSnapshotForSymbol(client, pos.Symbol, markets, candles, books, tf, candleN)
			if warning != "" {
				warnings = append(warnings, warning)
			}
			if refreshed.Symbol != "" {
				snap = refreshed
				ok = true
			}
		}
		if !ok {
			inputs[pos.Symbol] = execution.ManageInput{
				Symbol:     pos.Symbol,
				MarkPrice:  pos.MarkPrice,
				Stale:      true,
				Warning:    "stale management snapshot; preserving existing stop/target",
				ForceExit:  staleExitAfter > 0 && pos.BarsHeld >= staleExitAfter,
				ExitReason: "STALE_DATA_EXIT",
			}
			continue
		}
		inputs[pos.Symbol] = execution.ManageInput{
			Symbol:       pos.Symbol,
			MarkPrice:    snap.Last,
			WeakFlow:     weakFlowForPosition(pos.Side, snap),
			NearFriction: nearFrictionForPosition(pos, snap),
			LiqSpike:     snap.Signals.WideSpread,
		}
	}
	return inputs, warnings
}

func refreshSnapshotForSymbol(client *hyperliquid.Client, symbol string, markets map[string]market.Market, candles map[string][]market.Candle, books map[string]market.OrderBook, tf string, candleN int) (features.Snapshot, string) {
	mkt, ok := markets[symbol]
	if !ok {
		return features.Snapshot{}, fmt.Sprintf("open position %s missing from market universe; stale management snapshot", symbol)
	}
	c, ok := candles[symbol]
	if !ok || len(c) == 0 {
		var err error
		c, err = client.LoadCandles(symbol, tf, candleN)
		if err != nil || len(c) == 0 {
			return features.Snapshot{}, fmt.Sprintf("open position %s missing candles for management: %v", symbol, err)
		}
	}
	b, ok := books[symbol]
	if !ok || (len(b.Bids) == 0 && len(b.Asks) == 0) {
		var err error
		b, err = client.FetchOrderBook(symbol, 10)
		if err != nil {
			return features.Snapshot{}, fmt.Sprintf("open position %s missing order book for management: %v", symbol, err)
		}
	}
	return features.BuildSnapshot(mkt, c, b), ""
}

func weakFlowForPosition(side market.Side, snap features.Snapshot) bool {
	if side == market.SideLong {
		return snap.Signals.AskPressure || snap.VolumeRatio < 1.0
	}
	return snap.Signals.BidPressure || snap.VolumeRatio < 1.0
}

func nearFrictionForPosition(pos market.Position, snap features.Snapshot) bool {
	target := pos.TargetPrice
	if target <= 0 {
		if pos.Side == market.SideLong {
			target = snap.ValueHigh
		} else {
			target = snap.ValueLow
		}
	}
	return nearTarget(snap.Last, target)
}

func gradeHint(grade string) int {
	switch grade {
	case "A+", "A":
		return 3
	case "B":
		return 2
	case "C":
		return 1
	default:
		return 0
	}
}

func selectCandidate(candidates []market.RankedMarket, snaps map[string]features.Snapshot, entries map[string]inplay.Entry, grades map[string]string, gateCfg gate.Config, riskCfg risk.Config, equity float64, open map[string]bool) *market.RankedMarket {
	for _, item := range candidates {
		entry := entries[item.Symbol]
		if passesTradeChecks(item, snaps[item.Symbol], entry, grades[item.Symbol], gateCfg, riskCfg, equity, open) {
			cp := item
			return &cp
		}
	}
	return nil
}

func passesTradeChecks(item market.RankedMarket, snap features.Snapshot, entry inplay.Entry, grade string, gateCfg gate.Config, riskCfg risk.Config, equity float64, open map[string]bool) bool {
	if open[item.Symbol] {
		return false
	}
	if grade == "" {
		grade = market.FallbackGradeDirectional(item.Score, item.ChangePct, strings.ToLower(string(item.Side)))
	}
	dec := gate.Evaluate(gate.Input{
		Symbol:      item.Symbol,
		Side:        string(item.Side),
		Grade:       grade,
		Score:       item.Score,
		Slope:       entry.ScoreSlope,
		VolumeRatio: snap.VolumeRatio,
		RegimeATR:   item.ATRPct,
	}, gateCfg)
	if !dec.Allow {
		return false
	}
	size := risk.SizeForRisk(item, equity, riskCfg)
	if size <= 0 {
		return false
	}
	riskDec := risk.Approve(riskCfg, risk.Input{
		Side:              string(item.Side),
		Entry:             item.Last,
		Stop:              item.StopPrice,
		Leverage:          riskCfg.Leverage,
		NotionalUSD:       size * item.Last,
		FundingRate:       item.Funding,
		HoldHours:         8,
		SpreadBps:         item.SpreadBps,
		BookImbalance:     bookImbalance(item),
		RecentSlippageBps: item.SpreadBps * 0.5,
		VenueHealthy:      true,
	})
	return riskDec.Approved
}

func bookImbalance(item market.RankedMarket) float64 {
	if item.BookSkew == 0 {
		return 1
	}
	if item.Side == market.SideLong {
		return 1 + math.Max(0, item.BookSkew)
	}
	return 1 + math.Max(0, -item.BookSkew)
}

func nearTarget(mark, target float64) bool {
	if mark <= 0 || target <= 0 {
		return false
	}
	return math.Abs((target-mark)/mark) <= 0.0025
}

type runtimeControl struct {
	mu          sync.RWMutex
	paused      bool
	closeAll    bool
	closeSymbol map[string]struct{}
	longRows    []market.Scored
	shortRows   []market.Scored
	longGrades  map[string]string
	shortGrades map[string]string
	longInPlay  []inplay.Entry
	shortInPlay []inplay.Entry
	lastSignal  string
	lastScanAt  time.Time
	session     string
}

func newRuntimeControl() *runtimeControl {
	return &runtimeControl{closeSymbol: make(map[string]struct{})}
}

func (r *runtimeControl) UpdateScans(longRows, shortRows []market.Scored, longGrades, shortGrades map[string]string, longInPlay, shortInPlay []inplay.Entry, at time.Time, session string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.longRows = append([]market.Scored(nil), longRows...)
	r.shortRows = append([]market.Scored(nil), shortRows...)
	r.longGrades = cloneMap(longGrades)
	r.shortGrades = cloneMap(shortGrades)
	r.longInPlay = append([]inplay.Entry(nil), longInPlay...)
	r.shortInPlay = append([]inplay.Entry(nil), shortInPlay...)
	r.lastScanAt = at
	r.session = session
}

func (r *runtimeControl) UpdateSignal(signal string) {
	r.mu.Lock()
	r.lastSignal = signal
	r.mu.Unlock()
}

func (r *runtimeControl) IsPaused() bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.paused
}

func (r *runtimeControl) SetPaused(v bool) {
	r.mu.Lock()
	r.paused = v
	r.mu.Unlock()
}

func (r *runtimeControl) RequestCloseAll() {
	r.mu.Lock()
	r.closeAll = true
	r.mu.Unlock()
}

func (r *runtimeControl) RequestCloseSymbol(symbol string) {
	r.mu.Lock()
	r.closeSymbol[strings.ToUpper(strings.TrimSpace(symbol))] = struct{}{}
	r.mu.Unlock()
}

func (r *runtimeControl) ConsumeCloseRequests() (bool, []string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	all := r.closeAll
	r.closeAll = false
	symbols := make([]string, 0, len(r.closeSymbol))
	for sym := range r.closeSymbol {
		symbols = append(symbols, sym)
	}
	r.closeSymbol = make(map[string]struct{})
	return all, symbols
}

type controlSnapshot struct {
	Paused      bool
	LongRows    []market.Scored
	ShortRows   []market.Scored
	LongGrades  map[string]string
	ShortGrades map[string]string
	LongInPlay  []inplay.Entry
	ShortInPlay []inplay.Entry
	LastSignal  string
	LastScanAt  time.Time
	Session     string
}

func (r *runtimeControl) Snapshot() controlSnapshot {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return controlSnapshot{
		Paused:      r.paused,
		LongRows:    append([]market.Scored(nil), r.longRows...),
		ShortRows:   append([]market.Scored(nil), r.shortRows...),
		LongGrades:  cloneMap(r.longGrades),
		ShortGrades: cloneMap(r.shortGrades),
		LongInPlay:  append([]inplay.Entry(nil), r.longInPlay...),
		ShortInPlay: append([]inplay.Entry(nil), r.shortInPlay...),
		LastSignal:  r.lastSignal,
		LastScanAt:  r.lastScanAt,
		Session:     r.session,
	}
}

func cloneMap(in map[string]string) map[string]string {
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func placeOrder(mode string, paper *execution.Paper, client *hyperliquid.Client, req market.OrderRequest) (market.OrderResult, error) {
	if mode == "live" {
		return client.PlaceOrder(req)
	}
	return paper.PlaceOrder(req)
}

func applyManualCloses(ctrl *runtimeControl, paper *execution.Paper, client *hyperliquid.Client, positions []market.Position, tg *notify.Telegram, mode string) {
	closeAll, symbols := ctrl.ConsumeCloseRequests()
	targets := map[string]market.Position{}
	for _, pos := range positions {
		key := strings.ToUpper(strings.TrimSpace(pos.Symbol))
		if closeAll {
			targets[key] = pos
			continue
		}
		for _, sym := range symbols {
			if key == sym {
				targets[key] = pos
				break
			}
		}
	}
	for _, pos := range targets {
		price := pos.MarkPrice
		if price <= 0 {
			price = pos.EntryPrice
		}
		if mode == "live" {
			openOrders, _ := client.OpenOrders(pos.Symbol)
			for _, order := range openOrders {
				_ = client.CancelOrder(order.Symbol, order.ID)
			}
			exitSide := market.SideShort
			if pos.Side == market.SideShort {
				exitSide = market.SideLong
			}
			_, _ = client.PlaceOrder(market.OrderRequest{
				Symbol:     pos.Symbol,
				Side:       exitSide,
				Size:       pos.Size,
				Price:      price,
				ReduceOnly: true,
				Type:       "market",
			})
		} else {
			_, _ = paper.PlaceOrder(market.OrderRequest{
				Symbol:     pos.Symbol,
				Side:       pos.Side,
				Size:       pos.Size,
				Price:      price,
				ReduceOnly: true,
				Type:       "market",
			})
		}
		if tg.Enabled() {
			tg.Sendf("%s", notify.BuildEventHTML("🧹", "MANUAL CLOSE", fmt.Sprintf("%s closed at %.4f", pos.Symbol, price)))
		}
	}
}

func scoreToConfluence(score float64) float64 {
	if score <= 0 {
		return 0
	}
	if score >= 100 {
		return 1
	}
	return score / 100
}

func handleTelegram(text string, ctrl *runtimeControl, paper *execution.Paper, client *hyperliquid.Client, mode string) string {
	cmd := strings.TrimSpace(text)
	if cmd == "" {
		return ""
	}
	parts := strings.Fields(cmd)
	head := strings.ToLower(parts[0])
	switch head {
	case "/help":
		return notify.BuildEventHTML("📘", "COMMANDS", "/status", "/balance", "/positions", "/pause", "/resume", "/close SYMBOL", "/closeall")
	case "/pause":
		ctrl.SetPaused(true)
		return notify.BuildEventHTML("⏸️", "PAUSED", "New entries paused. Risk management remains active.")
	case "/resume":
		ctrl.SetPaused(false)
		return notify.BuildEventHTML("▶️", "RESUMED", "New entries resumed.")
	case "/closeall":
		ctrl.RequestCloseAll()
		return notify.BuildEventHTML("🧹", "CLOSE ALL REQUESTED", "Open positions will be flattened on the next cycle.")
	case "/close":
		if len(parts) < 2 {
			return notify.BuildEventHTML("⚠️", "MISSING SYMBOL", "Usage: /close SYMBOL")
		}
		ctrl.RequestCloseSymbol(parts[1])
		return notify.BuildEventHTML("🧹", "CLOSE REQUESTED", fmt.Sprintf("%s queued for manual close.", strings.ToUpper(parts[1])))
	case "/balance":
		return buildBalanceMessage(ctrl, paper, client, mode)
	case "/positions":
		return buildPositionsMessage(paper, client, mode)
	case "/status":
		return buildStatusMessage(ctrl, paper, client, mode)
	default:
		return notify.BuildEventHTML("ℹ️", "UNKNOWN COMMAND", "Use /help")
	}
}

func buildBalanceMessage(ctrl *runtimeControl, paper *execution.Paper, client *hyperliquid.Client, mode string) string {
	ps := paper.Snapshot()
	eq := ps.Equity
	bal := ps.Balance
	if mode == "live" {
		if acct, err := client.Balance(); err == nil {
			eq = acct.Equity
			bal = acct.AvailableUSD + acct.ReservedUSD
		}
	}
	snap := ctrl.Snapshot()
	netDay := ps.DayRealized + ps.OpenPnL
	netDayPct := 0.0
	if bal > 0 {
		netDayPct = (netDay / bal) * 100
	}
	return notify.BuildSessionPulseHTML(notify.PulseSnapshot{
		Title:     strings.ToUpper(mode),
		TimeLabel: snap.LastScanAt.In(chicago()).Format("15:04:05 CT"),
		Session:   snap.Session,
		Balance:   bal,
		Equity:    eq,
		Realized:  ps.DayRealized,
		OpenPnL:   ps.OpenPnL,
		NetDay:    netDay,
		OpenCount: ps.OpenCount,
		OpenCap:   5,
		NetDayPct: netDayPct,
	})
}

func buildPositionsMessage(paper *execution.Paper, client *hyperliquid.Client, mode string) string {
	if mode == "live" {
		positions, err := client.Positions()
		if err != nil {
			return notify.BuildEventHTML("⚠️", "LIVE POSITIONS", err.Error())
		}
		if len(positions) == 0 {
			return notify.BuildEventHTML("📦", "POSITIONS", "No open positions.")
		}
		cards := make([]string, 0, len(positions))
		for _, pos := range positions {
			pct := 0.0
			if pos.EntryPrice > 0 {
				if pos.Side == market.SideLong {
					pct = ((pos.MarkPrice - pos.EntryPrice) / pos.EntryPrice) * 100
				} else {
					pct = ((pos.EntryPrice - pos.MarkPrice) / pos.EntryPrice) * 100
				}
			}
			cards = append(cards, notify.BuildPositionCard(notify.PositionCard{
				Symbol:           market.DisplaySymbol(pos.Symbol),
				Side:             string(pos.Side),
				Qty:              pos.Size,
				EntryPrice:       pos.EntryPrice,
				MarkPrice:        pos.MarkPrice,
				LastPrice:        pos.MarkPrice,
				UnrealizedPnL:    pos.Unrealized,
				UnrealizedPnLPct: pct,
				Leverage:         int(pos.Leverage),
				Setup:            "hyperliquid-live",
				Confluence:       0,
				AgeMin:           pos.BarsHeld * 5,
				StopLoss:         pos.StopPrice,
				TakeProfit:       pos.TargetPrice,
			}))
		}
		return strings.Join(cards, "\n\n")
	}
	positions, _ := paper.Positions()
	if len(positions) == 0 {
		return notify.BuildEventHTML("📦", "POSITIONS", "No open positions.")
	}
	cards := make([]string, 0, len(positions))
	for _, pos := range positions {
		pct := 0.0
		if pos.EntryPrice > 0 {
			if pos.Side == market.SideLong {
				pct = ((pos.MarkPrice - pos.EntryPrice) / pos.EntryPrice) * 100
			} else {
				pct = ((pos.EntryPrice - pos.MarkPrice) / pos.EntryPrice) * 100
			}
		}
		cards = append(cards, notify.BuildPositionCard(notify.PositionCard{
			Symbol:           market.DisplaySymbol(pos.Symbol),
			Side:             string(pos.Side),
			Qty:              pos.Size,
			EntryPrice:       pos.EntryPrice,
			MarkPrice:        pos.MarkPrice,
			LastPrice:        pos.MarkPrice,
			UnrealizedPnL:    pos.Unrealized,
			UnrealizedPnLPct: pct,
			Leverage:         int(pos.Leverage),
			Setup:            "hyperliquid",
			Confluence:       0,
			AgeMin:           pos.BarsHeld * 5,
			StopLoss:         pos.StopPrice,
			TakeProfit:       pos.TargetPrice,
		}))
	}
	return strings.Join(cards, "\n\n")
}

func venueState(mode string, paper *execution.Paper, client *hyperliquid.Client) (market.AccountSnapshot, []market.Position, []execution.Receipt) {
	if mode == "live" {
		bal, _ := client.Balance()
		pos, _ := client.Positions()
		return bal, pos, nil
	}
	bal, _ := paper.Balance()
	pos, _ := paper.Positions()
	return bal, pos, nil
}

func reconcileLiveOrders(client *hyperliquid.Client, store *execution.LiveStateStore, positions []market.Position) []string {
	if len(positions) == 0 {
		_ = store.TouchPositions(nil)
		return nil
	}
	_ = store.TouchPositions(positions)
	snap := store.Snapshot()
	warnings := make([]string, 0)
	for _, pos := range positions {
		state, ok := snap.Positions[strings.ToUpper(strings.TrimSpace(pos.Symbol))]
		if !ok {
			_ = store.UpsertImported(pos)
			warnings = append(warnings, fmt.Sprintf("%s imported live position without local TP/SL state", pos.Symbol))
			continue
		}
		orders, err := client.OpenOrders(pos.Symbol)
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("%s open-order lookup failed: %v", pos.Symbol, err))
			continue
		}
		stopID, targetID, repairWarnings, err := client.EnsureProtection(pos, state.StopPrice, state.TargetPrice, orders)
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("%s protection repair failed: %v", pos.Symbol, err))
			continue
		}
		for _, msg := range repairWarnings {
			warnings = append(warnings, msg)
		}
		if stopID != "" || targetID != "" {
			_ = store.UpdateProtection(pos.Symbol, stopID, targetID)
		}
	}
	return warnings
}

func buildStatusMessage(ctrl *runtimeControl, paper *execution.Paper, client *hyperliquid.Client, mode string) string {
	snap := ctrl.Snapshot()
	balanceBlock := buildBalanceMessage(ctrl, paper, client, mode)
	longs := topScanItems(snap.LongRows, snap.LongGrades, 3)
	shorts := topScanItems(snap.ShortRows, snap.ShortGrades, 3)
	bias := "NEUTRAL"
	if len(longs) > 0 && len(shorts) == 0 {
		bias = "LONG"
	} else if len(shorts) > 0 && len(longs) == 0 {
		bias = "SHORT"
	} else if len(longs) > 0 && len(shorts) > 0 {
		if longs[0].Score >= shorts[0].Score {
			bias = "LONG"
		} else {
			bias = "SHORT"
		}
	}
	scanBlock := notify.BuildScannerSnapshotHTML(longs, shorts, bias)
	stateLine := notify.BuildEventHTML("🎛️", "ENGINE", fmt.Sprintf("paused=%v", snap.Paused), fmt.Sprintf("signal=%s", snap.LastSignal))
	return strings.Join([]string{balanceBlock, scanBlock, stateLine}, "\n\n")
}

func serveLiveHTTP(port string, ctrl *runtimeControl, paper *execution.Paper, client *hyperliquid.Client, liveState *execution.LiveStateStore, modeFn func() string) {
	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]string{"status": "ok"})
	})
	mux.HandleFunc("/api/engine/status", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, buildEngineSnapshot(ctrl, paper, client, liveState, modeFn(), port))
	})
	mux.HandleFunc("/api/engine/control", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		var req struct {
			Action string `json:"action"`
			Symbol string `json:"symbol"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		switch strings.ToLower(strings.TrimSpace(req.Action)) {
		case "pause":
			ctrl.SetPaused(true)
		case "resume":
			ctrl.SetPaused(false)
		case "closeall":
			ctrl.RequestCloseAll()
		case "close":
			if strings.TrimSpace(req.Symbol) != "" {
				ctrl.RequestCloseSymbol(req.Symbol)
			}
		default:
			http.Error(w, "invalid action", http.StatusBadRequest)
			return
		}
		writeJSON(w, map[string]any{"ok": true, "action": req.Action, "symbol": strings.ToUpper(strings.TrimSpace(req.Symbol))})
	})
	if err := http.ListenAndServe(":"+port, mux); err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "address already in use") {
			fmt.Printf("live-lite status server disabled: port %s already in use\n", port)
			return
		}
		fmt.Println("live-lite http server error:", err)
	}
}

func buildEngineSnapshot(ctrl *runtimeControl, paper *execution.Paper, client *hyperliquid.Client, liveState *execution.LiveStateStore, mode string, port string) status.EngineSnapshot {
	snap := ctrl.Snapshot()
	baseURL := "http://localhost:" + port
	out := status.EngineSnapshot{
		Mode:       mode,
		Paused:     snap.Paused,
		Session:    snap.Session,
		LastSignal: snap.LastSignal,
		UpdatedAt:  snap.LastScanAt,
		ControlURL: baseURL + "/api/engine/control",
		StatusURL:  baseURL + "/api/engine/status",
	}
	if mode == "live" {
		if acct, err := client.Balance(); err == nil {
			pos, _ := client.Positions()
			out.Account = status.EngineAccount{
				Equity:       acct.Equity,
				AvailableUSD: acct.AvailableUSD,
				ReservedUSD:  acct.ReservedUSD,
				OpenCount:    len(pos),
			}
			stateSnap := liveState.Snapshot()
			for _, pos := range pos {
				st := stateSnap.Positions[strings.ToUpper(strings.TrimSpace(pos.Symbol))]
				out.Positions = append(out.Positions, status.EnginePosition{
					Symbol:   pos.Symbol,
					Side:     pos.Side,
					Size:     pos.Size,
					Entry:    pos.EntryPrice,
					Mark:     pos.MarkPrice,
					Stop:     st.StopPrice,
					Target:   st.TargetPrice,
					PnL:      pos.Unrealized,
					Imported: st.Imported,
				})
			}
		}
		return out
	}
	ps := paper.Snapshot()
	positions, _ := paper.Positions()
	out.Account = status.EngineAccount{
		Equity:       ps.Equity,
		AvailableUSD: ps.Available,
		ReservedUSD:  ps.Reserved,
		OpenCount:    ps.OpenCount,
	}
	for _, pos := range positions {
		out.Positions = append(out.Positions, status.EnginePosition{
			Symbol: pos.Symbol,
			Side:   pos.Side,
			Size:   pos.Size,
			Entry:  pos.EntryPrice,
			Mark:   pos.MarkPrice,
			Stop:   pos.StopPrice,
			Target: pos.TargetPrice,
			PnL:    pos.Unrealized,
		})
	}
	return out
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func topScanItems(rows []market.Scored, grades map[string]string, n int) []notify.ScanItem {
	if n <= 0 {
		return nil
	}
	items := make([]notify.ScanItem, 0, n)
	for i, row := range rows {
		if i >= n {
			break
		}
		items = append(items, notify.ScanItem{
			Symbol: market.DisplaySymbol(row.Symbol),
			Grade:  grades[row.Symbol],
			Score:  row.Score,
		})
	}
	return items
}
