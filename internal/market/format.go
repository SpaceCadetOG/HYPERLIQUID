package market

import (
	"fmt"
	"math"
	"strings"
)

func HumanUSD(x float64) string {
	ax := math.Abs(x)
	switch {
	case ax >= 1_000_000_000:
		return fmt.Sprintf("%.2fB", x/1_000_000_000)
	case ax >= 1_000_000:
		return fmt.Sprintf("%.2fM", x/1_000_000)
	case ax >= 1_000:
		return fmt.Sprintf("%.2fK", x/1_000)
	default:
		return fmt.Sprintf("%.0f", x)
	}
}

func FormatHeader(exchange string, activeLabels []string) string {
	return fmt.Sprintf("%s • [%s]", exchange, strings.Join(activeLabels, ","))
}

func FormatRow(s Scored) string {
	funding := "-"
	if s.FundingRate != nil {
		funding = fmt.Sprintf("%.4f", (*s.FundingRate)*100.0)
	}
	oi := "-"
	if s.OIUSD != nil {
		oi = HumanUSD(*s.OIUSD)
	}
	dayUTC := fmt.Sprintf("%+.1f", s.Change24h)
	if s.DayUTC24h != nil {
		dayUTC = fmt.Sprintf("%+.1f", *s.DayUTC24h)
	}
	utc4h := "-"
	if s.UTC4hPct != nil {
		utc4h = fmt.Sprintf("%+.1f", *s.UTC4hPct)
	}
	utc1h := "-"
	if s.UTC1hPct != nil {
		utc1h = fmt.Sprintf("%+.1f", *s.UTC1hPct)
	}
	openUTC := "-"
	if s.OpenPrice > 0 {
		openUTC = fmt.Sprintf("%.4f", s.OpenPrice)
	}
	last := "-"
	if s.LastPrice > 0 {
		last = fmt.Sprintf("%.4f", s.LastPrice)
	}
	return fmt.Sprintf("%-12s | %6.2f | %7s | %7s | %7s | %8.1f | %7s | %7s | %10s | %8s | %8s",
		DisplaySymbol(s.Symbol), s.Score, dayUTC, utc4h, utc1h, s.Change24h, HumanUSD(s.VolumeUSD), oi, funding, openUTC, last)
}
