package execution

import (
	"math"
	"strings"
)

type Config struct {
	FrontRunPct            float64
	NoFollowThroughBars    int
	NoFollowThroughMinMFER float64
	NoFollowThroughMinMAER float64
	WeakFlowArmBER         float64
	LiqSpikePartialPct     float64
	StallBarsForTighten    int
	StallTightenToR        float64
}

type Manager struct {
	cfg Config
}

type ProtectInput struct {
	Side          string
	Entry         float64
	Stop          float64
	Mark          float64
	MFER          float64
	MAER          float64
	BarsHeld      int
	StallBars     int
	WeakFlow      bool
	NearFriction  bool
	LiqSpike      bool
	UnrealizedPct float64
}

type ProtectDecision struct {
	Reason         string
	MoveStopToBE   bool
	TightenStop    bool
	TightenToPrice float64
	PartialExitPct float64
	FullExit       bool
}

func NewManager(cfg Config) *Manager {
	if cfg.FrontRunPct <= 0 {
		cfg.FrontRunPct = 0.001
	}
	if cfg.NoFollowThroughBars <= 0 {
		cfg.NoFollowThroughBars = 8
	}
	if cfg.NoFollowThroughMinMFER <= 0 {
		cfg.NoFollowThroughMinMFER = 0.25
	}
	if cfg.NoFollowThroughMinMAER <= 0 {
		cfg.NoFollowThroughMinMAER = 0.70
	}
	if cfg.WeakFlowArmBER <= 0 {
		cfg.WeakFlowArmBER = 0.45
	}
	if cfg.LiqSpikePartialPct <= 0 || cfg.LiqSpikePartialPct > 1 {
		cfg.LiqSpikePartialPct = 0.35
	}
	if cfg.StallBarsForTighten <= 0 {
		cfg.StallBarsForTighten = 3
	}
	if cfg.StallTightenToR <= 0 {
		cfg.StallTightenToR = 0.20
	}
	return &Manager{cfg: cfg}
}

func (m *Manager) EvaluateProtect(in ProtectInput) ProtectDecision {
	dec := ProtectDecision{}
	if in.Entry <= 0 || in.Stop <= 0 || in.Mark <= 0 {
		return dec
	}
	if in.LiqSpike && in.UnrealizedPct > 0 {
		dec.PartialExitPct = m.cfg.LiqSpikePartialPct
		dec.Reason = "LIQ_SPIKE_PARTIAL"
	}
	if in.BarsHeld >= m.cfg.NoFollowThroughBars &&
		in.MFER < m.cfg.NoFollowThroughMinMFER &&
		in.MAER >= m.cfg.NoFollowThroughMinMAER {
		dec.FullExit = true
		dec.Reason = "NO_FOLLOW_THROUGH"
		return dec
	}
	if in.WeakFlow && in.MFER >= m.cfg.WeakFlowArmBER {
		dec.MoveStopToBE = true
		dec.Reason = "WEAK_FLOW_BE"
	}
	if in.StallBars >= m.cfg.StallBarsForTighten && in.NearFriction {
		dec.TightenStop = true
		dec.TightenToPrice = tightenToR(in.Side, in.Entry, in.Stop, m.cfg.StallTightenToR)
		if dec.Reason == "" {
			dec.Reason = "STALL_NEAR_FRICTION"
		}
	}
	return dec
}

func tightenToR(side string, entry, stop, r float64) float64 {
	risk := math.Abs(entry - stop)
	if risk <= 0 || r <= 0 {
		return stop
	}
	if strings.EqualFold(side, "BUY") || strings.EqualFold(side, "LONG") {
		return entry - risk*r
	}
	return entry + risk*r
}
