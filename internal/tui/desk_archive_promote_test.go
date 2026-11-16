package tui

import (
	"fmt"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// The notice the CTO's promote leaves in the Desk, with the undo the owner can
// take back. The text is the one the daemon writes for a promote.
func TestDeskArchivePromoteNoticeGoldens(t *testing.T) {
	for _, size := range [][2]int{{80, 24}, {120, 34}} {
		m := openInbox(newModel(size[0], size[1]))
		m.asks = askItems([]protocol.InboxItem{{
			ID: "i_archive", Kind: "check", Node: "cto", SessionID: "s_cto",
			Text:      "CTO chose version 1 of skill graphify as the latest",
			Options:   []string{"skill_archive_undo", `{"change":"sc_1"}`},
			Status:    "open",
			CreatedAt: t0.Add(-time.Minute),
		}})
		golden(t, fmt.Sprintf("desk_archive_promote_%dx%d", size[0], size[1]), m.testView())
	}
}
