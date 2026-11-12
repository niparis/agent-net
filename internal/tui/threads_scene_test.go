package tui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/niparis/agent-net/internal/protocol"
)

// TestMessageThreadScenes renders the three Messages sub-tabs at a small and a
// wide size. scripts/screens.sh writes the ANSI files and the PNGs; the test
// itself does nothing in a normal run.
func TestMessageThreadScenes(t *testing.T) {
	for _, size := range [][2]int{{80, 24}, {160, 40}} {
		m := messageScene(t, size[0], size[1])
		for _, tab := range []struct {
			name string
			sub  messageSubtab
		}{
			{"agents", messageAgents},
			{"workers", messageWorkers},
			{"system", messageSystem},
		} {
			m.messageSubtab = tab.sub
			m.convSel = 0
			if m.convLoaded {
				m.rebuildConversations()
			}
			writeScreen(t, fmt.Sprintf("messages_threads_%s_%dx%d", tab.name, size[0], size[1]), m.testView())
		}
	}
}

// messageScene is one network with a busy peer thread, an app thread, a worker
// run and the system notices.
func messageScene(t *testing.T, w, h int) Model {
	t.Helper()
	const (
		owner   = "s_owner_scene"
		strats  = "s_strats_scene"
		teneb   = "s_teneb_scene"
		app     = "s_app_scene"
		worker  = "s_worker_scene"
		agentnt = "s_agentnet_scene"
	)
	snap := Snapshot{Connected: true, NodeID: "n_scene", Hostname: "mac", Version: "v0.4.0", OwnerSession: owner}
	snap.Agents = []protocol.AgentView{
		agent(owner, "n_scene", "mac", "owner", "you", nil),
		agent(strats, "n_scene", "mac", "claude", "tenebrae-strats", func(a *protocol.AgentView) {
			a.AgentID = "agent_strats"
			a.Repo.Project = "tenebrae"
		}),
		agent(teneb, "n_scene", "mac", "pi", "tenebrae 3", func(a *protocol.AgentView) {
			a.AgentID = "agent_teneb"
			a.Repo.Project = "tenebrae"
		}),
		agent(app, "n_scene", "mac", "bot", "CTO (Trade)", nil),
		agent(agentnt, "n_scene", "mac", "claude", "agent-net 2", nil),
		agent(worker, "n_scene", "mac", "codex", "tenebrae-strats worker 1", func(a *protocol.AgentView) {
			a.Worker = &protocol.WorkerInfo{ID: "w_1", Owner: strats, Task: "T8 registration PR #182", StartedAt: t0.Add(-8 * time.Minute).Unix()}
		}),
	}
	snap.Edges = []protocol.Edge{
		{From: "tenebrae-strats", To: "tenebrae", Kind: "member_of", FromRef: &protocol.GraphRef{Kind: "agent", ID: "agent_strats"}, ToRef: &protocol.GraphRef{Kind: "team", ID: "tenebrae"}},
		{From: "tenebrae 3", To: "tenebrae", Kind: "member_of", FromRef: &protocol.GraphRef{Kind: "agent", ID: "agent_teneb"}, ToRef: &protocol.GraphRef{Kind: "team", ID: "tenebrae"}},
	}
	src := &fakeSource{snap: snap}
	now := t0
	src.convFirst = protocol.ConversationPage{Total: 14, Conversations: []protocol.ConversationView{
		{
			ID: messagePairID(strats, teneb), From: strats, To: teneb, FromName: "tenebrae-strats", ToName: "tenebrae 3",
			Count: 24, LastLine: "blocked on the migration: PR #182 needs review", Type: protocol.TypeRequest,
			State: protocol.StateStored, LastMessageID: "m_scene_1", At: now.Add(-2 * time.Minute),
			FromHarness: "claude", ToHarness: "pi", OwnerNamed: true, Blocked: true,
			PeakHour: 30, MedianHour: 2,
		},
		{
			ID: "c_app", From: app, To: strats, FromName: "CTO (Trade)", ToName: "tenebrae-strats",
			Count: 3, LastLine: "the owner approved the trade", Type: protocol.TypeReply,
			State: protocol.StateReplied, LastMessageID: "m_scene_2", At: now.Add(-6 * time.Minute),
			FromHarness: "bot", ToHarness: "claude", OwnerNamed: true,
		},
		{
			ID: messagePairID(teneb, agentnt), From: teneb, To: agentnt, FromName: "tenebrae 3", ToName: "agent-net 2",
			Count: 5, LastLine: "T8 registration is ready; see ch_8c3bc9d80f8dd4aa", Type: protocol.TypeRequest,
			State: protocol.StateStored, LastMessageID: "m_scene_3", At: now.Add(-12 * time.Minute),
			FromHarness: "pi", ToHarness: "claude",
		},
		{
			ID: "worker:" + worker, From: strats, To: worker, FromName: "tenebrae-strats",
			WorkerSession: worker, WorkerOwner: strats, Task: "T8 registration PR #182",
			Count: 8, LastLine: "[agent-net worker notice]", Type: protocol.TypeReply,
			State: protocol.StateStored, LastMessageID: "m_scene_w", At: now.Add(-5 * time.Minute),
			FromHarness: "claude", ToHarness: "codex",
		},
	}}
	for i := 0; i < 8; i++ {
		src.convFirst.Conversations = append(src.convFirst.Conversations, protocol.ConversationView{
			ID: fmt.Sprintf("c_step_%d", i), From: "agent-net", To: agentnt, FromName: "agent-net", ToName: "agent-net 2",
			Count: 1, LastLine: "[agent-net step task reminder] T8 registration", Type: protocol.TypeNotification,
			State: protocol.StateStored, LastMessageID: fmt.Sprintf("m_step_%d", i),
			At: now.Add(-time.Duration(i+1) * time.Minute), FromHarness: "agent-net", ToHarness: "claude",
			System: true, SystemLine: "[agent-net step task reminder] T8 registration",
		})
	}
	for i := 0; i < 2; i++ {
		src.convFirst.Conversations = append(src.convFirst.Conversations, protocol.ConversationView{
			ID: fmt.Sprintf("c_check_%d", i), From: "agent-net", To: agentnt, FromName: "agent-net", ToName: "agent-net 2",
			Count: 1, LastLine: idleCheckBodyForTest, Type: protocol.TypeNotification,
			State: protocol.StateStored, LastMessageID: fmt.Sprintf("m_check_%d", i),
			At: now.Add(-time.Duration(i+10) * time.Minute), FromHarness: "agent-net", ToHarness: "claude",
			System: true, SystemLine: idleCheckBodyForTest,
		})
	}
	src.events = []protocol.FeedEvent{
		ev(1, -2*time.Minute, "msg.stored", "m_scene_1", strats, teneb, "request", func(e *protocol.FeedEvent) {
			e.ConversationID = "c_burst"
		}),
		ev(2, -time.Minute, "msg.stored", "m_scene_1b", teneb, strats, "reply", func(e *protocol.FeedEvent) {
			e.ConversationID = "c_burst"
			e.Parent = "m_scene_1"
		}),
		ev(3, -5*time.Minute, "msg.stored", "m_scene_w", worker, strats, "notification", func(e *protocol.FeedEvent) {
			e.ConversationID = "c_worker"
		}),
		ev(4, -30*time.Minute, "node.connected", "", "", "", "", func(e *protocol.FeedEvent) { e.Hostname = "build-vm" }),
	}
	src.messages = map[string]protocol.Message{
		"m_scene_1": {
			ID: "m_scene_1", ConversationID: messagePairID(strats, teneb), From: strats, To: teneb, Type: protocol.TypeRequest,
			Body:      "The migration is blocked on the review of PR #182. Change ch_8c3bc9d80f8dd4aa holds the fix, and the Desk ask i_4d2a91c0d4e5 waits for your answer. This paragraph is long on purpose so the conversation panel has to wrap it over several lines instead of cutting it with an ellipsis.",
			CreatedAt: t0.Add(-2 * time.Minute),
		},
		"m_scene_1b": {
			ID: "m_scene_1b", ConversationID: "c_burst", From: teneb, To: strats, Type: protocol.TypeReply,
			Body:      "I will review it after the next check. The rollback path is documented in docs/runbook.md and the test suite is green.",
			CreatedAt: t0.Add(-time.Minute),
		},
		"m_scene_w": {
			ID: "m_scene_w", ConversationID: "c_worker", From: worker, To: strats, Type: protocol.TypeNotification,
			Body:      "[agent-net worker notice]\nstatus: done\nrun time: 8m\nchanges (git diff --stat):\n 4 files changed, 60 insertions(+)",
			CreatedAt: t0.Add(-5 * time.Minute),
		},
	}
	m := New(src)
	m.loc, m.now = time.UTC, func() time.Time { return t0 }
	m.tab, m.focus = messagesTab, focusFeed
	m = send(m, tea.WindowSizeMsg{Width: w, Height: h})
	m = refresh(m, src)
	m = drainSnippets(t, m)
	m = keys(m, "4")
	return m
}

// The scene's key facts are visible in the three views.
func TestMessageThreadSceneContent(t *testing.T) {
	m := messageScene(t, 160, 40)
	m.messageSubtab = messageAgents
	m.convSel = 0
	if m.convLoaded {
		m.rebuildConversations()
	}
	agents := ansiStripJoin(m.messagesView(160, 40))
	for _, want := range []string{"Threads", "tenebrae-strats", "tenebrae 3", "blocked", "burst 30/h", "(app)", "PR #182", "ch_8c3bc9d8", "ask i_4d2a91c0"} {
		if !strings.Contains(agents, want) {
			t.Errorf("Agents scene is missing %q:\n%s", want, agents)
		}
	}
	m.messageSubtab = messageWorkers
	if m.convLoaded {
		m.rebuildConversations()
	}
	workers := ansiStripJoin(m.messagesView(160, 40))
	for _, want := range []string{"worker 1", "T8 registration", "codex", "PR #182", "done"} {
		if !strings.Contains(workers, want) {
			t.Errorf("Workers scene is missing %q:\n%s", want, workers)
		}
	}
	m.messageSubtab = messageSystem
	if m.convLoaded {
		m.rebuildConversations()
	}
	system := ansiStripJoin(m.messagesView(160, 40))
	for _, want := range []string{"step reminders", "agent-net 2", "8 today", "idle checks", "2 today"} {
		if !strings.Contains(system, want) {
			t.Errorf("System scene is missing %q:\n%s", want, system)
		}
	}
}

func ansiStripJoin(lines []string) string {
	return ansi.Strip(strings.Join(lines, "\n"))
}
