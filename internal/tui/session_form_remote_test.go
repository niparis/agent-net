package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/niparis/agent-net/internal/protocol"
)

// The "n" form and Enter reach a project or row on another machine
// (docs/design/start-sessions.md, build plan part 8).

func remoteStartedSession() protocol.StartedSession {
	return protocol.StartedSession{
		ID: "start_remote", Status: "started", NodeID: "n_vm", Hostname: "build-vm",
		Harness: "pi", Project: "agent-net", Path: "/srv/agent-net", SessionID: "s_owner_remote",
		EnterNote: "Enter opens it through ssh to build-vm.", StartedAt: t0.Unix(),
	}
}

func TestNewSessionFormAcceptsAProjectOnAnotherMachine(t *testing.T) {
	m, src := workerModel(120, 34)
	m = selectID(m, "s_0000000000000004") // ci-runner, pi, on build-vm
	m, _ = press(m, sessionKey())
	if m.mode != modeSessionForm {
		t.Fatalf("n opened mode %v, want the New session form", m.mode)
	}
	if got := m.sform.value(sProject); got != "agent-net@build-vm" {
		t.Fatalf("project %q", got)
	}
	view := m.testView()
	for _, want := range []string{"on build-vm, /srv/agent-net", "empty: start and open it in herdr (on build-vm)"} {
		if !strings.Contains(view, want) {
			t.Errorf("the form does not show %q:\n%s", want, view)
		}
	}
	if why := m.sessionFormRefusal(m.sform); why != "" {
		t.Fatalf("the form refuses another machine: %q", why)
	}

	src.sessionStart = remoteStartedSession()
	src.enterPlan = protocol.EnterPlan{Argv: []string{"ssh", "-t", "--", "build-vm", "pi"}, Dir: "/srv/agent-net"}
	got, dir, _, _ := captureName(&m, "agent-net (build-vm)")
	m, cmd := press(m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("enter gave no command")
	}
	m, cmd = press2(m, cmd())
	if len(src.sessionStarts) != 1 {
		t.Fatalf("session_start asked %+v", src.sessionStarts)
	}
	req := src.sessionStarts[0]
	if req.Project != "agent-net" || req.Harness != "pi" || req.NodeID != "n_vm" {
		t.Fatalf("session_start request: %+v", req)
	}
	if cmd == nil {
		t.Fatal("a start with no first message did not lead to the enter plan")
	}
	m, cmd = press2(m, cmd())
	if cmd == nil || len(*got) == 0 || *dir != "/srv/agent-net" {
		t.Fatalf("enter: %v in %q", *got, *dir)
	}
}

func TestNewSessionRemoteStartNotesSSH(t *testing.T) {
	m, src := workerModel(120, 34)
	m = selectID(m, "s_0000000000000004")
	src.sessionStart = remoteStartedSession()
	m, _ = press(m, sessionKey())
	m = typeText(m, "Run the suite")
	m, cmd := press(m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("enter gave no command")
	}
	m, _ = press2(m, cmd())
	if len(src.sessionStarts) != 1 || src.sessionStarts[0].NodeID != "n_vm" {
		t.Fatalf("session_start asked %+v", src.sessionStarts)
	}
	if len(src.enters) != 0 {
		t.Fatalf("a start with a first message entered the session: %+v", src.enters)
	}
	want := "started agent-net (pi) on build-vm: first message sent. Enter opens it through ssh to build-vm."
	if !strings.Contains(m.notice.text, want) {
		t.Fatalf("result line %q, want %q", m.notice.text, want)
	}
}

func TestNewSessionRemoteGoldens(t *testing.T) {
	for _, size := range []struct{ w, h int }{{80, 24}, {120, 34}} {
		name := func(s string) string { return "view_" + itoa(size.w) + "x" + itoa(size.h) + "_" + s }

		m, src := workerModel(size.w, size.h)
		m = quietFixtures(m)
		m = selectID(m, "s_0000000000000004")
		m, _ = press(m, sessionKey())
		golden(t, name("new_session_remote"), m.testView())

		src.sessionStart = remoteStartedSession()
		m = typeText(m, "Run the suite")
		m, cmd := press(m, tea.KeyPressMsg{Code: tea.KeyEnter})
		if cmd == nil {
			t.Fatalf("%dx%d: enter gave no command", size.w, size.h)
		}
		m, _ = press2(m, cmd())
		golden(t, name("new_session_remote_started"), m.testView())
	}
}
