package levels

import (
	"time"

	"hyperliquid/internal/market"
)

type PrevLevels struct {
	PDH float64
	PDL float64
	PWH float64
	PWL float64
}

type FailedAuctionMagnet struct {
	Level     float64
	Direction string
	CreatedAt time.Time
	FixedAt   time.Time
	Active    bool
}

type SwingStrength struct {
	Index         int
	Price         float64
	IsHigh        bool
	Strength      float64
	Tests         int
	FastRejection bool
	Strong        bool
}

func OpenForAnchorDay(candles []market.Candle, ts time.Time, anchorHour int, loc *time.Location) (float64, bool) {
	if len(candles) == 0 {
		return 0, false
	}
	if loc == nil {
		loc = time.UTC
	}
	want := anchorDayKey(ts.In(loc), anchorHour)
	for i := 0; i < len(candles); i++ {
		lt := candles[i].Time.In(loc)
		if anchorDayKey(lt, anchorHour) == want {
			return candles[i].Open, true
		}
	}
	return 0, false
}

func PrevLevelsAt(candles []market.Candle, idx int, dayAnchorHour int, loc *time.Location) (PrevLevels, bool) {
	if idx < 0 || idx >= len(candles) || len(candles) < 2 {
		return PrevLevels{}, false
	}
	if loc == nil {
		loc = time.UTC
	}
	cur := candles[idx].Time.In(loc)
	curDay := anchorDayKey(cur, dayAnchorHour)
	prevDay := anchorDay(cur, dayAnchorHour).AddDate(0, 0, -1).Format("2006-01-02")
	prevWeekAnchor := anchorDay(cur, dayAnchorHour).AddDate(0, 0, -7)
	prevWY, prevWW := prevWeekAnchor.ISOWeek()

	pl := PrevLevels{}
	dayFound := false
	weekFound := false
	for i := 0; i <= idx; i++ {
		t := candles[i].Time.In(loc)
		dk := anchorDayKey(t, dayAnchorHour)
		if dk == curDay {
			continue
		}
		if dk == prevDay {
			if !dayFound || candles[i].High > pl.PDH {
				pl.PDH = candles[i].High
			}
			if !dayFound || candles[i].Low < pl.PDL {
				pl.PDL = candles[i].Low
			}
			dayFound = true
		}
		ad := anchorDay(t, dayAnchorHour)
		yw, ww := ad.ISOWeek()
		if yw == prevWY && ww == prevWW {
			if !weekFound || candles[i].High > pl.PWH {
				pl.PWH = candles[i].High
			}
			if !weekFound || candles[i].Low < pl.PWL {
				pl.PWL = candles[i].Low
			}
			weekFound = true
		}
	}
	if !dayFound && !weekFound {
		return PrevLevels{}, false
	}
	return pl, true
}

func DetectFailedAuctionMagnet(c []market.Candle, lookback int, tolPct float64) (FailedAuctionMagnet, bool) {
	if len(c) < 5 {
		return FailedAuctionMagnet{}, false
	}
	if lookback <= 0 || lookback > len(c)-1 {
		lookback = minInt(len(c)-1, 20)
	}
	if tolPct <= 0 {
		tolPct = 0.10
	}
	start := len(c) - lookback - 1
	if start < 0 {
		start = 0
	}
	last := c[len(c)-1]
	for i := start; i < len(c)-2; i++ {
		for j := i + 1; j < len(c)-1; j++ {
			hiTol := c[i].High * tolPct / 100.0
			loTol := c[i].Low * tolPct / 100.0
			if absf(c[i].High-c[j].High) <= hiTol && last.Close < c[j].Low {
				m := FailedAuctionMagnet{Level: (c[i].High + c[j].High) / 2.0, Direction: "down", CreatedAt: c[j].Time, Active: true}
				if fixedAt, ok := magnetFixedAt(c[j+1:], m.Level, true); ok {
					m.FixedAt = fixedAt
					m.Active = false
				}
				return m, true
			}
			if absf(c[i].Low-c[j].Low) <= loTol && last.Close > c[j].High {
				m := FailedAuctionMagnet{Level: (c[i].Low + c[j].Low) / 2.0, Direction: "up", CreatedAt: c[j].Time, Active: true}
				if fixedAt, ok := magnetFixedAt(c[j+1:], m.Level, false); ok {
					m.FixedAt = fixedAt
					m.Active = false
				}
				return m, true
			}
		}
	}
	return FailedAuctionMagnet{}, false
}

func DetectStrongWeakSwings(c []market.Candle, leftRight int, testTolPct float64) []SwingStrength {
	if len(c) < 2*leftRight+1 {
		return nil
	}
	if leftRight <= 0 {
		leftRight = 2
	}
	if testTolPct <= 0 {
		testTolPct = 0.10
	}
	out := make([]SwingStrength, 0, 8)
	for i := leftRight; i < len(c)-leftRight; i++ {
		hi := c[i].High
		lo := c[i].Low
		isHi, isLo := true, true
		for j := i - leftRight; j <= i+leftRight; j++ {
			if j == i {
				continue
			}
			if c[j].High >= hi {
				isHi = false
			}
			if c[j].Low <= lo {
				isLo = false
			}
		}
		if !isHi && !isLo {
			continue
		}
		isHigh := isHi
		lvl := hi
		if !isHigh {
			lvl = lo
		}
		tests := 0
		tol := lvl * (testTolPct / 100.0)
		if tol <= 0 {
			tol = 1e-8
		}
		for k := i + 1; k < len(c); k++ {
			if absf(c[k].High-lvl) <= tol || absf(c[k].Low-lvl) <= tol {
				tests++
			}
		}
		body := absf(c[i].Close - c[i].Open)
		rng := c[i].High - c[i].Low
		wick := 0.0
		if isHigh {
			wick = c[i].High - maxf(c[i].Open, c[i].Close)
		} else {
			wick = minf(c[i].Open, c[i].Close) - c[i].Low
		}
		fastRej := wick > body && rng > 0
		strength := 0.55
		if fastRej {
			strength += 0.20
		}
		if tests > 0 {
			strength -= minf(0.25, float64(tests)*0.05)
		}
		if strength < 0 {
			strength = 0
		}
		if strength > 1 {
			strength = 1
		}
		out = append(out, SwingStrength{
			Index:         i,
			Price:         lvl,
			IsHigh:        isHigh,
			Strength:      strength,
			Tests:         tests,
			FastRejection: fastRej,
			Strong:        strength >= 0.60,
		})
	}
	return out
}

func anchorDayKey(t time.Time, anchorHour int) string {
	at := time.Date(t.Year(), t.Month(), t.Day(), anchorHour, 0, 0, 0, t.Location())
	if t.Before(at) {
		t = t.AddDate(0, 0, -1)
	}
	return t.Format("2006-01-02")
}

func anchorDay(t time.Time, anchorHour int) time.Time {
	a := time.Date(t.Year(), t.Month(), t.Day(), anchorHour, 0, 0, 0, t.Location())
	if t.Before(a) {
		return a.AddDate(0, 0, -1)
	}
	return a
}

func magnetFixedAt(c []market.Candle, lvl float64, breakAbove bool) (time.Time, bool) {
	for _, x := range c {
		if breakAbove {
			if x.Close > lvl {
				return x.Time, true
			}
		} else if x.Close < lvl {
			return x.Time, true
		}
	}
	return time.Time{}, false
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
