package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// The owner's "Learned" list (decision 0022): the playbook notes the gateway
// promoted, with their support. It is quiet: it never opens by itself, and the
// header only counts what was learned recently.

// learnedWindow is how long a promoted note counts as new in the header.
const learnedWindow = 24 * time.Hour

// learnedNew is the quiet header count: automatic notes promoted recently.
func (m Model) learnedNew() int {
	now, n := m.now(), 0
	for i := range m.snap.Playbook {
		p := m.snap.Playbook[i]
		if p.Source == protocol.PlaybookSourceAuto && p.UpdatedAt > 0 &&
			now.Sub(time.Unix(p.UpdatedAt, 0)) < learnedWindow {
			n++
		}
	}
	return n
}

// learnedView lists the notes with the support behind them.
func (m Model) learnedView(w, h int) []string {
	out := []string{section("Learned", w)}
	if len(m.snap.Playbook) == 0 {
		out = append(out, sDim.Render(" nothing learned yet"))
		return out
	}
	for _, p := range m.snap.Playbook {
		label := p.Subject
		if p.Harness != "" {
			label += " · " + p.Harness
		}
		source := p.Source
		if source == "" {
			source = protocol.PlaybookSourceOwner
		}
		head := " " + sTitle.Render(label) + "  " + sDim.Render(fmt.Sprintf("%s, support %d", source, p.Support))
		if p.Pinned {
			head += "  " + sBold.Render("pinned")
		}
		out = append(out, clip(head, w))
		for _, line := range strings.Split(sanitizeBody(p.Note), "\n") {
			out = append(out, clip("   "+line, w))
		}
		if p.TrackLine != "" {
			out = append(out, clip("   "+sDim.Render(p.TrackLine), w))
		}
	}
	if len(out) > h {
		out = out[:h]
	}
	return out
}
