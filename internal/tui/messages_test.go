package tui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"
	"github.com/niparis/agent-net/internal/protocol"
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
	m.feed.byMsg["three"].Snippet = "latest topic"
	rows := m.messageConversations()
	if len(rows) != 2 || rows[0].id != "a" || len(rows[0].items) != 2 {
		t.Fatalf("rows: %+v", rows)
	}
	v := ansi.Strip(strings.Join(m.messagesView(120, 20), "\n"))
	if !strings.Contains(v, "latest") || !strings.Contains(v, "secon") {
		t.Fatal(v)
	}
}

func TestMessageThreadShowsLongBriefAndShortReply(t *testing.T) {
	const a, b = "remote-a", "remote-b"
	brief := "Worker brief: " + strings.Repeat("run the checks and report what changed; ", 40)
	events := []protocol.FeedEvent{
		ev(1, -2*time.Minute, "msg.stored", "brief", a, b, "request", func(e *protocol.FeedEvent) { e.ConversationID = "thread" }),
		ev(2, -time.Minute, "msg.stored", "reply", b, a, "reply", func(e *protocol.FeedEvent) { e.ConversationID = "thread"; e.Parent = "brief" }),
	}
	m, src := feedModel(events, map[string]protocol.Message{
		"brief": {ID: "brief", Body: brief},
		"reply": {ID: "reply", Body: "Checks passed."},
	})
	// The owner can read a thread between two remote sessions through the
	// message_status relay, even though neither end runs on this machine.
	m.snap.OwnerSession = ownerID
	src.snap.OwnerSession = ownerID
	m = drainSnippets(t, m)
	if got := m.feed.byMsg["brief"].Snippet; !strings.HasPrefix(got, "Worker brief: ") || !strings.HasSuffix(got, "…") {
		t.Fatalf("long first line should keep real text and end in an ellipsis, got %q", got)
	}
	rows := m.messageConversations()
	if len(rows) != 1 {
		t.Fatalf("got %d threads", len(rows))
	}
	chat := strings.Join(m.conversationChat(rows[0], 80), "\n")
	for _, want := range []string{"Worker brief: ", "Checks passed."} {
		if !strings.Contains(chat, want) {
			t.Errorf("thread is missing %q:\n%s", want, chat)
		}
	}
}

func TestConversationExplainsUnavailableMessageBody(t *testing.T) {
	m := newModel(120, 34)
	m.snap.OwnerSession = ownerID
	m.feed = newFeed()
	m.feed.apply([]protocol.FeedEvent{{Event: protocol.Event{ID: 1, Kind: "msg.stored", MessageID: "held", Time: t0}, From: "remote-a", To: "remote-b", Type: "request"}})
	m.applySnippet(snippetMsg{id: "held", err: fmt.Errorf("body held on build-vm, offline")})
	chat := strings.Join(m.conversationChat(m.messageConversations()[0], 80), "\n")
	if !strings.Contains(chat, "body held on build-vm, offline") {
		t.Fatalf("the thread did not explain why its body is unavailable:\n%s", chat)
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

func TestMessageThreadFallsBackToSessionPairAndShowsLatestSummary(t *testing.T) {
	const a, b = "s_0000000000000001", "s_0000000000000002"
	m := newModel(120, 34)
	m.tab = 1
	m.focus = focusFeed
	m.feed = newFeed()
	m.feed.apply([]protocol.FeedEvent{
		ev(1, -5*time.Minute, "msg.stored", "one", a, b, "request", nil),
		ev(2, -3*time.Minute, "msg.delivered", "one", a, b, "request", nil),
		ev(3, -2*time.Minute, "msg.stored", "two", b, a, "reply", nil),
		ev(4, -time.Minute, "msg.delivered", "two", b, a, "reply", nil),
	})
	m.feed.byMsg["one"].Snippet = "First request"
	m.feed.byMsg["two"].Snippet = "Latest reply"
	rows := m.messageConversations()
	if len(rows) != 1 || len(rows[0].items) != 2 {
		t.Fatalf("messages without a conversation ID should group by session pair: %+v", rows)
	}
	line := ansi.Strip(m.messageConversationLine(rows[0], 110, false))
	for _, want := range []string{"Latest reply", "2 messages", "1m ago"} {
		if !strings.Contains(line, want) {
			t.Errorf("thread summary is missing %q: %q", want, line)
		}
	}
	m = keys(m, "enter")
	if m.open != nil || m.conv != nil || m.feedCur != rows[0].items[1] {
		t.Fatalf("enter should open the latest message in the thread pane, got open=%+v conv=%+v cursor=%d", m.open, m.conv, m.feedCur)
	}
}

func TestWorkerMessagesCollapseByWorkerAndCanBeHidden(t *testing.T) {
	const owner, other, w1, w2 = "s_owner", "s_other", "s_worker1", "s_worker2"
	m := newModel(120, 34)
	m.tab = 1
	m.focus = focusFeed
	m.snap = Snapshot{NodeID: "n", Agents: []protocol.AgentView{
		agent(owner, "n", "Mac", "claude", "a2a-ocs", nil),
		agent(other, "n", "Mac", "pi", "reviewer", nil),
		agent(w1, "n", "Mac", "opencode", "shop worker 6", func(a *protocol.AgentView) { a.Worker = &protocol.WorkerInfo{ID: "w_6", Owner: owner} }),
		agent(w2, "n", "Mac", "opencode", "shop worker 7", func(a *protocol.AgentView) { a.Worker = &protocol.WorkerInfo{ID: "w_7", Owner: owner} }),
	}}
	m.feed = newFeed()
	m.feed.apply([]protocol.FeedEvent{
		ev(1, -4*time.Minute, "msg.stored", "agent", owner, other, "request", nil),
		ev(2, -3*time.Minute, "msg.stored", "w1a", owner, w1, "request", nil),
		ev(3, -2*time.Minute, "msg.stored", "w1b", w1, owner, "reply", func(e *protocol.FeedEvent) { e.Parent = "w1a" }),
		ev(4, -time.Minute, "msg.stored", "w1c", owner, w1, "request", nil),
		ev(5, -time.Second, "msg.stored", "w2a", owner, w2, "request", nil),
	})
	m.feed.byMsg["w1c"].Body = "[agent-net worker notice]\nstatus: done"
	rows := m.messageConversations()
	if len(rows) != 3 || rows[0].worker || !rows[1].worker || !rows[2].worker {
		t.Fatalf("expected agent thread first, followed by two collapsed worker threads: %+v", rows)
	}
	var workerRow *messageConversation
	for i := range rows {
		if rows[i].workerSession == w1 {
			workerRow = &rows[i]
		}
	}
	if workerRow == nil {
		t.Fatal("worker 6 was not listed")
	}
	line := ansi.Strip(m.messageConversationLine(*workerRow, 110, false))
	for _, want := range []string{"a2a-ocs → worker 6", "3 messages", "done"} {
		if !strings.Contains(line, want) {
			t.Errorf("worker summary is missing %q: %q", want, line)
		}
	}
	m = keys(m, "w")
	if got := m.messageConversations(); len(got) != 1 || got[0].worker {
		t.Fatalf("w should hide worker threads, got %+v", got)
	}
	m = keys(m, "w")
	if got := m.messageConversations(); len(got) != 3 {
		t.Fatalf("w should show worker threads again, got %+v", got)
	}
}

func TestMessageOrderingInstructionMarkAndColorLegend(t *testing.T) {
	const owner, a, b, worker = "s_owner", "s_agent", "s_other", "s_worker"
	m := newModel(120, 34)
	m.tab = 1
	m.focus = focusFeed
	m.now = func() time.Time { return t0 }
	m.snap = Snapshot{NodeID: "n", Agents: []protocol.AgentView{
		agent(owner, "n", "Mac", "owner", "you", nil),
		agent(a, "n", "Mac", "claude", "a2a-ocs", nil),
		agent(b, "n", "Mac", "pi", "reviewer", nil),
		agent(worker, "n", "Mac", "opencode", "shop worker 6", func(a *protocol.AgentView) { a.Worker = &protocol.WorkerInfo{ID: "w_6", Owner: owner} }),
	}}
	m.feed = newFeed()
	m.feed.apply([]protocol.FeedEvent{
		ev(1, -10*time.Minute, "msg.stored", "instruction", owner, a, "request", func(e *protocol.FeedEvent) { e.ConversationID = "instruction" }),
		ev(2, -2*time.Minute, "msg.stored", "older", a, b, "notification", func(e *protocol.FeedEvent) { e.ConversationID = "older" }),
		ev(3, -time.Minute, "msg.stored", "recent", a, b, "request", func(e *protocol.FeedEvent) { e.ConversationID = "recent" }),
		ev(4, -time.Second, "msg.stored", "worker-task", owner, worker, "request", func(e *protocol.FeedEvent) { e.ConversationID = "worker-task" }),
	})
	m.feed.byMsg["instruction"].OwnerInstruction = "please inspect the failing check"
	m.feed.byMsg["instruction"].Snippet = "Inspect the failing check"
	m.feed.byMsg["older"].Snippet = "Older agent note"
	m.feed.byMsg["recent"].Snippet = "Waiting on test"
	rows := m.messageConversations()
	if len(rows) != 4 || rows[0].id != "instruction" || rows[1].id != "recent" || rows[2].id != "older" || !rows[3].worker {
		t.Fatalf("instruction, recent agent threads, then worker threads expected: %+v", rows)
	}
	old := lipgloss.ColorProfile()
	defer lipgloss.SetColorProfile(old)
	lipgloss.SetColorProfile(termenv.TrueColor)
	line := m.messageConversationLine(rows[0], 110, false)
	if !strings.Contains(ansi.Strip(line), "you→") {
		t.Fatalf("owner instruction marker missing: %s", line)
	}
	warning := lipgloss.NewStyle().Foreground(roleWarning).Render("Waiting on test · a2a-ocs ↔ reviewer")
	if !strings.Contains(m.messageConversationLine(rows[1], 110, false), warning) {
		t.Fatal("a request without a reply is not orange")
	}
	view := ansi.Strip(strings.Join(m.messagesView(120, 22), "\n"))
	if !strings.Contains(view, "orange: waiting for a reply") || !strings.Contains(view, "you→") {
		t.Fatalf("the screen does not explain its marker and orange lines:\n%s", view)
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

func TestConversationAgeUsesLastMessageTime(t *testing.T) {
	m := newModel(120, 34)
	m.tab = 1
	m.feed = newFeed()
	m.feed.apply([]protocol.FeedEvent{{Event: protocol.Event{ID: 1, Kind: "msg.stored", MessageID: "m", Time: t0.Add(-5 * time.Minute)}, ConversationID: "c"}, {Event: protocol.Event{ID: 2, Kind: "msg.delivered", MessageID: "m", Time: t0.Add(-time.Minute)}, ConversationID: "c"}})
	v := ansi.Strip(m.messageConversationLine(m.messageConversations()[0], 80, false))
	if !strings.Contains(v, "5m ago") {
		t.Fatal(v)
	}
}
