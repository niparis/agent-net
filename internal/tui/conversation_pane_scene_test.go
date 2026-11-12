package tui

import (
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/niparis/agent-net/internal/protocol"
)

// readingThreadID is the stored conversation the reading scenes open.
const readingThreadID = "c_review"

// readingScene is the Messages tab with one thread open that spans two days:
// a reply, a long body that wraps, and a body that names a change, a Desk ask
// and a PR. Other threads of different ages sit in the list so the sort order
// is visible.
func readingScene(t *testing.T, w, h int) Model {
	t.Helper()
	const (
		owner  = "s_owner_scene"
		strats = "s_strats_scene"
		teneb  = "s_teneb_scene"
		ops    = "s_ops_scene"
	)
	snap := Snapshot{Connected: true, NodeID: "n_scene", Hostname: "mac", Version: "v0.4.0", OwnerSession: owner}
	snap.Agents = []protocol.AgentView{
		agent(owner, "n_scene", "mac", "owner", "you", nil),
		agent(strats, "n_scene", "mac", "claude", "tenebrae-strats", nil),
		agent(teneb, "n_scene", "mac", "pi", "tenebrae 3", nil),
		agent(ops, "n_scene", "mac", "codex", "homelab-agent", nil),
	}
	src := &fakeSource{snap: snap}
	src.convFirst = protocol.ConversationPage{Total: 4, Conversations: []protocol.ConversationView{
		{ID: "c_ops", From: ops, To: owner, FromName: "homelab-agent", ToName: "you", Count: 2,
			LastLine: "The backup job finished and the disk is at 61 percent.", Type: protocol.TypeReply, State: protocol.StateReplied,
			LastMessageID: "m_ops", At: t0.Add(-40 * time.Minute), FromHarness: "codex", ToHarness: "owner"},
		{ID: readingThreadID, From: strats, To: teneb, FromName: "tenebrae-strats", ToName: "tenebrae 3", Count: 5,
			LastLine: "Merged. Thanks for the quick review.", Type: protocol.TypeNotification, State: protocol.StateStored,
			LastMessageID: "m_5", At: t0.Add(-2 * time.Minute), FromHarness: "claude", ToHarness: "pi"},
		{ID: "c_waiting", From: owner, To: ops, FromName: "you", ToName: "homelab-agent", Count: 1,
			LastLine: "Please rotate the backup key this week.", Type: protocol.TypeRequest, State: protocol.StateDelivered,
			LastMessageID: "m_w", At: t0.Add(-3 * time.Hour), FromHarness: "owner", ToHarness: "codex", OwnerNamed: true},
		{ID: "c_old", From: teneb, To: ops, FromName: "tenebrae 3", ToName: "homelab-agent", Count: 7,
			LastLine: "Done. The runner is back online.", Type: protocol.TypeReply, State: protocol.StateReplied,
			LastMessageID: "m_old", At: t0.Add(-30 * time.Hour), FromHarness: "pi", ToHarness: "codex"},
	}}
	day1 := t0.Add(-20 * time.Hour) // 28 Oct 18:05
	day2 := t0.Add(-5 * time.Hour)  // 29 Oct 09:05
	// The gateway sends the newest message first.
	src.convMsgs = map[string][]protocol.Message{readingThreadID: {
		{ID: "m_5", ConversationID: readingThreadID, From: strats, To: teneb, Type: protocol.TypeNotification,
			Body: "Merged. Thanks for the quick review.", CreatedAt: t0.Add(-2 * time.Minute)},
		{ID: "m_4", ConversationID: readingThreadID, ParentID: "m_3", From: teneb, To: strats, Type: protocol.TypeReply,
			Body: "Reviewed. One comment on the retry loop, otherwise it is good to go.", CreatedAt: t0.Add(-35 * time.Minute)},
		{ID: "m_3", ConversationID: readingThreadID, From: strats, To: teneb, Type: protocol.TypeRequest,
			Body:      "The migration is ready for review: PR #182 holds the fix and change ch_8c3bc9d80f8dd4aa carries the evidence. The Desk ask i_4d2a91c0d4e5 waits for your answer. This paragraph is long on purpose so the conversation pane has to wrap it over several lines and never cut a word in the middle or run past the edge of the pane.",
			CreatedAt: day2},
		{ID: "m_2", ConversationID: readingThreadID, ParentID: "m_1", From: teneb, To: strats, Type: protocol.TypeReply,
			Body: "I can look at it tomorrow morning.", CreatedAt: day1.Add(12 * time.Minute)},
		{ID: "m_1", ConversationID: readingThreadID, From: strats, To: teneb, Type: protocol.TypeRequest,
			Body: "Can you review the registration migration before the end of the week?", CreatedAt: day1},
	}}
	m := New(src)
	m.loc, m.now = time.UTC, func() time.Time { return t0 }
	m.tab, m.focus = messagesTab, focusFeed
	m = send(m, tea.WindowSizeMsg{Width: w, Height: h})
	m = refreshAndRun(t, m, src)
	m = selectConversation(t, m, readingThreadID)
	return m
}

// selectConversation moves the list cursor to a stored conversation and loads
// its messages, as the owner does with the arrow keys.
func selectConversation(t *testing.T, m Model, id string) Model {
	t.Helper()
	for i, c := range m.messageConversations() {
		if c.id == id {
			m.convSel = i
			cmd := m.wantConversationDetails()
			return runCommands(t, m, cmd)
		}
	}
	t.Fatalf("conversation %s is not in the list", id)
	return m
}

// TestConversationPaneScenes writes the reading scenes for scripts/screens.sh
// and, when AGENT_NET_PANE_FRAMES names a directory, plain text frames too.
func TestConversationPaneScenes(t *testing.T) {
	for _, size := range [][2]int{{80, 24}, {120, 34}, {160, 45}} {
		m := readingScene(t, size[0], size[1])
		name := fmt.Sprintf("messages_reading_%dx%d", size[0], size[1])
		writeScreen(t, name, m.testView())
		if dir := os.Getenv("AGENT_NET_PANE_FRAMES"); dir != "" {
			if err := os.WriteFile(dir+"/"+name+".txt", []byte(screen(m)), 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
}

// frameLines is the plain text of a rendered frame, one entry per row.
func frameLines(m Model) []string { return strings.Split(screen(m), "\n") }
