package strategies

import (
	"time"

	"hyperliquid/internal/features"
	"hyperliquid/internal/market"
)

type Signal struct {
	Active          bool
	Name            string
	Side            market.Side
	Entry           float64
	Stop            float64
	TP1             float64
	TP2             float64
	Confidence      float64
	Tags            []string
	Invalidation    string
	VPSetup         string
	VPLevel         float64
	VPTargetLevel   float64
	StopMode        string
	TargetMode      string
	RejectReason    string
	Reasons         []string
	Confluence      map[string]float64
	ConfluenceScore ConfluenceScore
	RegimeTag       string
	SignalSource    []string
	Ts              time.Time
}

type ConfluenceScore struct {
	TotalScore     float64
	StrategyScore  float64
	FlowScore      float64
	StructureScore float64
	Reasons        []string
	Approved       bool
}

type Context struct {
	Symbol       string
	TF           string
	ScannerScore float64
	ScannerGrade string
	ScoreSlope   float64
	ScanAccel    float64
	Snapshot     features.Snapshot
	Candles      []market.Candle
}

type Strategy interface {
	Name() string
	Eval(ctx Context) Signal
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

func rrTargets(entry, stop float64, side market.Side) (float64, float64) {
	r := entry - stop
	if side == market.SideShort {
		r = stop - entry
	}
	if r <= 0 {
		return 0, 0
	}
	if side == market.SideLong {
		return entry + 2*r, entry + 3*r
	}
	return entry - 2*r, entry - 3*r
}
