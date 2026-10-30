package tui

import (
	"context"
	"errors"
	"fmt"
	"os"
	osexec "os/exec"
	"path/filepath"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/niparis/agent-net/internal/protocol"
	"github.com/niparis/agent-net/internal/ptyproxy"
)

// Enter a session (decision 0002, W16). The key e asks the daemon how to take
// over the terminal for the selected session (the session_enter method) and
// runs what it answers. The TUI builds no command of its own: the daemon knows
// the harness, the machine and the tmux pane, and the phone app will show the
// same plan. When the owner leaves the session, the TUI draws itself again.

// enterMsg is the daemon's answer to session_enter.
type enterMsg struct {
	name string
	plan protocol.EnterPlan
	err  error
}

// enterDoneMsg is the end of the entered command.
type enterDoneMsg struct {
	name     string
	err      error
	fallback string // the manual way, when the plan uses ssh
	target   string // the ssh target of the plan, or ""
}

// startEnter asks how to enter the selected session.
func (m Model) startEnter() (Model, tea.Cmd) {
	a, ok := m.selectedAgent()
	switch {
	case !ok:
		m.fail("Nothing is selected: pick a session first.")
		return m, nil
	case protocol.IsOwner(a.Harness):
		m.fail("That is you, not a session.")
		return m, nil
	}
	name := sanitize(dashIfEmpty(a.Profile.Name)) + " (" + sanitize(a.Hostname) + ")"
	m.say("Looking up how to enter " + name + "...")
	return m, m.enterCmd(a.SessionID, name)
}

func (m Model) enterCmd(id, name string) tea.Cmd {
	src, sock, hsock := m.src, m.tmuxSocket, m.herdrSocket
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		plan, err := src.SessionEnter(ctx, protocol.EnterRequest{SessionID: id, TmuxSocket: sock, HerdrSocket: hsock})
		return enterMsg{name: name, plan: plan, err: err}
	}
}

// onEnter runs the plan, or says why there is none.
func (m Model) onEnter(r enterMsg) (Model, tea.Cmd) {
	switch {
	case r.err != nil:
		m.fail("Cannot enter " + r.name + ": " + explain(r.err))
		return m, nil
	case len(r.plan.Argv) == 0:
		m.say(sanitize(firstNonEmpty(r.plan.Note, "Cannot enter "+r.name+" from here.")))
		return m, nil
	}
	cmd := enterCommand(r.plan)
	m.enterNote = r.plan.Note
	run := m.execProcess
	if run == nil {
		run = tea.Exec
	}
	name := r.name
	fallback, target := r.plan.Fallback, sshTarget(r.plan.Argv)
	proxy := ptyproxy.New(cmd)
	proxy.Name = name
	proxy.Title = name + " - ctrl+] to come back"
	return m, run(proxy, func(err error) tea.Msg {
		return enterDoneMsg{name: name, err: err, fallback: fallback, target: target}
	})
}

// onEnterDone is the return from the session.
func (m Model) onEnterDone(r enterDoneMsg) (Model, tea.Cmd) {
	switch {
	case r.err == nil:
		m.say("Back from " + r.name + ".")
	case r.fallback != "" && exitCodeOf(r.err) == 255:
		// ssh could not connect or log in. The command to run on the other
		// machine is more useful than the exit status.
		target := firstNonEmpty(sanitize(r.target), "the other machine")
		m.fail("Could not ssh to " + target + ". Set enter_ssh_hosts in node.json, or " + lowerFirst(sanitize(r.fallback)))
	default:
		// The note holds the way back and the usual cause (herdr: an attach is
		// already open), which the screen of the entered command hid.
		m.fail("Left " + r.name + ": " + explain(r.err) + noteSuffix(m.enterNote))
	}
	if !m.fetching {
		m.fetching = true
		return m, m.fetchCmd()
	}
	return m, nil
}

// sshTarget is the target of an ssh plan, or "" for another plan.
func sshTarget(argv []string) string {
	if len(argv) == 0 || filepath.Base(argv[0]) != "ssh" {
		return ""
	}
	for i, a := range argv {
		if a == "--" && i+1 < len(argv) {
			return argv[i+1]
		}
	}
	return ""
}

// exitCodeOf is the exit status of a finished command, or -1.
func exitCodeOf(err error) int {
	var e interface{ ExitCode() int }
	if errors.As(err, &e) {
		return e.ExitCode()
	}
	return -1
}

// lowerFirst makes the daemon's "On <host> run: ..." fit after "or ".
func lowerFirst(s string) string {
	if s == "" {
		return s
	}
	return strings.ToLower(s[:1]) + s[1:]
}

func noteSuffix(n string) string {
	if n == "" {
		return ""
	}
	return ". " + sanitize(n)
}

// enterCommand turns a plan into the command to run on this terminal.
func enterCommand(p protocol.EnterPlan) *osexec.Cmd {
	cmd := osexec.Command(p.Argv[0], p.Argv[1:]...)
	cmd.Dir = p.Dir
	if len(p.Env) > 0 {
		cmd.Env = os.Environ()
		for k, v := range p.Env {
			cmd.Env = append(cmd.Env, fmt.Sprintf("%s=%s", k, v))
		}
	}
	return cmd
}
