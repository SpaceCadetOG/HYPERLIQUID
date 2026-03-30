package strategies

import (
	"os"
	"strconv"
	"strings"
	"sync"
)

type RankConfig struct {
	EnableDataIntegrity bool
	MinCompleteness     float64
	IntegrityMaxPenalty float64

	EnableExecPenalty bool
	SpreadBpsSoft     float64
	SpreadBpsHard     float64
	MinTopBookUSD     float64
	TargetClipUSD     float64
	MaxEstSlipBps     float64
	ExecMaxPenalty    float64

	EnableNormalizedScore bool
	UseNormalizedInScore  bool
	NormMinRaw            float64
	NormMaxRaw            float64

	EnableMomentum bool
	MomW5m         float64
	MomW30m        float64
	MomW4h         float64
	MomW24h        float64
	MomMaxBoost    float64

	EnableConfidence bool
	MinConfidence    float64
}

var (
	rankCfgMu sync.RWMutex
	rankCfg   *RankConfig
)

func currentRankConfig() RankConfig {
	rankCfgMu.RLock()
	if rankCfg != nil {
		cfg := *rankCfg
		rankCfgMu.RUnlock()
		return cfg
	}
	rankCfgMu.RUnlock()
	cfg := loadRankConfigFromEnv()
	rankCfgMu.Lock()
	rankCfg = &cfg
	rankCfgMu.Unlock()
	return cfg
}

func loadRankConfigFromEnv() RankConfig {
	return RankConfig{
		EnableDataIntegrity: envBool("RANK_ENABLE_DATA_INTEGRITY", false),
		MinCompleteness:     envFloat("RANK_MIN_COMPLETENESS", 0.70),
		IntegrityMaxPenalty: envFloat("RANK_INTEGRITY_MAX_PENALTY", 20),

		EnableExecPenalty: envBool("RANK_ENABLE_EXEC_PENALTY", false),
		SpreadBpsSoft:     envFloat("RANK_SPREAD_BPS_SOFT", 4),
		SpreadBpsHard:     envFloat("RANK_SPREAD_BPS_HARD", 12),
		MinTopBookUSD:     envFloat("RANK_MIN_TOPBOOK_USD", 25000),
		TargetClipUSD:     envFloat("RANK_TARGET_CLIP_USD", 1000),
		MaxEstSlipBps:     envFloat("RANK_MAX_EST_SLIP_BPS", 12),
		ExecMaxPenalty:    envFloat("RANK_EXEC_MAX_PENALTY", 18),

		EnableNormalizedScore: envBool("RANK_ENABLE_NORMALIZED_SCORE", true),
		UseNormalizedInScore:  envBool("RANK_USE_NORMALIZED_IN_SCORE", false),
		NormMinRaw:            envFloat("RANK_NORM_MIN_RAW", 40),
		NormMaxRaw:            envFloat("RANK_NORM_MAX_RAW", 130),

		EnableMomentum: envBool("RANK_ENABLE_MULTI_HORIZON_MOMENTUM", true),
		MomW5m:         envFloat("RANK_MOM_W_5M", 0.40),
		MomW30m:        envFloat("RANK_MOM_W_30M", 0.30),
		MomW4h:         envFloat("RANK_MOM_W_4H", 0.20),
		MomW24h:        envFloat("RANK_MOM_W_24H", 0.10),
		MomMaxBoost:    envFloat("RANK_MOM_MAX_BOOST", 20),

		EnableConfidence: envBool("RANK_ENABLE_CONFIDENCE", true),
		MinConfidence:    envFloat("RANK_MIN_CONFIDENCE", 0.0),
	}
}

func envBool(k string, def bool) bool {
	v := strings.TrimSpace(os.Getenv(k))
	if v == "" {
		return def
	}
	switch strings.ToLower(v) {
	case "1", "true", "yes", "y", "on":
		return true
	case "0", "false", "no", "n", "off":
		return false
	default:
		return def
	}
}

func envFloat(k string, def float64) float64 {
	v := strings.TrimSpace(os.Getenv(k))
	if v == "" {
		return def
	}
	f, err := strconv.ParseFloat(v, 64)
	if err != nil {
		return def
	}
	return f
}
