package sessions

import (
	"fmt"
	"strings"
	"time"

	"hyperliquid/internal/data"
)

func ActiveSessionLabels(nowUTC time.Time) []string {
	regime := data.CurrentRegimeCT(nowUTC)
	label := string(regime)
	if regime == data.RegimeDead {
		loc, err := time.LoadLocation("America/Chicago")
		if err != nil {
			loc = time.FixedZone("CST", -6*3600)
		}
		lt := nowUTC.In(loc)
		switch {
		case lt.Hour() >= 16 && lt.Hour() < 18:
			label = "MAINT_1600_1800"
		case lt.Hour() >= 22 && (lt.Hour() < 23 || (lt.Hour() == 23 && lt.Minute() < 30)):
			label = "MAINT_2200_2330"
		default:
			label = "OFF_SESSION"
		}
	}
	labels := []string{label}
	if data.IsMajorOverlapCT(nowUTC) {
		labels = append(labels, "MAJOR_OVERLAP")
	}
	loc, err := time.LoadLocation("America/Chicago")
	if err != nil {
		loc = time.FixedZone("CST", -6*3600)
	}
	lt := nowUTC.In(loc)
	if lt.Hour() >= 7 && lt.Hour() < 10 {
		labels = append(labels, "US_OPEN_CTX")
	}
	if lt.Hour() == 16 {
		labels = append(labels, "NY17_ROLLOVER")
	}
	return labels
}

func AdapterBanner(exchange, side string, now time.Time) string {
	return fmt.Sprintf("%s adapter - live fetch @ %s", strings.ToUpper(exchange)+" "+strings.ToUpper(side), now.Format(time.RFC3339))
}

func ScannerScoreMultiplier(nowUTC time.Time) float64 {
	_ = nowUTC
	return 1.0
}
