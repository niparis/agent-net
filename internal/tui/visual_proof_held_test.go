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

// A message Claude Code held for review is not delivered. The Messages tab
// says held, and the body gives the action: approve it in that session, or
// accept peer messages there.
func TestVisualProofHeldClaudeMessage(t *testing.T) {
	detail := "held by Claude Code: in that Claude Code session, review and approve the held message, or set \"crossSessionInbound\": \"accept\" in /home/owner/.claude/settings.json. " +
		"The sender's permission mode class does not match this session's. agent-net did not send it again. " +
		"Claude Code writes no notice when it drops a held message, so this one may have expired there; agent-net cannot see whether it still waits."
	events := []protocol.FeedEvent{ev(1, -2*time.Minute, "msg.held_by_session", "m_held", "s_0000000000000002", "s_0000000000000001", "request", func(e *protocol.FeedEvent) {
		e.Detail = detail
	})}
	messages := map[string]protocol.Message{"m_held": {ID: "m_held", Body: "please rotate the backup key"}}
	for _, size := range [][2]int{{80, 24}, {120, 34}} {
		src := &fakeSource{snap: fakeSnapshot(), events: events, messages: messages}
		m := New(src)
		m.loc, m.now = time.UTC, func() time.Time { return t0 }
		m = send(m, tea.WindowSizeMsg{Width: size[0], Height: size[1]})
		m = refresh(m, src)
		m.tab = messagesTab
		m.feedCur = m.feedLastVisible()
		rows := m.messageConversations()
		if len(rows) != 1 || m.threadState(rows[0]) != "held" {
			t.Fatalf("%dx%d: thread state = %+v", size[0], size[1], rows)
		}
		list := ansi.Strip(m.testView())
		// The threads pane is narrow at 120, so the row uses the state mark
		// and the legend explains it.
		if !strings.Contains(list, "! strategies") || !strings.Contains(list, "! held or blocked") {
			t.Fatalf("%dx%d: the held mark is missing from the list:\n%s", size[0], size[1], list)
		}
		m.open = &openMsg{id: "m_held", loading: true}
		body := ansi.Strip(strings.Join(m.messageView(size[0], 12), "\n"))
		for _, want := range []string{"held_by_session", "review and approve the", "crossSessionInbound"} {
			if !strings.Contains(body, want) {
				t.Fatalf("%dx%d: %q missing from the body:\n%s", size[0], size[1], want, body)
			}
		}
		if dir := os.Getenv("AGENT_NET_PROOF_DIR"); dir != "" {
			if err := os.MkdirAll(dir, 0o700); err != nil {
				t.Fatal(err)
			}
			capture := fmt.Sprintf("build: %s\nstep: open Messages after Claude Code held a peer message at %dx%d\naction: open the Messages tab, select the held row, then open the message body\nexpected: the row says held, and the body names the approve action\nerrors: none\n\n%s\n\n%s\n",
				os.Getenv("AGENT_NET_PROOF_BUILD"), size[0], size[1], list, body)
			if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("held-claude-%dx%d.txt", size[0], size[1])), []byte(capture), 0o600); err != nil {
				t.Fatal(err)
			}
		}
	}
}
