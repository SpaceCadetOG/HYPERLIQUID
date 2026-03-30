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
	Symbol       string
	SideBias     string
	CurrentGrade string
	CurrentScore float64
	ScoreSlope   float64
	FirstSeen    time.Time
	LastSeen     time.Time
	StateSince   time.Time
	AgeMinutes   float64
	State        State
	Momentum     bool
	Rank         float64
}

type Config struct {
	MinGrade       string
	MinVolumeUSD   float64
	HistoryN       int
	RiseN          int
	DropGradeScans int
	FallScans      int
	TTL            time.Duration
}

type scorePoint struct {
	ts     time.Time
	score  float64
	grade  string
	vol    float64
	price  float64
	change float64
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
	if cfg.DropGradeScans <= 0 {
		cfg.DropGradeScans = 2
	}
	if cfg.FallScans <= 0 {
		cfg.FallScans = 2
	}
	if cfg.TTL <= 0 {
		cfg.TTL = 30 * time.Minute
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
			g = market.FallbackGradeDirectional(r.Score, r.Change24h, t.side)
		}
		ss := t.data[sym]
		if ss == nil {
			ss = &symbolState{firstSeen: now, state: StateHeating, stateSince: now}
			t.data[sym] = ss
		}
		ss.lastSeen = now
		ss.history = append(ss.history, scorePoint{ts: now, score: r.Score, grade: g, vol: r.VolumeUSD, price: r.LastPrice, change: r.Change24h})
		if len(ss.history) > t.cfg.HistoryN {
			ss.history = append([]scorePoint(nil), ss.history[len(ss.history)-t.cfg.HistoryN:]...)
		}
		if len(ss.history) < 3 {
			if gradeValue(g) >= minGradeVal && r.VolumeUSD >= t.cfg.MinVolumeUSD {
				if favorablePriceMove(t.side, ss.history) {
					setState(ss, now, StateHeating)
				} else {
					setState(ss, now, StateCooling)
				}
			} else {
				setState(ss, now, StateCooling)
			}
			continue
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
		setState(ss, now, nextState)
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
		momentum := false
		if len(ss.history) >= 2 {
			momentum = (slope > 0 && isRising(ss.history, t.cfg.RiseN)) || ss.state == StatePumping
		}
		e := Entry{
			Symbol:       sym,
			SideBias:     t.side,
			CurrentGrade: last.grade,
			CurrentScore: last.score,
			ScoreSlope:   slope,
			FirstSeen:    ss.firstSeen,
			LastSeen:     ss.lastSeen,
			StateSince:   ss.stateSince,
			AgeMinutes:   maxFloat(0, ss.lastSeen.Sub(ss.firstSeen).Minutes()),
			State:        ss.state,
			Momentum:     momentum,
		}
		e.Rank = rankFor(e)
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Rank > out[j].Rank })
	return out
}

func rankFor(e Entry) float64 {
	stateBoost := 0.0
	switch e.State {
	case StatePumping:
		stateBoost = 35
	case StateInPlay:
		stateBoost = 25
	case StateHeating:
		stateBoost = 10
	case StateBalanced:
		stateBoost = 0
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
	return 20*float64(gradeValue(e.CurrentGrade)) + 0.4*e.CurrentScore + 8*e.ScoreSlope + stateBoost + momentum
}

func setState(ss *symbolState, now time.Time, next State) {
	if ss.state != next {
		ss.state = next
		ss.stateSince = now
		return
	}
	if ss.stateSince.IsZero() {
		ss.stateSince = now
	}
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

func maxFloat(a, b float64) float64 {
	if a > b {
		return a
	}
	return b
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

func isFalling(points []scorePoint, riseN int) bool {
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
	falls := 0
	for i := start + 1; i < len(points); i++ {
		if points[i].score < points[i-1].score {
			falls++
		}
	}
	return falls >= riseN-1
}

func favorablePriceMove(side string, points []scorePoint) bool {
	if len(points) < 2 {
		return true
	}
	first := points[0].change
	last := points[len(points)-1].change
	if side == "short" {
		return last <= first
	}
	return last >= first
}

func volumeRising(points []scorePoint) bool {
	if len(points) < 2 {
		return false
	}
	return points[len(points)-1].vol >= points[len(points)-2].vol
}

func volumeFalling(points []scorePoint) bool {
	if len(points) < 2 {
		return false
	}
	return points[len(points)-1].vol < points[len(points)-2].vol
}

func change24hFlipAgainst(side string, points []scorePoint) bool {
	if len(points) < 2 {
		return false
	}
	prev := points[len(points)-2].change
	cur := points[len(points)-1].change
	if side == "short" {
		return prev <= 0 && cur > 0
	}
	return prev >= 0 && cur < 0
}

func peakDropPct(points []scorePoint) float64 {
	if len(points) < 2 {
		return 0
	}
	peak := points[0].score
	last := points[len(points)-1].score
	for _, p := range points[1:] {
		if p.score > peak {
			peak = p.score
		}
	}
	if peak <= 0 {
		return 0
	}
	return (peak - last) / peak
}

func gradeValue(g string) int {
	switch strings.ToUpper(strings.TrimSpace(g)) {
	case "A+":
		return 6
	case "A":
		return 5
	case "B":
		return 4
	case "C":
		return 3
	case "D":
		return 2
	default:
		return 0
	}
}
