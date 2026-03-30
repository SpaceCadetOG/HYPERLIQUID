package market

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
)

const (
	defaultMinLongChange          = 1.5
	defaultMinShortChange         = -3.0
	defaultMinVolUSD              = 1_000_000
	defaultMinReversalScore       = 70
	defaultMinReversalConfidence  = 0.55
	defaultEnableReversalOverride = false
)

type EligibilityConfig struct {
	MinLongChange          float64
	MinShortChange         float64
	MinVolUSD              float64
	MinReversalScore       float64
	MinReversalConfidence  float64
	EnableReversalOverride bool
}

var (
	eligibilityCfgMu sync.RWMutex
	eligibilityCfg   *EligibilityConfig
)

func currentEligibilityConfig() EligibilityConfig {
	eligibilityCfgMu.RLock()
	if eligibilityCfg != nil {
		cfg := *eligibilityCfg
		eligibilityCfgMu.RUnlock()
		return cfg
	}
	eligibilityCfgMu.RUnlock()
	cfg := EligibilityConfig{
		MinLongChange:          envFloat("SCAN_MIN_LONG_CHANGE", defaultMinLongChange),
		MinShortChange:         envFloat("SCAN_MIN_SHORT_CHANGE", defaultMinShortChange),
		MinVolUSD:              envFloat("SCAN_MIN_VOL_USD", defaultMinVolUSD),
		MinReversalScore:       envFloat("SCAN_MIN_REVERSAL_SCORE", defaultMinReversalScore),
		MinReversalConfidence:  envFloat("SCAN_MIN_REVERSAL_CONFIDENCE", defaultMinReversalConfidence),
		EnableReversalOverride: envBool("SCAN_ENABLE_REVERSAL_OVERRIDE", defaultEnableReversalOverride),
	}
	eligibilityCfgMu.Lock()
	eligibilityCfg = &cfg
	eligibilityCfgMu.Unlock()
	return cfg
}

func EligibleLong(m Scored) (bool, []string) {
	cfg := currentEligibilityConfig()
	reasons := []string{}
	if m.VolumeUSD < cfg.MinVolUSD {
		return false, []string{fmt.Sprintf("Vol < $%.0f", cfg.MinVolUSD)}
	}
	reasons = append(reasons, "volume_ok")
	if m.Change24h >= cfg.MinLongChange {
		return true, append(reasons, fmt.Sprintf("delta >= %.1f", cfg.MinLongChange))
	}
	if cfg.EnableReversalOverride && longMomentumReversal(m.Signals, m.LastPrice, m.OpenPrice) && reversalQualityOK(m, cfg) {
		return true, append(reasons, "structured_long_reversal")
	}
	return false, append(reasons, fmt.Sprintf("Δ%% < %.1f", cfg.MinLongChange), "no_long_reversal")
}

func EligibleShort(m Scored) (bool, []string) {
	cfg := currentEligibilityConfig()
	reasons := []string{}
	if m.VolumeUSD < cfg.MinVolUSD {
		return false, []string{fmt.Sprintf("Vol < $%.0f", cfg.MinVolUSD)}
	}
	reasons = append(reasons, "volume_ok")
	if m.Change24h <= cfg.MinShortChange {
		return true, append(reasons, fmt.Sprintf("delta <= %.1f", cfg.MinShortChange))
	}
	if cfg.EnableReversalOverride && shortMomentumReversal(m.Signals, m.LastPrice, m.OpenPrice) && reversalQualityOK(m, cfg) {
		return true, append(reasons, "structured_short_reversal")
	}
	return false, append(reasons, fmt.Sprintf("Δ%% > %.1f", cfg.MinShortChange), "no_short_reversal")
}

func reversalQualityOK(m Scored, cfg EligibilityConfig) bool {
	if m.Score < cfg.MinReversalScore {
		return false
	}
	if m.Confidence > 0 && m.Confidence < cfg.MinReversalConfidence {
		return false
	}
	return true
}

func shortMomentumReversal(signals EligibilitySignals, lastPrice, openPrice float64) bool {
	if signals.ShortTermMomentumDown ||
		signals.AskPressure ||
		signals.AcceptanceBelowValue ||
		signals.BelowAVWAP ||
		signals.ResponsiveOfferNearValue ||
		signals.WhaleOfferNearby {
		return true
	}
	if lastPrice > 0 && openPrice > 0 && lastPrice < openPrice {
		return true
	}
	return false
}

func longMomentumReversal(signals EligibilitySignals, lastPrice, openPrice float64) bool {
	if signals.ShortTermMomentumUp ||
		signals.BidPressure ||
		signals.AcceptanceAboveValue ||
		signals.AboveAVWAP ||
		signals.ResponsiveBidNearValue ||
		signals.WhaleBidNearby {
		return true
	}
	if lastPrice > 0 && openPrice > 0 && lastPrice > openPrice {
		return true
	}
	return false
}

func envFloat(key string, def float64) float64 {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return def
	}
	f, err := strconv.ParseFloat(v, 64)
	if err != nil {
		return def
	}
	return f
}

func envBool(key string, def bool) bool {
	v := strings.TrimSpace(strings.ToLower(os.Getenv(key)))
	if v == "" {
		return def
	}
	switch v {
	case "1", "true", "yes", "y", "on":
		return true
	case "0", "false", "no", "n", "off":
		return false
	default:
		return def
	}
}
