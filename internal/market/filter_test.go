package market

import (
	"os"
	"testing"
)

func TestEligibleShortUsesStructuredSignals(t *testing.T) {
	t.Setenv("SCAN_ENABLE_REVERSAL_OVERRIDE", "true")
	eligibilityCfgMu.Lock()
	eligibilityCfg = nil
	eligibilityCfgMu.Unlock()
	defer func() {
		_ = os.Unsetenv("SCAN_ENABLE_REVERSAL_OVERRIDE")
		eligibilityCfgMu.Lock()
		eligibilityCfg = nil
		eligibilityCfgMu.Unlock()
	}()

	row := Scored{
		VolumeUSD:  2_000_000,
		Change24h:  0.5,
		OpenPrice:  100,
		LastPrice:  99,
		Score:      78,
		Confidence: 0.72,
		Reason:     "renamed text should not matter",
		Signals:    EligibilitySignals{AskPressure: true},
	}
	ok, reasons := EligibleShort(row)
	if !ok {
		t.Fatalf("expected structured short eligibility, got reasons=%v", reasons)
	}
}

func TestEligibleLongRejectsWithoutSignals(t *testing.T) {
	eligibilityCfgMu.Lock()
	eligibilityCfg = nil
	eligibilityCfgMu.Unlock()

	row := Scored{
		VolumeUSD: 2_000_000,
		Change24h: 0.3,
		OpenPrice: 100,
		LastPrice: 99.8,
		Signals:   EligibilitySignals{},
	}
	ok, reasons := EligibleLong(row)
	if ok {
		t.Fatalf("expected reject, got reasons=%v", reasons)
	}
}
