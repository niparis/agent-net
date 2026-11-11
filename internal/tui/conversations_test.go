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

// runCommands runs the commands an update returned, including a batch, so a
// test sees the answer a real program would have received.
func runCommands(t *testing.T, m Model, cmd tea.Cmd) Model {
	t.Helper()
	if cmd == nil {
		return m
	}
	msg := cmd()
	if msg == nil {
		return m
	}
	if batch, ok := msg.(tea.BatchMsg); ok {
		for _, c := range batch {
			m = runCommands(t, m, c)
		}
		return m
	}
	next, more := m.Update(msg)
	m = next.(Model)
	return runCommands(t, m, more)
}

// refreshAndRun is refresh, but it also runs the commands the answer starts,
// such as the messages of the selected stored conversation.
func refreshAndRun(t *testing.T, m Model, src Source) Model {
	t.Helper()
	m.src = src
	res := m.fetchCmd()().(fetchedMsg)
	next, cmd := m.Update(res)
	m = next.(Model)
	return runCommands(t, m, cmd)
}

func conversationView(id, from, to, fromName, toName string, count int, line, state string, at time.Time) protocol.ConversationView {
	return protocol.ConversationView{
		ID: id, From: from, To: to, FromName: fromName, ToName: toName,
		Count: count, LastLine: line, State: state, LastMessageID: "m_" + id,
		At: at,
	}
}

func TestMessageListShowsStoredRowsBeyondTheFeedBuffer(t *testing.T) {
	src := &fakeSource{snap: fakeSnapshot()}
	const a, b = "s_0000000000000001", "s_0000000000000002"
	var rows []protocol.ConversationView
	for i := 0; i < 40; i++ {
		line := fmt.Sprintf("stored conversation %02d", i)
		rows = append(rows, conversationView(fmt.Sprintf("c_%02d", i), a, b, "tenebrae-53", "strategies",
			3, line, protocol.StateReplied, t0.Add(-time.Duration(i)*time.Minute)))
	}
	src.convFirst = protocol.ConversationPage{Conversations: rows, Total: 400, Next: "older"}
	m := New(src)
	m.loc, m.now = time.UTC, func() time.Time { return t0 }
	m.tab, m.focus = messagesTab, focusFeed
	m = send(m, tea.WindowSizeMsg{Width: 120, Height: 40})
	m = refreshAndRun(t, m, src)

	got := m.messageConversations()
	if len(got) != len(rows) {
		t.Fatalf("the list has %d rows, want the stored %d", len(got), len(rows))
	}
	last := got[len(got)-1]
	if last.lastLine != "stored conversation 39" {
		t.Fatalf("last stored row = %+v", last)
	}
	if len(m.feed.items) >= len(rows) {
		t.Skip("the fixture feed is larger than the stored list; the point is not tested")
	}
	m.move(1 << 20) // scroll to the oldest row
	view := ansi.Strip(strings.Join(m.messagesView(120, 40), "\n"))
	if !strings.Contains(view, "39m ago") {
		t.Fatalf("the TUI did not show a row beyond the feed buffer:\n%s", view)
	}
}

func TestMessageListLoadsTheRestOfTheListOnOpen(t *testing.T) {
	src := &fakeSource{snap: fakeSnapshot()}
	const a, b = "s_0000000000000001", "s_0000000000000002"
	var first []protocol.ConversationView
	for i := 0; i < 5; i++ {
		first = append(first, conversationView(fmt.Sprintf("c_%02d", i), a, b, "tenebrae-53", "strategies",
			1, fmt.Sprintf("first page %d", i), protocol.StateReplied, t0.Add(-time.Duration(i)*time.Minute)))
	}
	var second []protocol.ConversationView
	for i := 5; i < 8; i++ {
		second = append(second, conversationView(fmt.Sprintf("c_%02d", i), a, b, "tenebrae-53", "strategies",
			1, fmt.Sprintf("second page %d", i), protocol.StateReplied, t0.Add(-time.Duration(i)*time.Minute)))
	}
	src.convFirst = protocol.ConversationPage{Conversations: first, Total: 8, Next: "p2"}
	src.convMore = map[string]protocol.ConversationPage{"p2": {Conversations: second, Total: 8}}
	m := New(src)
	m.loc, m.now = time.UTC, func() time.Time { return t0 }
	m.tab, m.focus = messagesTab, focusFeed
	m = send(m, tea.WindowSizeMsg{Width: 120, Height: 40})
	m = refreshAndRun(t, m, src)

	if got := len(m.messageConversations()); got != 8 {
		t.Fatalf("after the load the list has %d rows, want 8", got)
	}
	if len(src.convCalls) < 2 || src.convCalls[len(src.convCalls)-1].Cursor != "p2" {
		t.Fatalf("the rest of the list was not asked for: %+v", src.convCalls)
	}
	rows := m.messageConversations()
	if len(rows) != 8 || rows[len(rows)-1].lastLine != "second page 7" {
		t.Fatalf("the second page is not in the list: %+v", rows)
	}
	if view := ansi.Strip(strings.Join(m.messagesView(120, 40), "\n")); !strings.Contains(view, "8 threads") {
		t.Fatalf("the second page is not on screen:\n%s", view)
	}
}

func TestMessagesViewNeverCallsTheConversationSource(t *testing.T) {
	src := &fakeSource{snap: fakeSnapshot(), events: fakeEvents()}
	const a, b = "s_0000000000000001", "s_0000000000000002"
	src.convFirst = protocol.ConversationPage{Conversations: []protocol.ConversationView{
		conversationView("c_1", a, b, "tenebrae-53", "strategies", 2, "one stored row", protocol.StateReplied, t0.Add(-time.Minute)),
	}, Total: 1}
	m := New(src)
	m.loc, m.now = time.UTC, func() time.Time { return t0 }
	m.tab, m.focus = messagesTab, focusFeed
	m = send(m, tea.WindowSizeMsg{Width: 120, Height: 40})
	m = refreshAndRun(t, m, src)

	src.convCalls, src.convMsgCalls = nil, nil
	for i := 0; i < 5; i++ {
		_ = m.testView()
	}
	if len(src.convCalls) != 0 {
		t.Fatalf("View asked for conversations %d times", len(src.convCalls))
	}
	if len(src.convMsgCalls) != 0 {
		t.Fatalf("View asked for conversation messages %d times", len(src.convMsgCalls))
	}
}

func TestWorkerConversationRowShowsTheTaskLine(t *testing.T) {
	const owner, worker = "s_owner_fixture", "s_worker_fixture"
	snap := fakeSnapshot()
	snap.OwnerSession = owner
	snap.Agents = append(snap.Agents,
		agent(owner, "n_mac", "Mac", "owner", "you", nil),
		agent(worker, "n_mac", "Mac", "opencode", "shop worker 8", func(a *protocol.AgentView) {
			a.Worker = &protocol.WorkerInfo{ID: "w_8", Owner: owner, Task: "Polish the sessions panel\nRun the checks."}
		}))
	src := &fakeSource{snap: snap}
	src.convFirst = protocol.ConversationPage{Conversations: []protocol.ConversationView{
		conversationView("c_worker", owner, worker, "you", "shop worker 8", 2, "worker says done",
			protocol.StateReplied, t0.Add(-time.Minute)),
	}, Total: 1}
	m := New(src)
	m.loc, m.now = time.UTC, func() time.Time { return t0 }
	m.tab, m.focus = messagesTab, focusFeed
	m.messageSubtab = messageWorkers
	m = send(m, tea.WindowSizeMsg{Width: 120, Height: 40})
	m = refreshAndRun(t, m, src)

	rows := m.messageConversations()
	if len(rows) != 1 || !rows[0].worker {
		t.Fatalf("worker row not found: %+v", rows)
	}
	line := ansi.Strip(m.messageConversationText(rows[0], 110, false))
	if !strings.Contains(line, "worker 8: Polish the sessions panel") {
		t.Fatalf("worker row does not show the task: %q", line)
	}
	if !strings.Contains(line, "opencode") {
		t.Fatalf("worker row does not show the harness: %q", line)
	}
}

func TestWorkerConversationRowShowsTheTitle(t *testing.T) {
	const owner, worker = "s_owner_fixture", "s_worker_fixture"
	snap := fakeSnapshot()
	snap.OwnerSession = owner
	snap.Agents = append(snap.Agents,
		agent(owner, "n_mac", "Mac", "owner", "you", nil),
		agent(worker, "n_mac", "Mac", "opencode", "shop worker 8", func(a *protocol.AgentView) {
			a.Worker = &protocol.WorkerInfo{ID: "w_8", Owner: owner, Task: "Polish the sessions panel\nRun the checks.", Title: "Fix the band"}
		}))
	src := &fakeSource{snap: snap}
	src.convFirst = protocol.ConversationPage{Conversations: []protocol.ConversationView{
		conversationView("c_worker", owner, worker, "you", "shop worker 8", 2, "worker says done",
			protocol.StateReplied, t0.Add(-time.Minute)),
	}, Total: 1}
	m := New(src)
	m.loc, m.now = time.UTC, func() time.Time { return t0 }
	m.tab, m.focus = messagesTab, focusFeed
	m.messageSubtab = messageWorkers
	m = send(m, tea.WindowSizeMsg{Width: 120, Height: 40})
	m = refreshAndRun(t, m, src)

	rows := m.messageConversations()
	if len(rows) != 1 || !rows[0].worker {
		t.Fatalf("worker row not found: %+v", rows)
	}
	line := ansi.Strip(m.messageConversationText(rows[0], 110, false))
	if !strings.Contains(line, "worker 8: Fix the band") {
		t.Fatalf("worker row does not show the title: %q", line)
	}
	if strings.Contains(line, "Polish the sessions panel") {
		t.Fatalf("worker row did not prefer the title: %q", line)
	}
}

func TestEndedWorkerRowUsesTheTaskAndCallerFromTheGateway(t *testing.T) {
	const caller, ended = "s_0000000000000001", "s_ended_worker_fixture"
	src := &fakeSource{snap: fakeSnapshot()}
	src.convFirst = protocol.ConversationPage{Conversations: []protocol.ConversationView{{
		ID: "worker:" + ended, From: caller, To: ended, FromName: "tenebrae-53",
		WorkerSession: ended, WorkerOwner: caller, Task: "Polish the sessions panel",
		Count: 2, LastLine: "[agent-net worker notice]", Type: protocol.TypeReply,
		State: protocol.StateStored, LastMessageID: "m_done", At: t0.Add(-time.Minute),
	}}, Total: 1}
	m := New(src)
	m.loc, m.now = time.UTC, func() time.Time { return t0 }
	m.tab, m.focus = messagesTab, focusFeed
	m.messageSubtab = messageWorkers
	m = send(m, tea.WindowSizeMsg{Width: 120, Height: 40})
	m = refreshAndRun(t, m, src)

	rows := m.messageConversations()
	if len(rows) != 1 || !rows[0].worker || rows[0].workerSession != ended {
		t.Fatalf("ended worker row not found: %+v", rows)
	}
	line := ansi.Strip(m.messageConversationText(rows[0], 110, false))
	if !strings.Contains(line, "tenebrae-53 → worker: Polish the sessions panel") {
		t.Fatalf("ended worker row = %q", line)
	}
	if strings.Contains(line, "unknown session") || strings.Contains(line, ended) {
		t.Fatalf("ended worker row leaks a missing session: %q", line)
	}
}

func TestConversationEndedWorkerScreens(t *testing.T) {
	const caller, first, second = "s_0000000000000001", "s_ended_worker_one", "s_ended_worker_two"
	src := &fakeSource{snap: fakeSnapshot(), events: nil}
	src.convFirst = protocol.ConversationPage{Total: 3, Conversations: []protocol.ConversationView{
		{
			ID: "worker:" + first, From: caller, To: first, FromName: "tenebrae-53",
			WorkerSession: first, WorkerOwner: caller, Task: "Polish the sessions panel and make the long list readable",
			Count: 2, LastLine: "[agent-net worker notice]", Type: protocol.TypeReply,
			State: protocol.StateStored, LastMessageID: "m_done_one", At: t0.Add(-2 * time.Minute),
		},
		{
			ID: "worker:" + second, From: caller, To: second, FromName: "tenebrae-53",
			WorkerSession: second, WorkerOwner: caller, Task: "Review the deploy",
			Count: 1, LastLine: "The deploy is ready.", Type: protocol.TypeRequest,
			State: protocol.StateReplied, LastMessageID: "m_done_two", At: t0.Add(-20 * time.Minute),
		},
		{
			ID: "c_plain", From: "s_0000000000000002", To: "s_0000000000000003", FromName: "strategies", ToName: "web",
			Count: 1, LastLine: "A conversation whose sides are still here.", Type: protocol.TypeRequest,
			State: protocol.StateReplied, LastMessageID: "m_plain", At: t0.Add(-30 * time.Minute),
		},
	}}
	src.convMsgs = map[string][]protocol.Message{
		"worker:" + first: {
			{ID: "m_done_one", ConversationID: "c_done_one", From: first, To: caller, Type: protocol.TypeReply,
				Body: "[agent-net worker notice]\nstatus: done\nrun time: 12m\n", CreatedAt: t0.Add(-2 * time.Minute)},
			{ID: "m_started_one", ConversationID: "c_started_one", From: first, To: caller, Type: protocol.TypeNotification,
				Body: protocol.WorkerStartedText(first, "opencode", "m1", "shop", "mac", time.Minute, ""), CreatedAt: t0.Add(-5 * time.Minute)},
			{ID: "m_task_one", ConversationID: "c_task_one", From: caller, To: first, Type: protocol.TypeRequest,
				Body: protocol.WorkerTaskBody("Polish the sessions panel and make the long list readable", "", ""), CreatedAt: t0.Add(-10 * time.Minute)},
		},
	}
	for _, size := range [][2]int{{80, 24}, {120, 34}} {
		m := New(src)
		m.loc, m.now = time.UTC, func() time.Time { return t0 }
		m.tab, m.focus = messagesTab, focusFeed
		m.messageSubtab = messageWorkers
		m = send(m, tea.WindowSizeMsg{Width: size[0], Height: size[1]})
		m = refreshAndRun(t, m, src)
		golden(t, fmt.Sprintf("messages_ended_workers_%dx%d", size[0], size[1]), m.testView())
	}
}

func TestOpeningAStoredConversationShowsItsGatewayMessages(t *testing.T) {
	src := &fakeSource{snap: fakeSnapshot(), events: nil}
	const a, b = "s_0000000000000001", "s_0000000000000002"
	src.convFirst = protocol.ConversationPage{Conversations: []protocol.ConversationView{
		conversationView("c_old", a, b, "tenebrae-53", "strategies", 2, "old summary", protocol.StateReplied, t0.Add(-48*time.Hour)),
	}, Total: 1}
	src.convMsgs = map[string][]protocol.Message{
		"c_old": {
			{ID: "m_old_reply", ConversationID: "c_old", From: b, To: a, Type: protocol.TypeReply,
				Body: "the old reply body", CreatedAt: t0.Add(-48*time.Hour + time.Minute)},
			{ID: "m_old_request", ConversationID: "c_old", From: a, To: b, Type: protocol.TypeRequest,
				Body: "the old request body", CreatedAt: t0.Add(-48 * time.Hour)},
		},
	}
	m := New(src)
	m.loc, m.now = time.UTC, func() time.Time { return t0 }
	m.tab, m.focus = messagesTab, focusFeed
	m = send(m, tea.WindowSizeMsg{Width: 120, Height: 40})
	m = refreshAndRun(t, m, src)

	view := ansi.Strip(strings.Join(m.messagesView(120, 30), "\n"))
	for _, want := range []string{"the old request body", "the old reply body"} {
		if !strings.Contains(view, want) {
			t.Fatalf("opening the stored conversation is missing %q:\n%s", want, view)
		}
	}
	if len(src.convMsgCalls) != 1 || src.convMsgCalls[0] != "c_old" {
		t.Fatalf("conversation messages were asked for %+v", src.convMsgCalls)
	}
}

func TestOpeningARemoteConversationShowsItsGatewayMessages(t *testing.T) {
	src := &fakeSource{snap: fakeSnapshot(), events: nil}
	const a, b = "s_remote_carol", "s_remote_dave"
	src.convFirst = protocol.ConversationPage{Conversations: []protocol.ConversationView{
		conversationView("c_remote", a, b, "carol", "dave", 2, "remote summary", protocol.StateReplied, t0.Add(-48*time.Hour)),
	}, Total: 1}
	src.convMsgs = map[string][]protocol.Message{
		"c_remote": {
			{ID: "m_remote_reply", ConversationID: "c_remote", From: b, To: a, Type: protocol.TypeReply,
				Body: "a reply between two other machines", CreatedAt: t0.Add(-48*time.Hour + time.Minute)},
			{ID: "m_remote_request", ConversationID: "c_remote", From: a, To: b, Type: protocol.TypeRequest,
				Body: "a request between two other machines", CreatedAt: t0.Add(-48 * time.Hour)},
		},
	}
	m := New(src)
	m.loc, m.now = time.UTC, func() time.Time { return t0 }
	m.tab, m.focus = messagesTab, focusFeed
	m = send(m, tea.WindowSizeMsg{Width: 120, Height: 40})
	m = refreshAndRun(t, m, src)

	rows := m.messageConversations()
	if len(rows) != 1 || rows[0].convID != "c_remote" {
		t.Fatalf("remote conversation not listed: %+v", rows)
	}
	view := ansi.Strip(strings.Join(m.messagesView(120, 30), "\n"))
	for _, want := range []string{"a request between two other machines", "a reply between two other machines"} {
		if !strings.Contains(view, want) {
			t.Fatalf("opening the remote conversation is missing %q:\n%s", want, view)
		}
	}
	if len(src.convMsgCalls) != 1 || src.convMsgCalls[0] != "c_remote" {
		t.Fatalf("remote conversation messages were asked for %+v", src.convMsgCalls)
	}
}

func TestConversationFullListScreens(t *testing.T) {
	const owner, worker = "s_owner_fixture", "s_worker_fixture"
	snap := fakeSnapshot()
	snap.OwnerSession = owner
	snap.Agents = append(snap.Agents,
		agent(owner, "n_mac", "Mac", "owner", "you", nil),
		agent(worker, "n_mac", "Mac", "opencode", "shop worker 8", func(a *protocol.AgentView) {
			a.Worker = &protocol.WorkerInfo{ID: "w_8", Owner: owner, Task: "Polish the sessions panel and make the long list readable"}
		}))
	const a, b = "s_0000000000000001", "s_0000000000000002"
	var rows []protocol.ConversationView
	for i := 0; i < 30; i++ {
		from, to, fromName, toName := a, b, "tenebrae-53", "strategies"
		if i == 2 {
			from, to, fromName, toName = owner, worker, "you", "shop worker 8"
		}
		line := fmt.Sprintf("Stored conversation %02d: keep the list full and readable", i)
		state := protocol.StateReplied
		if i%9 == 0 {
			state = protocol.StateFailed
		}
		rows = append(rows, conversationView(fmt.Sprintf("c_%02d", i), from, to, fromName, toName,
			i%5+1, line, state, t0.Add(-time.Duration(i)*7*time.Minute)))
	}
	src := &fakeSource{snap: snap}
	src.convFirst = protocol.ConversationPage{Conversations: rows, Total: 320, Next: "older"}
	src.convMsgs = map[string][]protocol.Message{
		"c_00": {
			{ID: "m_c_00_reply", ConversationID: "c_00", From: b, To: a, Type: protocol.TypeReply,
				Body:      "The full stored conversation is in the right pane.\nIt comes from the gateway, not the feed.",
				CreatedAt: t0.Add(-time.Minute)},
			{ID: "m_c_00", ConversationID: "c_00", From: a, To: b, Type: protocol.TypeRequest,
				Body: "Show the whole stored history of this conversation.", CreatedAt: t0.Add(-2 * time.Minute)},
		},
	}
	for _, size := range [][2]int{{80, 24}, {120, 34}} {
		m := New(src)
		m.loc, m.now = time.UTC, func() time.Time { return t0 }
		m.tab, m.focus = messagesTab, focusFeed
		m = send(m, tea.WindowSizeMsg{Width: size[0], Height: size[1]})
		m = refreshAndRun(t, m, src)
		golden(t, fmt.Sprintf("messages_full_%dx%d", size[0], size[1]), m.testView())
	}
}
