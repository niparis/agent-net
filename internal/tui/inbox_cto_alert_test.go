package tui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
	"github.com/niparis/agent-net/internal/protocol"
)

// A CTO alert reaches the owner only when the chief sends it on (money,
// secrets or security, decision 0152). Then it shows on the Desk as a notice.
func TestInboxShowsCTOAlertTheChiefSentOn(t *testing.T) {
	for _, size := range [][2]int{{80, 24}, {120, 34}} {
		m, _ := inboxAllKindsModel(size[0], size[1])
		m.asks = append(m.asks, askItems([]protocol.InboxItem{{
			ID: "i_alert", Kind: protocol.AskKindCTOAlert, SessionID: "agent-net", AskerName: "agent-net", FromKind: protocol.AskFromGateway,
			Text: "CTO alert: zai ran out (5h; resets 14:00)", NextWho: protocol.AskNextOwner, Status: "open", CreatedAt: t0.Add(-5 * time.Minute),
		}})...)
		m = openInbox(m)
		m.inboxIndex = -1
		for i, row := range m.inboxRows() {
			if row.key == "notice:i_alert" {
				m.inboxIndex = i
			}
		}
		if m.inboxIndex < 0 {
			t.Fatalf("%dx%d: no Desk row for the CTO alert", size[0], size[1])
		}
		view := ansi.Strip(m.testView())
		if !strings.Contains(view, "CTO alert: zai ran out") {
			t.Fatalf("%dx%d: the CTO alert is not on the Desk:\n%s", size[0], size[1], view)
		}
		golden(t, fmt.Sprintf("inbox_cto_alert_%dx%d", size[0], size[1]), m.testView())
	}
}
