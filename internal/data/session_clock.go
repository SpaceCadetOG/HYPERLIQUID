package data

import "time"

const chicagoTZ = "America/Chicago"

type Regime string

const (
	RegimeAsia  Regime = "ASIA"
	RegimeEU    Regime = "EU"
	RegimeUS    Regime = "US"
	RegimeDead  Regime = "DEAD"
	OverlapAE   Regime = "ASIA_EU_OVERLAP"
	OverlapEUUS Regime = "EU_US_OVERLAP"
)

func mustChicago() *time.Location {
	loc, err := time.LoadLocation(chicagoTZ)
	if err != nil {
		return time.FixedZone("CST", -6*3600)
	}
	return loc
}

func CurrentRegimeCT(ts time.Time) Regime {
	t := ts.In(mustChicago())
	cur := t.Hour()*60 + t.Minute()
	if inMinuteRange(cur, 2*60, 3*60) {
		return OverlapAE
	}
	if inMinuteRange(cur, 7*60, 10*60) {
		return OverlapEUUS
	}
	if inMinuteWrap(cur, 19*60, 2*60) {
		return RegimeAsia
	}
	if inMinuteRange(cur, 2*60, 10*60) {
		return RegimeEU
	}
	if inMinuteRange(cur, 7*60, 16*60) {
		return RegimeUS
	}
	return RegimeDead
}

func IsMajorOverlapCT(ts time.Time) bool {
	r := CurrentRegimeCT(ts)
	return r == OverlapAE || r == OverlapEUUS
}

func inMinuteRange(cur, start, end int) bool {
	return cur >= start && cur < end
}

func inMinuteWrap(cur, start, end int) bool {
	if start < end {
		return inMinuteRange(cur, start, end)
	}
	return cur >= start || cur < end
}
