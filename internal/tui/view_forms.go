package tui

import (
	"fmt"
	"time"

	"charm.land/lipgloss/v2"
)

// noticeLine is the result of the last action, on the key line where the hints
// sit. It is one row, so showing or clearing it never moves the layout:
// "!" and the error colour when it failed, the accent colour otherwise. The
// bool is false when there is no result to show.
func (m Model) noticeLine(w int) (string, bool) {
	n := m.activeNotice()
	if n == nil {
		return "", false
	}
	prefix := "  "
	if a := m.sessionPending; a != nil {
		var line string
		switch a.kind {
		case askCreateFolder:
			line = confirmName("Create ", sanitize(a.display), " on "+sanitize(a.host)+" and run git init?", w)
		case askTrustClaude:
			line = confirmName("Trust ", sanitize(a.display), " for Claude Code?", w)
		}
		return m.noticeStyle(n).Render(line), true
	}
	if a := m.resumeConfirm; a != nil {
		suffix := fmt.Sprintf(" (claude, last active %s ago) and enter it?", ago(m.now().Sub(time.Unix(a.LastSeen, 0)))) + confirmSuffix
		name := sanitize(a.Profile.Name)
		return m.noticeStyle(n).Render("Resume " + clip(name, max(w-len("Resume ")-len(suffix), 0)) + suffix), true
	}
	if n.err {
		prefix = "! "
	}
	return m.noticeStyle(n).Render(clip(prefix+sanitize(n.text), w)), true
}

// noticeStyle is the colour of the result: full brightness for the hold, then
// three steps toward dim while it fades.
func (m Model) noticeStyle(n *notice) lipgloss.Style {
	base := roleAccent
	if n.err {
		base = roleError
	}
	age := m.now().Sub(n.at)
	if age < n.hold() {
		return lipgloss.NewStyle().Foreground(base)
	}
	elapsed := age - n.hold()
	step := int((elapsed + noticeFadeStep - 1) / noticeFadeStep)
	if step > 3 {
		step = 3
	}
	return lipgloss.NewStyle().Foreground(blendColor(base, roleDim, float64(step)/3))
}
