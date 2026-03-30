package strategies

import (
	"math"

	"hyperliquid/internal/features"
	"hyperliquid/internal/market"
)

const (
	wChangeLong  = 1.0
	wLogVolLong  = 8.0
	wLogOILong   = 3.0
	fundKLong    = 500.0
	wChangeShort = 1.0
	wLogVolShort = 8.0
	wLogOIShort  = 3.0
	fundKShort   = 500.0
)

func scoreSnapshot(s features.Snapshot, side market.Side, cfg RankConfig) market.RankedMarket {
	rawBase := baseRawScore(s, side)
	mom := evalMomentum(s, side, cfg)
	integ := evalIntegrity(s, cfg)
	exec := evalExecution(s, cfg)
	raw := round2(rawBase + mom.Score - integ.Penalty - exec.Penalty)
	norm := normalizeRawScore(raw, cfg.NormMinRaw, cfg.NormMaxRaw)
	scoreOut := raw
	if cfg.EnableNormalizedScore && cfg.UseNormalizedInScore {
		scoreOut = norm
	}
	conf := confidenceFromParts(cfg, integ.Completeness, mom.Agreement, penaltyFrac(exec.Penalty, cfg.ExecMaxPenalty))
	flags := append([]string{}, integ.Flags...)
	flags = append(flags, exec.Flags...)
	grade := gradeFromNormalized(norm)
	reason := s.LongReason
	whalePrice := s.LongWhalePrice
	whaleDist := s.LongWhaleDistanceBps
	stop := s.LongStopPrice
	target := s.LongTargetPrice
	confluenceLabel := s.LongConfluenceLabel
	if side == market.SideShort {
		reason = s.ShortReason
		whalePrice = s.ShortWhalePrice
		whaleDist = s.ShortWhaleDistanceBps
		stop = s.ShortStopPrice
		target = s.ShortTargetPrice
		confluenceLabel = s.ShortConfluenceLabel
	}
	reliabilityAdj := 0.0
	tradePriority := scoreOut * clamp(conf, 0.05, 1.0) * clamp(integ.Completeness, 0.05, 1.0)
	return market.RankedMarket{
		Symbol:            s.Symbol,
		Side:              side,
		Score:             scoreOut,
		RawScore:          raw,
		NormalizedScore:   norm,
		GradeLabel:        grade,
		ConfluenceLabel:   confluenceLabel,
		Completeness:      integ.Completeness,
		IntegrityPenalty:  round2(integ.Penalty),
		ExecutionPenalty:  round2(exec.Penalty),
		Last:              s.Last,
		Reason:            reason,
		ValueLow:          s.ValueLow,
		ValueHigh:         s.ValueHigh,
		POC:               s.POC,
		AVWAP:             s.AVWAP,
		AVWAPDistancePct:  s.AVWAPDistancePct,
		ADX:               s.ADX,
		ATR:               s.ATR,
		ATRPct:            s.ATRPct,
		BookSkew:          s.BookSkew,
		SpreadBps:         s.SpreadBps,
		EstSlippageBps:    s.EstSlippageBps,
		TopBookUSD:        s.TopBookUSD,
		Momentum5m:        round2(mom.M5m * 100),
		Momentum30m:       round2(mom.M30m * 100),
		Momentum4h:        round2(mom.M4h * 100),
		Momentum24h:       round2(mom.M24h * 100),
		MomentumAgreement: round2(mom.Agreement),
		RegimeTag:         mom.Regime,
		Confidence:        round2(conf),
		Uncertainty:       round2(1 - conf),
		DataFlags:         flags,
		ReliabilityAdj:    reliabilityAdj,
		TradePriority:     round2(tradePriority + reliabilityAdj),
		WhalePrice:        whalePrice,
		WhaleDistanceBps:  whaleDist,
		StopPrice:         stop,
		TargetPrice:       target,
		Funding:           s.Funding,
		ChangePct:         s.ChangePct,
		DayUTCChangePct:   s.DayUTCChangePct,
		WindowOpenPrice:   s.WindowOpenPrice,
		Eligibility:       s.Signals,
	}
}

func baseRawScore(s features.Snapshot, side market.Side) float64 {
	change := s.ChangePct
	if side == market.SideShort {
		change = math.Max(0, -change)
	} else {
		change = math.Max(0, change)
	}
	score := 0.0
	score += change * wChangeLong
	score += math.Log10(math.Max(s.VolumeUSD, 1)) * wLogVolLong
	if s.OIUSD > 0 {
		score += math.Log10(math.Max(s.OIUSD, 1)) * wLogOILong
	}
	funding := math.Abs(s.Funding)
	if side == market.SideShort {
		score = change*wChangeShort + math.Log10(math.Max(s.VolumeUSD, 1))*wLogVolShort
		if s.OIUSD > 0 {
			score += math.Log10(math.Max(s.OIUSD, 1)) * wLogOIShort
		}
		score -= funding * fundKShort
		return score
	}
	score -= funding * fundKLong
	return score
}

type momentumResult struct {
	M5m, M30m, M4h, M24h float64
	Agreement            float64
	Score                float64
	Regime               string
}

func evalMomentum(s features.Snapshot, side market.Side, cfg RankConfig) momentumResult {
	m5 := directional(s.Change5m, side)
	m30 := directional(s.Change30m, side)
	m4 := directional(s.Change4h, side)
	m24 := directional(s.ChangePct, side)
	weights := []float64{cfg.MomW5m, cfg.MomW30m, cfg.MomW4h, cfg.MomW24h}
	values := []float64{m5, m30, m4, m24}
	sumW := 0.0
	weighted := 0.0
	signRef := 0.0
	agreeW := 0.0
	for i, v := range values {
		w := weights[i]
		if w <= 0 {
			continue
		}
		sumW += w
		weighted += w * v
		if v != 0 {
			sgn := math.Copysign(1, v)
			if signRef == 0 {
				signRef = sgn
			}
			if sgn == signRef {
				agreeW += w
			}
		}
	}
	agreement := 0.0
	if sumW > 0 {
		agreement = clamp01(agreeW / sumW)
	}
	score := 0.0
	if cfg.EnableMomentum {
		score = clamp(weighted*cfg.MomMaxBoost, -cfg.MomMaxBoost, cfg.MomMaxBoost)
	}
	regime := "mixed"
	switch {
	case m4 > 0 && m30 > 0 && m5 > 0:
		regime = "continuation_long"
	case m4 > 0 && m30 > 0 && m5 < 0:
		regime = "countertrend_short"
	case m4 < 0 && m30 < 0 && m5 < 0:
		regime = "continuation_short"
	case m4 < 0 && m30 < 0 && m5 > 0:
		regime = "countertrend_long"
	}
	if agreement < 0.45 {
		regime = "mixed"
	}
	if agreement < 0.30 {
		regime = "exhaustion_risk"
	}
	return momentumResult{M5m: m5, M30m: m30, M4h: m4, M24h: m24, Agreement: agreement, Score: score, Regime: regime}
}

type integrityResult struct {
	Completeness float64
	Penalty      float64
	Flags        []string
}

func evalIntegrity(s features.Snapshot, cfg RankConfig) integrityResult {
	flags := make([]string, 0, 6)
	miss := 0.0
	weight := 0.0
	add := func(name string, cond bool, w float64) {
		if w <= 0 {
			return
		}
		weight += w
		if cond {
			miss += w
			flags = append(flags, name)
		}
	}
	add("missing_oi", s.OIUSD <= 0, 0.20)
	add("missing_funding", s.Funding == 0, 0.18)
	add("missing_spread", s.SpreadBps <= 0, 0.14)
	add("missing_topbook", s.TopBookUSD <= 0, 0.14)
	add("missing_est_slip", s.EstSlippageBps <= 0, 0.08)
	if weight <= 0 {
		return integrityResult{Completeness: 1, Flags: flags}
	}
	completeness := clamp01(1 - miss/weight)
	penalty := 0.0
	if cfg.EnableDataIntegrity {
		penalty = (1 - completeness) * cfg.IntegrityMaxPenalty
	}
	return integrityResult{Completeness: completeness, Penalty: penalty, Flags: flags}
}

type executionResult struct {
	Penalty float64
	Flags   []string
}

func evalExecution(s features.Snapshot, cfg RankConfig) executionResult {
	flags := make([]string, 0, 6)
	if !cfg.EnableExecPenalty {
		return executionResult{Flags: flags}
	}
	spreadPen := 0.0
	if s.SpreadBps <= 0 {
		spreadPen = cfg.ExecMaxPenalty * 0.35
		flags = append(flags, "spread_missing")
	} else if s.SpreadBps > cfg.SpreadBpsSoft {
		den := cfg.SpreadBpsHard - cfg.SpreadBpsSoft
		if den <= 0 {
			den = 1
		}
		spreadPen = clamp((s.SpreadBps-cfg.SpreadBpsSoft)/den, 0, 1) * (cfg.ExecMaxPenalty * 0.45)
		if s.SpreadBps > cfg.SpreadBpsHard {
			flags = append(flags, "spread_hard")
		}
	}
	depthPen := 0.0
	if s.TopBookUSD <= 0 {
		depthPen = cfg.ExecMaxPenalty * 0.30
		flags = append(flags, "topbook_missing")
	} else {
		minDepth := max(cfg.MinTopBookUSD, cfg.TargetClipUSD)
		if minDepth <= 0 {
			minDepth = 1
		}
		if s.TopBookUSD < minDepth {
			depthPen = (1 - clamp(s.TopBookUSD/minDepth, 0, 1)) * (cfg.ExecMaxPenalty * 0.35)
			flags = append(flags, "topbook_thin")
		}
	}
	slipPen := 0.0
	if s.EstSlippageBps <= 0 {
		slipPen = cfg.ExecMaxPenalty * 0.20
		flags = append(flags, "slippage_missing")
	} else if cfg.MaxEstSlipBps > 0 {
		slipPen = clamp(s.EstSlippageBps/cfg.MaxEstSlipBps, 0, 1) * (cfg.ExecMaxPenalty * 0.30)
		if s.EstSlippageBps > cfg.MaxEstSlipBps {
			flags = append(flags, "slippage_high")
		}
	}
	return executionResult{Penalty: clamp(spreadPen+depthPen+slipPen, 0, cfg.ExecMaxPenalty), Flags: flags}
}

func directional(v float64, side market.Side) float64 {
	x := v / 100.0
	if side == market.SideShort {
		x = -x
	}
	return clamp(x, -1, 1)
}

func gradeFromNormalized(score float64) string {
	switch {
	case score >= 92:
		return "A+"
	case score >= 85:
		return "A"
	case score >= 75:
		return "B"
	case score >= 62:
		return "C"
	case score >= 50:
		return "D"
	default:
		return "N/A"
	}
}

func confidenceFromParts(cfg RankConfig, completeness, agreement, execPenaltyFrac float64) float64 {
	c := 0.45 + 0.35*clamp01(completeness) + 0.20*clamp01(agreement) - 0.15*clamp01(execPenaltyFrac)
	if !cfg.EnableConfidence {
		return 1
	}
	return clamp(c, 0.05, 1.0)
}

func penaltyFrac(v, maxPenalty float64) float64 {
	if maxPenalty <= 0 {
		return 0
	}
	return clamp01(v / maxPenalty)
}

func normalizeRawScore(raw, minRaw, maxRaw float64) float64 {
	if maxRaw <= minRaw {
		return clamp(raw, 0, 100)
	}
	x := (raw - minRaw) / (maxRaw - minRaw)
	return math.Round(clamp01(x)*10000) / 100
}

func round2(x float64) float64 {
	return math.Round(x*100) / 100
}

func clamp(v, lo, hi float64) float64 {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return lo
	}
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func max(a, b float64) float64 {
	if a > b {
		return a
	}
	return b
}
