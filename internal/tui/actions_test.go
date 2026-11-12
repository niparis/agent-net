package tui

import (
	"errors"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/niparis/agent-net/internal/client"
	"github.com/niparis/agent-net/internal/protocol"
)

const (
	ownerID  = "s_0000000000000007"
	workerID = "s_0000000000000008"
)

// workerFixture is the fake network plus the owner of the Mac, a worker that
// the owner started there, and the messages between them.
func workerFixture() (Snapshot, []protocol.FeedEvent) {
	snap := fakeSnapshot()
	snap.OwnerSession = ownerID
	snap.Hostname = "Mac"
	snap.Nodes[0].Harnesses[1].Models = []string{"anthropic/claude-haiku-4-5", "anthropic/claude-sonnet-5", "openai/gpt-6"}
	snap.Agents = append(snap.Agents,
		agent(ownerID, "n_mac", "Mac", "owner", "you (on Mac)", func(a *protocol.AgentView) { a.Mode = protocol.ModeForeground }),
		agent(workerID, "n_mac", "Mac", "opencode", "tenebrae", func(a *protocol.AgentView) {
			a.Status, a.Mode, a.Model = "busy", protocol.ModeBackground, "anthropic/claude-haiku-4-5"
			a.FirstSeen = t0.Add(-55 * time.Minute).Unix()
			a.Cwd = "/Users/n/code/tenebrae"
			a.Repo = protocol.RepoInfo{Root: "/Users/n/code/tenebrae", Worktree: "/Users/n/code/tenebrae", Project: "tenebrae", Branch: "main"}
			a.Worker = &protocol.WorkerInfo{ID: "w_1", Owner: ownerID, Project: "tenebrae", StartedAt: t0.Add(-5 * time.Minute).Unix(), Task: "tenebrae"}
		}))
	evs := fakeEvents()
	evs = append(evs,
		ev(13, -5*time.Minute, "worker.started", "", "", "", "", func(e *protocol.FeedEvent) {
			e.SessionID, e.Detail = workerID, "project=tenebrae harness=opencode owner="+ownerID
		}),
		ev(14, -5*time.Minute, "msg.stored", "m_10", ownerID, workerID, "request", nil),
		ev(15, -5*time.Minute, "msg.delivered", "m_10", ownerID, workerID, "request", nil),
		ev(16, -2*time.Minute, "msg.stored", "m_11", workerID, ownerID, "reply", func(e *protocol.FeedEvent) { e.Parent = "m_10" }),
		ev(17, -2*time.Minute, "msg.delivered", "m_11", workerID, ownerID, "reply", nil),
		ev(18, -1*time.Minute, "worker.refused", "", "", "", "", func(e *protocol.FeedEvent) {
			e.SessionID, e.Detail = ownerID, "project=nope reason=no project named nope is known"
		}),
	)
	return snap, evs
}

func workerModel(w, h int) (Model, *fakeSource) {
	snap, evs := workerFixture()
	src := &fakeSource{snap: snap, events: evs}
	m := New(src)
	m.activeOnly = false
	m.loc = time.UTC
	m.now = func() time.Time { return t0 }
	m = send(m, tea.WindowSizeMsg{Width: w, Height: h})
	return refresh(m, src), src
}

// selectID moves the selection to a session.
func selectID(m Model, id string) Model {
	if indexOfID(ids(m), id) < 0 {
		if _, ok := m.agentByID(id); ok {
			m.activeOnly = false
		}
	}
	m.selected = id
	m.ensureSelection()
	m.fixScroll()
	return m
}

// run executes the command an Update returned and feeds its message back.
func finish(t *testing.T, m Model, cmd tea.Cmd) Model {
	t.Helper()
	if cmd == nil {
		t.Fatal("no command")
	}
	msg := cmd()
	if _, ok := msg.(actionMsg); !ok {
		t.Fatalf("command gave %T", msg)
	}
	next, _ := m.Update(msg)
	return next.(Model)
}

func press(m Model, k tea.KeyPressMsg) (Model, tea.Cmd) {
	next, cmd := m.Update(k)
	return next.(Model), cmd
}

func typeText(m Model, s string) Model {
	for _, r := range s {
		m, _ = press(m, tea.KeyPressMsg{Text: string(r), Code: r})
	}
	return m
}

// ---- the owner and workers in the lists ----

func TestOwnerIsNotARowButIsNamedInTheFeed(t *testing.T) {
	m, _ := workerModel(80, 24)
	for _, a := range m.visible() {
		if a.Harness == "owner" {
			t.Fatalf("the owner is a row: %+v", a)
		}
	}
	if got := m.who(ownerID); got != "you (on Mac)" {
		t.Errorf("who(owner) = %q", got)
	}
	v := keys(send(m, tea.WindowSizeMsg{Width: 80, Height: 40}), "4", "tab").testView() // the feed needs room
	if !strings.Contains(v, "you → ") || !strings.Contains(v, "tenebrae") {
		t.Errorf("the feed does not show the owner:\n%s", v)
	}
	title := lines(send(m, tea.WindowSizeMsg{Width: 160, Height: 40}).testView())[0]
	if !strings.Contains(title, "7 sessions, 6 online") {
		t.Errorf("title: %q", title)
	}
}

func TestWorkerShowsItsOwner(t *testing.T) {
	m, _ := workerModel(120, 34)
	m = selectID(m, workerID)
	v := m.testView() + "\n" + detailText(m) + "\n" + feedText(m, 120)
	for _, want := range []string{"↳ tenebrae", "worker   started by you (on Mac), 14:00, project tenebrae", "s_0000000000000008 w_1",
		"worker started: tenebrae opencode in tenebrae for you", "worker refused in nope: no project named nope is known"} {
		if !strings.Contains(v, want) {
			t.Errorf("missing %q in:\n%s", want, v)
		}
	}
}

// ---- send a message as the owner ----

func TestSendAsOwner(t *testing.T) {
	m, src := workerModel(80, 24)
	m = selectID(m, workerID)
	m, _ = press(m, tea.KeyPressMsg{Text: "m", Code: []rune("m")[0]})
	if m.mode != modeCompose {
		t.Fatal("m did not open the prompt")
	}
	if h := lines(m.testView())[23]; !strings.Contains(h, "to tenebrae> ") || !strings.Contains(h, "esc cancels") {
		t.Errorf("help row: %q", h)
	}
	// An empty message is refused, and the prompt stays.
	m, _ = press(m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if m.mode != modeCompose || m.activeNotice() == nil || !m.notice.err {
		t.Fatalf("empty message: mode %v notice %+v", m.mode, m.notice)
	}
	m = typeText(m, "also run the tests")
	m, cmd := press(m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if m.mode != modeNormal {
		t.Fatal("enter did not close the prompt")
	}
	m = finish(t, m, cmd)
	if src.sentTo != workerID || src.sentBody != "also run the tests" {
		t.Errorf("sent to %q: %q", src.sentTo, src.sentBody)
	}
	n := m.activeNotice()
	if n == nil || n.err || !strings.Contains(n.text, "Sent to tenebrae (Mac) (m_sent) as you") {
		t.Fatalf("notice: %+v", n)
	}
	if v := m.testView(); !strings.Contains(v, "Sent to tenebrae (Mac) (m_sent) as you") {
		t.Errorf("the result is not on screen:\n%s", v)
	}
	// The next key clears the result line.
	m, _ = press(m, tea.KeyPressMsg{Code: tea.KeyDown})
	if m.activeNotice() != nil {
		t.Error("the result stayed after a key")
	}
}

func TestSendCancelAndErrors(t *testing.T) {
	m, src := workerModel(80, 24)
	m = selectID(m, workerID)
	m, _ = press(m, tea.KeyPressMsg{Text: "m", Code: []rune("m")[0]})
	m = typeText(m, "never sent")
	m, cmd := press(m, tea.KeyPressMsg{Code: tea.KeyEsc})
	if m.mode != modeNormal || cmd != nil || src.sentTo != "" || m.activeNotice() == nil || m.notice.text != "Message cancelled." {
		t.Errorf("esc: mode %v sent %q notice %+v", m.mode, src.sentTo, m.notice)
	}
	// A refusal from the daemon shows as one red line.
	src.actErr = client.RemoteError("only the owner at the command line can do this")
	m, _ = press(m, tea.KeyPressMsg{Text: "m", Code: []rune("m")[0]})
	m = typeText(m, "hi")
	m, cmd = press(m, tea.KeyPressMsg{Code: tea.KeyEnter})
	m = finish(t, m, cmd)
	if n := m.activeNotice(); n == nil || !n.err || !strings.Contains(n.text, "Not sent to tenebrae (Mac): only the owner") {
		t.Errorf("error notice: %+v", n)
	}
	if l := lastLine(m.testView()); !strings.HasPrefix(l, "! Not sent to") {
		t.Errorf("error line: %q", l)
	}
	// An offline session and an empty selection say why they cannot get a message.
	m = selectID(m, "s_0000000000000006") // offline
	m.showOffline = true
	m = selectID(m, "s_0000000000000006")
	m, _ = press(m, tea.KeyPressMsg{Text: "m", Code: []rune("m")[0]})
	if m.mode != modeNormal || m.activeNotice() == nil || !strings.Contains(m.notice.text, "offline") {
		t.Errorf("offline: %+v", m.notice)
	}
	m.selected = ""
	m, _ = press(m, tea.KeyPressMsg{Text: "m", Code: []rune("m")[0]})
	if !strings.Contains(m.notice.text, "Nothing is selected") {
		t.Errorf("nothing selected: %+v", m.notice)
	}
}

// ---- stop a worker ----

func TestStopAWorkerAsksFirst(t *testing.T) {
	m, src := workerModel(80, 24)
	// An owner session uses its own confirmation.
	m = selectID(m, "s_0000000000000001")
	m, _ = press(m, tea.KeyPressMsg{Text: "x", Code: []rune("x")[0]})
	if m.mode != modeConfirm || m.notice.err {
		t.Errorf("x on a plain session: %+v", m.notice)
	}
	m.mode = modeNormal
	m = selectID(m, workerID)
	m, _ = press(m, tea.KeyPressMsg{Text: "x", Code: []rune("x")[0]})
	if m.mode != modeConfirm || !strings.Contains(lines(m.testView())[23], "Stop worker tenebrae (Mac)?"+confirmSuffix) {
		t.Fatalf("confirm: mode %v help %q", m.mode, lines(m.testView())[23])
	}
	// n and esc leave it alone.
	for _, k := range []tea.KeyPressMsg{{Text: "n", Code: []rune("n")[0]}, {Code: tea.KeyEsc}} {
		mm, cmd := press(m, k)
		if mm.mode != modeNormal || cmd != nil || src.stopped != "" || mm.notice.text != "Not stopped." {
			t.Errorf("%v: mode %v stopped %q notice %q", k, mm.mode, src.stopped, mm.notice.text)
		}
	}
	// Other keys are ignored: the question stays.
	if mm, _ := press(m, tea.KeyPressMsg{Text: "q", Code: []rune("q")[0]}); mm.mode != modeConfirm || mm.quitting {
		t.Error("q must not quit while asking")
	}
	m, cmd := press(m, tea.KeyPressMsg{Text: "y", Code: []rune("y")[0]})
	m = finish(t, m, cmd)
	if src.stopped != workerID || m.notice.err || m.notice.text != "Stopped worker tenebrae (Mac)." {
		t.Errorf("stop: %q %+v", src.stopped, m.notice)
	}
	// An error is a red line.
	src.actErr = errors.New("machine Mac is offline")
	m = selectID(m, workerID)
	m, _ = press(m, tea.KeyPressMsg{Text: "x", Code: []rune("x")[0]})
	m, cmd = press(m, tea.KeyPressMsg{Text: "y", Code: []rune("y")[0]})
	m = finish(t, m, cmd)
	if !m.notice.err || m.notice.text != "Not stopped: machine Mac is offline" {
		t.Errorf("stop error: %+v", m.notice)
	}
}

// ---- golden screens ----

func TestActionScreensGolden(t *testing.T) {
	for _, size := range []struct{ w, h int }{{80, 24}, {120, 34}} {
		name := func(s string) string { return "view_" + itoa(size.w) + "x" + itoa(size.h) + "_" + s }

		m, _ := workerModel(size.w, size.h)
		m = quietFixtures(m)
		golden(t, name("worker"), selectID(m, workerID).testView())

		compose, _ := press(selectID(m, workerID), tea.KeyPressMsg{Text: "m", Code: []rune("m")[0]})
		golden(t, name("compose"), typeText(compose, "also run the tests").testView())

		confirm, _ := press(selectID(m, workerID), tea.KeyPressMsg{Text: "x", Code: []rune("x")[0]})
		golden(t, name("confirm_stop"), confirm.testView())

		ok := m
		ok.say("Sent to tenebrae (Mac) (m_sent) as you. The reply will show in the messages.")
		golden(t, name("notice_ok"), ok.testView())
		bad := m
		bad.fail("Not stopped: machine build-vm is offline")
		golden(t, name("notice_error"), bad.testView())

		feed := keys(selectID(m, ownerID), "C")
		if feed.focus != focusCTO {
			t.Fatal("C did not focus the CTO")
		}
		golden(t, name("feed_owner_reply"), feed.testView())
	}
}

func TestWorkerRowUsesTheTitle(t *testing.T) {
	a := protocol.AgentView{SessionID: workerID, Harness: "opencode", Worker: &protocol.WorkerInfo{
		ID: "w_1", Task: "Repo: shop\nBase: main\n\nFix the parser crash.", Title: "Parser fix"}}
	if got := taskText(a); got != "Parser fix" {
		t.Fatalf("taskText = %q, want the title", got)
	}
	a.Worker.Title = ""
	if got := taskText(a); got != "Repo: shop Base: main  Fix the parser crash." {
		t.Fatalf("taskText without a title = %q", got)
	}
}

func TestTitledWorkerScreenGolden(t *testing.T) {
	m, src := workerModel(120, 34)
	for i := range src.snap.Agents {
		if src.snap.Agents[i].SessionID == workerID && src.snap.Agents[i].Worker != nil {
			src.snap.Agents[i].Worker.Title = "Fix band"
		}
	}
	m = refresh(m, src)
	m = quietFixtures(m)
	golden(t, "view_120x34_worker_title", selectID(m, workerID).testView())
}

func itoa(i int) string {
	if i < 0 {
		return "-" + itoa(-i)
	}
	if i < 10 {
		return string(rune('0' + i))
	}
	return itoa(i/10) + string(rune('0'+i%10))
}

// ---- layout ----

func TestActionScreensFitEverySize(t *testing.T) {
	base, _ := workerModel(80, 24)
	base = selectID(base, workerID)
	setups := map[string]func(Model) Model{
		"compose": func(m Model) Model {
			m, _ = press(m, tea.KeyPressMsg{Text: "m", Code: []rune("m")[0]})
			return typeText(m, strings.Repeat("long ", 40))
		},
		"confirm": func(m Model) Model { m, _ = press(m, tea.KeyPressMsg{Text: "x", Code: []rune("x")[0]}); return m },
		"new session": func(m Model) Model {
			m, _ = press(m, sessionKey())
			return typeText(m, strings.Repeat("long ", 40))
		},
		"notice": func(m Model) Model {
			m.fail(strings.Repeat("a long error line ", 20))
			return m
		},
	}
	for name, setup := range setups {
		for w := 30; w <= 200; w += 17 {
			for h := 8; h <= 60; h += 7 {
				m := setup(send(base, tea.WindowSizeMsg{Width: w, Height: h}))
				ls := lines(m.testView())
				if len(ls) > max(h, 10) {
					t.Fatalf("%s %dx%d: %d lines", name, w, h, len(ls))
				}
				for i, l := range ls {
					if sw := ansi.StringWidth(l); sw > max(w, 40) {
						t.Fatalf("%s %dx%d line %d is %d wide: %q", name, w, h, i, sw, l)
					}
				}
			}
		}
	}
}
