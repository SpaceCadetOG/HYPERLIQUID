package inplay

import (
	"math"
	"sort"
	"strings"
	"sync"
	"time"

	"hyperliquid/internal/market"
)

type State string

const (
	StateHeating   State = "heating"
	StateInPlay    State = "in-play"
	StateBalanced  State = "balanced"
	StateCooling   State = "cooling"
	StatePumping   State = "pumping"
	StateDumping   State = "dumping"
	StateExhausted State = "exhausted"
)

type Entry struct {
	Symbol                  string    `json:"symbol"`
	SideBias                string    `json:"sideBias"`
	CurrentGrade            string    `json:"currentGrade"`
	CurrentScore            float64   `json:"currentScore"`
	ScoreSlope              float64   `json:"scoreSlope"`
	D24Pct                  float64   `json:"d24Pct"`
	DayUTCPct               float64   `json:"dayUtcPct"`
	FirstSeen               time.Time `json:"firstSeen"`
	LastSeen                time.Time `json:"lastSeen"`
	StateSince              time.Time `json:"stateSince"`
	State                   State     `json:"state"`
	Momentum                bool      `json:"momentum"`
	Rank                    float64   `json:"rank"`
	MarketConfidence        float64   `json:"marketConfidence"`
	Completeness            float64   `json:"completeness"`
	Uncertainty             float64   `json:"uncertainty"`
	TimeInStateMin          float64   `json:"timeInStateMin"`
	StateBoostRaw           float64   `json:"stateBoostRaw"`
	StateBoostDecayed       float64   `json:"stateBoostDecayed"`
	StalenessPenalty        float64   `json:"stalenessPenalty"`
	PeakPriceLookback       float64   `json:"peakPriceLookback"`
	TroughPriceLookback     float64   `json:"troughPriceLookback"`
	PeakScoreLookback       float64   `json:"peakScoreLookback"`
	TroughScoreLookback     float64   `json:"troughScoreLookback"`
	BarsSincePeak           int       `json:"barsSincePeak"`
	BarsSinceTrough         int       `json:"barsSinceTrough"`
	DrawdownFromPeakPct     float64   `json:"drawdownFromPeakPct"`
	DrawupFromTroughPct     float64   `json:"drawupFromTroughPct"`
	ScoreOffPeakPct         float64   `json:"scoreOffPeakPct"`
	ScoreOffTroughPct       float64   `json:"scoreOffTroughPct"`
	FailedReclaimCount      int       `json:"failedReclaimCount"`
	FailedBounceCount       int       `json:"failedBounceCount"`
	FailedBreakdownCount    int       `json:"failedBreakdownCount"`
	FailedBreakLowCount     int       `json:"failedBreakLowCount"`
	LongDemotionFlag        bool      `json:"longDemotionFlag"`
	ShortDemotionFlag       bool      `json:"shortDemotionFlag"`
	ReversalWatchFlag       bool      `json:"reversalWatchFlag"`
	ExhaustionRisk          float64   `json:"exhaustionRisk"`
	BullReversalScore       float64   `json:"bullReversalScore"`
	BearReversalScore       float64   `json:"bearReversalScore"`
	IntradayReversalScore   float64   `json:"intradayReversalScore"`
	FollowThroughDecayScore float64   `json:"followThroughDecayScore"`
	EntryStyle              string    `json:"entryStyle"`
	MetaState               string    `json:"metaState"`
	AgeMinutes              float64   `json:"ageMinutes"`
}

type Config struct {
	MinGrade               string
	MinVolumeUSD           float64
	HistoryN               int
	PeakLookbackN          int
	RiseN                  int
	DropGradeScans         int
	FallScans              int
	TTL                    time.Duration
	EnableStateDecay       bool
	StateDecayMin          float64
	EnableStalenessPenalty bool
	StaleImpulseMin        float64
}

type scorePoint struct {
	ts           time.Time
	score        float64
	grade        string
	vol          float64
	price        float64
	change       float64
	dayUTC       float64
	completeness float64
	confidence   float64
	uncertainty  float64
}

type symbolState struct {
	firstSeen  time.Time
	lastSeen   time.Time
	history    []scorePoint
	dStreak    int
	fallStreak int
	state      State
	stateSince time.Time
}

type Tracker struct {
	mu   sync.RWMutex
	side string
	cfg  Config
	data map[string]*symbolState
}

func NewTracker(side string, cfg Config) *Tracker {
	if cfg.MinGrade == "" {
		cfg.MinGrade = "C"
	}
	if cfg.HistoryN <= 0 {
		cfg.HistoryN = 5
	}
	if cfg.RiseN <= 0 {
		cfg.RiseN = 3
	}
	if cfg.PeakLookbackN <= 0 {
		cfg.PeakLookbackN = maxInt(cfg.HistoryN, 12)
	}
	if cfg.HistoryN < cfg.PeakLookbackN {
		cfg.HistoryN = cfg.PeakLookbackN
	}
	if cfg.DropGradeScans <= 0 {
		cfg.DropGradeScans = 2
	}
	if cfg.FallScans <= 0 {
		cfg.FallScans = 2
	}
	if cfg.TTL <= 0 {
		cfg.TTL = 30 * time.Minute
	}
	if cfg.StateDecayMin <= 0 {
		cfg.StateDecayMin = 25
	}
	if cfg.StaleImpulseMin <= 0 {
		cfg.StaleImpulseMin = 20
	}
	return &Tracker{side: strings.ToLower(strings.TrimSpace(side)), cfg: cfg, data: map[string]*symbolState{}}
}

func (t *Tracker) Update(now time.Time, rows []market.Scored, grades map[string]string) {
	t.mu.Lock()
	defer t.mu.Unlock()

	minGradeVal := gradeValue(t.cfg.MinGrade)
	for _, r := range rows {
		sym := strings.ToUpper(strings.TrimSpace(r.Symbol))
		if sym == "" {
			continue
		}
		g := strings.TrimSpace(grades[sym])
		if g == "" {
			g = strings.TrimSpace(r.Grade)
		}
		if g == "" {
			g = market.FallbackGradeDirectionalView(r.Score, ptrFloat(r.DayUTC24h), r.Change24h, t.side)
		}
		ss := t.data[sym]
		if ss == nil {
			ss = &symbolState{firstSeen: now, state: StateHeating, stateSince: now}
			t.data[sym] = ss
		}
		ss.lastSeen = now
		ss.history = append(ss.history, scorePoint{
			ts:           now,
			score:        r.Score,
			grade:        g,
			vol:          r.VolumeUSD,
			price:        r.LastPrice,
			change:       r.Change24h,
			dayUTC:       ptrFloat(r.DayUTC24h),
			completeness: r.Completeness,
			confidence:   r.Confidence,
			uncertainty:  r.Uncertainty,
		})
		if len(ss.history) > t.cfg.HistoryN {
			ss.history = append([]scorePoint(nil), ss.history[len(ss.history)-t.cfg.HistoryN:]...)
		}

		if gradeValue(g) <= gradeValue("D") {
			ss.dStreak++
		} else {
			ss.dStreak = 0
		}
		slope := calcSlope(ss.history)
		if slope < 0 {
			ss.fallStreak++
		} else {
			ss.fallStreak = 0
		}

		rising := isRising(ss.history, t.cfg.RiseN)
		falling := isFalling(ss.history, t.cfg.RiseN)
		gradeOK := gradeValue(g) >= minGradeVal
		volOK := r.VolumeUSD >= t.cfg.MinVolumeUSD
		priceFav := favorablePriceMove(t.side, ss.history)
		volRise := volumeRising(ss.history)
		volFall := volumeFalling(ss.history)
		signFlipAgainst := change24hFlipAgainst(t.side, ss.history)
		dropPct := peakDropPct(ss.history)
		aPlusLosing := gradeValue(g) >= gradeValue("A+") && (dropPct >= 0.08 || slope <= -0.8) && (!priceFav || volFall)
		momentumLoss := (falling && !priceFav && volFall) || signFlipAgainst || aPlusLosing
		exhausted := (ss.state == StatePumping || ss.state == StateInPlay || gradeValue(g) >= gradeValue("A")) &&
			(dropPct >= 0.06 || (slope < -0.45 && volFall) || signFlipAgainst)

		nextState := ss.state
		switch {
		case gradeOK && volOK && rising && priceFav && volRise:
			nextState = StatePumping
		case gradeOK && volOK && rising:
			nextState = StateInPlay
		case gradeOK && volOK && priceFav && volRise:
			nextState = StateHeating
		case exhausted:
			nextState = StateExhausted
		case momentumLoss:
			nextState = StateDumping
		case rising || (slope > 0 && (priceFav || !volFall)):
			nextState = StateHeating
		case math.Abs(slope) < 0.10 || (!volRise && !volFall):
			nextState = StateBalanced
		case slope <= 0 || volFall || !priceFav:
			nextState = StateCooling
		default:
			nextState = StateBalanced
		}
		if nextState != ss.state {
			ss.state = nextState
			ss.stateSince = now
		}
		if ss.dStreak >= t.cfg.DropGradeScans || ss.fallStreak >= t.cfg.FallScans {
			delete(t.data, sym)
		}
	}

	for sym, ss := range t.data {
		if now.Sub(ss.lastSeen) > t.cfg.TTL {
			delete(t.data, sym)
		}
	}
}

func (t *Tracker) Reset() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.data = map[string]*symbolState{}
}

func (t *Tracker) Entries() []Entry {
	t.mu.RLock()
	defer t.mu.RUnlock()

	out := make([]Entry, 0, len(t.data))
	for sym, ss := range t.data {
		if len(ss.history) == 0 {
			continue
		}
		last := ss.history[len(ss.history)-1]
		slope := calcSlope(ss.history)
		continuation := favorablePriceMove(t.side, ss.history) && volumeRising(ss.history)
		peakPrice, peakPriceIdx := peakPriceLookback(ss.history, t.cfg.PeakLookbackN)
		troughPrice, troughPriceIdx := troughPriceLookback(ss.history, t.cfg.PeakLookbackN)
		peakScore, peakScoreIdx := peakScoreLookback(ss.history, t.cfg.PeakLookbackN)
		troughScore, _ := troughScoreLookback(ss.history, t.cfg.PeakLookbackN)
		barsSincePeak := 0
		if peakPriceIdx >= 0 {
			barsSincePeak = maxInt(0, len(ss.history)-1-peakPriceIdx)
		}
		barsSinceTrough := 0
		if troughPriceIdx >= 0 {
			barsSinceTrough = maxInt(0, len(ss.history)-1-troughPriceIdx)
		}
		failedReclaims := countFailedReclaims(ss.history, peakPriceIdx)
		failedBounces := countFailedBounces(ss.history, peakPriceIdx)
		failedBreakdowns := countFailedBreakdowns(ss.history, troughPriceIdx)
		failedBreakLows := countFailedBreakLows(ss.history, troughPriceIdx)
		e := Entry{
			Symbol:               sym,
			SideBias:             t.side,
			CurrentGrade:         last.grade,
			CurrentScore:         last.score,
			ScoreSlope:           slope,
			D24Pct:               last.change,
			DayUTCPct:            last.dayUTC,
			FirstSeen:            ss.firstSeen,
			LastSeen:             ss.lastSeen,
			StateSince:           ss.stateSince,
			State:                ss.state,
			Momentum:             (slope > 0 && isRising(ss.history, t.cfg.RiseN)) || ss.state == StatePumping || continuation,
			MarketConfidence:     clamp(last.confidence, 0, 1),
			Completeness:         clamp(last.completeness, 0, 1),
			Uncertainty:          clamp(last.uncertainty, 0, 1),
			PeakPriceLookback:    peakPrice,
			TroughPriceLookback:  troughPrice,
			PeakScoreLookback:    peakScore,
			TroughScoreLookback:  troughScore,
			BarsSincePeak:        barsSincePeak,
			BarsSinceTrough:      barsSinceTrough,
			DrawdownFromPeakPct:  drawdownFromPeakPct(last.price, peakPrice),
			DrawupFromTroughPct:  drawupFromTroughPct(last.price, troughPrice),
			ScoreOffPeakPct:      scoreOffPeakPct(last.score, peakScore),
			ScoreOffTroughPct:    scoreOffTroughPct(last.score, troughScore),
			FailedReclaimCount:   failedReclaims,
			FailedBounceCount:    failedBounces,
			FailedBreakdownCount: failedBreakdowns,
			FailedBreakLowCount:  failedBreakLows,
			AgeMinutes:           maxF(0, ss.lastSeen.Sub(ss.firstSeen).Minutes()),
		}
		e.TimeInStateMin = maxF(0, ss.lastSeen.Sub(ss.stateSince).Minutes())
		if peakScoreIdx >= 0 {
			e.FollowThroughDecayScore = followThroughDecayScore(e)
			e.IntradayReversalScore = intradayReversalScore(e)
			e.BearReversalScore = e.IntradayReversalScore
			e.BullReversalScore = bullReversalScore(e)
			e.ExhaustionRisk = exhaustionRisk(e)
			e.LongDemotionFlag = shouldDemoteLong(e)
			e.ShortDemotionFlag = shouldDemoteShort(e)
			e.ReversalWatchFlag = e.LongDemotionFlag || e.ShortDemotionFlag || e.IntradayReversalScore >= 5.5 || e.BullReversalScore >= 4.5
			e.EntryStyle = deriveEntryStyle(e)
			e.MetaState = deriveMetaState(e)
		}
		e.Rank, e.StateBoostRaw, e.StateBoostDecayed, e.StalenessPenalty = rankFor(e, t.cfg)
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Rank > out[j].Rank })
	return out
}

func rankFor(e Entry, cfg Config) (float64, float64, float64, float64) {
	stateBoost := 0.0
	switch e.State {
	case StatePumping:
		stateBoost = 35
	case StateInPlay:
		stateBoost = 25
	case StateHeating:
		stateBoost = 10
	case StateBalanced:
		stateBoost = 2
	case StateCooling:
		stateBoost = -5
	case StateDumping:
		stateBoost = -12
	case StateExhausted:
		stateBoost = -18
	}
	momentum := 0.0
	if e.Momentum {
		momentum = 5
	}
	decayedBoost := stateBoost
	if cfg.EnableStateDecay && cfg.StateDecayMin > 0 {
		decayedBoost = stateBoost * math.Exp(-e.TimeInStateMin/cfg.StateDecayMin)
	}
	stalenessPenalty := 0.0
	if cfg.EnableStalenessPenalty && cfg.StaleImpulseMin > 0 && e.TimeInStateMin >= cfg.StaleImpulseMin && math.Abs(e.ScoreSlope) < 0.08 && !e.Momentum {
		stalenessPenalty = 6 + minF(12, (e.TimeInStateMin-cfg.StaleImpulseMin)*0.12)
	}
	confAdj := 0.0
	if e.MarketConfidence > 0 {
		confAdj = (e.MarketConfidence - 0.5) * 8
	}
	rank := 20*float64(gradeValue(e.CurrentGrade)) + 0.4*e.CurrentScore + 8*e.ScoreSlope + decayedBoost + momentum + confAdj - stalenessPenalty
	return rank, stateBoost, decayedBoost, stalenessPenalty
}

func calcSlope(points []scorePoint) float64 {
	n := len(points)
	if n < 2 {
		return 0
	}
	return (points[n-1].score - points[0].score) / math.Max(1, float64(n-1))
}

func isRising(points []scorePoint, riseN int) bool {
	if len(points) < 2 {
		return false
	}
	if riseN <= 1 {
		riseN = 2
	}
	if len(points) < riseN {
		riseN = len(points)
	}
	start := len(points) - riseN
	rises := 0
	for i := start + 1; i < len(points); i++ {
		if points[i].score > points[i-1].score {
			rises++
		}
	}
	return rises >= riseN-1
}

func isFalling(points []scorePoint, n int) bool {
	if len(points) < 2 {
		return false
	}
	if n <= 1 {
		n = 2
	}
	if len(points) < n {
		n = len(points)
	}
	start := len(points) - n
	falls := 0
	for i := start + 1; i < len(points); i++ {
		if points[i].score < points[i-1].score {
			falls++
		}
	}
	return falls >= n-1
}

func favorablePriceMove(side string, points []scorePoint) bool {
	if len(points) < 2 {
		return false
	}
	last := points[len(points)-1]
	prev := points[len(points)-2]
	if side == "short" {
		return last.price < prev.price
	}
	return last.price > prev.price
}

func balancedImpulse(slope float64, priceFav, volRise, volFall bool) bool {
	if math.Abs(slope) > 0.20 {
		return false
	}
	if volRise == volFall {
		return true
	}
	if !priceFav {
		return true
	}
	return !volRise || volFall
}

func volumeRising(points []scorePoint) bool {
	if len(points) < 2 {
		return false
	}
	return points[len(points)-1].vol >= points[len(points)-2].vol*1.03
}

func volumeFalling(points []scorePoint) bool {
	if len(points) < 2 {
		return false
	}
	return points[len(points)-1].vol <= points[len(points)-2].vol*0.97
}

func change24hFlipAgainst(side string, points []scorePoint) bool {
	if len(points) < 2 {
		return false
	}
	last := points[len(points)-1].change
	prev := points[len(points)-2].change
	if side == "short" {
		return prev < 0 && last >= 0
	}
	return prev > 0 && last <= 0
}

func peakDropPct(points []scorePoint) float64 {
	if len(points) == 0 {
		return 0
	}
	peak := points[0].score
	for i := 1; i < len(points); i++ {
		if points[i].score > peak {
			peak = points[i].score
		}
	}
	last := points[len(points)-1].score
	if peak <= 0 {
		return 0
	}
	d := (peak - last) / peak
	if d < 0 {
		return 0
	}
	return d
}

func peakPriceLookback(points []scorePoint, lookback int) (float64, int) {
	if len(points) == 0 {
		return 0, -1
	}
	if lookback <= 0 || lookback > len(points) {
		lookback = len(points)
	}
	start := len(points) - lookback
	peak, peakIdx := points[start].price, start
	for i := start + 1; i < len(points); i++ {
		if points[i].price > peak {
			peak = points[i].price
			peakIdx = i
		}
	}
	return peak, peakIdx
}

func troughPriceLookback(points []scorePoint, lookback int) (float64, int) {
	if len(points) == 0 {
		return 0, -1
	}
	if lookback <= 0 || lookback > len(points) {
		lookback = len(points)
	}
	start := len(points) - lookback
	trough, troughIdx := points[start].price, start
	for i := start + 1; i < len(points); i++ {
		if points[i].price < trough {
			trough = points[i].price
			troughIdx = i
		}
	}
	return trough, troughIdx
}

func peakScoreLookback(points []scorePoint, lookback int) (float64, int) {
	if len(points) == 0 {
		return 0, -1
	}
	if lookback <= 0 || lookback > len(points) {
		lookback = len(points)
	}
	start := len(points) - lookback
	peak, peakIdx := points[start].score, start
	for i := start + 1; i < len(points); i++ {
		if points[i].score > peak {
			peak = points[i].score
			peakIdx = i
		}
	}
	return peak, peakIdx
}

func troughScoreLookback(points []scorePoint, lookback int) (float64, int) {
	if len(points) == 0 {
		return 0, -1
	}
	if lookback <= 0 || lookback > len(points) {
		lookback = len(points)
	}
	start := len(points) - lookback
	trough, troughIdx := points[start].score, start
	for i := start + 1; i < len(points); i++ {
		if points[i].score < trough {
			trough = points[i].score
			troughIdx = i
		}
	}
	return trough, troughIdx
}

func drawdownFromPeakPct(last, peak float64) float64 {
	if last <= 0 || peak <= 0 {
		return 0
	}
	return ((last - peak) / peak) * 100.0
}

func drawupFromTroughPct(last, trough float64) float64 {
	if last <= 0 || trough <= 0 {
		return 0
	}
	return ((last - trough) / trough) * 100.0
}

func scoreOffPeakPct(score, peakScore float64) float64 {
	if peakScore <= 0 {
		return 0
	}
	return ((score - peakScore) / peakScore) * 100.0
}

func scoreOffTroughPct(score, troughScore float64) float64 {
	if troughScore == 0 {
		return 0
	}
	denom := math.Abs(troughScore)
	if denom < 1 {
		denom = 1
	}
	return ((score - troughScore) / denom) * 100.0
}

func countFailedReclaims(points []scorePoint, peakIdx int) int {
	if len(points) < 4 || peakIdx < 0 || peakIdx >= len(points)-2 {
		return 0
	}
	peak := points[peakIdx].price
	count := 0
	for i := peakIdx + 1; i < len(points)-1; i++ {
		prev, cur, next := points[i-1].price, points[i].price, points[i+1].price
		if cur > prev && cur > next && cur < peak*0.997 {
			count++
		}
	}
	return count
}

func countFailedBounces(points []scorePoint, peakIdx int) int {
	if len(points) < 4 || peakIdx < 0 || peakIdx >= len(points)-3 {
		return 0
	}
	count := 0
	for i := peakIdx + 1; i < len(points)-2; i++ {
		if points[i].price < points[i-1].price && points[i+1].price > points[i].price && points[i+2].price < points[i+1].price {
			count++
		}
	}
	return count
}

func countFailedBreakdowns(points []scorePoint, troughIdx int) int {
	if len(points) < 4 || troughIdx < 0 || troughIdx >= len(points)-2 {
		return 0
	}
	trough := points[troughIdx].price
	count := 0
	for i := troughIdx + 1; i < len(points)-1; i++ {
		prev, cur, next := points[i-1].price, points[i].price, points[i+1].price
		if cur < prev && cur < next && cur > trough*1.003 {
			count++
		}
	}
	return count
}

func countFailedBreakLows(points []scorePoint, troughIdx int) int {
	if len(points) < 4 || troughIdx < 0 || troughIdx >= len(points)-3 {
		return 0
	}
	trough := points[troughIdx].price
	count := 0
	for i := troughIdx + 1; i < len(points)-1; i++ {
		if points[i].price < trough && points[i+1].price > points[i].price && points[i+1].price >= trough {
			count++
		}
	}
	return count
}

func intradayReversalScore(e Entry) float64 {
	score := 0.0
	if e.D24Pct >= 25 {
		score += 1.0
	}
	if e.D24Pct >= 40 {
		score += 1.0
	}
	if e.DayUTCPct <= -3 {
		score += 1.0
	}
	if e.DayUTCPct <= -7 {
		score += 1.5
	}
	if e.DrawdownFromPeakPct <= -8 {
		score += 1.5
	}
	if e.DrawdownFromPeakPct <= -12 {
		score += 1.5
	}
	if e.ScoreSlope <= -0.75 {
		score += 1.0
	}
	if e.ScoreSlope <= -1.50 {
		score += 1.0
	}
	if e.State == StateDumping {
		score += 1.5
	}
	if e.State == StateExhausted {
		score += 2.0
	}
	if !e.Momentum {
		score += 0.75
	}
	score += float64(minInt(e.FailedReclaimCount, 3)) * 0.5
	score += float64(minInt(e.FailedBounceCount, 3)) * 0.4
	return round2(score)
}

func exhaustionRisk(e Entry) float64 {
	risk := 0.0
	if e.D24Pct >= 30 {
		risk += 1.0
	}
	if e.DrawdownFromPeakPct <= -10 {
		risk += 2.0
	}
	if e.ScoreSlope < 0 {
		risk += 1.0
	}
	if e.State == StateDumping || e.State == StateExhausted {
		risk += 2.0
	}
	if !e.Momentum {
		risk += 0.5
	}
	if e.FailedReclaimCount >= 2 {
		risk += 1.0
	}
	return round2(risk)
}

func bullReversalScore(e Entry) float64 {
	score := 0.0
	if e.D24Pct <= -20 {
		score += 1.0
	}
	if e.D24Pct <= -35 {
		score += 1.0
	}
	if e.DayUTCPct >= 2 {
		score += 1.0
	}
	if e.DayUTCPct >= 5 {
		score += 1.5
	}
	if e.DrawupFromTroughPct >= 6 {
		score += 1.5
	}
	if e.DrawupFromTroughPct >= 10 {
		score += 1.5
	}
	if e.ScoreSlope >= 0.50 {
		score += 1.0
	}
	if e.ScoreSlope >= 1.00 {
		score += 1.0
	}
	if e.State == StateHeating {
		score += 1.25
	}
	if e.State == StateInPlay {
		score += 1.75
	}
	if e.State == StateBalanced {
		score += 0.5
	}
	if e.Momentum {
		score += 0.75
	}
	score += float64(minInt(e.FailedBreakdownCount, 3)) * 0.6
	score += float64(minInt(e.FailedBreakLowCount, 3)) * 0.5
	return round2(score)
}

func followThroughDecayScore(e Entry) float64 {
	decay := 0.0
	if e.BarsSincePeak >= 2 {
		decay += minF(2.0, float64(e.BarsSincePeak-1)*0.35)
	}
	if e.ScoreOffPeakPct <= -8 {
		decay += 1.0
	}
	if e.DrawdownFromPeakPct <= -6 {
		decay += 1.0
	}
	if e.State == StateDumping || e.State == StateExhausted {
		decay += 1.0
	}
	return round2(decay)
}

func shouldDemoteLong(e Entry) bool {
	if e.D24Pct < 25 {
		return false
	}
	reversal := e.DayUTCPct <= -5 || e.DrawdownFromPeakPct <= -8
	weakState := e.State == StateDumping || e.State == StateExhausted
	weakSlope := e.ScoreSlope <= -0.75
	return reversal && weakState && weakSlope && !e.Momentum
}

func shouldDemoteShort(e Entry) bool {
	if e.D24Pct > -20 {
		return false
	}
	reversal := e.DayUTCPct >= 3 || e.DrawupFromTroughPct >= 6
	improvingSlope := e.ScoreSlope >= 0.50
	improvedState := e.State == StateBalanced || e.State == StateHeating || e.State == StateInPlay
	reclaiming := e.Momentum || e.FailedBreakdownCount >= 1 || e.FailedBreakLowCount >= 1
	return reversal && improvingSlope && improvedState && reclaiming
}

func EarlyShortAdmission(e Entry, threshold float64) bool {
	if threshold <= 0 {
		threshold = 5.0
	}
	if e.D24Pct < 20 {
		return false
	}
	if !(e.DayUTCPct <= -5 || e.DrawdownFromPeakPct <= -8) {
		return false
	}
	if e.ScoreSlope > -0.75 {
		return false
	}
	if e.State != StateDumping && e.State != StateExhausted {
		return false
	}
	if e.Momentum {
		return false
	}
	return e.IntradayReversalScore >= threshold
}

func EarlyLongAdmissionFromShortLeader(e Entry, threshold float64) bool {
	if threshold <= 0 {
		threshold = 4.5
	}
	if e.D24Pct > -20 {
		return false
	}
	if !(e.DayUTCPct >= 3 || e.DrawupFromTroughPct >= 6) {
		return false
	}
	if e.ScoreSlope < 0.50 {
		return false
	}
	if e.State != StateBalanced && e.State != StateHeating && e.State != StateInPlay {
		return false
	}
	if e.FailedBreakdownCount < 1 && e.FailedBreakLowCount < 1 && !e.Momentum {
		return false
	}
	return e.BullReversalScore >= threshold
}

func deriveMetaState(e Entry) string {
	if shouldDemoteLong(e) && e.BearReversalScore >= 6 {
		return "long_exhausting"
	}
	if shouldDemoteShort(e) && e.BullReversalScore >= 5 {
		return "short_exhausting"
	}
	if e.EntryStyle == "leader_unwind_short" {
		return "leader_unwind_short"
	}
	if e.EntryStyle == "momentum_ignite_long" {
		return "ignite_long"
	}
	if e.EntryStyle == "momentum_ignite_short" {
		return "ignite_short"
	}
	if e.EntryStyle == "reversal_watch_short" {
		return "reversal_watch_short"
	}
	if e.EntryStyle == "reversal_watch_long" {
		return "reversal_watch_long"
	}
	if e.State == StateInPlay && e.ScoreSlope > 0 {
		return "trend_long"
	}
	if e.State == StateInPlay && e.ScoreSlope < 0 {
		return "trend_short"
	}
	return "neutral"
}

func deriveEntryStyle(e Entry) string {
	if shouldDemoteLong(e) && e.BearReversalScore >= 5.5 {
		return "reversal_watch_short"
	}
	if shouldDemoteShort(e) && e.BullReversalScore >= 4.5 {
		return "reversal_watch_long"
	}
	if e.ExhaustionRisk >= 4.5 {
		return "avoid_chase"
	}
	if strings.EqualFold(strings.TrimSpace(e.SideBias), "short") && e.CurrentScore >= 88 && e.DayUTCPct <= -20 && (e.State == StateHeating || e.State == StateInPlay || e.State == StatePumping) && (e.ScoreSlope >= 0.35 || e.Momentum) && e.BearReversalScore >= 3.0 {
		return "leader_unwind_short"
	}
	if e.Momentum && e.ScoreSlope >= 0.08 {
		freshHeating := e.State == StateHeating && e.TimeInStateMin <= 14
		freshInPlay := e.State == StateInPlay && e.TimeInStateMin <= 8
		if freshHeating || freshInPlay {
			if strings.EqualFold(strings.TrimSpace(e.SideBias), "short") {
				return "momentum_ignite_short"
			}
			return "momentum_ignite_long"
		}
	}
	if e.State == StateInPlay && e.Momentum && e.ScoreSlope > 0.5 {
		if e.D24Pct >= 0 {
			return "breakout_hold_long"
		}
		return "breakout_hold_short"
	}
	if e.State == StateHeating && e.ScoreSlope > 0 {
		if e.D24Pct >= 0 {
			return "pullback_long"
		}
		return "pullback_short"
	}
	return "none"
}

func gradeValue(g string) int {
	switch strings.ToUpper(strings.TrimSpace(g)) {
	case "A+":
		return 5
	case "A":
		return 4
	case "B":
		return 3
	case "C":
		return 2
	case "D":
		return 1
	default:
		return 0
	}
}

func clamp(v, lo, hi float64) float64 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func maxF(a, b float64) float64 {
	if a > b {
		return a
	}
	return b
}

func minF(a, b float64) float64 {
	if a < b {
		return a
	}
	return b
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func ptrFloat(p *float64) float64 {
	if p == nil {
		return 0
	}
	return *p
}

func round2(x float64) float64 { return math.Round(x*100) / 100 }
