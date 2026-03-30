package strategies

import (
	"time"

	"hyperliquid/internal/features"
	"hyperliquid/internal/levels"
	"hyperliquid/internal/market"
)

type VPAccumulation struct{}
type VPTrendRetest struct{}
type VPRejection struct{}
type VPReversal struct{}
type DailyOpenSR struct{}
type PDLevelsRetest struct{}
type FailedAuctionMagnetStrategy struct{}
type VWAPConfluenceStrategy struct{}
type OpenDrive struct{}

func (s VPAccumulation) Name() string              { return "vp_accumulation" }
func (s VPTrendRetest) Name() string               { return "vp_trend" }
func (s VPRejection) Name() string                 { return "vp_rejection" }
func (s VPReversal) Name() string                  { return "vp_reversal" }
func (s DailyOpenSR) Name() string                 { return "daily_open_sr" }
func (s PDLevelsRetest) Name() string              { return "pd_levels_retest" }
func (s FailedAuctionMagnetStrategy) Name() string { return "failed_auction_magnet" }
func (s VWAPConfluenceStrategy) Name() string      { return "vwap_confluence" }
func (s OpenDrive) Name() string                   { return "od" }

func (s VPAccumulation) Eval(ctx Context) Signal {
	c := ctx.Candles
	if len(c) < 30 {
		return Signal{Name: s.Name()}
	}
	last := c[len(c)-1]
	look := c[len(c)-20:]
	lo, hi := look[0].Low, look[0].High
	for _, x := range look {
		if x.Low < lo {
			lo = x.Low
		}
		if x.High > hi {
			hi = x.High
		}
	}
	lvl, ok := levelAtHeaviestInRange(ctx.Snapshot, lo, hi)
	if !ok || lvl <= 0 {
		return Signal{Name: s.Name()}
	}
	side := market.SideLong
	if c[len(c)-1].Close <= c[len(c)-6].Close {
		side = market.SideShort
	}
	tol := last.Close * 0.0015
	if tol <= 0 || absf(last.Close-lvl) > tol {
		return Signal{Name: s.Name()}
	}
	stop := lo
	if side == market.SideShort {
		stop = hi
	}
	tp1, tp2 := rrTargets(last.Close, stop, side)
	conf := 0.54
	if ctx.Snapshot.VolumeRatio >= 1.5 {
		conf += 0.07
	}
	if side == market.SideLong && flowBias(ctx.Snapshot) > 0 {
		conf += 0.07
	}
	if side == market.SideShort && flowBias(ctx.Snapshot) < 0 {
		conf += 0.07
	}
	return Signal{Active: tp1 > 0, Name: s.Name(), Side: side, Entry: last.Close, Stop: stop, TP1: tp1, TP2: tp2, Confidence: clamp01(conf), Tags: []string{"vp", "accumulation", "retest"}, Invalidation: "lose accumulation zone", VPSetup: "accumulation", VPLevel: lvl, Ts: last.Time}
}

func (s VPTrendRetest) Eval(ctx Context) Signal {
	c := ctx.Candles
	if len(c) < 24 {
		return Signal{Name: s.Name()}
	}
	side := trendFromSnapshot(ctx.Snapshot, c)
	seg := c[len(c)-16:]
	lo, hi := seg[0].Low, seg[0].High
	for _, x := range seg {
		if x.Low < lo {
			lo = x.Low
		}
		if x.High > hi {
			hi = x.High
		}
	}
	lvl, ok := levelAtHeaviestInRange(ctx.Snapshot, lo, hi)
	if !ok || lvl <= 0 {
		return Signal{Name: s.Name()}
	}
	last := c[len(c)-1]
	tol := last.Close * 0.0012
	if tol <= 0 || absf(last.Close-lvl) > tol {
		return Signal{Name: s.Name()}
	}
	stop := lo
	if side == market.SideShort {
		stop = hi
	}
	tp1, tp2 := rrTargets(last.Close, stop, side)
	conf := 0.60
	if ctx.Snapshot.VolumeRatio >= 1.5 {
		conf += 0.06
	}
	if side == market.SideLong && flowBias(ctx.Snapshot) > 0 {
		conf += 0.09
	}
	if side == market.SideShort && flowBias(ctx.Snapshot) < 0 {
		conf += 0.09
	}
	return Signal{Active: tp1 > 0, Name: s.Name(), Side: side, Entry: last.Close, Stop: stop, TP1: tp1, TP2: tp2, Confidence: clamp01(conf), Tags: []string{"vp", "trend", "retest"}, Invalidation: "trend retest fails", VPSetup: "trend_retest", VPLevel: lvl, Ts: last.Time}
}

func (s VPRejection) Eval(ctx Context) Signal {
	c := ctx.Candles
	if len(c) < 8 {
		return Signal{Name: s.Name()}
	}
	last := c[len(c)-1]
	prev := c[len(c)-2]
	upWick := last.High - maxf(last.Open, last.Close)
	loWick := minf(last.Open, last.Close) - last.Low
	body := absf(last.Close - last.Open)
	if body <= 0 {
		return Signal{Name: s.Name()}
	}
	side := market.SideLong
	rejLow := false
	if loWick >= body*1.2 && last.Close > prev.Close {
		side = market.SideLong
		rejLow = true
	} else if upWick >= body*1.2 && last.Close < prev.Close {
		side = market.SideShort
	} else {
		return Signal{Name: s.Name()}
	}
	lvl, ok := levelAtHeaviestInRange(ctx.Snapshot, last.Low, last.High)
	if !ok || lvl <= 0 {
		return Signal{Name: s.Name()}
	}
	stop := last.Low
	if side == market.SideShort {
		stop = last.High
	}
	tp1, tp2 := rrTargets(last.Close, stop, side)
	conf := 0.50
	if rejLow && flowBias(ctx.Snapshot) > 0 {
		conf += 0.06
	}
	if !rejLow && flowBias(ctx.Snapshot) < 0 {
		conf += 0.06
	}
	return Signal{Active: tp1 > 0, Name: s.Name(), Side: side, Entry: last.Close, Stop: stop, TP1: tp1, TP2: tp2, Confidence: clamp01(conf), Tags: []string{"vp", "rejection"}, Invalidation: "rejection level fails", VPSetup: "rejection", VPLevel: lvl, Ts: last.Time}
}

func (s VPReversal) Eval(ctx Context) Signal {
	c := ctx.Candles
	if len(c) < 12 || ctx.Snapshot.POC <= 0 {
		return Signal{Name: s.Name()}
	}
	last := c[len(c)-1]
	prev := c[len(c)-2]
	side := market.SideLong
	if prev.Close > ctx.Snapshot.POC && last.Close < ctx.Snapshot.POC {
		side = market.SideShort
	} else if prev.Close < ctx.Snapshot.POC && last.Close > ctx.Snapshot.POC {
		side = market.SideLong
	} else {
		return Signal{Name: s.Name()}
	}
	stop := last.Low
	if side == market.SideShort {
		stop = last.High
	}
	tp1, tp2 := rrTargets(last.Close, stop, side)
	conf := 0.52
	if side == market.SideLong && flowBias(ctx.Snapshot) > 0 {
		conf += 0.06
	}
	if side == market.SideShort && flowBias(ctx.Snapshot) < 0 {
		conf += 0.06
	}
	return Signal{Active: tp1 > 0, Name: s.Name(), Side: side, Entry: last.Close, Stop: stop, TP1: tp1, TP2: tp2, Confidence: clamp01(conf), Tags: []string{"vp", "reversal", "role_flip"}, Invalidation: "flip level reclaimed", VPSetup: "reversal", VPLevel: ctx.Snapshot.POC, Ts: last.Time}
}

func (s DailyOpenSR) Eval(ctx Context) Signal {
	if len(ctx.Candles) < 20 {
		return Signal{Name: s.Name()}
	}
	loc := chicago()
	last := ctx.Candles[len(ctx.Candles)-1]
	dOpen, ok := levels.OpenForAnchorDay(ctx.Candles, last.Time, 7, loc)
	if !ok || dOpen <= 0 {
		return Signal{Name: s.Name()}
	}
	accN := 2
	above, below := 0, 0
	for i := len(ctx.Candles) - 1 - accN; i < len(ctx.Candles)-1; i++ {
		if ctx.Candles[i].Close > dOpen {
			above++
		}
		if ctx.Candles[i].Close < dOpen {
			below++
		}
	}
	tol := last.Close * 0.0008
	if tol <= 0 || absf(last.Close-dOpen) > tol {
		return Signal{Name: s.Name()}
	}
	side := market.SideLong
	if above < accN && below < accN {
		return Signal{Name: s.Name()}
	}
	if below >= accN {
		side = market.SideShort
	}
	stop := dOpen * (1 - 0.0035)
	if side == market.SideShort {
		stop = dOpen * (1 + 0.0035)
	}
	tp1, tp2 := rrTargets(last.Close, stop, side)
	conf := 0.58
	if side == market.SideLong && flowBias(ctx.Snapshot) > 0 {
		conf += 0.08
	}
	if side == market.SideShort && flowBias(ctx.Snapshot) < 0 {
		conf += 0.08
	}
	return Signal{Active: tp1 > 0, Name: s.Name(), Side: side, Entry: last.Close, Stop: stop, TP1: tp1, TP2: tp2, Confidence: clamp01(conf), Tags: []string{"ipa", "daily_open", "retest"}, Invalidation: "daily open rejection failed", Reasons: []string{"daily_open_acceptance", "daily_open_retest"}, Confluence: map[string]float64{"daily_open": 0.65}, SignalSource: []string{"institutional_pa"}, Ts: last.Time}
}

func (s PDLevelsRetest) Eval(ctx Context) Signal {
	c := ctx.Candles
	if len(c) < 40 {
		return Signal{Name: s.Name()}
	}
	loc := chicago()
	idx := len(c) - 1
	pl, ok := levels.PrevLevelsAt(c, idx, 16, loc)
	if !ok {
		return Signal{Name: s.Name()}
	}
	last := c[idx]
	accN := 2
	check := func(level float64, long bool) bool {
		if level <= 0 || len(c) < accN+3 {
			return false
		}
		start := idx - (accN + 1)
		if start < 0 {
			return false
		}
		breach := false
		for i := start; i < idx-accN; i++ {
			if long && c[i].High > level {
				breach = true
			}
			if !long && c[i].Low < level {
				breach = true
			}
		}
		if !breach {
			return false
		}
		accept := true
		for i := idx - accN; i < idx; i++ {
			if long && c[i].Close <= level {
				accept = false
			}
			if !long && c[i].Close >= level {
				accept = false
			}
		}
		tol := last.Close * 0.0010
		return accept && absf(last.Close-level) <= tol
	}
	side := market.SideLong
	trigger := 0.0
	reason := ""
	switch {
	case check(pl.PDH, true):
		side, trigger, reason = market.SideLong, pl.PDH, "pdh_breach_accept_retest"
	case check(pl.PDL, false):
		side, trigger, reason = market.SideShort, pl.PDL, "pdl_breach_accept_retest"
	case check(pl.PWH, true):
		side, trigger, reason = market.SideLong, pl.PWH, "pwh_breach_accept_retest"
	case check(pl.PWL, false):
		side, trigger, reason = market.SideShort, pl.PWL, "pwl_breach_accept_retest"
	default:
		return Signal{Name: s.Name()}
	}
	stop := trigger * (1 - 0.004)
	if side == market.SideShort {
		stop = trigger * (1 + 0.004)
	}
	tp1, tp2 := rrTargets(last.Close, stop, side)
	conf := 0.62
	sw := levels.DetectStrongWeakSwings(c, 2, 0.10)
	if len(sw) > 0 {
		latest := sw[len(sw)-1]
		if latest.Strong {
			conf += 0.05
		} else {
			conf -= 0.05
		}
	}
	dOpen, _ := levels.OpenForAnchorDay(c, last.Time, 7, loc)
	if side == market.SideLong && last.Close > dOpen {
		conf += 0.06
	}
	if side == market.SideShort && last.Close < dOpen {
		conf += 0.06
	}
	return Signal{Active: tp1 > 0, Name: s.Name(), Side: side, Entry: last.Close, Stop: stop, TP1: tp1, TP2: tp2, Confidence: clamp01(conf), Tags: []string{"ipa", "pd_levels", "acceptance", "retest"}, Invalidation: "level acceptance lost", Reasons: []string{reason}, Confluence: map[string]float64{"pd_levels": 0.70}, SignalSource: []string{"institutional_pa"}, Ts: last.Time}
}

func (s FailedAuctionMagnetStrategy) Eval(ctx Context) Signal {
	if len(ctx.Candles) < 12 {
		return Signal{Name: s.Name()}
	}
	m, ok := levels.DetectFailedAuctionMagnet(ctx.Candles, 30, 0.10)
	if !ok || m.Level <= 0 {
		return Signal{Name: s.Name()}
	}
	last := ctx.Candles[len(ctx.Candles)-1]
	side := market.SideLong
	if m.Level < last.Close {
		side = market.SideShort
	}
	stop := last.Close * (1 - 0.004)
	if side == market.SideShort {
		stop = last.Close * (1 + 0.004)
	}
	tp1, tp2 := rrTargets(last.Close, stop, side)
	if side == market.SideLong && m.Level > last.Close && m.Level < tp1 {
		tp1 = m.Level * 0.999
	}
	if side == market.SideShort && m.Level < last.Close && m.Level > tp1 {
		tp1 = m.Level * 1.001
	}
	conf := 0.56
	if m.Active {
		conf += 0.06
	}
	return Signal{Active: tp1 > 0, Name: s.Name(), Side: side, Entry: last.Close, Stop: stop, TP1: tp1, TP2: tp2, Confidence: clamp01(conf), Tags: []string{"ipa", "failed_auction", "magnet"}, Invalidation: "magnet fixed or invalidated", Reasons: []string{"failed_auction_magnet_pull"}, Confluence: map[string]float64{"failed_auction": 0.62}, SignalSource: []string{"institutional_pa"}, Ts: last.Time}
}

func (s VWAPConfluenceStrategy) Eval(ctx Context) Signal {
	c := ctx.Candles
	if len(c) < 15 {
		return Signal{Name: s.Name()}
	}
	v := rollingVWAP(c, minInt(40, len(c)))
	if v <= 0 {
		return Signal{Name: s.Name()}
	}
	last := c[len(c)-1]
	prev := c[len(c)-2]
	side := market.SideLong
	reason := "above_vwap_bias"
	flip := false
	if last.Close < v {
		side = market.SideShort
		reason = "below_vwap_bias"
	}
	if prev.Close < v && last.Close > v {
		side, reason, flip = market.SideLong, "vwap_reclaim", true
	}
	if prev.Close > v && last.Close < v {
		side, reason, flip = market.SideShort, "vwap_loss", true
	}
	stop := v * (1 - 0.003)
	if side == market.SideShort {
		stop = v * (1 + 0.003)
	}
	tp1, tp2 := rrTargets(last.Close, stop, side)
	conf := 0.55
	if flip {
		conf += 0.10
	}
	if side == market.SideLong && flowBias(ctx.Snapshot) > 0 {
		conf += 0.05
	}
	if side == market.SideShort && flowBias(ctx.Snapshot) < 0 {
		conf += 0.05
	}
	return Signal{Active: tp1 > 0, Name: s.Name(), Side: side, Entry: last.Close, Stop: stop, TP1: tp1, TP2: tp2, Confidence: clamp01(conf), Tags: []string{"ipa", "vwap", "confluence"}, Invalidation: "vwap role flip fails", Reasons: []string{reason}, Confluence: map[string]float64{"vwap": 0.66}, SignalSource: []string{"institutional_pa"}, Ts: last.Time}
}

func (s OpenDrive) Eval(ctx Context) Signal {
	cands := ctx.Candles
	if len(cands) < 4 {
		return Signal{Name: s.Name()}
	}
	loc := chicago()
	session := sessionName(cands[len(cands)-1].Time.In(loc))
	open := sessionOpen(cands, session, loc)
	if open <= 0 {
		return Signal{Name: s.Name()}
	}
	last3 := cands[len(cands)-3:]
	up, dn := 0, 0
	for _, c := range last3 {
		if c.Close > c.Open {
			up++
		} else if c.Close < c.Open {
			dn++
		}
	}
	side := market.SideLong
	if dn > up {
		side = market.SideShort
	}
	entry := cands[len(cands)-1].Close
	if side == market.SideLong && entry < open {
		return Signal{Name: s.Name()}
	}
	if side == market.SideShort && entry > open {
		return Signal{Name: s.Name()}
	}
	stop := open
	if side == market.SideLong {
		stop = minf(stop, minf(last3[0].Low, minf(last3[1].Low, last3[2].Low)))
	} else {
		stop = maxf(stop, maxf(last3[0].High, maxf(last3[1].High, last3[2].High)))
	}
	tp1, tp2 := rrTargets(entry, stop, side)
	return Signal{Active: tp1 > 0, Name: s.Name(), Side: side, Entry: entry, Stop: stop, TP1: tp1, TP2: tp2, Confidence: clamp01(0.55), Tags: []string{"open_drive", session}, Invalidation: "lose drive origin", Ts: cands[len(cands)-1].Time}
}

func chicago() *time.Location {
	loc, err := time.LoadLocation("America/Chicago")
	if err != nil {
		return time.UTC
	}
	return loc
}

func rollingVWAP(c []market.Candle, n int) float64 {
	if len(c) == 0 {
		return 0
	}
	if n <= 0 || n > len(c) {
		n = len(c)
	}
	start := len(c) - n
	num := 0.0
	den := 0.0
	for i := start; i < len(c); i++ {
		tp := (c[i].High + c[i].Low + c[i].Close) / 3.0
		num += tp * c[i].Volume
		den += c[i].Volume
	}
	if den <= 0 {
		return c[len(c)-1].Close
	}
	return num / den
}

func levelAtHeaviestInRange(snap features.Snapshot, low, high float64) (float64, bool) {
	bestVol := -1.0
	bestPx := 0.0
	for _, bin := range snap.ProfileBins {
		if bin.Price < low || bin.Price > high {
			continue
		}
		if bin.Volume > bestVol {
			bestVol = bin.Volume
			bestPx = bin.Price
		}
	}
	return bestPx, bestVol > 0
}

func firstSignificantOpposingLevel(entry float64, side market.Side, snap features.Snapshot, minShare float64) (float64, bool) {
	if entry <= 0 || snap.ProfileTotalVolume <= 0 {
		return 0, false
	}
	best := 0.0
	for _, bin := range snap.ProfileBins {
		if bin.Price <= 0 || bin.Volume <= 0 || bin.Volume/snap.ProfileTotalVolume < minShare {
			continue
		}
		if side == market.SideLong {
			if bin.Price <= entry {
				continue
			}
			if best == 0 || bin.Price < best {
				best = bin.Price
			}
		} else {
			if bin.Price >= entry {
				continue
			}
			if best == 0 || bin.Price > best {
				best = bin.Price
			}
		}
	}
	return best, best > 0
}

func stopByFixedPct(entry float64, side market.Side, stopPct float64) float64 {
	d := stopPct / 100.0
	if d <= 0 {
		d = 0.006
	}
	if side == market.SideLong {
		return entry * (1 - d)
	}
	return entry * (1 + d)
}

func stopByVP(sig Signal, snap features.Snapshot) float64 {
	if sig.Side == market.SideLong {
		if snap.ValueLow > 0 && snap.ValueLow < sig.Entry {
			return snap.ValueLow
		}
	}
	if sig.Side == market.SideShort && snap.ValueHigh > sig.Entry {
		return snap.ValueHigh
	}
	return 0
}

func targetsByVP(entry float64, side market.Side, snap features.Snapshot, minShare, frontRunPct float64) (float64, float64, float64) {
	lvl, ok := firstSignificantOpposingLevel(entry, side, snap, minShare)
	if !ok || lvl <= 0 {
		return 0, 0, 0
	}
	adj := frontRunPct / 100.0
	tp1 := lvl
	if side == market.SideLong {
		tp1 = lvl * (1 - adj)
	} else {
		tp1 = lvl * (1 + adj)
	}
	dist := rewardDistance(entry, tp1, side)
	tp2 := tp1
	if side == market.SideLong {
		tp2 = tp1 + dist
	} else {
		tp2 = tp1 - dist
	}
	return tp1, tp2, lvl
}

func riskDistance(entry, stop float64, side market.Side) float64 {
	if side == market.SideLong {
		return entry - stop
	}
	return stop - entry
}

func rewardDistance(entry, target float64, side market.Side) float64 {
	if side == market.SideLong {
		return target - entry
	}
	return entry - target
}

func sessionName(t time.Time) string {
	h := t.Hour()
	switch {
	case h >= 7 && h < 12:
		return "us"
	case h >= 2 && h < 7:
		return "eu"
	default:
		return "asia"
	}
}

func sessionOpen(c []market.Candle, session string, loc *time.Location) float64 {
	for i := len(c) - 1; i >= 0; i-- {
		if sessionName(c[i].Time.In(loc)) == session {
			for j := i; j >= 0; j-- {
				if sessionName(c[j].Time.In(loc)) != session {
					return c[j+1].Open
				}
				if j == 0 {
					return c[0].Open
				}
			}
		}
	}
	return 0
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func absf(x float64) float64 {
	if x < 0 {
		return -x
	}
	return x
}

func minf(a, b float64) float64 {
	if a < b {
		return a
	}
	return b
}

func maxf(a, b float64) float64 {
	if a > b {
		return a
	}
	return b
}
