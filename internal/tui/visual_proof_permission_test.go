package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/niparis/agent-net/internal/protocol"
)

// The owner told the CTO in its chat to grant a rule, and the CTO applied it
// (decision 0159). The Desk log shows the grant with the owner's words, and
// its undo removes exactly that grant. Frames at 80x24 and 120x34 go to
// AGENT_NET_PROOF_DIR when it is set (scripts/visual-proof.sh).
func TestVisualProofCTOPermissionGrant(t *testing.T) {
	at := time.Date(2026, 11, 13, 5, 30, 0, 0, time.UTC)
	words := "help wbgpoclead to have merge authority on all his repos"
	grant := protocol.DeskLogEntry{ID: 9, At: at, Source: "cto", AboutKind: "permission", AboutID: "pp_example",
		Text:    "Allowed Claude Code permissions in wbgpoc-lead",
		Detail:  "wbgpoc-lead on host-a (/home/owner/code/wbgpoc-lead): applied, 1 new rule(s)\nOwner's words: \"" + words + "\"",
		Options: []string{protocol.OpPermissionUndo, `{"id":"pp_example"}`}}
	for _, size := range [][2]int{{80, 24}, {120, 34}} {
		src := &ctoUndoSource{}
		m := logModel(src, grant)
		m.width, m.height = size[0], size[1]
		m.now = func() time.Time { return at.Add(2 * time.Minute) }
		frame := ansi.Strip(m.testView())
		for _, want := range []string{"cto      Allowed Claude Code permissions in wbgpoc-lead", "undo"} {
			if !strings.Contains(frame, want) {
				t.Fatalf("%dx%d: %q missing:\n%s", size[0], size[1], want, frame)
			}
		}
		if size[0] >= 120 && !strings.Contains(frame, "Owner's words") {
			t.Fatalf("%dx%d: the owner's words are not shown:\n%s", size[0], size[1], frame)
		}
		_, cmd := m.Update(tea.KeyPressMsg{Text: "u", Code: 'u'})
		if cmd == nil {
			t.Fatal("missing undo command")
		}
		if _, ok := cmd().(deskUndoneMsg); !ok {
			t.Fatal("undo did not run")
		}
		if len(src.methods) == 0 || src.methods[0] != protocol.OpPermissionUndo {
			t.Fatalf("undo called %v", src.methods)
		}
		if dir := os.Getenv("AGENT_NET_PROOF_DIR"); dir != "" {
			if err := os.MkdirAll(dir, 0o700); err != nil {
				t.Fatal(err)
			}
			capture := fmt.Sprintf("build: %s\nstep: open the Desk log after the CTO applied a permission at %dx%d\naction: open tab 7, show the log, select the grant line; then press u\nexpected: the grant line with the owner's words and an undo; u calls %s\nerrors: none\n\n%s\n",
				os.Getenv("AGENT_NET_PROOF_BUILD"), size[0], size[1], protocol.OpPermissionUndo, frame)
			if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("cto-permission-%dx%d.txt", size[0], size[1])), []byte(capture), 0o600); err != nil {
				t.Fatal(err)
			}
		}
	}
}
