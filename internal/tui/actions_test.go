package tui

import (
	"errors"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
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
			a.Cwd = "/Users/n/code/tenebrae"
			a.Repo = protocol.RepoInfo{Root: "/Users/n/code/tenebrae", Worktree: "/Users/n/code/tenebrae", Project: "tenebrae", Branch: "main"}
			a.Worker = &protocol.WorkerInfo{ID: "w_1", Owner: ownerID, Project: "tenebrae", StartedAt: t0.Add(-5 * time.Minute).Unix()}
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
	m.loc = time.UTC
	m.now = func() time.Time { return t0 }
	m = send(m, tea.WindowSizeMsg{Width: w, Height: h})
	return refresh(m, src), src
}

// selectID moves the selection to a session.
func selectID(m Model, id string) Model {
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

func press(m Model, k tea.KeyMsg) (Model, tea.Cmd) {
	next, cmd := m.Update(k)
	return next.(Model), cmd
}

func typeText(m Model, s string) Model {
	for _, r := range s {
		m, _ = press(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
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
	v := keys(send(m, tea.WindowSizeMsg{Width: 80, Height: 40}), "tab").View() // the feed needs room
	if !strings.Contains(v, "you (on Mac) -> tenebrae (Mac)") || !strings.Contains(v, "tenebrae (Mac) -> you (on Mac)") {
		t.Errorf("the feed does not show the owner:\n%s", v)
	}
	title := lines(m.View())[0]
	if !strings.Contains(title, "7 sessions, 6 online, 1 workers") {
		t.Errorf("title: %q", title)
	}
}

func TestWorkerShowsItsOwner(t *testing.T) {
	m, _ := workerModel(120, 34)
	m = selectID(m, workerID)
	v := m.View()
	for _, want := range []string{"[w] tenebrae", "worker   started by you (on Mac), 14:00, project tenebrae",
		"worker started: tenebrae (Mac) opencode in tenebrae for you (on Mac)", "worker refused in nope: no project named nope is known"} {
		if !strings.Contains(v, want) {
			t.Errorf("missing %q in:\n%s", want, v)
		}
	}
}

// ---- send a message as the owner ----

func TestSendAsOwner(t *testing.T) {
	m, src := workerModel(80, 24)
	m = selectID(m, workerID)
	m, _ = press(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("m")})
	if m.mode != modeCompose {
		t.Fatal("m did not open the prompt")
	}
	if h := lines(m.View())[23]; !strings.Contains(h, "to tenebrae> ") || !strings.Contains(h, "esc cancels") {
		t.Errorf("help row: %q", h)
	}
	// An empty message is refused, and the prompt stays.
	m, _ = press(m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.mode != modeCompose || m.activeNotice() == nil || !m.notice.err {
		t.Fatalf("empty message: mode %v notice %+v", m.mode, m.notice)
	}
	m = typeText(m, "also run the tests")
	m, cmd := press(m, tea.KeyMsg{Type: tea.KeyEnter})
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
	if v := m.View(); !strings.Contains(v, "Sent to tenebrae (Mac) (m_sent) as you") {
		t.Errorf("the result is not on screen:\n%s", v)
	}
	// The next key clears the result line.
	m, _ = press(m, tea.KeyMsg{Type: tea.KeyDown})
	if m.activeNotice() != nil {
		t.Error("the result stayed after a key")
	}
}

func TestSendCancelAndErrors(t *testing.T) {
	m, src := workerModel(80, 24)
	m = selectID(m, workerID)
	m, _ = press(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("m")})
	m = typeText(m, "never sent")
	m, cmd := press(m, tea.KeyMsg{Type: tea.KeyEsc})
	if m.mode != modeNormal || cmd != nil || src.sentTo != "" || m.activeNotice() == nil || m.notice.text != "Message cancelled." {
		t.Errorf("esc: mode %v sent %q notice %+v", m.mode, src.sentTo, m.notice)
	}
	// A refusal from the daemon shows as one red line.
	src.actErr = client.RemoteError("only the owner at the command line can do this")
	m, _ = press(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("m")})
	m = typeText(m, "hi")
	m, cmd = press(m, tea.KeyMsg{Type: tea.KeyEnter})
	m = finish(t, m, cmd)
	if n := m.activeNotice(); n == nil || !n.err || !strings.Contains(n.text, "Not sent to tenebrae (Mac): only the owner") {
		t.Errorf("error notice: %+v", n)
	}
	if l := lines(m.View())[1]; !strings.HasPrefix(l, "! Not sent to") {
		t.Errorf("error line: %q", l)
	}
	// An offline session and an empty selection say why they cannot get a message.
	m = selectID(m, "s_0000000000000006") // offline
	m.showOffline = true
	m = selectID(m, "s_0000000000000006")
	m, _ = press(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("m")})
	if m.mode != modeNormal || m.activeNotice() == nil || !strings.Contains(m.notice.text, "offline") {
		t.Errorf("offline: %+v", m.notice)
	}
	m.selected = ""
	m, _ = press(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("m")})
	if !strings.Contains(m.notice.text, "Nothing is selected") {
		t.Errorf("nothing selected: %+v", m.notice)
	}
}

// ---- stop a worker ----

func TestStopAWorkerAsksFirst(t *testing.T) {
	m, src := workerModel(80, 24)
	// A session that is not a worker.
	m = selectID(m, "s_0000000000000001")
	m, _ = press(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("x")})
	if m.mode != modeNormal || !m.notice.err || !strings.Contains(m.notice.text, "not a worker") {
		t.Errorf("x on a plain session: %+v", m.notice)
	}
	m = selectID(m, workerID)
	m, _ = press(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("x")})
	if m.mode != modeConfirm || !strings.Contains(lines(m.View())[23], "Stop worker tenebrae (Mac)? y/n") {
		t.Fatalf("confirm: mode %v help %q", m.mode, lines(m.View())[23])
	}
	// n and esc leave it alone.
	for _, k := range []tea.KeyMsg{{Type: tea.KeyRunes, Runes: []rune("n")}, {Type: tea.KeyEsc}} {
		mm, cmd := press(m, k)
		if mm.mode != modeNormal || cmd != nil || src.stopped != "" || mm.notice.text != "Not stopped." {
			t.Errorf("%v: mode %v stopped %q notice %q", k, mm.mode, src.stopped, mm.notice.text)
		}
	}
	// Other keys are ignored: the question stays.
	if mm, _ := press(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")}); mm.mode != modeConfirm || mm.quitting {
		t.Error("q must not quit while asking")
	}
	m, cmd := press(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("y")})
	m = finish(t, m, cmd)
	if src.stopped != workerID || m.notice.err || m.notice.text != "Stopped worker tenebrae (Mac)." {
		t.Errorf("stop: %q %+v", src.stopped, m.notice)
	}
	// An error is a red line.
	src.actErr = errors.New("machine Mac is offline")
	m = selectID(m, workerID)
	m, _ = press(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("x")})
	m, cmd = press(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("y")})
	m = finish(t, m, cmd)
	if !m.notice.err || m.notice.text != "Not stopped: machine Mac is offline" {
		t.Errorf("stop error: %+v", m.notice)
	}
}

// ---- start a worker ----

func openForm(t *testing.T, m Model) Model {
	t.Helper()
	m, _ = press(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("s")})
	if m.mode != modeForm {
		t.Fatal("s did not open the form")
	}
	return m
}

func TestStartFormCompletesAndStarts(t *testing.T) {
	m, src := workerModel(120, 34)
	m = selectID(m, "s_0000000000000004") // a session with no project on a known list
	m = openForm(t, m)
	if m.form.cur != fProject || m.form.value(fHarness) != "opencode" {
		t.Fatalf("form start: cur %v harness %q", m.form.cur, m.form.value(fHarness))
	}
	// Project: "te" matches tenebrae only; tab completes it.
	m = typeText(m, "ten")
	if c := m.candidates(); len(c) != 1 || c[0].value != "tenebrae" || !strings.Contains(c[0].note, "both on Mac") {
		t.Fatalf("project candidates: %+v", c)
	}
	m, _ = press(m, tea.KeyMsg{Type: tea.KeyTab})
	if m.form.value(fProject) != "tenebrae" {
		t.Fatalf("tab did not complete: %q", m.form.value(fProject))
	}
	if note := m.fieldNote(fProject); note != "on Mac" {
		t.Errorf("project note %q", note)
	}
	// A second tab moves on to the harness, then the model.
	m, _ = press(m, tea.KeyMsg{Type: tea.KeyTab})
	if m.form.cur != fHarness {
		t.Fatalf("cur %v", m.form.cur)
	}
	m, _ = press(m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.form.cur != fModel {
		t.Fatalf("cur %v", m.form.cur)
	}
	// Model: the models of that machine, narrowed by what is typed.
	if c := m.candidates(); len(c) != 3 {
		t.Fatalf("model candidates: %+v", c)
	}
	m = typeText(m, "anthropic/claude-h")
	if c := m.candidates(); len(c) != 1 || c[0].value != "anthropic/claude-haiku-4-5" {
		t.Fatalf("narrowed: %+v", c)
	}
	m, _ = press(m, tea.KeyMsg{Type: tea.KeyTab})
	if m.form.value(fModel) != "anthropic/claude-haiku-4-5" {
		t.Fatalf("model: %q", m.form.value(fModel))
	}
	m, _ = press(m, tea.KeyMsg{Type: tea.KeyEnter})
	// Task. Enter with an empty task is refused with a line in the form.
	m, _ = press(m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.mode != modeForm || m.form.err != "Write the task." {
		t.Fatalf("empty task: mode %v err %q", m.mode, m.form.err)
	}
	if v := m.View(); !strings.Contains(v, "! Write the task.") {
		t.Errorf("the form error is not on screen:\n%s", v)
	}
	m = typeText(m, "reply with the word ok via agent_reply")
	m, cmd := press(m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.mode != modeNormal || m.notice.text == "" {
		t.Fatalf("submit: mode %v notice %+v", m.mode, m.notice)
	}
	if !strings.Contains(m.notice.text, "Starting a worker in tenebrae") {
		t.Errorf("progress line: %q", m.notice.text)
	}
	m = finish(t, m, cmd)
	want := protocol.WorkerStartRequest{Project: "tenebrae", Harness: "opencode", Model: "anthropic/claude-haiku-4-5", Task: "reply with the word ok via agent_reply"}
	if src.started == nil || *src.started != want {
		t.Fatalf("request: %+v", src.started)
	}
	if m.notice.err || !strings.Contains(m.notice.text, "Started worker s_new (opencode, tenebrae on Mac)") {
		t.Errorf("result: %+v", m.notice)
	}
}

func TestStartFormFillsTheProjectOfTheSelection(t *testing.T) {
	m, _ := workerModel(80, 24)
	m = selectID(m, "s_0000000000000001") // tenebrae-53, project tenebrae
	m = openForm(t, m)
	if m.form.value(fProject) != "tenebrae" || m.form.cur != fTask {
		t.Errorf("project %q, cur %v", m.form.value(fProject), m.form.cur)
	}
	// A project that is not in the list is not filled in.
	m, _ = workerModel(80, 24)
	m = selectID(m, "s_0000000000000004") // agent-net project: not in the project list
	m = openForm(t, m)
	if m.form.value(fProject) != "" || m.form.cur != fProject {
		t.Errorf("project %q, cur %v", m.form.value(fProject), m.form.cur)
	}
}

func TestStartFormMachineAndErrors(t *testing.T) {
	m, src := workerModel(80, 24)
	m = selectID(m, "s_0000000000000004") // its project is not in the list: nothing is filled in
	m.snap.Projects = append(m.snap.Projects,
		protocol.ProjectView{Name: "shop", NodeID: "n_mac", Hostname: "Mac", Path: "/a/shop", Source: protocol.SourceRegistered},
		protocol.ProjectView{Name: "shop", NodeID: "n_vm", Hostname: "build-vm", Path: "/b/shop", Source: protocol.SourceRegistered})
	m = openForm(t, m)
	m = typeText(m, "sho")
	cands := m.candidates()
	if len(cands) != 2 || cands[0].value != "shop@Mac" || cands[1].value != "shop@build-vm" {
		t.Fatalf("a project on two machines is offered per machine: %+v", cands)
	}
	m, _ = press(m, tea.KeyMsg{Type: tea.KeyTab}) // common start: "shop@"
	if m.form.value(fProject) != "shop@" {
		t.Fatalf("common prefix: %q", m.form.value(fProject))
	}
	m = typeText(m, "build-vm")
	for i := 0; i < 3; i++ {
		m, _ = press(m, tea.KeyMsg{Type: tea.KeyEnter})
	}
	m = typeText(m, "write tests")
	m, cmd := press(m, tea.KeyMsg{Type: tea.KeyEnter})
	src.actErr = errors.New("machine build-vm does not accept workers (it was set up with --no-launch)")
	m = finish(t, m, cmd)
	if src.started == nil || src.started.Project != "shop" || src.started.Machine != "build-vm" {
		t.Fatalf("request: %+v", src.started)
	}
	if !m.notice.err || m.notice.text != "Worker not started: machine build-vm does not accept workers (it was set up with --no-launch)" {
		t.Errorf("error: %+v", m.notice)
	}
	// Esc cancels, and up goes back a field.
	m = openForm(t, m)
	m, _ = press(m, tea.KeyMsg{Type: tea.KeyDown})
	m, _ = press(m, tea.KeyMsg{Type: tea.KeyUp})
	if m.form.cur != fProject {
		t.Errorf("cur %v", m.form.cur)
	}
	m, cmd = press(m, tea.KeyMsg{Type: tea.KeyEsc})
	if m.mode != modeNormal || cmd != nil || m.notice.text != "Start cancelled." {
		t.Errorf("esc: %v %+v", m.mode, m.notice)
	}
	// With no project known the form says how to register one.
	m.snap.Projects = nil
	m = openForm(t, m)
	if v := m.View(); !strings.Contains(v, "No projects are known. Register one with: agent-net projects add <path>") {
		t.Errorf("empty project list:\n%s", v)
	}
}

// ---- golden screens ----

func TestActionScreensGolden(t *testing.T) {
	for _, size := range []struct{ w, h int }{{80, 24}, {120, 34}} {
		name := func(s string) string { return "view_" + itoa(size.w) + "x" + itoa(size.h) + "_" + s }

		m, _ := workerModel(size.w, size.h)
		golden(t, name("worker"), selectID(m, workerID).View())

		compose, _ := press(selectID(m, workerID), tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("m")})
		golden(t, name("compose"), typeText(compose, "also run the tests").View())

		confirm, _ := press(selectID(m, workerID), tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("x")})
		golden(t, name("confirm_stop"), confirm.View())

		form := openForm(t, selectID(m, "s_0000000000000004"))
		golden(t, name("start_form_empty"), form.View())
		golden(t, name("start_form_project"), typeText(form, "t").View())
		form = typeText(form, "tenebrae")
		form, _ = press(form, tea.KeyMsg{Type: tea.KeyEnter})
		form, _ = press(form, tea.KeyMsg{Type: tea.KeyEnter})
		golden(t, name("start_form_model"), typeText(form, "anthropic/").View())

		ok := m
		ok.say("Sent to tenebrae (Mac) (m_sent) as you. The reply will show in the messages.")
		golden(t, name("notice_ok"), ok.View())
		bad := m
		bad.fail("Worker not started: machine build-vm does not accept workers (it was set up with --no-launch)")
		golden(t, name("notice_error"), bad.View())

		feed := keys(selectID(m, ownerID), "tab")
		golden(t, name("feed_owner_reply"), feed.View())
	}
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
			m, _ = press(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("m")})
			return typeText(m, strings.Repeat("long ", 40))
		},
		"confirm": func(m Model) Model { m, _ = press(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("x")}); return m },
		"form": func(m Model) Model {
			m, _ = press(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("s")})
			return typeText(m, "t")
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
				ls := lines(m.View())
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
