package ta

import (
	"math"
	"strings"
)

type ConfluenceResult struct {
	Score float64
	Label string
	Notes []string
}

func ComputeConfluence(tr TrendResult, ef EffortResult, ob OBContext, side string) ConfluenceResult {
	side = strings.ToLower(side)
	if side != "short" {
		side = "long"
	}
	const trendW = 0.45
	const effortW = 0.35
	const obW = 0.20

	trendNorm := clamp01(tr.TrendScore / 80.0)
	effortNorm := clamp01(ef.EffortScore / 60.0)
	obNorm := 0.0
	switch side {
	case "long":
		obNorm = clamp01(0.5 + 0.5*clampSym(ob.Imbalance))
	case "short":
		obNorm = clamp01(0.5 - 0.5*clampSym(ob.Imbalance))
	}

	score := 100.0 * (trendW*trendNorm + effortW*effortNorm + obW*obNorm)
	notes := make([]string, 0, 6)
	if tr.ABOVE() && tr.SLOPES_UP() && tr.Bias == "bull" {
		notes = append(notes, "trend: ema9>ema21 & above VWAP")
	}
	if tr.Slope9 > 0 && tr.Slope21 > 0 {
		notes = append(notes, "trend: ema slope rising")
	}
	if tr.Bias == "bear" && side == "short" {
		notes = append(notes, "trend: HTF aligns with short")
	}
	if ef.SpikeDensity >= 0.05 {
		notes = append(notes, "effort: spike cluster")
	}
	if ef.EMAvol > ef.MeanVol {
		notes = append(notes, "effort: volume EMA rising")
	}
	if side == "long" {
		if ob.Imbalance > 0.1 {
			notes = append(notes, "ob: bid support > asks")
		} else if ob.Imbalance < -0.1 {
			notes = append(notes, "ob: ask pressure")
		}
		if ob.TopAskWall != nil && ob.TopBidWall != nil && ob.TopAskWall.Rank <= 3 && ob.TopAskWall.Size > ob.TopBidWall.Size*1.5 {
			notes = append(notes, "ob: large ask wall near")
		}
		if ob.Imbalance > 0.20 {
			score += 3
		}
		if ob.TopBidWall != nil && ob.TopBidWall.Rank <= 2 {
			score += 2
		}
	} else {
		if ob.Imbalance < -0.1 {
			notes = append(notes, "ob: ask supply > bids")
		} else if ob.Imbalance > 0.1 {
			notes = append(notes, "ob: bid absorption risk")
		}
		if ob.TopBidWall != nil && ob.TopAskWall != nil && ob.TopBidWall.Rank <= 3 && ob.TopBidWall.Size > ob.TopAskWall.Size*1.5 {
			notes = append(notes, "ob: large bid wall near")
		}
		if ob.Imbalance < -0.20 {
			score += 3
		}
		if ob.TopAskWall != nil && ob.TopAskWall.Rank <= 2 {
			score += 2
		}
	}

	score = round2(score)
	return ConfluenceResult{Score: score, Label: grade(score), Notes: notes}
}

func grade(s float64) string {
	switch {
	case s >= 75:
		return "A"
	case s >= 60:
		return "B"
	default:
		return "C"
	}
}

func clampSym(x float64) float64 {
	if x > 1 {
		return 1
	}
	if x < -1 {
		return -1
	}
	return x
}

func round2(x float64) float64 {
	return math.Round(x*100) / 100
}

func clamp01(x float64) float64 {
	if x < 0 {
		return 0
	}
	if x > 1 {
		return 1
	}
	return x
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

func (t TrendResult) ABOVE() bool     { return t.EMARatio >= 1.0 && t.AboveVWAP >= 0.5 }
func (t TrendResult) SLOPES_UP() bool { return t.Slope9 > 0 && t.Slope21 > 0 }
