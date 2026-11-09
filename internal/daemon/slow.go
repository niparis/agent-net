package daemon

import (
	"fmt"
	"strconv"
	"time"

	"github.com/niparis/agent-net/internal/diagnostics"
)

func (d *Daemon) slowCall(method string, start time.Time, phases ...*callPhases) {
	diagnostics.Slow(start, func(ms int64) {
		if d.log != nil {
			// Quote arbitrary input while keeping ordinary method names easy to scan.
			safe := strconv.Quote(method)
			if simpleMethod(method) {
				safe = method
			}
			line := fmt.Sprintf("event=slow method=%s ms=%d", safe, ms)
			for _, ph := range phases {
				if text := ph.String(); text != "" {
					line += " phases=" + text
				}
			}
			d.log.Printf("%s", line)
		}
	})
}

func simpleMethod(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range s {
		if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '_' || c == '.') {
			return false
		}
	}
	return true
}
