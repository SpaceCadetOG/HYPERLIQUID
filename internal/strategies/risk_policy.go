package strategies

import "hyperliquid/internal/features"

type StopMode string
type TargetMode string

const (
	StopModeFixed  StopMode = "fixed"
	StopModeVP     StopMode = "vp"
	StopModeHybrid StopMode = "hybrid"

	TargetModeRR     TargetMode = "rr"
	TargetModeVP     TargetMode = "vp"
	TargetModeHybrid TargetMode = "hybrid"
)

type RiskPolicyConfig struct {
	StopMode             StopMode
	TargetMode           TargetMode
	FixedStopPct         float64
	VPMinShare           float64
	VPFrontRunPct        float64
	MinTargetDistancePct float64
	MinRMultiple         float64
}

func DefaultRiskPolicy() RiskPolicyConfig {
	return RiskPolicyConfig{
		StopMode:             StopModeHybrid,
		TargetMode:           TargetModeHybrid,
		FixedStopPct:         0.60,
		VPMinShare:           0.10,
		VPFrontRunPct:        0.10,
		MinTargetDistancePct: 0.10,
		MinRMultiple:         1.20,
	}
}

func ApplyRiskPolicy(sig Signal, snap features.Snapshot, cfg RiskPolicyConfig) Signal {
	if !sig.Active || sig.Entry <= 0 {
		return sig
	}
	stopFixed := stopByFixedPct(sig.Entry, sig.Side, cfg.FixedStopPct)
	stopVP := stopByVP(sig, snap)
	stop := sig.Stop
	switch cfg.StopMode {
	case StopModeFixed:
		stop = stopFixed
	case StopModeVP:
		if stopVP > 0 {
			stop = stopVP
		}
	case StopModeHybrid:
		stop = stopFixed
		if stopVP > 0 && riskDistance(sig.Entry, stopVP, sig.Side) > riskDistance(sig.Entry, stopFixed, sig.Side) {
			stop = stopVP
		}
	}
	if stop <= 0 {
		stop = sig.Stop
	}
	tp1RR, tp2RR := rrTargets(sig.Entry, stop, sig.Side)
	tp1VP, tp2VP, tgtLevel := targetsByVP(sig.Entry, sig.Side, snap, cfg.VPMinShare, cfg.VPFrontRunPct)
	tp1, tp2 := tp1RR, tp2RR
	switch cfg.TargetMode {
	case TargetModeVP:
		if tp1VP > 0 {
			tp1, tp2 = tp1VP, tp2VP
		}
	case TargetModeHybrid:
		if tp1VP > 0 {
			risk := riskDistance(sig.Entry, stop, sig.Side)
			rewardVP := rewardDistance(sig.Entry, tp1VP, sig.Side)
			if risk > 0 && rewardVP/risk >= cfg.MinRMultiple {
				tp1, tp2 = tp1VP, tp2VP
			}
		}
	}
	sig.Stop = stop
	sig.TP1 = tp1
	sig.TP2 = tp2
	sig.VPTargetLevel = tgtLevel
	sig.StopMode = string(cfg.StopMode)
	sig.TargetMode = string(cfg.TargetMode)
	return sig
}
