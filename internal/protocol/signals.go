package protocol

import (
	"errors"
	"fmt"
	"unicode"
)

// Free quality signals of a worker change (the owner's decision of 2026-11-11).
// The daemon computes them from the change's diff at hand-back; the gateway
// stores them with the revision and the three views show them. Signals inform
// a person or a later grader; they never block a step.
const (
	SignalStrong = "strong"
	SignalWeak   = "weak"

	// SignalRepeatedLine is the same non-trivial line added in several places.
	SignalRepeatedLine = "repeated_line"
	// SignalTestTiming is a new sleep or short timer in a test.
	SignalTestTiming = "test_timing"
	// SignalOutsideBrief is work in files the brief did not name.
	SignalOutsideBrief = "outside_brief"
	// SignalStruggle is a run the struggle detector flagged.
	SignalStruggle = "struggle"
	// SignalOutOfProportion is more lines than the difficulty allows.
	SignalOutOfProportion = "out_of_proportion"
	// SignalHeavyTests is many more test lines than code lines.
	SignalHeavyTests = "heavy_tests"
	// SignalTestHelpers is new non-Test helper functions in test files.
	SignalTestHelpers = "test_helpers"
	// SignalHandbackCheckFailed is a project's pre-hand-back check that failed.
	SignalHandbackCheckFailed = "handback_check_failed"
	// SignalHandbackCheckTimedOut is a check that ran past its timeout.
	SignalHandbackCheckTimedOut = "handback_check_timed_out"
)

const (
	// MaxQualitySignals bounds a submit's signal list.
	MaxQualitySignals = 16
	// MaxSignalCodeBytes bounds a signal code.
	MaxSignalCodeBytes = 32
	// MaxSignalEvidenceBytes bounds one signal's evidence line.
	MaxSignalEvidenceBytes = 512
)

// QualitySignal is one free quality signal of a change revision. Code names
// it, Strength is strong or weak, and Evidence is the one line that shows why
// it fired, for example "repeated line ×4: <line> in server.go, workers.go".
type QualitySignal struct {
	Code     string `json:"code"`
	Strength string `json:"strength"`
	Evidence string `json:"evidence"`
}

// Line is the signal as the views show it: strength and evidence, one line.
func (s QualitySignal) Line() string { return s.Strength + ": " + s.Evidence }

// Validate checks one stored signal before it reaches a message or the TUI.
func (s QualitySignal) Validate() error {
	if s.Code == "" || len(s.Code) > MaxSignalCodeBytes {
		return fmt.Errorf("signal code must be 1 to %d bytes", MaxSignalCodeBytes)
	}
	for _, r := range s.Code {
		if r < 0x21 || r > 0x7e || r == '"' || r == '\\' {
			return errors.New("signal code must be printable ASCII without quotes")
		}
	}
	if s.Strength != SignalStrong && s.Strength != SignalWeak {
		return fmt.Errorf("signal strength must be %q or %q", SignalStrong, SignalWeak)
	}
	if s.Evidence == "" || len(s.Evidence) > MaxSignalEvidenceBytes {
		return fmt.Errorf("signal evidence must be 1 to %d bytes", MaxSignalEvidenceBytes)
	}
	for _, r := range s.Evidence {
		if (r < 0x20 && r != '\t') || r == 0x7f || unicode.Is(unicode.Cf, r) {
			return errors.New("signal evidence holds a control character")
		}
	}
	return nil
}

// SignalLines renders signals in stored order, one line per signal.
func SignalLines(signals []QualitySignal) []string {
	out := make([]string, 0, len(signals))
	for _, s := range signals {
		out = append(out, s.Line())
	}
	return out
}
