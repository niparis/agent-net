package protocol

import (
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"
)

// The project's pre-hand-back check (owner decision 2026-11-12). The daemon
// reads delivery.handback_check, runs it in the worker's worktree after the
// hand-back commit, and records the result with the change revision. It never
// blocks a step: a failure is a change signal for the accept step.
const (
	HandbackCheckPassed        = "passed"
	HandbackCheckFailed        = "failed"
	HandbackCheckTimedOut      = "timed_out"
	HandbackCheckNotConfigured = "not_configured"
)

const (
	// MaxHandbackCheckOutputBytes bounds the output tail kept for a change and
	// its notice.
	MaxHandbackCheckOutputBytes = 4 << 10
)

// HandbackCheck is the result of one pre-hand-back check. Status is passed,
// failed, timed_out or not_configured. ExitCode is the command's exit code
// when it ran and failed. DurationMS is how long it ran. OutputTail is the
// last few KiB of its combined output, with terminal controls removed.
type HandbackCheck struct {
	Status     string `json:"status"`
	ExitCode   int    `json:"exit_code,omitempty"`
	DurationMS int64  `json:"duration_ms,omitempty"`
	OutputTail string `json:"output_tail,omitempty"`
}

// Validate checks one result before a submit carries it.
func (c HandbackCheck) Validate() error {
	switch c.Status {
	case HandbackCheckPassed, HandbackCheckFailed, HandbackCheckTimedOut, HandbackCheckNotConfigured:
	default:
		return fmt.Errorf("handback check status must be passed, failed, timed_out or not_configured, not %q", c.Status)
	}
	if c.Status == HandbackCheckFailed && (c.ExitCode < -1 || c.ExitCode > 255) {
		return errors.New("handback check exit code must be between 0 and 255")
	}
	if c.DurationMS < 0 {
		return errors.New("handback check duration must not be negative")
	}
	if len(c.OutputTail) > MaxHandbackCheckOutputBytes {
		return fmt.Errorf("handback check output is longer than %d bytes", MaxHandbackCheckOutputBytes)
	}
	if !utf8.ValidString(c.OutputTail) {
		return errors.New("handback check output is not valid UTF-8")
	}
	return nil
}

// NoticeLine is the one line a worker notice shows for the check.
func (c HandbackCheck) NoticeLine() string {
	switch c.Status {
	case HandbackCheckPassed:
		return "hand-back check: passed in " + checkDuration(c.DurationMS)
	case HandbackCheckFailed:
		return fmt.Sprintf("hand-back check: failed (exit %d) in %s", c.ExitCode, checkDuration(c.DurationMS))
	case HandbackCheckTimedOut:
		return "hand-back check: timed out after " + checkDuration(c.DurationMS)
	case HandbackCheckNotConfigured:
		return "hand-back check: not configured"
	}
	return "hand-back check: " + c.Status
}

// Signal is the change signal a failed or timed-out check raises, or false
// when the check passed, was not configured or did not run.
func (c HandbackCheck) Signal() (QualitySignal, bool) {
	code := ""
	what := ""
	switch c.Status {
	case HandbackCheckFailed:
		code = SignalHandbackCheckFailed
		what = fmt.Sprintf("hand-back check failed (exit %d)", c.ExitCode)
	case HandbackCheckTimedOut:
		code = SignalHandbackCheckTimedOut
		what = "hand-back check timed out"
	default:
		return QualitySignal{}, false
	}
	evidence := what + " in " + checkDuration(c.DurationMS)
	if line := lastCheckLine(c.OutputTail); line != "" {
		evidence += ": " + line
	}
	return QualitySignal{Code: code, Strength: SignalStrong, Evidence: boundSignalEvidence(evidence)}, true
}

// checkDuration renders milliseconds without a unit package at the call site.
func checkDuration(ms int64) string {
	if ms < 1000 {
		return fmt.Sprintf("%dms", max(ms, 0))
	}
	return fmt.Sprintf("%.1fs", float64(ms)/1000)
}

// lastCheckLine is the last non-empty line of a check's output, for one-line
// signal evidence.
func lastCheckLine(out string) string {
	line := ""
	for _, l := range strings.Split(out, "\n") {
		if t := strings.TrimSpace(l); t != "" {
			line = t
		}
	}
	return line
}

// boundSignalEvidence cuts evidence to the signal limit and removes the
// control characters a shell command can print.
func boundSignalEvidence(s string) string {
	clean := make([]rune, 0, len(s))
	for _, r := range s {
		if r == '\t' || (r >= 0x20 && r != 0x7f) {
			clean = append(clean, r)
		} else {
			clean = append(clean, ' ')
		}
	}
	s = string(clean)
	if len(s) > MaxSignalEvidenceBytes {
		s = s[:MaxSignalEvidenceBytes]
		for len(s) > 0 && !utf8.RuneStart(s[len(s)-1]) {
			s = s[:len(s)-1]
		}
	}
	if s == "" {
		s = "no output"
	}
	return s
}
