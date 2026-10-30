package tui

import (
	"fmt"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	"github.com/niparis/agent-net/internal/protocol"
	"strings"
	"testing"
	"time"
)

func TestMessageConversationsGroupAndSort(t *testing.T) {
	m := newModel(120, 34)
	m.tab = 1
	m.focus = focusFeed
	m.feed = newFeed()
	m.feed.apply([]protocol.FeedEvent{
		{Event: protocol.Event{ID: 1, Kind: "msg.stored", MessageID: "one", Time: t0.Add(-3 * time.Minute)}, ConversationID: "a", From: "s_0000000000000001", To: "s_0000000000000002", Type: "request"},
		{Event: protocol.Event{ID: 2, Kind: "msg.stored", MessageID: "two", Time: t0.Add(-2 * time.Minute)}, ConversationID: "b", From: "s_0000000000000001", To: "s_0000000000000002", Type: "notification"},
		{Event: protocol.Event{ID: 3, Kind: "msg.stored", MessageID: "three", Time: t0.Add(-time.Minute)}, ConversationID: "a", Parent: "one", From: "s_0000000000000002", To: "s_0000000000000001", Type: "reply"},
	})
	m.feed.byMsg["one"].Snippet = "first topic"
	m.feed.byMsg["two"].Snippet = "second topic"
	rows := m.messageConversations()
	if len(rows) != 2 || rows[0].id != "a" || len(rows[0].items) != 2 {
		t.Fatalf("rows: %+v", rows)
	}
	v := ansi.Strip(strings.Join(m.messagesView(120, 20), "\n"))
	if !strings.Contains(v, "first") || !strings.Contains(v, "secon") {
		t.Fatal(v)
	}
}

func TestMessageConversationNavigation(t *testing.T) {
	m := newModel(120, 34)
	m.tab = 1
	m.focus = focusFeed
	m.feed = newFeed()
	for i, id := range []string{"first", "second"} {
		m.feed.apply([]protocol.FeedEvent{{Event: protocol.Event{ID: int64(i + 1), Kind: "msg.stored", MessageID: id, Time: t0.Add(time.Duration(i) * time.Minute)}, ConversationID: id}})
	}
	m.feedCur = 1
	m = keys(m, "down")
	if got := m.feed.items[m.feedCur].MsgID; got != "first" {
		t.Fatalf("selected %s", got)
	}
}

func TestMessageChatShowsBodyAndCollapsesWorkerNotice(t *testing.T) {
	m := newModel(120, 34)
	m.tab = 1
	m.focus = focusFeed
	m.feed = newFeed()
	m.feed.apply([]protocol.FeedEvent{{Event: protocol.Event{ID: 1, Kind: "msg.stored", MessageID: "one", Time: t0.Add(-time.Minute)}, ConversationID: "a", From: "s_0000000000000001", To: "s_0000000000000002", Type: "request"}, {Event: protocol.Event{ID: 2, Kind: "msg.stored", MessageID: "two", Time: t0}, ConversationID: "a", From: "s_0000000000000002", To: "s_0000000000000001", Type: "reply"}})
	m.feed.byMsg["one"].Body = "first line\nsecond line\nthird line\nfourth line"
	m.feed.byMsg["two"].Body = "[agent-net worker notice]\nstatus: done\nrun time: 1h5m\nchanges (git diff --stat):\n 15 files changed, 20 insertions(+)"
	m.feedCur = 1
	v := ansi.Strip(strings.Join(m.messagesView(120, 22), "\n"))
	for _, s := range []string{"first line", "second line", "third line", "…", "done · 1h5m · 15 files"} {
		if !strings.Contains(v, s) {
			t.Errorf("missing %q: %s", s, v)
		}
	}
	if strings.Contains(v, "fourth line") {
		t.Fatal(v)
	}
}

func TestMessageSystemEventsToggle(t *testing.T) {
	m := newModel(120, 34)
	m.tab = 1
	m.focus = focusFeed
	m.feed = newFeed()
	m.feed.apply([]protocol.FeedEvent{{Event: protocol.Event{ID: 1, Kind: "node.connected", Hostname: "build", Time: t0}}})
	v := ansi.Strip(strings.Join(m.messagesView(120, 12), "\n"))
	if !strings.Contains(v, "1 system events · e to show") || strings.Contains(v, "build connected") {
		t.Fatal(v)
	}
	m = keys(m, "e")
	v = ansi.Strip(strings.Join(m.messagesView(120, 12), "\n"))
	if !strings.Contains(v, "build connected") {
		t.Fatal(v)
	}
}

func TestConversationWorkerNameAndActivity(t *testing.T) {
	m := newModel(120, 34)
	m.tab = 1
	m.feed = newFeed()
	id := m.snap.Agents[0].SessionID
	m.snap.Agents[0].Worker = &protocol.WorkerInfo{ID: "w_8"}
	m.snap.Agents[0].Profile.Name = "shop worker 8"
	m.feed.apply([]protocol.FeedEvent{{Event: protocol.Event{ID: 1, Kind: "msg.stored", MessageID: "one", Time: t0.Add(-5 * time.Minute)}, ConversationID: "a", From: id, To: "s_0000000000000002", Type: "request"}, {Event: protocol.Event{ID: 2, Kind: "msg.delivered", MessageID: "one", Time: t0.Add(-time.Minute)}, ConversationID: "a"}})
	rows := m.messageConversations()
	if !rows[0].at.Equal(t0.Add(-time.Minute)) {
		t.Fatal(rows[0].at)
	}
	v := ansi.Strip(m.messageConversationLine(rows[0], 80, false))
	if !strings.Contains(v, "worker 8") || strings.Contains(v, "shop worker") {
		t.Fatal(v)
	}
}

func TestMessageSelectBodyWithinConversation(t *testing.T) {
	m := newModel(120, 34)
	m.tab = 1
	m.focus = focusFeed
	m.feed = newFeed()
	m.feed.apply([]protocol.FeedEvent{{Event: protocol.Event{ID: 1, Kind: "msg.stored", MessageID: "one", Time: t0}, ConversationID: "a"}, {Event: protocol.Event{ID: 2, Kind: "msg.stored", MessageID: "two", Time: t0}, ConversationID: "a"}})
	m.feedCur = 0
	m = keys(m, "right")
	next, _ := m.update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("v")})
	m = next.(Model)
	if m.open == nil || m.open.id != "two" {
		t.Fatalf("opened %+v", m.open)
	}
}

func TestConversationScreens(t *testing.T) {
	for _, size := range [][2]int{{80, 24}, {120, 34}} {
		snap := fakeSnapshot()
		worker := snap.Agents[0]
		worker.SessionID = "s_worker"
		worker.Profile.Name = "build worker 8"
		worker.Worker = &protocol.WorkerInfo{ID: "w_8"}
		snap.Agents = append(snap.Agents, worker)
		a, b := "s_0000000000000001", "s_0000000000000002"
		events := []protocol.FeedEvent{
			ev(1, -10*time.Minute, "node.connected", "", "", "", "", func(e *protocol.FeedEvent) { e.Hostname = "build" }),
			ev(2, -9*time.Minute, "msg.stored", "m_a", a, "s_worker", "request", func(e *protocol.FeedEvent) { e.ConversationID = "c_a" }),
			ev(3, -7*time.Minute, "msg.stored", "m_b", "s_worker", a, "reply", func(e *protocol.FeedEvent) { e.ConversationID = "c_a"; e.Parent = "m_a" }),
			ev(4, -6*time.Minute, "msg.stored", "m_c", a, b, "notification", func(e *protocol.FeedEvent) { e.ConversationID = "c_b" }),
			ev(5, -5*time.Minute, "node.disconnected", "", "", "", "", func(e *protocol.FeedEvent) { e.Hostname = "build" }),
			ev(6, -3*time.Minute, "msg.stored", "m_d", a, "s_worker", "request", func(e *protocol.FeedEvent) { e.ConversationID = "c_c" }),
			ev(7, -time.Minute, "msg.stored", "m_e", "s_worker", a, "reply", func(e *protocol.FeedEvent) { e.ConversationID = "c_c"; e.Parent = "m_d" }),
		}
		src := &fakeSource{snap: snap, events: events, messages: map[string]protocol.Message{
			"m_a": {ID: "m_a", ConversationID: "c_a", Body: "Review the change\nCheck the tests."},
			"m_b": {ID: "m_b", ConversationID: "c_a", Body: "[agent-net worker notice]\nstatus: done\nrun time: 1h5m\nchanges (git diff --stat):\n 15 files changed, 20 insertions(+)"},
			"m_c": {ID: "m_c", ConversationID: "c_b", Body: "The deploy is ready."},
			"m_d": {ID: "m_d", ConversationID: "c_c", Body: "Polish the sessions panel\nRun the checks."},
			"m_e": {ID: "m_e", ConversationID: "c_c", Body: "[agent-net worker notice]\nstatus: done\nrun time: 12m\nchanges (git diff --stat):\n 3 files changed, 4 insertions(+)"},
		}}
		m := New(src)
		m.loc, m.now = time.UTC, func() time.Time { return t0 }
		m = send(m, tea.WindowSizeMsg{Width: size[0], Height: size[1]})
		m = refresh(m, src)
		m = drainSnippets(t, m)
		m = keys(m, "2")
		v := m.View()
		if !strings.Contains(ansi.Strip(v), "Polish") {
			t.Fatal(v)
		}
		golden(t, fmt.Sprintf("messages_conversations_%dx%d", size[0], size[1]), v)
		m = keys(m, "e")
		golden(t, fmt.Sprintf("messages_events_%dx%d", size[0], size[1]), m.View())
	}
}

func TestConversationFeedIncludesSessionDisconnect(t *testing.T) {
	m := newModel(120, 34)
	m.tab = 1
	m.focus = focusFeed
	m.feed = newFeed()
	m.feed.apply([]protocol.FeedEvent{{Event: protocol.Event{ID: 1, Kind: "session.disconnected", SessionID: "s_0000000000000001", Time: t0}}})
	m = keys(m, "e")
	v := ansi.Strip(strings.Join(m.messagesView(120, 12), "\n"))
	if !strings.Contains(v, "tenebrae-53 disconnected") {
		t.Fatal(v)
	}
}

func TestMessageChatScrollsToSelectedMessage(t *testing.T) {
	m := newModel(120, 34)
	m.tab = 1
	m.focus = focusFeed
	m.feed = newFeed()
	for i := 0; i < 8; i++ {
		id := fmt.Sprintf("m%d", i)
		m.feed.apply([]protocol.FeedEvent{{Event: protocol.Event{ID: int64(i + 1), Kind: "msg.stored", MessageID: id, Time: t0}, ConversationID: "a"}})
		m.feed.byMsg[id].Body = id + " body"
	}
	m.feedCur = 7
	v := ansi.Strip(strings.Join(m.messagesView(120, 10), "\n"))
	if !strings.Contains(v, "m7 body") {
		t.Fatal(v)
	}
}
