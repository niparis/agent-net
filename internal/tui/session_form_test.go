package tui

import (
	"errors"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/niparis/agent-net/internal/protocol"
	"github.com/niparis/agent-net/internal/tui/graphview"
)

// The "n" form on Sessions and on the Graph card (docs/design/start-sessions.md,
// build plan part 2). It starts an owner session, not a worker.

func sessionKey() tea.KeyMsg {
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("n")}
}

func TestSDoesNotOpenAWorkerFormAndNOpensANewSession(t *testing.T) {
	m, src := workerModel(120, 34)
	m = selectID(m, "s_0000000000000001")

	m, cmd := press(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("s")})
	if m.mode != modeNormal || cmd != nil || src.started != nil {
		t.Fatalf("s opened a form or started a worker: mode %v, cmd %v, started %+v", m.mode, cmd != nil, src.started)
	}
	if strings.Contains(m.View(), "Start a worker") {
		t.Fatalf("worker form is still visible:\n%s", m.View())
	}

	m, _ = press(m, sessionKey())
	if m.mode != modeSessionForm {
		t.Fatalf("n opened mode %v, want the New session form", m.mode)
	}
}

func TestSKeyLinesGoldenAt80And120Columns(t *testing.T) {
	for _, size := range []struct{ w, h int }{{80, 24}, {120, 34}} {
		m, _ := workerModel(size.w, size.h)
		m = selectID(m, "s_0000000000000004")
		m, _ = press(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("s")})
		golden(t, "keyline_sessions_"+itoa(size.w), m.helpLine(size.w))

		quota := newModel(size.w, size.h)
		quota.tab = quotasTab
		golden(t, "keyline_quotas_"+itoa(size.w), quota.helpLine(size.w))

		outcome := newModel(size.w, size.h)
		outcome.snap.Outcomes = []protocol.OutcomeInboxEntry{{EdgeOutcome: protocol.EdgeOutcome{ReplyID: "r"}, Sender: "sender", Recipient: "receiver"}}
		outcome.selected = "outcome:r"
		golden(t, "keyline_outcome_"+itoa(size.w), outcome.helpLine(size.w))
	}
}

// startedSession is what the fake daemon answers for a successful start.
func startedSession(project, harness string) protocol.StartedSession {
	return protocol.StartedSession{
		ID: "start_1", Status: "started", NodeID: "n_mac", Harness: harness,
		Project: project, Path: "/Users/n/code/" + project, SessionID: "s_owner_new",
		StartedAt: t0.Unix(),
	}
}

func TestNewSessionFormFilledFromTheSelectedRow(t *testing.T) {
	m, _ := workerModel(120, 34)
	m = selectID(m, "s_0000000000000002") // strategies, claude, idle, in a linked worktree
	m, _ = press(m, sessionKey())
	if m.mode != modeSessionForm {
		t.Fatalf("n opened mode %v, want the New session form", m.mode)
	}
	if got := m.sform.value(sProject); got != "strategies" {
		t.Errorf("project %q", got)
	}
	if got := m.sform.value(sHarness); got != "claude" {
		t.Errorf("harness %q", got)
	}
	if got := m.sform.value(sModel); got != "" {
		t.Errorf("model %q: the harness default is empty", got)
	}
	if m.sform.cur != sFirstMessage {
		t.Errorf("focus %v, want First message", m.sform.cur)
	}
	if m.sform.folder != protocol.FolderProject {
		t.Errorf("folder %q, want the project folder", m.sform.folder)
	}
	v := m.View()
	for _, want := range []string{
		"New session", "Project", "strategies", "Harness", "claude", "Model",
		"Folder", "project folder", "no other session here", "First message",
		"empty: start and open it in herdr",
		"text: send this and open it in herdr",
	} {
		if !strings.Contains(v, want) {
			t.Errorf("form does not show %q:\n%s", want, v)
		}
	}
	if strings.Contains(v, "Branch") {
		t.Errorf("the project-folder form shows a branch line:\n%s", v)
	}
}

func TestNewSessionEnterWithEmptyMessageStartsAndEnters(t *testing.T) {
	m, src := workerModel(120, 34)
	m = selectID(m, "s_0000000000000001")
	src.sessionStart = startedSession("tenebrae", "claude")
	src.enterPlan = protocol.EnterPlan{Argv: []string{"claude", "attach", "job_9"}, Dir: "/Users/n/code/tenebrae"}
	got, dir, _, _ := capture(&m)

	m, _ = press(m, sessionKey())
	m, cmd := press(m, tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("enter gave no command")
	}
	if len(src.sessionStarts) != 0 {
		t.Fatal("session_start ran outside the command")
	}
	m, cmd = press2(m, cmd())
	if len(src.sessionStarts) != 1 {
		t.Fatalf("session_start asked %+v", src.sessionStarts)
	}
	req := src.sessionStarts[0]
	if req.Project != "tenebrae" || req.Harness != "claude" || req.Model != "" || req.FirstMessage != "" {
		t.Fatalf("session_start request: %+v", req)
	}
	if cmd == nil {
		t.Fatal("a start with no first message did not lead to the enter plan")
	}
	m, cmd = press2(m, cmd())
	if cmd == nil {
		t.Fatal("the enter plan did not run")
	}
	if want := []string{"claude", "attach", "job_9"}; !equalStrings(*got, want) || *dir != "/Users/n/code/tenebrae" {
		t.Fatalf("ran %v in %q, want %v in /Users/n/code/tenebrae", *got, *dir, want)
	}
	if len(src.enters) != 1 || src.enters[0].SessionID != "s_owner_new" {
		t.Fatalf("session_enter asked %+v", src.enters)
	}
	if m.mode != modeNormal {
		t.Errorf("the form is still open: %v", m.mode)
	}
}

func TestNewSessionEnterWithTextStartsInTheBackground(t *testing.T) {
	m, src := workerModel(120, 34)
	m = selectID(m, "s_0000000000000001")
	src.sessionStart = startedSession("tenebrae", "claude")

	m, _ = press(m, sessionKey())
	m = typeText(m, "Refactor the outbox retry into its own file.")
	m, cmd := press(m, tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("enter gave no command")
	}
	m, cmd = press2(m, cmd())
	if len(src.sessionStarts) != 1 || src.sessionStarts[0].FirstMessage != "Refactor the outbox retry into its own file." {
		t.Fatalf("session_start asked %+v", src.sessionStarts)
	}
	if cmd != nil && len(src.enters) > 0 {
		t.Fatal("a start with a first message entered the session")
	}
	if m.mode != modeNormal {
		t.Fatalf("form mode %v after the start", m.mode)
	}
	if len(src.enters) != 0 {
		t.Fatalf("session_enter asked %+v", src.enters)
	}
	want := "started tenebrae (claude) on Mac: first message sent. Enter opens it."
	if !strings.Contains(m.notice.text, want) {
		t.Fatalf("result line %q, want %q", m.notice.text, want)
	}
	if v := m.View(); !strings.Contains(v, want) {
		t.Errorf("result line is not on screen:\n%s", v)
	}
}

func TestNewSessionRefusesOtherHarnesses(t *testing.T) {
	for _, tc := range []struct{ id, harness, why string }{
		{"s_0000000000000002", "other", "unknown harness"},
		{"s_0000000000000004", "aider", "unknown harness on another machine"},
	} {
		t.Run(tc.why, func(t *testing.T) {
			m, src := workerModel(120, 34)
			m = selectID(m, tc.id)
			m, _ = press(m, sessionKey())
			if m.mode != modeSessionForm {
				t.Fatalf("n opened mode %v", m.mode)
			}
			m.sform.in[sHarness].SetValue(tc.harness)
			if v := m.View(); !strings.Contains(strings.ToLower(v), "only claude, opencode, codex and pi for now") {
				t.Errorf("the form does not refuse:\n%s", v)
			}
			m, cmd := press(m, tea.KeyMsg{Type: tea.KeyEnter})
			if cmd != nil || len(src.sessionStarts) != 0 {
				t.Fatalf("refused start still ran: cmd %v, asks %+v", cmd != nil, src.sessionStarts)
			}
			if m.mode != modeSessionForm || !strings.Contains(strings.ToLower(m.View()), "only claude, opencode, codex and pi for now") {
				t.Errorf("the refusal is not on the form:\n%s", m.View())
			}
		})
	}
}

func TestNewSessionShowsTheFailureReason(t *testing.T) {
	m, src := workerModel(120, 34)
	m = selectID(m, "s_0000000000000001")
	src.sessionStart = protocol.StartedSession{ID: "start_1", Status: "failed", Error: "Claude Code does not trust ~/code/tenebrae"}

	m, _ = press(m, sessionKey())
	m, cmd := press(m, tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("enter gave no command")
	}
	m, _ = press2(m, cmd())
	if !m.notice.err || !strings.Contains(m.notice.text, "Claude Code does not trust ~/code/tenebrae") {
		t.Fatalf("failure line %+v", m.notice)
	}
}

func TestNewSessionEscCancels(t *testing.T) {
	m, _ := workerModel(120, 34)
	m = selectID(m, "s_0000000000000001")
	m, _ = press(m, sessionKey())
	m, cmd := press(m, tea.KeyMsg{Type: tea.KeyEsc})
	if m.mode != modeNormal || cmd != nil || m.notice.text != "Start cancelled." {
		t.Fatalf("esc: mode %v cmd %v notice %q", m.mode, cmd != nil, m.notice.text)
	}
}

func TestNewSessionFromTheGraphCard(t *testing.T) {
	m := openMap(t, mapModel(120, 34, fakeGraph()))
	m.mp.selected = "tenebrae"
	m, _ = press(m, sessionKey())
	if m.mode != modeSessionForm {
		t.Fatalf("n on the card opened mode %v", m.mode)
	}
	if got := m.sform.value(sProject); got != "tenebrae" {
		t.Errorf("project %q", got)
	}
	if got := m.sform.value(sHarness); got != "claude" {
		t.Errorf("harness %q", got)
	}
	if m.sform.cur != sFirstMessage {
		t.Errorf("focus %v", m.sform.cur)
	}
	if v := m.View(); !strings.Contains(v, "New session") {
		t.Errorf("the card did not open the form:\n%s", v)
	}
}

func TestNewSessionFormGoldens(t *testing.T) {
	for _, size := range []struct{ w, h int }{{80, 24}, {120, 34}} {
		name := func(s string) string { return "view_" + itoa(size.w) + "x" + itoa(size.h) + "_" + s }

		m, src := workerModel(size.w, size.h)
		m = selectID(m, "s_0000000000000002") // strategies, idle, project folder free
		m, _ = press(m, sessionKey())
		golden(t, name("new_session_empty"), m.View())

		m = typeText(m, "Refactor the outbox retry into its own file.")
		golden(t, name("new_session_typed"), m.View())

		src.sessionStart = startedSession("strategies", "claude")
		m, cmd := press(m, tea.KeyMsg{Type: tea.KeyEnter})
		if cmd == nil {
			t.Fatalf("%dx%d: enter gave no command", size.w, size.h)
		}
		m, _ = press2(m, cmd())
		golden(t, name("new_session_started"), m.View())
	}
}

func TestNewSessionRefusalUsesHarnessWords(t *testing.T) {
	m, src := workerModel(120, 34)
	m = selectID(m, "s_0000000000000001")
	src.sessionErr = errors.New("Claude Code is not installed on Mac")
	m, _ = press(m, sessionKey())
	m, cmd := press(m, tea.KeyMsg{Type: tea.KeyEnter})
	m, _ = press2(m, cmd())
	if !m.notice.err || !strings.Contains(m.notice.text, "Claude Code is not installed on Mac") {
		t.Fatalf("failure line %+v", m.notice)
	}
}

func TestNewSessionKeyIsInTheKeyLineAndHelp(t *testing.T) {
	m := keys(newModel(120, 34), "1")
	if line := ansi.Strip(m.helpLine(120)); !strings.Contains(line, "n new") {
		t.Errorf("Sessions key line does not show n: %q", line)
	}
	help := ansi.Strip(strings.Join(m.helpContent(120), "\n"))
	if !strings.Contains(help, "start a new session") {
		t.Errorf("Sessions help does not list n:\n%s", help)
	}
	card := openMap(t, mapModel(120, 34, fakeGraph()))
	if line := ansi.Strip(card.helpLine(120)); !strings.Contains(line, "n new") {
		t.Errorf("Graph card key line does not show n: %q", line)
	}
	if help := ansi.Strip(strings.Join(card.helpContent(120), "\n")); !strings.Contains(help, "filled from here") {
		t.Errorf("Graph card help does not list n:\n%s", help)
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestNewSessionOpenCodeStartsAndEnters(t *testing.T) {
	m, src := workerModel(120, 34)
	m = selectID(m, "s_0000000000000003")
	src.sessionStart = startedSession("web", "opencode")
	src.enterPlan = protocol.EnterPlan{Argv: []string{"opencode", "--session", "ses_owner"}, Dir: "/Users/n/code/web"}
	got, _, _, _ := captureName(&m, "web (Mac)")
	m, _ = press(m, sessionKey())
	m, cmd := press(m, tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("OpenCode refused")
	}
	m, cmd = press2(m, cmd())
	if len(src.sessionStarts) != 1 || src.sessionStarts[0].Harness != "opencode" {
		t.Fatalf("requests: %+v", src.sessionStarts)
	}
	if cmd == nil {
		t.Fatal("no enter")
	}
	_, cmd = press2(m, cmd())
	if cmd == nil || !equalStrings(*got, []string{"opencode", "--session", "ses_owner"}) {
		t.Fatalf("enter: %v", *got)
	}
}

func TestNewSessionHarnessSupportGoldens(t *testing.T) {
	for _, size := range []struct{ w, h int }{{80, 24}, {120, 34}} {
		m, _ := workerModel(size.w, size.h)
		m = selectID(m, "s_0000000000000003")
		m, _ = press(m, sessionKey())
		golden(t, "view_"+itoa(size.w)+"x"+itoa(size.h)+"_new_session_opencode", m.View())
		m.sform.in[sHarness].SetValue("pi")
		golden(t, "view_"+itoa(size.w)+"x"+itoa(size.h)+"_new_session_pi", m.View())
		m.sform.in[sHarness].SetValue("aider")
		golden(t, "view_"+itoa(size.w)+"x"+itoa(size.h)+"_new_session_unsupported", m.View())
	}
}

func TestNewSessionCodexAlwaysEnters(t *testing.T) {
	for _, prompt := range []string{"", "Inspect this project"} {
		t.Run(prompt, func(t *testing.T) {
			m, src := workerModel(120, 34)
			m = selectID(m, "s_0000000000000002")
			src.sessionStart = startedSession("tenebrae", "codex")
			src.sessionStart.EnterPlan = &protocol.EnterPlan{Argv: []string{"codex"}, Dir: "/project"}
			got, dir, _, _ := capture(&m)
			m, _ = press(m, sessionKey())
			m.sform.in[sHarness].SetValue("codex")
			m = typeText(m, prompt)
			view := m.View()
			if !strings.Contains(view, "codex: opens it now") || strings.Contains(view, "background and send") {
				t.Fatalf("misleading Codex form:\n%s", view)
			}
			m, cmd := press(m, tea.KeyMsg{Type: tea.KeyEnter})
			if cmd == nil {
				t.Fatal("Codex refused")
			}
			m, cmd = press2(m, cmd())
			if cmd == nil {
				t.Fatal("Codex did not enter")
			}
			_, cmd = press2(m, cmd())
			if cmd == nil || !equalStrings(*got, []string{"codex"}) || *dir != "/project" {
				t.Fatalf("entered %v in %q", *got, *dir)
			}
			if len(src.sessionStarts) != 1 || src.sessionStarts[0].FirstMessage != prompt {
				t.Fatalf("request: %+v", src.sessionStarts)
			}
		})
	}
}

func TestNewSessionCodexGoldens(t *testing.T) {
	for _, size := range []struct{ w, h int }{{80, 24}, {120, 34}} {
		m, _ := workerModel(size.w, size.h)
		m = selectID(m, "s_0000000000000002")
		m, _ = press(m, sessionKey())
		m.sform.in[sHarness].SetValue("codex")
		golden(t, "view_"+itoa(size.w)+"x"+itoa(size.h)+"_new_session_codex", m.View())
	}
}

func TestNewSessionFromGraphNodeAndCard(t *testing.T) {
	for _, card := range []bool{false, true} {
		t.Run(itoa(map[bool]int{false: 0, true: 1}[card]), func(t *testing.T) {
			m := openMap(t, mapModel(120, 34, fakeGraph()))
			m.mapOn = card
			m.mp.selected = "tenebrae"
			m.graphState, _ = graphview.Update(m.graphState, graphview.SelectNode{Name: "tenebrae"})
			a := m.snap.Agents[0]
			a.Repo.Project, a.NodeID, a.Harness, a.Model, a.LastSeen = "tenebrae", m.snap.NodeID, "codex", "model-a", 1
			b, c := a, a
			b.LastSeen, c.LastSeen, c.Harness, c.Model = 2, 3, "claude", "model-b"
			m.snap.Agents = []protocol.AgentView{a, b, c}
			m, _ = press(m, sessionKey())
			if m.mode != modeSessionForm {
				t.Fatalf("n did not open form: %v", m.mode)
			}
			if m.sform.value(sProject) != "tenebrae" || m.sform.nodeID != a.NodeID || m.sform.value(sHarness) != "codex" || m.sform.value(sModel) != "model-a" {
				t.Fatalf("defaults: project %q node %q harness %q model %q", m.sform.value(sProject), m.sform.nodeID, m.sform.value(sHarness), m.sform.value(sModel))
			}
		})
	}
}

func TestNewSessionFromGraphNodeWithoutSessions(t *testing.T) {
	m := openMap(t, mapModel(120, 34, fakeGraph()))
	m.mapOn = false
	m.snap.Agents = nil
	m.graphState, _ = graphview.Update(m.graphState, graphview.SelectNode{Name: "shop"})
	m, _ = press(m, sessionKey())
	if m.mode != modeSessionForm || m.sform.value(sProject) != "shop" {
		t.Fatalf("mode %v project %q", m.mode, m.sform.value(sProject))
	}
}

func TestGraphNewSessionHelp(t *testing.T) {
	m := openMap(t, mapModel(120, 34, fakeGraph()))
	m.mapOn = false
	m = keys(m, "?")
	if !strings.Contains(ansi.Strip(strings.Join(m.helpContent(120), "\n")), "start a new session") {
		t.Fatalf("Graph help: %s", m.View())
	}
	m.mapOn, m.help = true, false
	if !strings.Contains(ansi.Strip(strings.Join(m.helpContent(120), "\n")), "n") || !strings.Contains(ansi.Strip(strings.Join(m.helpContent(120), "\n")), "filled from here") {
		t.Fatal("card help missing new session")
	}
}

func TestNewSessionGraphGoldens(t *testing.T) {
	for _, size := range []struct{ w, h int }{{80, 24}, {120, 34}} {
		m := openMap(t, mapModel(size.w, size.h, fakeGraph()))
		m.mapOn = false
		m.graphState, _ = graphview.Update(m.graphState, graphview.SelectNode{Name: "tenebrae"})
		m, _ = press(m, sessionKey())
		golden(t, "view_"+itoa(size.w)+"x"+itoa(size.h)+"_new_session_graph", m.View())
	}
}

func TestNewSessionHerdrNoticeWithFirstMessage(t *testing.T) {
	m, src := workerModel(120, 34)
	m = selectID(m, "s_0000000000000001")
	src.sessionStart = startedSession("tenebrae", "claude")
	src.sessionStart.EnterPlan = &protocol.EnterPlan{OpenedHerdr: true, Note: "opened tenebrae in herdr"}
	m, _ = press(m, sessionKey())
	m = typeText(m, "Inspect it")
	m, cmd := press(m, tea.KeyMsg{Type: tea.KeyEnter})
	m, _ = press2(m, cmd())
	if m.notice.text != "opened tenebrae in herdr" || len(src.enters) != 0 {
		t.Fatalf("notice %q, enters %+v", m.notice.text, src.enters)
	}
}
