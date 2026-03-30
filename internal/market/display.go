package market

import "strings"

func DisplaySymbol(symbol string) string {
	sym := strings.ToUpper(strings.TrimSpace(symbol))
	if sym == "" {
		return ""
	}
	if strings.Contains(sym, "-") {
		return sym
	}
	return sym + "-USD"
}
