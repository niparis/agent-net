package protocol

import "strings"

// ReworkCounts classifies a caller follow-up using the worker's last hand-back
// or idle state. Message wording does not change the count.
func ReworkCounts(handback, partial, idle bool) (cont, fix int) {
	if handback && !partial {
		return 0, 1
	}
	if handback || idle {
		return 1, 0
	}
	return 0, 0
}

// WorkerReportComplete reads the final nonempty report line requested by the
// Codex brief. Without an explicit complete line, work counts as complete.
func WorkerReportComplete(report string) (complete, ok bool) {
	complete = true
	lines := strings.Split(strings.TrimSpace(report), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.ToLower(strings.TrimSpace(lines[i]))
		if line == "" {
			continue
		}
		switch line {
		case "complete: no":
			return false, true
		case "complete: yes":
			return true, true
		}
		return true, false
	}
	return true, false
}
