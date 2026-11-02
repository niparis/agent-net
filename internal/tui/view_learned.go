package tui

import (
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// The owner's learned notes (decision 0022) live on the Graph agent card; the
// header still counts what was learned recently.

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
