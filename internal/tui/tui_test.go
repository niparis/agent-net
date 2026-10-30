package tui

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"

	"github.com/niparis/agent-net/internal/client"
	"github.com/niparis/agent-net/internal/protocol"
)

var update = flag.Bool("update", false, "rewrite the golden files")

func TestMain(m *testing.M) {
	// Plain text in tests: the golden files hold no colour codes.
	lipgloss.SetColorProfile(termenv.Ascii)
	initScreenColors()
	os.Exit(m.Run())
}

// ---- fake data ----

var t0 = time.Date(2026, 10, 29, 14, 5, 0, 0, time.UTC)

func fakeNodes() []protocol.NodeView {
	return []protocol.NodeView{
		{NodeID: "n_mac", Hostname: "Mac", Version: "v0.4.0", Online: true, Harnesses: []protocol.HarnessInfo{
			{Name: "claude", Version: "2.1.312 (Claude Code)"}, {Name: "opencode", Version: "opencode v2.2.0"}, {Name: "pi", Version: "1.4.0"}}},
		{NodeID: "n_vm", Hostname: "build-vm", Version: "v0.4.0", Online: true, Harnesses: []protocol.HarnessInfo{{Name: "pi", Version: "1.4.0"}}},
		{NodeID: "n_old", Hostname: "old-box", Version: "v0.3.2", Online: false, Harnesses: nil},
	}
}

func agent(id, node, host, harness, name string, f func(*protocol.AgentView)) protocol.AgentView {
	a := protocol.AgentView{SessionID: id, NodeID: node, Hostname: host, Harness: harness, Status: "idle", Online: true,
		LastSeen: t0.Unix(), Profile: protocol.Profile{Name: name}}
	if f != nil {
		f(&a)
	}
	return a
}

func fakeAgents() []protocol.AgentView {
	return []protocol.AgentView{
		agent("s_0000000000000001", "n_mac", "Mac", "claude", "tenebrae-53", func(a *protocol.AgentView) {
			a.Status, a.Mode, a.Model, a.PID = "busy", protocol.ModeForeground, "claude-opus-5-5", 4242
			a.FirstSeen, a.LastSeen = t0.Add(-50*time.Minute).Unix(), t0.Add(-2*time.Minute).Unix()
			a.Cwd = "/Users/n/code/tenebrae"
			a.Repo = protocol.RepoInfo{Root: "/Users/n/code/tenebrae", Worktree: "/Users/n/code/tenebrae", Project: "tenebrae",
				Remote: "https://git.example.com/team/tenebrae.git", Branch: "main"}
			a.Profile.Function = "research"
		}),
		agent("s_0000000000000002", "n_mac", "Mac", "claude", "strategies", func(a *protocol.AgentView) {
			a.Mode, a.Model, a.PID = protocol.ModeBackground, "claude-sonnet-5-5", 4300
			a.FirstSeen, a.LastSeen = t0.Add(-60*time.Minute).Unix(), t0.Add(-1*time.Minute).Unix()
			a.Cwd = "/Users/n/code/strategies/.claude/worktrees/agent-a7"
			a.Repo = protocol.RepoInfo{Root: "/Users/n/code/strategies", Worktree: a.Cwd, Project: "strategies",
				Remote: "git@git.example.com:team/strategies.git", Branch: "feature/risk-model"}
			a.Profile.Function = "trading strategies"
			a.Profile.Task = "backtest v2"
		}),
		agent("s_0000000000000003", "n_mac", "Mac", "opencode", "web", func(a *protocol.AgentView) {
			a.Mode, a.Model = protocol.ModeUnknown, "anthropic/claude-sonnet-5"
			a.FirstSeen, a.LastSeen = t0.Add(-40*time.Minute).Unix(), t0.Add(-3*time.Minute).Unix()
			a.Cwd = "/Users/n/code/shop/web"
			a.Repo = protocol.RepoInfo{Root: "/Users/n/code/shop", Worktree: "/Users/n/code/shop", Project: "shop", Branch: "main"}
		}),
		agent("s_0000000000000004", "n_vm", "build-vm", "pi", "ci-runner", func(a *protocol.AgentView) {
			a.Mode, a.Model, a.PID = protocol.ModeBackground, "openai/gpt-6", 981
			a.FirstSeen, a.LastSeen = t0.Add(-30*time.Minute).Unix(), t0.Add(-4*time.Minute).Unix()
			a.Cwd = "/srv/agent-net"
			a.Repo = protocol.RepoInfo{Root: "/srv/agent-net", Worktree: "/srv/agent-net", Project: "agent-net", Branch: "main"}
			a.Profile.Function = "runs the test suite"
		}),
		agent("s_0000000000000005", "n_vm", "build-vm", "pi", "scratch", func(a *protocol.AgentView) {
			a.Status, a.Mode = "shell", protocol.ModeForeground
			a.FirstSeen, a.LastSeen = t0.Add(-20*time.Minute).Unix(), t0.Add(-5*time.Minute).Unix()
			a.Cwd = "/home/ci"
		}),
		agent("s_0000000000000006", "n_old", "old-box", "claude", "old-session", func(a *protocol.AgentView) {
			a.Online, a.Status = false, "unknown"
			a.FirstSeen, a.LastSeen = t0.Add(-26*time.Hour).Unix(), t0.Add(-26*time.Hour).Unix()
			a.Cwd = "/home/n/old"
			a.Repo = protocol.RepoInfo{Project: "old", Branch: "main"}
		}),
	}
}

func fakeSnapshot() Snapshot {
	return Snapshot{Connected: true, NodeID: "n_mac", Version: "v0.4.0", Nodes: fakeNodes(), Agents: fakeAgents(),
		Projects: []protocol.ProjectView{
			{Name: "tenebrae", NodeID: "n_mac", Path: "/Users/n/code/tenebrae", Source: protocol.SourceBoth},
			{Name: "strategies", NodeID: "n_mac", Path: "/Users/n/code/strategies", Source: protocol.SourceRegistered},
			{Name: "shop", NodeID: "n_mac", Path: "/Users/n/code/shop"}, // an older gateway sends no source
		}}
}

func ev(id int64, at time.Duration, kind, msg, from, to, typ string, f func(*protocol.FeedEvent)) protocol.FeedEvent {
	e := protocol.FeedEvent{Event: protocol.Event{ID: id, Time: t0.Add(at), Kind: kind, MessageID: msg}, From: from, To: to, Type: typ}
	if f != nil {
		f(&e)
	}
	return e
}

func fakeEvents() []protocol.FeedEvent {
	const a, b, c = "s_0000000000000001", "s_0000000000000002", "s_0000000000000004"
	return []protocol.FeedEvent{
		ev(1, -10*time.Minute, "node.connected", "", "", "", "", func(e *protocol.FeedEvent) { e.Hostname = "build-vm" }),
		ev(2, -9*time.Minute, "msg.stored", "m_1", a, b, "request", nil),
		ev(3, -9*time.Minute, "msg.dispatched", "m_1", a, b, "request", nil),
		ev(4, -9*time.Minute, "msg.delivered", "m_1", a, b, "request", nil),
		ev(5, -8*time.Minute, "msg.stored", "m_2", b, a, "reply", func(e *protocol.FeedEvent) { e.Parent = "m_1" }),
		ev(6, -8*time.Minute, "msg.delivered", "m_2", b, a, "reply", nil),
		ev(7, -4*time.Minute, "msg.stored", "m_3", c, a, "request", nil),
		ev(8, -3*time.Minute, "msg.delivered", "m_3", c, a, "request", nil),
		ev(9, -3*time.Minute, "msg.stored", "m_4", a, c, "notification", nil),
		ev(10, -2*time.Minute, "msg.failed", "m_4", a, c, "notification", func(e *protocol.FeedEvent) { e.Detail = "plugin refused" }),
		ev(11, -1*time.Minute, "msg.stored", "m_5", a, "s_0000000000000099", "request", nil),
		ev(12, -1*time.Minute, "msg.dispatched", "m_5", a, "s_0000000000000099", "request", nil),
	}
}

// fakeSource answers from memory.
type trustCall struct {
	id string
	on bool
}

type renameCall struct{ id, name string }

type fakeSource struct {
	snap     Snapshot
	snapErr  error
	events   []protocol.FeedEvent
	evErr    error
	messages map[string]protocol.Message
	msgErr   error
	msgReqs  []string // message IDs whose body was asked for

	// What the actions were asked to do, and the error they answer with.
	sentTo, sentBody string
	started          *protocol.WorkerStartRequest
	stopped          string
	actErr           error
	trusts           []trustCall
	trustErr         error
	renames          []renameCall
	renameErr        error
	policyFn         func(protocol.SessionPolicyRequest) (protocol.SessionPolicyResult, error)
	policyErr        error
	intg             protocol.Integrations
	intgSet          bool
	intgGetErr       error
	intgSetErr       error
	intgSaved        []protocol.Integrations

	// CPU, memory, counts, top lists and conversations. Nil means the daemon
	// is older than the feature.
	usage   *Usage
	counts  *protocol.SessionCounts
	tops    map[string]protocol.UsageTop
	textFn  func(q protocol.TextQuery) (protocol.TextPage, error)
	queries []protocol.TextQuery
	topReqs []string

	// What session_enter was asked, and the plan or error it answers with.
	enters    []protocol.EnterRequest
	enterPlan protocol.EnterPlan
	enterErr  error

	// The graph the map reads, and the error it answers with.
	graph    protocol.GraphView
	graphErr error
}

func (f *fakeSource) GraphView(_ context.Context, _ protocol.QueryGraph) (protocol.GraphView, error) {
	return f.graph, f.graphErr
}

func (f *fakeSource) Usage(context.Context) (Usage, error) {
	if f.usage == nil {
		return Usage{}, ErrUsageUnavailable
	}
	return *f.usage, nil
}
func (f *fakeSource) Counts(context.Context) (protocol.SessionCounts, error) {
	if f.counts == nil {
		return protocol.SessionCounts{}, ErrUsageUnavailable
	}
	return *f.counts, nil
}
func (f *fakeSource) UsageTop(_ context.Context, id string) (protocol.UsageTop, error) {
	f.topReqs = append(f.topReqs, id)
	if t, ok := f.tops[id]; ok {
		return t, nil
	}
	return protocol.UsageTop{}, ErrUsageUnavailable
}
func (f *fakeSource) SessionText(_ context.Context, q protocol.TextQuery) (protocol.TextPage, error) {
	f.queries = append(f.queries, q)
	if f.textFn == nil {
		return protocol.TextPage{}, errors.New("session_text is not available")
	}
	return f.textFn(q)
}

func (f *fakeSource) SetSessionPolicy(_ context.Context, q protocol.SessionPolicyRequest) (protocol.SessionPolicyResult, error) {
	if f.policyFn != nil {
		return f.policyFn(q)
	}
	return protocol.SessionPolicyResult{SessionID: q.SessionID}, f.policyErr
}

func (f *fakeSource) Integrations(context.Context) (protocol.IntegrationsResult, error) {
	return protocol.IntegrationsResult{Set: f.intgSet, Integrations: f.intg}, f.intgGetErr
}

func (f *fakeSource) SetIntegrations(_ context.Context, in protocol.Integrations) (protocol.IntegrationsResult, error) {
	f.intgSaved = append(f.intgSaved, in)
	if f.intgSetErr != nil {
		return protocol.IntegrationsResult{}, f.intgSetErr
	}
	f.intg, f.intgSet = in, true
	return protocol.IntegrationsResult{Set: true, Integrations: in}, nil
}

func (f *fakeSource) SetPeerTrust(_ context.Context, id string, on bool) error {
	f.trusts = append(f.trusts, trustCall{id, on})
	return f.trustErr
}

func (f *fakeSource) RenameNode(_ context.Context, id, name string) (protocol.NodeRenameResult, error) {
	f.renames = append(f.renames, renameCall{id, name})
	return protocol.NodeRenameResult{NodeID: id, Hostname: name, DisplayName: name}, f.renameErr
}

func (f *fakeSource) SessionEnter(_ context.Context, q protocol.EnterRequest) (protocol.EnterPlan, error) {
	f.enters = append(f.enters, q)
	return f.enterPlan, f.enterErr
}

func (f *fakeSource) Snapshot(context.Context) (Snapshot, error) { return f.snap, f.snapErr }
func (f *fakeSource) Events(_ context.Context, after int64, _ int) ([]protocol.FeedEvent, error) {
	var out []protocol.FeedEvent
	for _, e := range f.events {
		if e.ID > after {
			out = append(out, e)
		}
	}
	return out, f.evErr
}
func (f *fakeSource) Message(_ context.Context, id string) (protocol.Message, error) {
	f.msgReqs = append(f.msgReqs, id)
	if f.msgErr != nil {
		return protocol.Message{}, f.msgErr
	}
	m, ok := f.messages[id]
	if !ok {
		return m, client.RemoteError(fmt.Sprintf("unknown message %q", id))
	}
	return m, nil
}
func (f *fakeSource) SendAsOwner(_ context.Context, to, body string) (protocol.MessageSent, error) {
	f.sentTo, f.sentBody = to, body
	if f.actErr != nil {
		return protocol.MessageSent{}, f.actErr
	}
	return protocol.MessageSent{Message: &protocol.Message{ID: "m_sent", To: to}}, nil
}
func (f *fakeSource) StartWorker(_ context.Context, r protocol.WorkerStartRequest) (protocol.WorkerStartResult, error) {
	f.started = &r
	if f.actErr != nil {
		return protocol.WorkerStartResult{}, f.actErr
	}
	return protocol.WorkerStartResult{SessionID: "s_new", Harness: r.Harness, Project: r.Project, Hostname: "Mac"}, nil
}
func (f *fakeSource) StopWorker(_ context.Context, id string) error {
	f.stopped = id
	return f.actErr
}
func (f *fakeSource) Workers(context.Context, protocol.WorkersQuery) ([]protocol.WorkerView, error) {
	return nil, nil
}
func (f *fakeSource) Close() {}

// newModel returns a model of the given size that has already shown the fake data.
func newModel(w, h int) Model {
	src := &fakeSource{snap: fakeSnapshot(), events: fakeEvents()}
	src.usage, src.counts, src.tops = fakeUsage(), fakeCounts(), fakeTops()
	m := New(src)
	m.loc = time.UTC
	m.now = func() time.Time { return t0 }
	m = send(m, tea.WindowSizeMsg{Width: w, Height: h})
	return settle(refresh(m, src))
}

// settle fetches the top processes the detail pane wants, the way the
// program does after a key or a refresh.
func settle(m Model) Model {
	// The tests drop the commands that Update returns, so a fetch that was
	// started is not running: forget it.
	for id, e := range m.top {
		if e.loading && !e.have {
			delete(m.top, id)
		}
	}
	if cmd := m.wantTop(); cmd != nil {
		m = send(m, cmd())
	}
	return m
}

// refresh runs one refresh, the way the tick does, and applies the result.
func refresh(m Model, src Source) Model {
	m.src = src
	res := m.fetchCmd()().(fetchedMsg)
	return send(m, res)
}

func send(m Model, msg tea.Msg) Model {
	next, _ := m.Update(msg)
	return next.(Model)
}

func keys(m Model, ks ...string) Model {
	for _, k := range ks {
		var msg tea.KeyMsg
		switch k {
		case "up":
			msg = tea.KeyMsg{Type: tea.KeyUp}
		case "down":
			msg = tea.KeyMsg{Type: tea.KeyDown}
		case "enter":
			msg = tea.KeyMsg{Type: tea.KeyEnter}
		case "esc":
			msg = tea.KeyMsg{Type: tea.KeyEsc}
		case "tab":
			msg = tea.KeyMsg{Type: tea.KeyTab}
		default:
			msg = tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(k)}
		}
		m = send(m, msg)
	}
	return settle(m)
}

func lines(s string) []string { return strings.Split(s, "\n") }

// ---- golden files ----

func golden(t *testing.T, name, got string) {
	t.Helper()
	writeScreen(t, name, got)
	if os.Getenv("AGENT_NET_SCREEN_CHILD") != "" {
		return
	}
	got = ansi.Strip(got)
	path := filepath.Join("testdata", name+".golden")
	// Trailing spaces are not part of the picture.
	var ls []string
	for _, l := range lines(got) {
		ls = append(ls, strings.TrimRight(l, " "))
	}
	got = strings.Join(ls, "\n") + "\n"
	if *update {
		os.MkdirAll("testdata", 0o755)
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%v (run go test ./internal/tui -update)", err)
	}
	if string(want) != got {
		t.Fatalf("%s differs.\n--- got ---\n%s\n--- want ---\n%s", name, got, want)
	}
}

func TestViewGolden80(t *testing.T)  { golden(t, "view_80x24", newModel(80, 24).View()) }
func TestViewGolden120(t *testing.T) { golden(t, "view_120x34", newModel(120, 34).View()) }

func TestViewGoldenOfflineAndFilter(t *testing.T) {
	m := keys(newModel(100, 30), "o", "/", "p", "i", "enter", "down")
	golden(t, "view_100x30_offline_filter", m.View())
}

func TestViewGoldenFeedFocusAndBody(t *testing.T) {
	src := &fakeSource{snap: fakeSnapshot(), events: fakeEvents(), messages: map[string]protocol.Message{
		"m_1": {ID: "m_1", ConversationID: "c_1", Body: "Which model do you run?\nPlease answer in one line.", CreatedAt: t0.Add(-9 * time.Minute)},
	}}
	m := New(src)
	m.loc, m.now = time.UTC, func() time.Time { return t0 }
	m = send(m, tea.WindowSizeMsg{Width: 80, Height: 24})
	m = refresh(m, src)
	m = drainSnippets(t, m)
	m = keys(m, "2")
	// Move up to the first message (the last items are newer).
	for m.feed.items[m.feedCur].MsgID != "m_1" {
		m = keys(m, "up")
	}
	golden(t, "view_80x24_feed_focus", m.View())
	m2, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("v")})
	m = m2.(Model)
	if cmd == nil || m.open == nil || !m.open.loading {
		t.Fatal("v did not start fetching the body")
	}
	m = send(m, cmd())
	golden(t, "view_80x24_body", m.View())
}

// ---- layout ----

func TestViewFitsEverySize(t *testing.T) {
	m := newModel(80, 24)
	for _, filter := range []string{"", "tenebrae"} {
		for w := 30; w <= 200; w += 7 {
			for h := 8; h <= 60; h += 5 {
				v := keys(send(m, tea.WindowSizeMsg{Width: w, Height: h}), "o")
				v.filter = filter
				v.ensureSelection()
				out := v.View()
				ls := lines(out)
				if len(ls) > max(h, 10) {
					t.Fatalf("%dx%d: %d lines", w, h, len(ls))
				}
				for i, l := range ls {
					if ansi.StringWidth(l) > max(w, 40) {
						t.Fatalf("%dx%d line %d is %d columns wide: %q", w, h, i, ansi.StringWidth(l), l)
					}
				}
			}
		}
	}
}

func TestViewHasEverythingAt80Columns(t *testing.T) {
	out := keys(newModel(80, 24), "down").View() // tenebrae-53
	for _, want := range []string{
		"Mac (here)", "v0.4.0", "online", "old-box", "offline", "12 online",
		"AGENT", "NAME", "PROJECT", "MODEL", "STATUS", "TRUST", "CPU", "RAM", "TASK",
		"tenebrae-53", "opus-5-5", "busy", "shell", "CTO",
		"up/down select", "q quit",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	if strings.Contains(out, "old-session") {
		t.Error("offline sessions must be hidden at first")
	}
}

// ---- filter, offline, selection ----

func names(m Model) []string {
	var out []string
	for _, a := range m.visible() {
		out = append(out, a.Profile.Name)
	}
	return out
}

func TestOfflineToggle(t *testing.T) {
	m := newModel(100, 30)
	if got := strings.Join(names(m), ","); got != "strategies,tenebrae-53,web,ci-runner,scratch" {
		t.Fatalf("online sessions: %s", got)
	}
	m = keys(m, "o")
	if got := strings.Join(names(m), ","); got != "strategies,tenebrae-53,web,ci-runner,scratch,old-session" {
		t.Fatalf("with offline: %s", got)
	}
	m = keys(m, "G") // the offline session is last
	if !strings.Contains(m.View(), "old-session") || !strings.Contains(m.View(), "off") || !strings.Contains(m.View(), "filter: offline") {
		t.Fatalf("the offline session is not on screen:\n%s", m.View())
	}
	if !strings.Contains(detailText(m), "offline, last seen 13 Oct") && !strings.Contains(detailText(m), "offline, last seen 28 Oct") {
		t.Fatalf("the detail of an offline session lacks its last seen time:\n%s", m.View())
	}
	m = keys(m, "o")
	if strings.Contains(strings.Join(names(m), ","), "old-session") {
		t.Fatal("o again did not hide it")
	}
}

func TestFilter(t *testing.T) {
	m := newModel(100, 30)
	m = keys(m, "/")
	if !m.filtering {
		t.Fatal("/ did not start the filter")
	}
	// While typing, keys like q and o are text, not commands.
	m = keys(m, "q", "o")
	if m.quitting || m.showOffline || m.filter != "qo" {
		t.Fatalf("typed keys acted as commands: filter %q quit %v offline %v", m.filter, m.quitting, m.showOffline)
	}
	m = keys(m, "esc")
	if m.filtering || m.filter != "" || len(names(m)) != 5 {
		t.Fatalf("esc must clear the filter: %q %v", m.filter, names(m))
	}

	m = keys(m, "/", "g", "p", "t")
	if got := strings.Join(names(m), ","); got != "ci-runner" {
		t.Fatalf("filter gpt: %s", got) // matches the model openai/gpt-6
	}
	if v := m.View(); !strings.Contains(v, "/gpt") || !strings.Contains(v, "enter keeps the filter") {
		t.Fatalf("the filter prompt is not shown:\n%s", v)
	}
	m = keys(m, "enter")
	if m.filtering || m.filter != "gpt" {
		t.Fatalf("enter must keep the filter: %v %q", m.filtering, m.filter)
	}
	if v := m.View(); !strings.Contains(v, "filter: /gpt") || strings.Contains(v, "tenebrae-53 ") && strings.Contains(v, "claude-opus-5-5") {
		t.Fatalf("the active filter is not shown or not applied:\n%s", v)
	}
	if m.selected != "s_0000000000000004" {
		t.Fatalf("the selection must follow the filter: %s", m.selected)
	}
	// Esc outside the input clears it too.
	m = keys(m, "esc")
	if m.filter != "" || len(names(m)) != 5 {
		t.Fatalf("esc did not clear the filter: %q", m.filter)
	}

	// Several words must all match, in any field, ignoring case.
	m = keys(m, "/", "M", "A", "C", " ", "b", "g")
	if got := strings.Join(names(m), ","); got != "strategies" {
		t.Fatalf("filter 'MAC bg': %s", got)
	}
	m = keys(m, "enter", "esc")
	m = keys(m, "/", "z", "z", "z", "enter")
	if got := m.View(); !strings.Contains(got, "no session matches the filter") {
		t.Fatalf("empty result not explained:\n%s", got)
	}
	if _, ok := m.selectedAgent(); ok {
		t.Fatal("a session is selected although none matches")
	}
}

func TestSelectionKeysAndStability(t *testing.T) {
	m := newModel(100, 30)
	// Sorted: Mac (strategies, tenebrae-53, web), build-vm... machines by name, case-insensitively.
	first := m.visible()[0]
	if m.selected != first.SessionID {
		t.Fatalf("the first session should start selected: %s", m.selected)
	}
	m = keys(m, "down", "down")
	third := m.selected
	if third == first.SessionID {
		t.Fatal("down did not move")
	}
	m = keys(m, "up")
	if m.selected == third {
		t.Fatal("up did not move")
	}
	m = keys(m, "G")
	if want := m.visible()[len(m.visible())-1].SessionID; m.selected != want {
		t.Fatalf("G: %s, want %s", m.selected, want)
	}
	m = keys(m, "down") // stays at the end
	if want := m.visible()[len(m.visible())-1].SessionID; m.selected != want {
		t.Fatal("selection ran past the end")
	}
	m = keys(m, "g", "up")
	if m.selected != first.SessionID {
		t.Fatal("selection ran past the start")
	}

	// A refresh that reorders and changes sessions keeps the selection on the same session.
	m = keys(m, "down", "down")
	want := m.selected
	src := &fakeSource{snap: fakeSnapshot(), events: fakeEvents()}
	src.snap.Agents = append([]protocol.AgentView{agent("s_00000000000000aa", "n_mac", "Mac", "pi", "aaa-first", nil)}, src.snap.Agents...)
	m = refresh(m, src)
	if m.selected != want {
		t.Fatalf("the selection jumped from %s to %s", want, m.selected)
	}

	// The selected session goes away: the one in its place is selected.
	var rest []protocol.AgentView
	for _, a := range src.snap.Agents {
		if a.SessionID != want {
			rest = append(rest, a)
		}
	}
	src.snap.Agents = rest
	m = refresh(m, src)
	if _, ok := m.selectedAgent(); !ok || m.selected == want {
		t.Fatalf("selection after the session left: %q", m.selected)
	}
}

func TestSelectionScrollsTheTable(t *testing.T) {
	src := &fakeSource{snap: fakeSnapshot(), events: fakeEvents()}
	for i := 0; i < 40; i++ {
		src.snap.Agents = append(src.snap.Agents, agent(fmt.Sprintf("s_%016d", 100+i), "n_vm", "build-vm", "pi", fmt.Sprintf("worker-%02d", i), nil))
	}
	m := New(src)
	m.loc, m.now = time.UTC, func() time.Time { return t0 }
	m = send(m, tea.WindowSizeMsg{Width: 100, Height: 24})
	m = refresh(m, src)
	m = keys(m, "G")
	v := m.View()
	last := m.visible()[len(m.visible())-1]
	if !strings.Contains(v, "> ") || !strings.Contains(v, last.Profile.Name) {
		t.Fatalf("the selected last session is not on screen:\n%s", v)
	}
	if len(lines(v)) > 24 {
		t.Fatalf("%d lines", len(lines(v)))
	}
	// Scrolled into the middle of a machine, its heading stays on top.
	m = keys(m, "g", "down", "down", "down", "down", "down", "down", "down", "down", "down", "down", "down", "down")
	if v := lines(m.View()); !strings.HasPrefix(strings.TrimSpace(v[7]), "build-vm") {
		t.Fatalf("no machine heading above the scrolled table:\n%s", strings.Join(v, "\n"))
	}
	if !strings.Contains(m.View(), "> ") {
		t.Fatal("the selection scrolled out of the table")
	}
	m = keys(m, "g")
	if !strings.Contains(m.View(), m.visible()[0].Profile.Name) {
		t.Fatal("going back to the top did not scroll back")
	}
}

// ---- problems ----

func TestDaemonDownBanner(t *testing.T) {
	m := newModel(80, 24)
	good := m.View()
	src := &fakeSource{snapErr: fmt.Errorf("%w (/tmp/x/daemon.sock)", ErrDaemonDown)}
	m = refresh(m, src)
	v := m.View()
	if !strings.Contains(v, "! Daemon not reachable (/tmp/x/daemon.sock). Showing last data. Retrying.") {
		t.Fatalf("no daemon banner:\n%s", v)
	}
	if m.problem != ProblemDaemon {
		t.Fatalf("problem %v", m.problem)
	}
	// The last data stays.
	for _, want := range []string{"tenebrae-53", "Mac (here)", "updated 14:05:00", "sonnet-5-5"} {
		if !strings.Contains(v, want) {
			t.Errorf("lost %q while the daemon is down", want)
		}
	}
	// And the screen keeps its size.
	if len(lines(v)) != len(lines(good)) {
		t.Errorf("height changed from %d to %d", len(lines(good)), len(lines(v)))
	}
	// The daemon comes back: the banner goes.
	m = refresh(m, &fakeSource{snap: fakeSnapshot(), events: fakeEvents()})
	if m.problem != ProblemNone || strings.Contains(m.View(), "!") && strings.Contains(m.View(), "Retrying") {
		t.Fatalf("the banner stayed:\n%s", m.View())
	}
	golden(t, "view_80x24_daemon_down", keysBanner(t, newModel(80, 24)).View())
}

func keysBanner(t *testing.T, m Model) Model {
	return refresh(m, &fakeSource{snapErr: fmt.Errorf("%w (/run/user/1000/agent-net/daemon.sock)", ErrDaemonDown)})
}

func TestGatewayOfflineBanner(t *testing.T) {
	m := newModel(80, 24)
	down := fakeSnapshot()
	down.Connected, down.Agents, down.Nodes = false, nil, nil // what the daemon says
	m = refresh(m, &fakeSource{snap: down})
	v := m.View()
	if !strings.Contains(v, "! Gateway offline: the daemon cannot reach it. Showing last data. Retrying.") {
		t.Fatalf("no gateway banner:\n%s", v)
	}
	if !strings.Contains(v, "tenebrae-53") || !strings.Contains(v, "build-vm") {
		t.Fatalf("the last data is gone:\n%s", v)
	}
	// The same through an error from a call (the link dropped between two calls).
	m = refresh(newModel(80, 24), &fakeSource{snapErr: fmt.Errorf("%w: not connected to the gateway", ErrGatewayDown)})
	if m.problem != ProblemGateway || !strings.Contains(m.View(), "Gateway offline") {
		t.Fatalf("problem %v", m.problem)
	}
	golden(t, "view_80x24_gateway_offline", refresh(newModel(80, 24), &fakeSource{snap: down}).View())
}

func TestNoDataYet(t *testing.T) {
	src := &fakeSource{snapErr: fmt.Errorf("%w (/tmp/x.sock)", ErrDaemonDown)}
	m := New(src)
	m.loc, m.now = time.UTC, func() time.Time { return t0 }
	v := send(m, tea.WindowSizeMsg{Width: 80, Height: 24}).View()
	if !strings.Contains(v, "connecting...") {
		t.Fatalf("first frame:\n%s", v)
	}
	m = refresh(m, src)
	v = m.View()
	for _, want := range []string{"! Daemon not reachable", "no data yet", "no machines yet"} {
		if !strings.Contains(v, want) {
			t.Errorf("missing %q:\n%s", want, v)
		}
	}
}

func TestOtherErrorIsShown(t *testing.T) {
	m := refresh(newModel(80, 24), &fakeSource{snapErr: errors.New("the first request must be hello")})
	if m.problem != ProblemOther || !strings.Contains(m.View(), "! the first request must be hello") {
		t.Fatalf("problem %v\n%s", m.problem, m.View())
	}
}

func TestClassifyError(t *testing.T) {
	cases := map[error]Problem{
		nil:                                 ProblemNone,
		ErrDaemonDown:                       ProblemDaemon,
		fmt.Errorf("x: %w", ErrGatewayDown): ProblemGateway,
		errors.New("other"):                 ProblemOther,
	}
	for err, want := range cases {
		if got := ClassifyError(err); got != want {
			t.Errorf("%v: %v, want %v", err, got, want)
		}
	}
}

// The screen keeps refreshing: Init starts a fetch and the clock, and every
// tick starts the next tick.
func TestInitStartsFetchAndClock(t *testing.T) {
	m := New(&fakeSource{snap: fakeSnapshot()})
	cmd := m.Init()
	batch, ok := cmd().(tea.BatchMsg)
	if !ok || len(batch) != 2 {
		t.Fatalf("Init must batch a fetch and a tick, got %T", cmd())
	}
	var sawFetch bool
	for _, c := range batch {
		if _, ok := c().(fetchedMsg); ok {
			sawFetch = true
		}
	}
	if !sawFetch {
		t.Fatal("Init does not fetch")
	}
	if !m.fetching {
		t.Fatal("the first fetch is not counted as running")
	}
}

// One refresh runs at a time, and the tick asks for the next one.
func TestTickStartsOneFetchAtATime(t *testing.T) {
	m := New(&fakeSource{snap: fakeSnapshot()})
	next, cmd := m.Update(tickMsg{})
	m = next.(Model)
	if !m.fetching || cmd == nil {
		t.Fatal("tick did not start a fetch")
	}
	_, cmd = m.Update(tickMsg{})
	if cmd == nil {
		t.Fatal("tick must schedule the next tick even while fetching")
	}
	if !m.fetching {
		t.Fatal("still fetching")
	}
	m = send(m, fetchedMsg{snap: fakeSnapshot(), at: t0})
	if m.fetching {
		t.Fatal("fetching stays true after the result")
	}
}

// ---- feed ----

func TestFeedTracksStateAndReplies(t *testing.T) {
	f := newFeed()
	f.apply(fakeEvents())
	get := func(id string) *feedItem { return f.byMsg[id] }
	if s := get("m_1").State; s != protocol.StateReplied {
		t.Errorf("m_1 got a reply: %s", s)
	}
	if s := get("m_2").State; s != protocol.StateDelivered {
		t.Errorf("m_2: %s", s)
	}
	if s := get("m_4").State; s != protocol.StateFailed || get("m_4").Detail != "plugin refused" {
		t.Errorf("m_4: %s %q", s, get("m_4").Detail)
	}
	if s := get("m_5").State; s != protocol.StateDispatched {
		t.Errorf("m_5: %s", s)
	}
	n := len(f.items)
	f.apply(fakeEvents()) // the same events again change nothing
	if len(f.items) != n || f.lastID != 12 {
		t.Fatalf("repeated events changed the feed: %d items, last %d", len(f.items), f.lastID)
	}
	// A late "delivered" does not move a replied message back.
	f.apply([]protocol.FeedEvent{ev(13, 0, "msg.delivered", "m_1", "", "", "", nil)})
	if s := get("m_1").State; s != protocol.StateReplied {
		t.Errorf("late delivered: %s", s)
	}
	// A message whose row is gone is read from the detail of its stored event.
	f.apply([]protocol.FeedEvent{ev(14, 0, "msg.stored", "m_old", "", "", "", func(e *protocol.FeedEvent) {
		e.SessionID, e.Detail = "s_a", "type=notification to=s_b bytes=12"
	})})
	if it := get("m_old"); it.From != "s_a" || it.To != "s_b" || it.Type != "notification" {
		t.Errorf("m_old: %+v", it)
	}
}

func TestFeedIsCapped(t *testing.T) {
	f := newFeed()
	var evs []protocol.FeedEvent
	for i := 1; i <= maxFeedItems+50; i++ {
		evs = append(evs, ev(int64(i), 0, "msg.stored", fmt.Sprintf("m_%d", i), "a", "b", "request", nil))
	}
	f.apply(evs)
	if len(f.items) != maxFeedItems || len(f.byMsg) != maxFeedItems {
		t.Fatalf("%d items, %d indexed", len(f.items), len(f.byMsg))
	}
	if f.items[0].MsgID != "m_51" {
		t.Fatalf("the oldest kept is %s", f.items[0].MsgID)
	}
}

func TestFeedLinesLiveUpdate(t *testing.T) {
	src := &fakeSource{snap: fakeSnapshot(), events: fakeEvents()}
	m := New(src)
	m.loc, m.now = time.UTC, func() time.Time { return t0 }
	m = send(m, tea.WindowSizeMsg{Width: 100, Height: 30})
	m = refresh(m, src)
	if !strings.Contains(m.View(), "tenebrae-53 (Mac) -> unknown") && !strings.Contains(m.View(), "s_00000000") {
		t.Logf("a message to a vanished session shows its ID:\n%s", m.View())
	}
	// New events arrive on the next refresh, after the cursor.
	src.events = append(src.events,
		ev(13, time.Minute, "msg.delivered", "m_5", "s_0000000000000001", "s_0000000000000099", "request", nil),
		ev(14, time.Minute, "msg.stored", "m_6", "s_0000000000000004", "s_0000000000000001", "request", nil),
		ev(15, time.Minute, "node.disconnected", "", "", "", "", func(e *protocol.FeedEvent) { e.Hostname = "build-vm" }),
	)
	m = refresh(m, src)
	v := keys(m, "2").View()
	if !strings.Contains(v, "ci-runner -> tenebrae-53") || !strings.Contains(v, "build-vm disconnected") {
		t.Fatalf("new events not shown:\n%s", v)
	}
	if m.feed.lastID != 15 {
		t.Fatalf("cursor %d", m.feed.lastID)
	}
}

func TestSessionDetailShowsItsMessages(t *testing.T) {
	m := newModel(120, 34)
	// tenebrae-53 is selected after strategies; select it explicitly.
	for m.selected != "s_0000000000000001" {
		m = keys(m, "down")
	}
	v := detailText(m)
	for _, want := range []string{"-> strategies (Mac)", "<- strategies (Mac)", "<- ci-runner (build-vm)", "notification  failed", "request  replied"} {
		if !strings.Contains(v, want) {
			t.Errorf("detail lacks %q:\n%s", want, v)
		}
	}
	// A session with no messages says so.
	for m.selected != "s_0000000000000003" {
		m = keys(m, "down")
	}
	if !strings.Contains(detailText(m), "none seen yet") {
		t.Fatalf("no 'none seen yet':\n%s", m.View())
	}
}

func TestFeedUnavailableOnOlderPeers(t *testing.T) {
	src := &fakeSource{snap: fakeSnapshot(), evErr: fmt.Errorf("%w: unknown frame type", ErrFeedUnavailable)}
	m := New(src)
	m.loc, m.now = time.UTC, func() time.Time { return t0 }
	m = send(m, tea.WindowSizeMsg{Width: 100, Height: 30})
	m = refresh(m, src)
	v := keys(m, "2").View()
	if m.problem != ProblemNone || !strings.Contains(v, "no message feed: the daemon or the gateway is older than the event feed") {
		t.Fatalf("not explained:\n%s", v)
	}
	if !strings.Contains(m.View(), "tenebrae-53") {
		t.Fatal("the sessions must still show")
	}
	// It does not ask again.
	src.evErr = errors.New("would be an error")
	m = refresh(m, src)
	if m.problem != ProblemNone {
		t.Fatal("asked for events again")
	}
}

// ---- message body ----

func TestOpenMessageBody(t *testing.T) {
	src := &fakeSource{snap: fakeSnapshot(), events: fakeEvents(), messages: map[string]protocol.Message{
		"m_3": {ID: "m_3", ConversationID: "c_9", Hop: 1, CreatedAt: t0.Add(-4 * time.Minute),
			Body: "line one\x1b[31m red?\nline \x07two\u202e reversed\n" + strings.Repeat("word ", 40)},
	}}
	m := New(src)
	m.loc, m.now = time.UTC, func() time.Time { return t0 }
	m = send(m, tea.WindowSizeMsg{Width: 80, Height: 30})
	m = refresh(m, src)

	// Enter on a session opens its conversation, not a message body.
	if next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter}); next.(Model).open != nil || next.(Model).conv == nil {
		t.Fatal("enter on the sessions table opened the wrong thing")
	}
	m = keys(m, "esc") // back to the lists
	m = keys(m, "2")
	for m.feed.items[m.feedCur].MsgID != "m_3" {
		m = keys(m, "up")
	}
	m2, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("v")})
	m = m2.(Model)
	if !strings.Contains(m.View(), "fetching...") {
		t.Fatalf("no loading state:\n%s", m.View())
	}
	m = send(m, cmd())
	v := m.View()
	if !strings.Contains(v, "line one[31m red?") || !strings.Contains(v, "two reversed") {
		t.Fatalf("body not shown (escape sequences must be defanged):\n%s", v)
	}
	if strings.ContainsAny(v, "\x1b\x07\u202e") {
		t.Fatalf("control characters reached the screen: %q", v)
	}
	for _, l := range lines(v) {
		if ansi.StringWidth(l) > 80 {
			t.Fatalf("body line too wide: %q", l)
		}
	}
	// esc goes back to the lists.
	m = keys(m, "esc")
	if m.open != nil || !strings.Contains(m.View(), "Messages") {
		t.Fatal("esc did not close the body")
	}

	// A message the machine cannot read.
	for m.feed.items[m.feedCur].MsgID != "m_1" {
		m = keys(m, "up")
	}
	m2, cmd = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("v")})
	m = send(m2.(Model), cmd())
	if v := m.View(); !strings.Contains(v, "The body is not available here") {
		t.Fatalf("no explanation:\n%s", v)
	}
	// A late answer for a message that is no longer open is ignored.
	m = keys(m, "esc")
	m = send(m, bodyMsg{id: "m_1", msg: protocol.Message{Body: "late"}})
	if m.open != nil {
		t.Fatal("a late body reopened the view")
	}
}

func TestSanitizeNetworkText(t *testing.T) {
	src := &fakeSource{snap: fakeSnapshot(), events: fakeEvents()}
	src.snap.Agents[0].Profile.Name = "evil\x1b[2J\x1b]0;pwned\x07name"
	src.snap.Agents[0].Model = "m\x1b[31m\nx"
	src.snap.Agents[0].Cwd = "/tmp/\x1b[2Jdir"
	src.snap.Nodes[0].Hostname = "Mac\x1b[H"
	m := New(src)
	m.loc, m.now = time.UTC, func() time.Time { return t0 }
	m = send(m, tea.WindowSizeMsg{Width: 120, Height: 34})
	m = refresh(m, src)
	for _, l := range lines(m.View()) {
		if strings.ContainsAny(l, "\x1b\x07") {
			t.Fatalf("an escape sequence from the network reached the screen: %q", l)
		}
	}
}

// ---- keys ----

func TestQuit(t *testing.T) {
	for _, k := range []tea.KeyMsg{{Type: tea.KeyRunes, Runes: []rune("q")}, {Type: tea.KeyCtrlC}} {
		_, cmd := newModel(80, 24).Update(k)
		if cmd == nil {
			t.Fatalf("%v did not quit", k)
		}
		if _, ok := cmd().(tea.QuitMsg); !ok {
			t.Fatalf("%v: not a quit", k)
		}
	}
	// q in the filter is text.
	m := keys(newModel(80, 24), "/")
	if _, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")}); cmd != nil {
		if _, ok := cmd().(tea.QuitMsg); ok {
			t.Fatal("q quit while filtering")
		}
	}
}

func TestTheScreenOnlyReads(t *testing.T) {
	// Every call the screen can make goes through Source, which has no method
	// that changes anything.
	var _ Source = (*DaemonSource)(nil)
	src := &recordingSource{fakeSource: fakeSource{snap: fakeSnapshot(), events: fakeEvents()}}
	m := New(src)
	m = send(m, tea.WindowSizeMsg{Width: 80, Height: 24})
	m = refresh(m, src)
	m = keys(m, "o", "/", "a", "enter", "esc", "tab", "up", "down", "r")
	for _, c := range src.calls {
		if c != "snapshot" && c != "events" && c != "message" {
			t.Fatalf("unexpected call %s", c)
		}
	}
}

type recordingSource struct {
	fakeSource
	calls []string
}

func (r *recordingSource) Snapshot(ctx context.Context) (Snapshot, error) {
	r.calls = append(r.calls, "snapshot")
	return r.fakeSource.Snapshot(ctx)
}
func (r *recordingSource) Events(ctx context.Context, a int64, l int) ([]protocol.FeedEvent, error) {
	r.calls = append(r.calls, "events")
	return r.fakeSource.Events(ctx, a, l)
}

// With colours on, the codes must be well formed and no text of ours may be
// broken by the clean-up of network text: stripping them leaves the plain view.
func TestColourOutputIsWellFormed(t *testing.T) {
	m := keys(newModel(100, 30), "o", "G")
	plain := m.View()
	lipgloss.SetColorProfile(termenv.ANSI256)
	defer lipgloss.SetColorProfile(termenv.Ascii)
	initScreenColors()
	coloured := m.View()
	if coloured == plain {
		t.Skip("this terminal profile draws no colour")
	}
	if got := ansi.Strip(coloured); got != plain {
		t.Fatalf("stripping the colours does not give the plain view.\n--- got ---\n%s\n--- plain ---\n%s", got, plain)
	}
	for i, l := range lines(coloured) {
		if ansi.StringWidth(l) > 100 {
			t.Fatalf("line %d is wider than the screen: %q", i, l)
		}
	}
}

func TestDetailShowsTheProjectSource(t *testing.T) {
	m := newModel(100, 30)
	want := map[string]string{
		"s_0000000000000002": "project source: registered",
		"s_0000000000000001": "project source: seen in a session and registered",
		"s_0000000000000003": "project source: seen in a session", // an older gateway sends no source
	}
	for id, text := range want {
		m.selected = id
		if v := detailText(m); !strings.Contains(v, text) {
			t.Errorf("%s: no %q in\n%s", id, text, v)
		}
	}
	// A session in a folder with no project entry shows no source.
	m.selected = "s_0000000000000004"
	if v := detailText(m); strings.Contains(v, "project source: ") {
		t.Errorf("a source for an unknown project:\n%s", v)
	}
}

// A dormant session is reachable but quiet. It is hidden with the offline
// ones and shows up under o.
func TestDormantSessionsAreHiddenWithTheOfflineOnes(t *testing.T) {
	snap := fakeSnapshot()
	snap.Agents = append(snap.Agents, agent("s_0000000000000007", "n_mac", "Mac", "opencode", "shop: quiet", func(a *protocol.AgentView) {
		a.Status = protocol.StatusDormant
	}))
	src := &fakeSource{snap: snap, events: fakeEvents()}
	m := New(src)
	m.loc = time.UTC
	m.now = func() time.Time { return t0 }
	m = send(m, tea.WindowSizeMsg{Width: 100, Height: 30})
	m = refresh(m, src)
	if strings.Contains(strings.Join(names(m), ","), "shop: quiet") {
		t.Fatalf("a dormant session is shown by default: %v", names(m))
	}
	m = keys(m, "o")
	if !strings.Contains(strings.Join(names(m), ","), "shop: quiet") {
		t.Fatalf("o does not show the dormant session: %v", names(m))
	}
	if got := statusShort(protocol.StatusDormant); got != "dorm" {
		t.Fatalf("status text %q", got)
	}
}

func TestNameColumnGrowsOnlyForLongNames(t *testing.T) {
	width := func(cols []column) int {
		for _, c := range cols {
			if c.name == "NAME" {
				return c.width
			}
		}
		return 0
	}
	if w := width(columns(120, 5)); w != 18 {
		t.Fatalf("short names: NAME is %d wide, want 18", w)
	}
	if w := width(columns(120, 28)); w != 28 {
		t.Fatalf("long names: NAME is %d wide, want 28", w)
	}
	if w := width(columns(120, 80)); w != 32 {
		t.Fatalf("very long names: NAME is %d wide, want 32", w)
	}
}
