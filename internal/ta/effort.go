package ta

import (
	"math"

	"hyperliquid/internal/market"
)

type VolSpike struct {
	I int
	T int64
	V float64
	Z float64
}

type EffortResult struct {
	Symbol       string
	Count        int
	VWAP         float64
	EMAvol       float64
	MeanVol      float64
	StdVol       float64
	SpikeDensity float64
	Spikes       []VolSpike
	TrendBias    string
	EffortScore  float64
}

func ComputeEffort(symbol string, bars []market.Candle, win int, zmin, vmin float64) EffortResult {
	n := len(bars)
	if n == 0 {
		return EffortResult{Symbol: symbol}
	}
	vwap := VWAP(bars)
	vs := make([]float64, n)
	for i, b := range bars {
		tp := (b.High + b.Low + b.Close) / 3.0
		vs[i] = tp * b.Volume
	}
	emavSeq := emaVolSeq(vs, max(2, win))
	emav := 0.0
	if len(emavSeq) > 0 {
		emav = emavSeq[len(emavSeq)-1]
	}
	meanV, stdV := meanStd(vs)
	spikes := make([]VolSpike, 0)
	if stdV > 0 {
		for i, b := range bars {
			z := (vs[i] - meanV) / stdV
			if z >= zmin && vs[i] >= vmin {
				spikes = append(spikes, VolSpike{I: i, T: b.Time.UnixMilli(), V: vs[i], Z: z})
			}
		}
	}
	spikeDensity := float64(len(spikes)) / float64(n)
	trendBias := "neutral"
	if bars[n-1].Close > vwap {
		trendBias = "bull"
	} else if bars[n-1].Close < vwap {
		trendBias = "bear"
	}
	emu := 0.0
	if meanV > 0 {
		emu = (emav - meanV) / meanV
	}
	emuScore := clamp01(emu/0.5) * 40.0
	spkScore := clamp01(spikeDensity/0.08) * 60.0
	score := emuScore + spkScore
	if score > 100 {
		score = 100
	}
	return EffortResult{
		Symbol:       symbol,
		Count:        n,
		VWAP:         vwap,
		EMAvol:       emav,
		MeanVol:      meanV,
		StdVol:       stdV,
		SpikeDensity: spikeDensity,
		Spikes:       spikes,
		TrendBias:    trendBias,
		EffortScore:  score,
	}
}

func emaVolSeq(vs []float64, win int) []float64 {
	if len(vs) == 0 || win <= 1 {
		return nil
	}
	k := 2.0 / (float64(win) + 1.0)
	out := make([]float64, len(vs))
	out[0] = vs[0]
	for i := 1; i < len(vs); i++ {
		out[i] = k*vs[i] + (1-k)*out[i-1]
	}
	return out
}

func meanStd(vs []float64) (float64, float64) {
	n := float64(len(vs))
	if n == 0 {
		return 0, 0
	}
	var s float64
	for _, v := range vs {
		s += v
	}
	m := s / n
	var s2 float64
	for _, v := range vs {
		d := v - m
		s2 += d * d
	}
	return m, math.Sqrt(s2 / n)
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
