package ta

import (
	"math"

	"hyperliquid/internal/market"
)

type TrendResult struct {
	Symbol     string
	EMA9       float64
	EMA21      float64
	EMASpread  float64
	EMARatio   float64
	Slope9     float64
	Slope21    float64
	AboveVWAP  float64
	Bias       string
	TrendScore float64
}

func VWAP(bars []market.Candle) float64 {
	var pv, v float64
	for _, b := range bars {
		tp := (b.High + b.Low + b.Close) / 3.0
		pv += tp * b.Volume
		v += b.Volume
	}
	if v == 0 {
		return 0
	}
	return pv / v
}

func TrendMetrics(symbol string, bars []market.Candle, vwap float64) TrendResult {
	n := len(bars)
	if n == 0 {
		return TrendResult{Symbol: symbol}
	}
	cls := make([]float64, n)
	for i, b := range bars {
		cls[i] = b.Close
	}
	e9 := emaSeq(cls, 9)
	e21 := emaSeq(cls, 21)
	ema9, ema21 := 0.0, 0.0
	if len(e9) > 0 {
		ema9 = e9[len(e9)-1]
	}
	if len(e21) > 0 {
		ema21 = e21[len(e21)-1]
	}
	slope9, slope21 := 0.0, 0.0
	if len(e9) >= 2 {
		slope9 = e9[len(e9)-1] - e9[len(e9)-2]
	}
	if len(e21) >= 2 {
		slope21 = e21[len(e21)-1] - e21[len(e21)-2]
	}
	above := 0
	for _, b := range bars {
		if b.Close > vwap {
			above++
		}
	}
	aboveVWAP := float64(above) / float64(n)
	last := cls[n-1]
	emaRatio := 0.0
	if ema21 != 0 {
		emaRatio = ema9 / ema21
	}
	emaMag := math.Abs(emaRatio - 1.0)
	emaScore := clamp01(emaMag/0.004) * 45.0
	slopeUnit := 5.0
	slopeMag := (math.Abs(slope9) + math.Abs(slope21)) / (2.0 * slopeUnit)
	slopeScore := clamp01(slopeMag) * 45.0
	distVWAP := 0.0
	if vwap > 0 && last > 0 {
		distVWAP = math.Abs(last-vwap) / last
	}
	vwapScore := clamp01(distVWAP/0.002) * 10.0
	score := clamp(emaScore+slopeScore+vwapScore, 0, 100)
	bias := "neutral"
	switch {
	case ema9 > ema21 && last > vwap && slope9 >= 0:
		bias = "bull"
	case ema9 < ema21 && last < vwap && slope9 <= 0:
		bias = "bear"
	}
	return TrendResult{
		Symbol:     symbol,
		EMA9:       ema9,
		EMA21:      ema21,
		EMASpread:  math.Abs(ema9 - ema21),
		EMARatio:   emaRatio,
		Slope9:     slope9,
		Slope21:    slope21,
		AboveVWAP:  aboveVWAP,
		Bias:       bias,
		TrendScore: score,
	}
}

func emaSeq(closes []float64, win int) []float64 {
	if len(closes) == 0 || win <= 1 {
		return nil
	}
	k := 2.0 / (float64(win) + 1.0)
	out := make([]float64, len(closes))
	out[0] = closes[0]
	for i := 1; i < len(closes); i++ {
		out[i] = k*closes[i] + (1-k)*out[i-1]
	}
	return out
}
