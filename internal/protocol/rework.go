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

// WorkerReportComplete reads the complete marker the worker brief asks for:
// "complete: yes" or "complete: no" on the first or the last nonempty line of
// the report. When both lines carry a marker and they differ, the last wins.
// Without a marker, work counts as complete and ok is false.
func WorkerReportComplete(report string) (complete, ok bool) {
	var first, last string
	for _, line := range strings.Split(strings.TrimSpace(report), "\n") {
		line = strings.ToLower(strings.TrimSpace(line))
		if line == "" {
			continue
		}
		if first == "" {
			first = line
		}
		last = line
	}
	for _, line := range []string{last, first} {
		switch line {
		case workerCompleteNo:
			return false, true
		case workerCompleteYes:
			return true, true
		}
	}
	return true, false
}
