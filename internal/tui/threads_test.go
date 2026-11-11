package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/niparis/agent-net/internal/protocol"
)

// one message in a feed row, for the thread tests.
func threadFeed(m *Model, events ...protocol.FeedEvent) {
	m.feed = newFeed()
	m.feed.apply(events)
}

func TestThreadsGroupByPartyPair(t *testing.T) {
	const a, b, c, d = "s_a", "s_b", "s_c", "s_d"
	m := newModel(120, 34)
	m.tab, m.focus = messagesTab, focusFeed
	threadFeed(&m,
		ev(1, -3*time.Minute, "msg.stored", "one", a, b, "request", nil),
		ev(2, -2*time.Minute, "msg.stored", "two", b, a, "reply", nil),
		ev(3, -time.Minute, "msg.stored", "three", c, d, "notification", nil),
	)
	m.feed.byMsg["one"].Snippet = "first"
	m.feed.byMsg["two"].Snippet = "second"
	m.feed.byMsg["three"].Snippet = "third"
	rows := m.messageConversations()
	if len(rows) != 2 {
		t.Fatalf("want one row per party pair, got %+v", rows)
	}
	for _, row := range rows {
		switch row.id {
		case messagePairID(a, b):
			if len(row.items) != 2 || row.count != 2 {
				t.Fatalf("the a/b thread did not hold both messages: %+v", row)
			}
		case messagePairID(c, d):
			if len(row.items) != 1 {
				t.Fatalf("the c/d thread did not hold its message: %+v", row)
			}
		default:
			t.Fatalf("unexpected thread %q", row.id)
		}
	}
}

func TestMessageSubtabsSplitAgentWorkerSystem(t *testing.T) {
	const owner, peer, worker = "s_owner", "s_peer", "s_worker"
	m := newModel(120, 34)
	m.tab, m.focus = messagesTab, focusFeed
	m.snap.Agents = append(m.snap.Agents,
		agent(owner, "n", "Mac", "claude", "lead", nil),
		agent(peer, "n", "Mac", "pi", "peer", nil),
		agent(worker, "n", "Mac", "opencode", "shop worker 1", func(a *protocol.AgentView) {
			a.Worker = &protocol.WorkerInfo{ID: "w_1", Owner: owner, Task: "Fix the deploy"}
		}),
	)
	threadFeed(&m,
		ev(1, -3*time.Minute, "msg.stored", "agent", owner, peer, "request", nil),
		ev(2, -2*time.Minute, "msg.stored", "worker", owner, worker, "request", nil),
		ev(3, -time.Minute, "msg.stored", "system", "agent-net", owner, "notification", func(e *protocol.FeedEvent) {
		}),
	)
	m.feed.byMsg["system"].Body = idleCheckBodyForTest
	m.feed.byMsg["agent"].Snippet = "agent talk"
	m.feed.byMsg["worker"].Snippet = "worker talk"

	if rows := m.messageConversations(); len(rows) != 1 || rows[0].worker || rows[0].system {
		t.Fatalf("Agents: %+v", rows)
	}
	m.messageSubtab = messageWorkers
	if rows := m.messageConversations(); len(rows) != 1 || !rows[0].worker {
		t.Fatalf("Workers: %+v", rows)
	}
	m.messageSubtab = messageSystem
	if rows := m.messageConversations(); len(rows) != 1 || !rows[0].system || rows[0].systemKind != systemIdleCheck {
		t.Fatalf("System: %+v", rows)
	}
	// tab cycles the same three views.
	m = keys(m, "tab")
	if m.messageSubtab != messageAgents {
		t.Fatalf("tab did not return to Agents: %v", m.messageSubtab)
	}
}

const idleCheckBodyForTest = "[agent-net] check: is your task done? Answer with agent_announce: done, more (one line), or blocked on X."

func TestWorkerRunCollapsesToOneLineAndExpands(t *testing.T) {
	const owner, worker = "s_owner", "s_worker"
	m := newModel(120, 34)
	m.tab, m.focus = messagesTab, focusFeed
	m.snap.Agents = append(m.snap.Agents,
		agent(owner, "n", "Mac", "claude", "tenebrae-strats", nil),
		agent(worker, "n", "Mac", "codex", "shop worker 1", func(a *protocol.AgentView) {
			a.Worker = &protocol.WorkerInfo{ID: "w_1", Owner: owner, Task: "T8 registration", StartedAt: t0.Add(-8 * time.Minute).Unix()}
		}),
	)
	threadFeed(&m,
		ev(1, -9*time.Minute, "msg.stored", "task", owner, worker, "request", func(e *protocol.FeedEvent) { e.ConversationID = "c1" }),
		ev(2, -8*time.Minute, "msg.stored", "started", worker, owner, "notification", func(e *protocol.FeedEvent) { e.ConversationID = "c2" }),
		ev(3, -time.Minute, "msg.stored", "done", worker, owner, "notification", func(e *protocol.FeedEvent) { e.ConversationID = "c3" }),
	)
	m.feed.byMsg["task"].Body = "T8 registration\nShip PR #182."
	m.feed.byMsg["started"].Body = "[agent-net worker notice]\nstatus: started\n"
	m.feed.byMsg["done"].Body = "[agent-net worker notice]\nstatus: done\nrun time: 8m\nchanges: 2 files changed"
	m.messageSubtab = messageWorkers
	rows := m.messageConversations()
	if len(rows) != 1 {
		t.Fatalf("the run should collapse to one row: %+v", rows)
	}
	line := ansi.Strip(m.messageConversationLine(rows[0], 120, false))
	for _, want := range []string{"tenebrae-strats → worker 1", "T8 registration", "codex", "PR #182", "done"} {
		if !strings.Contains(line, want) {
			t.Errorf("worker run line is missing %q: %q", want, line)
		}
	}
	m.threadExpanded = map[string]bool{rows[0].id: true}
	lines, _ := m.threadListLines(rows, 100, 0, 10)
	if len(lines) < 2 {
		t.Fatalf("an expanded worker run should add its messages, got %v", lines)
	}
}

func TestSystemKindClassifier(t *testing.T) {
	cases := []struct {
		name string
		from string
		body string
		want systemKind
		ok   bool
	}{
		{"idle check", "agent-net", idleCheckBodyForTest, systemIdleCheck, true},
		{"autonomy continue", "agent-net", "[agent-net] continue: you said you have work left (more). Carry on.", systemAutonomyContinue, true},
		{"reply reminder", "agent-net", "[agent-net] reminder: tenebrae is waiting for your answer to m_abc (sent 2m ago).", systemReplyReminder, true},
		{"loop tick", "agent-net", "[agent-net] Loop: Since your last loop tick. Your purpose is x.", systemLoopTick, true},
		{"step task", "agent-net", "[agent-net step tasks]\nclaim with agent_step", systemStepTask, true},
		{"step reminder", "agent-net", "[agent-net step task reminder]\nchange: ch_12345678", systemStepReminder, true},
		{"stuck step", "agent-net", "Fix the deploy (ch_12345678): review has waited 12h for tenebrae; nobody claimed it.", systemStuckStep, true},
		{"chief brief wake", "agent-net", "Daily brief due for 2026-11-11. Write it and deliver it with agent_owner_brief.", systemChiefBriefWake, true},
		{"chief ask batch", "agent-net", "Chief of staff: 2 ask(s) to sort. Sort each one with agent_ask_sort.", systemChiefAskBatch, true},
		{"undelivered", "agent-net", "[agent-net] message to tenebrae was not delivered: expired after 24h.", systemUndelivered, true},
		{"waiting notice", "agent-net", "[agent-net] tenebrae is waiting for its user's answer (since 2026-11-11).", systemWaitingNotice, true},
		{"wake failed", "agent-net", "[agent-net] tenebrae is offline and could not be woken: no harness.", systemWakeFailed, true},
		{"worker not started", "agent-net", "[agent-net worker notice]\nstatus: not started\nnote: the machine said no.", systemWorkerNotStarted, true},
		{"step result", "agent-net", "[agent-net step result]\nchange: ch_12345678\nstep: test\nresult: pass", systemStepResult, true},
		{"step closed", "agent-net", "[agent-net step closed]\nchange: ch_12345678", systemStepClosed, true},
		{"member blocked", "agent-net", "[agent-net] team: tenebrae (s_x) is blocked or waiting, since 10:00.", systemMemberBlocked, true},
		{"native MCP removed", "owner", "agent-net: the MCP server \"foo\" moved to agent-net; restart this session to drop the old copy.", systemNativeMCPRemoved, true},
		{"other notice", "agent-net", "[agent-net] a notice the table does not know yet", systemOther, true},
		{"normal agent message", "s_agent", "blocked on the deploy", systemOther, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := classifySystemMessage(tc.from, tc.body)
			if ok != tc.ok || (ok && got != tc.want) {
				t.Fatalf("classify(%q) = %v, %v; want %v, %v", tc.body, got, ok, tc.want, tc.ok)
			}
		})
	}
}

func TestThreadStateLabels(t *testing.T) {
	const a, b = "s_a", "s_b"
	base := func() Model {
		m := newModel(120, 34)
		m.tab, m.focus = messagesTab, focusFeed
		m.snap.Agents = append(m.snap.Agents,
			agent(a, "n", "Mac", "claude", "lead", nil),
			agent(b, "n", "Mac", "pi", "peer", nil))
		return m
	}
	waiting := base()
	threadFeed(&waiting, ev(1, -time.Minute, "msg.stored", "w", a, b, "request", nil))
	waiting.feed.byMsg["w"].State = protocol.StateStored
	row := waiting.messageConversations()[0]
	if got := waiting.threadState(row); got != "waiting" {
		t.Fatalf("waiting state = %q", got)
	}
	if line := ansi.Strip(waiting.messageConversationLine(row, 120, false)); !strings.Contains(line, "waiting") {
		t.Fatalf("waiting row = %q", line)
	}

	answered := base()
	threadFeed(&answered, ev(1, -time.Minute, "msg.stored", "r", b, a, "reply", nil))
	answered.feed.byMsg["r"].State = protocol.StateReplied
	row = answered.messageConversations()[0]
	if got := answered.threadState(row); got != "answered" {
		t.Fatalf("answered state = %q", got)
	}

	held := base()
	for i := range held.snap.Agents {
		if held.snap.Agents[i].SessionID == b {
			held.snap.Agents[i].GatewayHeld = 3
		}
	}
	threadFeed(&held, ev(1, -time.Minute, "msg.stored", "h", a, b, "request", nil))
	held.feed.byMsg["h"].State = protocol.StateStored
	row = held.messageConversations()[0]
	if got := held.threadState(row); got != "gw 3" {
		t.Fatalf("held state = %q", got)
	}
	if line := ansi.Strip(held.messageConversationLine(row, 120, false)); !strings.Contains(line, "gw 3") {
		t.Fatalf("held row = %q", line)
	}
}

func TestThreadFlagsAndBurstRule(t *testing.T) {
	m := newModel(120, 34)
	m.tab, m.focus = messagesTab, focusFeed
	m.now = func() time.Time { return t0 }
	owner := messageConversation{
		id: "owner", from: "s_a", to: "s_b", fromName: "lead", toName: "peer",
		lastLine: "deploy the fix", at: t0.Add(-time.Minute), ownerInstruction: true,
	}
	if line := ansi.Strip(m.messageConversationLine(owner, 120, false)); !strings.Contains(line, "you→") {
		t.Fatalf("owner marker missing: %q", line)
	}
	app := messageConversation{
		id: "app", from: "s_app", to: "s_b", fromName: "CTO (Trade)", toName: "peer",
		fromHarness: "bot", lastLine: "approve the trade", at: t0.Add(-time.Minute),
	}
	if line := ansi.Strip(m.messageConversationLine(app, 120, false)); !strings.Contains(line, "(app)") {
		t.Fatalf("app sender type missing: %q", line)
	}
	blocked := messageConversation{
		id: "blocked", from: "s_a", to: "s_b", fromName: "lead", toName: "peer",
		lastLine: "blocked on the deploy", at: t0.Add(-time.Minute), blocked: true,
	}
	if line := ansi.Strip(m.messageConversationLine(blocked, 120, false)); !strings.Contains(line, "blocked") {
		t.Fatalf("blocked marker missing: %q", line)
	}
	burst := messageConversation{
		id: "burst", from: "s_a", to: "s_b", fromName: "tenebrae-strats", toName: "tenebrae 3",
		lastLine: "many", at: t0.Add(-time.Minute), peakHour: 30, medianHour: 2,
	}
	if line := ansi.Strip(m.messageConversationLine(burst, 120, false)); !strings.Contains(line, "burst 30/h") {
		t.Fatalf("burst marker missing: %q", line)
	}
	for _, tc := range []struct {
		peak, median int
		want         bool
	}{
		{20, 0, true}, {19, 0, false}, {20, 7, false}, {30, 9, true}, {30, 10, false},
	} {
		if got := burstRule(tc.peak, tc.median); got != tc.want {
			t.Errorf("burstRule(%d, %d) = %v, want %v", tc.peak, tc.median, got, tc.want)
		}
	}
}

func TestStoredPairRowShowsTheBurstFlag(t *testing.T) {
	m := newModel(120, 34)
	m.tab, m.focus = messagesTab, focusFeed
	row := messageConversation{
		id: "pair:s_a\x00s_b", from: "s_a", to: "s_b", fromName: "tenebrae-strats", toName: "tenebrae 3",
		lastLine: "busy", count: 36, peakHour: 22, medianHour: 1, at: t0.Add(-time.Minute),
	}
	line := ansi.Strip(m.messageConversationLine(row, 120, false))
	if !strings.Contains(line, "burst 22/h") {
		t.Fatalf("stored pair row does not show its burst: %q", line)
	}
	row.peakHour, row.medianHour = 19, 1
	line = ansi.Strip(m.messageConversationLine(row, 120, false))
	if strings.Contains(line, "burst") {
		t.Fatalf("19 messages in an hour is not a burst: %q", line)
	}
}

func TestThreadProjectTeamFilterAndSearch(t *testing.T) {
	const a, b, c, d = "s_a", "s_b", "s_c", "s_d"
	m := newModel(120, 34)
	m.tab, m.focus = messagesTab, focusFeed
	m.snap.Agents = append(m.snap.Agents,
		agent(a, "n", "Mac", "claude", "lead", func(a *protocol.AgentView) {
			a.AgentID = "agent_a"
			a.Repo.Project = "agent-net"
		}),
		agent(b, "n", "Mac", "pi", "peer", func(a *protocol.AgentView) { a.Repo.Project = "agent-net" }),
		agent(c, "n", "Mac", "claude", "shop-lead", func(a *protocol.AgentView) {
			a.AgentID = "agent_c"
			a.Repo.Project = "shop"
		}),
		agent(d, "n", "Mac", "pi", "shop-peer", func(a *protocol.AgentView) { a.Repo.Project = "shop" }),
	)
	m.snap.Edges = []protocol.Edge{
		{From: "lead", To: "tenebrae", Kind: "member_of", FromRef: &protocol.GraphRef{Kind: "agent", ID: "agent_a"}, ToRef: &protocol.GraphRef{Kind: "team", ID: "tenebrae"}},
		{From: "shop-lead", To: "shop-team", Kind: "member_of", FromRef: &protocol.GraphRef{Kind: "agent", ID: "agent_c"}, ToRef: &protocol.GraphRef{Kind: "team", ID: "shop-team"}},
	}
	threadFeed(&m,
		ev(1, -3*time.Minute, "msg.stored", "one", a, b, "request", func(e *protocol.FeedEvent) { e.ConversationID = "one" }),
		ev(2, -2*time.Minute, "msg.stored", "two", c, d, "request", func(e *protocol.FeedEvent) { e.ConversationID = "two" }),
	)
	m.feed.byMsg["one"].Snippet = "deploy the agent-net fix"
	m.feed.byMsg["two"].Snippet = "restock the shop"

	m.threadProject = "agent-net"
	rows := m.messageConversations()
	if len(rows) != 1 || rows[0].from != a || rows[0].to != b {
		t.Fatalf("project filter: %+v", rows)
	}
	m.threadProject = ""
	m.threadTeam = "tenebrae"
	rows = m.messageConversations()
	if len(rows) != 1 || rows[0].from != a || rows[0].to != b {
		t.Fatalf("team filter: %+v", rows)
	}
	m.threadTeam = ""
	m.filter = "restock"
	rows = m.messageConversations()
	if len(rows) != 1 || rows[0].from != c || rows[0].to != d {
		t.Fatalf("text search: %+v", rows)
	}
	m.filter = ""
	m = keys(m, "p")
	if m.threadProject == "" {
		t.Fatal("p did not set a project filter")
	}
	m = keys(m, "esc")
	if m.threadProject != "" {
		t.Fatalf("esc did not clear the project filter: %q", m.threadProject)
	}
}

func TestConversationWrapsWithoutEllipsis(t *testing.T) {
	m := newModel(120, 34)
	m.tab, m.focus = messagesTab, focusFeed
	threadFeed(&m, ev(1, -time.Minute, "msg.stored", "long", "s_a", "s_b", "request", nil))
	body := "alpha beta gamma delta epsilon zeta eta theta iota kappa lambda mu"
	m.feed.byMsg["long"].Body = body
	rows := m.messageConversations()
	chat := m.conversationChat(rows[0], 40)
	plain := ansi.Strip(strings.Join(chat, "\n"))
	if strings.Contains(plain, "…") {
		t.Fatalf("the conversation cuts a line with an ellipsis:\n%s", plain)
	}
	for _, line := range strings.Split(plain, "\n") {
		if ansi.StringWidth(line) > 40 {
			t.Fatalf("wrapped line is %d wide: %q", ansi.StringWidth(line), line)
		}
	}
	if !strings.Contains(strings.Join(strings.Fields(plain), " "), body) {
		t.Fatalf("the wrapped conversation lost body text:\n%s", plain)
	}
}

func TestThreadLinksFindPRsChangesAndAsks(t *testing.T) {
	m := newModel(120, 34)
	m.tab, m.focus = messagesTab, focusFeed
	threadFeed(&m, ev(1, -time.Minute, "msg.stored", "links", "s_a", "s_b", "request", nil))
	m.feed.byMsg["links"].Body = "See PR #182, ch_8c3bc9d80f8dd4aa and ask i_4d2a91c0d4e5. Also https://github.com/niparis/agent-net/pull/190."
	row := m.messageConversations()[0]
	links := m.threadLinks(row)
	joined := strings.Join(links, " ")
	for _, want := range []string{"PR #182", "ch_8c3bc9d80f8dd4aa", "ask i_4d2a91c0d4e5", "pull/190"} {
		if !strings.Contains(joined, want) {
			t.Errorf("links are missing %q: %v", want, links)
		}
	}
}

func TestMessageSubtabsKeyLineStillKeepsTheOldKeys(t *testing.T) {
	m := keys(newModel(120, 34), "4")
	if got := ansi.Strip(m.helpLine(120)); !strings.Contains(got, "? help") || !strings.Contains(got, "q quit") {
		t.Fatalf("the Messages key line lost its help or quit key: %q", got)
	}
	if keyAction(contextMessages, "tab") != "next message subtab" {
		t.Fatal("tab is not bound to the Messages sub-tabs")
	}
}
