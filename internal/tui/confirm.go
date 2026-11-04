package tui

import (
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

// confirmHint is the default yes/no hint. Enter and y say yes, Esc and n
// say no, on every confirm in the TUI. Some questions name the actions.
const confirmHint = "enter/y: yes · esc/n: no"

const stopConfirmHint = "enter/y: stop · esc/n: keep"

// confirmSuffix separates a question from the hint.
const confirmSuffix = "  " + confirmHint

// confirmAnswer reports what a key says to a yes/no question.
func confirmAnswer(k tea.KeyMsg) (yes, no bool) {
	switch keyAction(contextConfirm, k.String()) {
	case "yes":
		return true, false
	case "no":
		return false, true
	}
	return false, false
}

// confirmLine puts the shared hint after a question. The hint is never cut:
// when the line is too narrow the question gives way.
func confirmLine(q string, w int) string {
	return confirmLineWithHint(q, confirmHint, w)
}

func confirmLineWithHint(q, hint string, w int) string {
	suffix := "  " + hint
	room := w - ansi.StringWidth(suffix)
	if room < 1 {
		return clip(hint, w)
	}
	return clip(q, room) + suffix
}

// confirmName puts a name inside a question, cutting the name first when the
// line is narrow, so the question and the hint stay whole.
func confirmName(prefix, name, suffix string, w int) string {
	return confirmNameWithHint(prefix, name, suffix, confirmHint, w)
}

func confirmNameWithHint(prefix, name, suffix, hint string, w int) string {
	hintSuffix := "  " + hint
	fixed := ansi.StringWidth(prefix) + ansi.StringWidth(suffix) + ansi.StringWidth(hintSuffix)
	if w < fixed {
		return confirmLineWithHint(prefix+name+suffix, hint, w)
	}
	return prefix + clip(name, w-fixed) + suffix + hintSuffix
}
