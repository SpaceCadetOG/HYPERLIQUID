package market

import (
	"fmt"
	"math"
	"strings"
)

func FallbackGradeDirectional(score, delta24h float64, side string) string {
	side = strings.ToLower(strings.TrimSpace(side))
	if score < 0 {
		score = 0
	}
	if score > 150 {
		score = 150
	}
	mover := delta24h
	if side == "short" {
		mover = -delta24h
	}
	bonus := math.Max(-15, math.Min(15, mover*0.25))
	adj := score + bonus
	switch {
	case adj >= 100:
		return "A+"
	case adj >= 95:
		return "A"
	case adj >= 89:
		return "B"
	case adj >= 79:
		return "C"
	case adj >= 69:
		return "D"
	default:
		return "N/A"
	}
}

func GradeColor(grade string) string {
	switch strings.ToUpper(strings.TrimSpace(grade)) {
	case "A+":
		return "\033[38;5;135m"
	case "A":
		return "\033[38;5;45m"
	case "B":
		return "\033[32m"
	case "C":
		return "\033[38;5;214m"
	case "D":
		return "\033[31m"
	default:
		return "\033[37m"
	}
}

func ResetColor() string { return "\033[0m" }

func ColorGrade(grade string) string {
	return fmt.Sprintf("%s%s%s", GradeColor(grade), grade, ResetColor())
}
