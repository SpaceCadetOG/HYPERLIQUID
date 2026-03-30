package gate

import (
	"fmt"
	"strings"

	"hyperliquid/internal/market"
)

type MTFSnapshot struct {
	TF      string
	EMAFast float64
	EMASlow float64
}

type Input struct {
	Symbol      string
	Side        string
	Grade       string
	Score       float64
	Slope       float64
	VolumeRatio float64
	MTF         []MTFSnapshot
	RegimeATR   float64
}

type MTFConfig struct {
	TFs     []string
	EMAFast int
	EMASlow int
	Use15m  bool
}

type RegimeConfig struct {
	ProxySymbol string
	MinATRPct   float64
}

type Config struct {
	MinGrade           string
	MinScore           float64
	MinSlope           float64
	RequireVolumeSpike bool
	MinVolumeRatio     float64
	RequireMTF         bool
	MTF                MTFConfig
	RequireRegime      bool
	Regime             RegimeConfig
}

type Decision struct {
	Allow   bool
	Reasons []string
}

func DefaultConfig() Config {
	return Config{
		MinGrade:           "B",
		MinScore:           75,
		MinSlope:           0.15,
		RequireVolumeSpike: true,
		MinVolumeRatio:     1.5,
		RequireMTF:         false,
		MTF: MTFConfig{
			TFs:     []string{"1m", "5m"},
			EMAFast: 8,
			EMASlow: 20,
			Use15m:  false,
		},
		RequireRegime: false,
		Regime: RegimeConfig{
			ProxySymbol: "BTC",
			MinATRPct:   0.8,
		},
	}
}

func Filter(in []market.RankedMarket, cfg Config) []market.RankedMarket {
	out := make([]market.RankedMarket, 0, len(in))
	for _, item := range in {
		if item.Score >= cfg.MinScore {
			out = append(out, item)
		}
	}
	return out
}

func Evaluate(in Input, cfg Config) Decision {
	reasons := make([]string, 0, 8)
	if gradeValue(in.Grade) < gradeValue(cfg.MinGrade) {
		reasons = append(reasons, fmt.Sprintf("grade_below_min:%s<%s", in.Grade, cfg.MinGrade))
	}
	if in.Score < cfg.MinScore {
		reasons = append(reasons, fmt.Sprintf("score_below_min:%.2f<%.2f", in.Score, cfg.MinScore))
	}
	if in.Slope < cfg.MinSlope {
		reasons = append(reasons, fmt.Sprintf("slope_below_min:%.3f<%.3f", in.Slope, cfg.MinSlope))
	}
	if cfg.RequireVolumeSpike {
		if in.VolumeRatio <= 0 {
			reasons = append(reasons, "volume_ratio_missing")
		} else if in.VolumeRatio < cfg.MinVolumeRatio {
			reasons = append(reasons, fmt.Sprintf("volume_ratio_below_min:%.2f<%.2f", in.VolumeRatio, cfg.MinVolumeRatio))
		}
	}
	if cfg.RequireMTF && !mtfAligned(in.Side, in.MTF, cfg.MTF) {
		reasons = append(reasons, "mtf_misaligned")
	}
	if cfg.RequireRegime && in.RegimeATR < cfg.Regime.MinATRPct {
		reasons = append(reasons, fmt.Sprintf("regime_below_min_atr:%.2f<%.2f", in.RegimeATR, cfg.Regime.MinATRPct))
	}
	return Decision{Allow: len(reasons) == 0, Reasons: reasons}
}

func mtfAligned(side string, snaps []MTFSnapshot, cfg MTFConfig) bool {
	if len(snaps) == 0 {
		return false
	}
	required := map[string]struct{}{}
	for _, tf := range cfg.TFs {
		tf = strings.ToLower(strings.TrimSpace(tf))
		if tf != "" {
			required[tf] = struct{}{}
		}
	}
	if cfg.Use15m {
		required["15m"] = struct{}{}
	}
	if len(required) == 0 {
		required["1m"] = struct{}{}
		required["5m"] = struct{}{}
	}
	good := 0
	for _, s := range snaps {
		tf := strings.ToLower(strings.TrimSpace(s.TF))
		if _, ok := required[tf]; !ok {
			continue
		}
		if strings.EqualFold(side, "BUY") || strings.EqualFold(side, "LONG") {
			if s.EMAFast > s.EMASlow {
				good++
			}
		} else if s.EMAFast < s.EMASlow {
			good++
		}
	}
	return good == len(required)
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
