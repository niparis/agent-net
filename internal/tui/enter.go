package tui

import (
	"context"
	"fmt"
	"os"
	osexec "os/exec"
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
	name string
	err  error
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
	return m, run(ptyproxy.New(cmd), func(err error) tea.Msg { return enterDoneMsg{name: name, err: err} })
}

// onEnterDone is the return from the session.
func (m Model) onEnterDone(r enterDoneMsg) (Model, tea.Cmd) {
	if r.err != nil {
		// The note holds the way back and the usual cause (herdr: an attach is
		// already open), which the screen of the entered command hid.
		m.fail("Left " + r.name + ": " + explain(r.err) + noteSuffix(m.enterNote))
	} else {
		m.say("Back from " + r.name + ".")
	}
	if !m.fetching {
		m.fetching = true
		return m, m.fetchCmd()
	}
	return m, nil
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
