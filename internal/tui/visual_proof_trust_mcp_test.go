package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
	"github.com/niparis/agent-net/internal/protocol"
)

// A trust proposal now names the folder's own .mcp.json servers that the one
// click approves. Frames at 80x24 and 120x34 go to AGENT_NET_PROOF_DIR when
// scripts/visual-proof.sh runs.
func TestVisualProofTrustAskNamesMCPServers(t *testing.T) {
	at := time.Date(2026, 11, 18, 9, 15, 0, 0, time.UTC)
	folder := "/home/owner/code/tenebrae-strats"
	text := "Trust 1 Claude Code folder(s) on host-a?\n" +
		"Machine: host-a (n_fixture)\n" +
		"Folders:\n" +
		"  - " + folder + "\n" +
		"    also approves MCP servers: tenebrae-library\n" +
		"Reason: you (on host-a) needs Claude Code folder trust for " + folder + " (project tenebrae-strats)\n" +
		"Proposed by: you (on host-a) (s_fixture)\n" +
		"After approval, the machine trusts the folder for Claude Code and approves those servers from the folder's own .mcp.json. A session already running there may need a restart, or /hooks opened once."
	item := protocol.InboxItem{
		ID: "ask_fixture", Kind: protocol.AskKindPermission, SessionID: "s_fixture", Node: "host-a",
		AskerName: "you", SourceRef: "permission:pp_fixture", AboutKind: protocol.AskAboutProject, AboutID: "tenebrae-strats",
		Text: text, Detail: text, Options: []string{"approve", "reject"}, Status: "open",
		NextWho: protocol.AskNextOwner, CreatedAt: at,
	}
	for _, size := range [][2]int{{80, 24}, {120, 34}} {
		m := newModel(size[0], size[1])
		m.src = &ctoUndoSource{}
		m.tab = inboxTab
		m.asks = askItems([]protocol.InboxItem{item})
		m.inboxIndex = m.askRowCount() - 1
		m.inboxDetail = true
		m.now = func() time.Time { return at.Add(2 * time.Minute) }
		frame := ansi.Strip(m.testView())
		for _, want := range []string{folder, "also approves MCP servers: tenebrae-library", "approve", "reject"} {
			if !strings.Contains(frame, want) {
				t.Fatalf("%dx%d: %q missing:\n%s", size[0], size[1], want, frame)
			}
		}
		if dir := os.Getenv("AGENT_NET_PROOF_DIR"); dir != "" {
			if err := os.MkdirAll(dir, 0o700); err != nil {
				t.Fatal(err)
			}
			capture := fmt.Sprintf("build: %s\nstep: open the Inbox and the trust ask at %dx%d\naction: select the permission ask and open its detail\nexpected: the ask names the folder and the MCP server the one click approves\nerrors: none\n\n%s\n",
				os.Getenv("AGENT_NET_PROOF_BUILD"), size[0], size[1], frame)
			if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("trust-mcp-%dx%d.txt", size[0], size[1])), []byte(capture), 0o600); err != nil {
				t.Fatal(err)
			}
		}
	}
}
