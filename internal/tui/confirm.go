package tui

import (
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

// confirmHint is how every yes/no question says how to answer it. Enter and y
// say yes, Esc and n say no, on every confirm in the TUI.
const confirmHint = "enter/y: yes · esc/n: no"

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
	room := w - ansi.StringWidth(confirmSuffix)
	if room < 1 {
		return clip(confirmHint, w)
	}
	return clip(q, room) + confirmSuffix
}

// confirmName puts a name inside a question, cutting the name first when the
// line is narrow, so the question and the hint stay whole.
func confirmName(prefix, name, suffix string, w int) string {
	fixed := ansi.StringWidth(prefix) + ansi.StringWidth(suffix) + ansi.StringWidth(confirmSuffix)
	if w < fixed {
		return confirmLine(prefix+name+suffix, w)
	}
	return prefix + clip(name, w-fixed) + suffix + confirmSuffix
}
