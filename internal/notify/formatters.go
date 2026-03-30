package notify

import (
	"fmt"
	"strings"
)

type PulseSnapshot struct {
	Title     string
	TimeLabel string
	Session   string
	Balance   float64
	Equity    float64
	Realized  float64
	OpenPnL   float64
	NetDay    float64
	OpenCount int
	OpenCap   int
	NetDayPct float64
}

type PositionCard struct {
	Symbol           string
	Side             string
	Source           string
	Qty              float64
	EntryPrice       float64
	MarkPrice        float64
	LastPrice        float64
	SpreadBps        float64
	UnrealizedPnL    float64
	UnrealizedPnLPct float64
	Leverage         int
	Setup            string
	Confluence       float64
	AgeMin           int
	StopLoss         float64
	TakeProfit       float64
}

type ScanItem struct {
	Symbol string
	Grade  string
	Score  float64
}

func BuildSessionPulseHTML(p PulseSnapshot) string {
	title := strings.TrimSpace(p.Title)
	if title == "" {
		title = "SESSION"
	}
	dayEmoji := "🟢"
	if p.NetDay < 0 {
		dayEmoji = "🔴"
	}
	return strings.TrimSpace(fmt.Sprintf(
		"<b>☀️ %s: %s (%s)</b>\n"+
			"<b>💰 ACCOUNT OVERVIEW</b>\n"+
			"• <b>Balance:</b> $%.2f | <b>Equity:</b> $%.2f\n"+
			"• <b>Net Day:</b> %s %+.2f (%.2f%%)\n"+
			"• <b>Realized:</b> %+.2f | <b>Open PnL:</b> %+.2f (%s)",
		title, strings.ToUpper(strings.TrimSpace(p.Session)), strings.TrimSpace(p.TimeLabel),
		p.Balance, p.Equity, dayEmoji, p.NetDay, p.NetDayPct, p.Realized, p.OpenPnL, openPosLabel(p.OpenCount, p.OpenCap),
	))
}

func BuildPositionCard(p PositionCard) string {
	direction := "🟢 BUY"
	if strings.EqualFold(strings.TrimSpace(p.Side), "SELL") || strings.EqualFold(strings.TrimSpace(p.Side), "SHORT") {
		direction = "🔴 SELL"
	}
	setup := strings.TrimSpace(p.Setup)
	if setup == "" {
		setup = "none"
	}
	if p.Leverage <= 0 {
		p.Leverage = 1
	}
	priceLine := fmt.Sprintf("• <b>Price:</b> %.4f → %.4f (<b>%+.2f%%</b>)", p.EntryPrice, p.MarkPrice, p.UnrealizedPnLPct)
	if p.LastPrice > 0 {
		priceLine = fmt.Sprintf("• <b>Price:</b> %.4f → %.4f | <b>Last:</b> %.4f (<b>%+.2f%%</b>)", p.EntryPrice, p.MarkPrice, p.LastPrice, p.UnrealizedPnLPct)
	}
	pnlLine := fmt.Sprintf("• <b>PnL:</b> %s$%.2f | <b>Lev:</b> %dx", pnlEmoji(p.UnrealizedPnL), p.UnrealizedPnL, p.Leverage)
	if p.Qty > 0 {
		pnlLine = fmt.Sprintf("• <b>PnL:</b> %s$%.2f | <b>Qty:</b> %.6f | <b>Lev:</b> %dx", pnlEmoji(p.UnrealizedPnL), p.UnrealizedPnL, p.Qty, p.Leverage)
	}
	setupLine := fmt.Sprintf("• <b>Setup:</b> <code>%s</code> (Conf: %.0f%%) | <b>Age:</b> %dm", setup, p.Confluence*100.0, p.AgeMin)
	if strings.TrimSpace(p.Source) != "" || p.SpreadBps > 0 {
		parts := make([]string, 0, 2)
		if strings.TrimSpace(p.Source) != "" {
			parts = append(parts, fmt.Sprintf("<b>Src:</b> %s", strings.ToUpper(strings.TrimSpace(p.Source))))
		}
		if p.SpreadBps > 0 {
			parts = append(parts, fmt.Sprintf("<b>Spread:</b> %.1fbps", p.SpreadBps))
		}
		setupLine += " | " + strings.Join(parts, " | ")
	}
	return strings.TrimSpace(fmt.Sprintf(
		"<b>📦 ACTIVE: %s (%s)</b>\n%s\n%s\n%s\n• <b>Safety:</b> SL: %.4f | TP: %.4f",
		strings.ToUpper(strings.TrimSpace(p.Symbol)), direction,
		priceLine, pnlLine, setupLine, p.StopLoss, p.TakeProfit,
	))
}

func BuildScannerSnapshotHTML(longs, shorts []ScanItem, bias string) string {
	return strings.TrimSpace(fmt.Sprintf(
		"<b>📡 TOP SCANS</b>\n"+
			"• <b>LONG:</b> %s\n"+
			"• <b>SHORT:</b> %s\n"+
			"⚡ <b>Bias:</b> %s",
		renderScanLine(longs),
		renderScanLine(shorts),
		biasLabel(bias),
	))
}

func BuildEventHTML(icon, title string, lines ...string) string {
	icon = strings.TrimSpace(icon)
	if icon == "" {
		icon = "ℹ️"
	}
	title = strings.TrimSpace(title)
	if title == "" {
		title = "UPDATE"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%s <b>%s</b>", icon, title)
	for _, l := range lines {
		s := strings.TrimSpace(l)
		if s == "" {
			continue
		}
		fmt.Fprintf(&b, "\n• %s", s)
	}
	return strings.TrimSpace(b.String())
}

func pnlEmoji(v float64) string {
	if v < 0 {
		return "🔴 "
	}
	return "🟢 "
}

func renderScanLine(items []ScanItem) string {
	if len(items) == 0 {
		return "(none)"
	}
	parts := make([]string, 0, len(items))
	for _, it := range items {
		parts = append(parts, fmt.Sprintf("%s (<b>%s</b> · <b>%.0f</b>)", shortSymbol(it.Symbol), strings.TrimSpace(it.Grade), it.Score))
	}
	return strings.Join(parts, " | ")
}

func openPosLabel(openCount, openCap int) string {
	if openCap > 0 {
		return fmt.Sprintf("%d/%d pos", openCount, openCap)
	}
	return fmt.Sprintf("%d pos", openCount)
}

func shortSymbol(sym string) string {
	s := strings.ToUpper(strings.TrimSpace(sym))
	s = strings.TrimSuffix(s, "USDT")
	s = strings.TrimSuffix(s, "-USD")
	if s == "" {
		return strings.ToUpper(strings.TrimSpace(sym))
	}
	return s
}

func biasLabel(b string) string {
	switch strings.ToUpper(strings.TrimSpace(b)) {
	case "LONG":
		return "🟢 LONG"
	case "SHORT":
		return "🔴 SHORT"
	default:
		return "🟡 NEUTRAL"
	}
}
