package notify

import "log"

type Logger struct{}

func (Logger) Infof(format string, args ...any) {
	log.Printf(format, args...)
}
