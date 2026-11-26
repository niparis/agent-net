package tui

import (
	"fmt"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/niparis/agent-net/internal/protocol"
)

// The owner has no Desk (decision 0196): with an open ask loaded, the main
// screen shows no Desk tab and no Desk count, and key 6 opens nothing.
func TestDeskTabIsHidden(t *testing.T) {
	for _, size := range [][2]int{{80, 24}, {120, 34}} {
		m := quietFixtures(newModel(size[0], size[1]))
		m.asks = askItems([]protocol.InboxItem{{ID: "i_q", SessionID: "kid", Kind: "question", Status: "open", Text: "Ship it?"}})
		view := m.testView()
		golden(t, fmt.Sprintf("desk_hidden_%dx%d", size[0], size[1]), view)
		plain := ansi.Strip(view)
		for _, gone := range []string{"Desk", "to decide", "needs you"} {
			if strings.Contains(lines(plain)[0], gone) {
				t.Fatalf("%dx%d title line shows %q:\n%s", size[0], size[1], gone, plain)
			}
		}
		if strings.Contains(plain, "to decide") || strings.Contains(plain, "needs you") {
			t.Fatalf("%dx%d shows a Desk count:\n%s", size[0], size[1], plain)
		}
		if m = keys(m, "6"); m.tab != sessionsTab {
			t.Fatalf("key 6 opened tab %d", m.tab)
		}
	}
}

// openDesk opens the Desk screen the way key 6 did before decision 0196. The
// screen code stays until a later cleanup, so its tests open it directly.
func openDesk(m Model) Model {
	m.tab, m.uiFilter, m.focus = inboxTab, "", focusSessions
	m.inboxDetail, m.inboxDetailOption, m.inboxEditing = false, 0, false
	m.delivery.detail = false
	m.inboxIndex = min(max(m.inboxIndex, 0), max(m.deskRowCount()-1, 0))
	return settle(m)
}
