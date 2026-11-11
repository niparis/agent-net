package protocol

import (
	"fmt"
	"strings"
)

// Struggle counts the signs that a worker fought its instructions or its
// environment before it reported. It is computed once at run end from the
// run's own tool record, with no model call (decision 0133).
//
// Trace names where the record came from: "transcript" (a Claude Code
// transcript), "rollout" (a Codex rollout), "messages" (the OpenCode messages
// API), or "no trace" when the harness has no tool-call record that the daemon
// reads. The zero value has no trace, so an older machine looks the same as a
// harness that cannot say.
type Struggle struct {
	Trace string `json:"trace"`
	// Failed counts tool calls that ended in an error, a non-zero exit or a
	// permission or sandbox refusal.
	Failed int `json:"failed,omitempty"`
	// Repeats counts distinct commands that ran StruggleRepeatRuns times or
	// more.
	Repeats int `json:"repeats,omitempty"`
	// Retries counts tool calls of a command that had already failed in the
	// same run.
	Retries int `json:"retries,omitempty"`
	// Exploration is how many tool calls came before the first edit. 0 when
	// the run made no edit.
	Exploration int `json:"exploration,omitempty"`
	// EnvErrors counts tool results that name a known environment problem.
	EnvErrors int `json:"env_errors,omitempty"`
	// Top names at most StruggleTop commands, worst first, each short.
	Top []string `json:"top,omitempty"`
	// Capped is true when the trace was longer than StruggleTraceMaxBytes and
	// only its end was read.
	Capped bool `json:"capped,omitempty"`
}

// Thresholds of the struggle line. They are code constants, not settings: the
// owner asked for a low bar, not a tunable.
const (
	// StruggleFailedThreshold is how many failed tool calls show the line.
	StruggleFailedThreshold = 3
	// StruggleRepeatRuns is how many times one command must run to count as a
	// repeat; any repeat shows the line.
	StruggleRepeatRuns = 3
	// StruggleTop is how many worst offenders the line names.
	StruggleTop = 2
	// StruggleTraceMaxBytes caps one read of a trace. Only the end is read,
	// because the signs the line shows are in the last part of a run.
	StruggleTraceMaxBytes = 20 << 20
)

// StruggleNoTrace is the Trace value of a harness whose tool calls the daemon
// cannot read.
const StruggleNoTrace = "no trace"

// StruggleShown tells whether the counts are above the low threshold: three
// or more failed tool calls, or any command repeated StruggleRepeatRuns times.
func StruggleShown(s *Struggle) bool {
	if s == nil || s.Trace == "" || s.Trace == StruggleNoTrace {
		return false
	}
	return s.Failed >= StruggleFailedThreshold || s.Repeats > 0
}

// StruggleLine is the one line a notice and the caller's accept task carry.
// It is empty below the threshold and for a run with no trace.
func StruggleLine(s *Struggle) string {
	if !StruggleShown(s) {
		return ""
	}
	line := fmt.Sprintf("struggle: %d failed tool calls, %d repeats", s.Failed, s.Repeats)
	if len(s.Top) > 0 {
		top := s.Top
		if len(top) > StruggleTop {
			top = top[:StruggleTop]
		}
		line += "; top: " + strings.Join(top, "; ")
	}
	if s.Capped {
		line += " (trace capped at the last 20 MB)"
	}
	return line
}

// StruggleDetail is the text agent_workers_find detail full shows. A harness
// with no tool-call record says so instead of guessing zeros.
func StruggleDetail(s *Struggle) string {
	if s == nil || s.Trace == "" || s.Trace == StruggleNoTrace {
		return "no trace"
	}
	text := fmt.Sprintf("%d failed tool calls, %d repeats, %d retries, %d tool calls before the first edit, %d environment errors; trace: %s",
		s.Failed, s.Repeats, s.Retries, s.Exploration, s.EnvErrors, s.Trace)
	if s.Capped {
		text += " (capped at the last 20 MB)"
	}
	if len(s.Top) > 0 {
		text += "; top: " + strings.Join(s.Top, "; ")
	}
	return text
}
