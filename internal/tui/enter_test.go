package tui

import (
	"errors"
	"fmt"
	"github.com/niparis/agent-net/internal/ptyproxy"
	"reflect"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/niparis/agent-net/internal/protocol"
)

// The key e hands the terminal to the selected session (decision 0002, W16).
// The daemon says what to run. These tests check that the TUI asks, runs
// exactly that, and shows the note when there is nothing to run.

func pressE(m Model) (Model, tea.Cmd) {
	return press(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("e")})
}

// capture replaces the terminal hand-over and records the command it got.
func capture(m *Model) (got *[]string, dir *string, env *[]string, done *func(error) tea.Msg) {
	return captureName(m, "tenebrae (Mac)")
}

// captureName is capture for a session with another name.
func captureName(m *Model, name string) (got *[]string, dir *string, env *[]string, done *func(error) tea.Msg) {
	got, dir, env, done = new([]string), new(string), new([]string), new(func(error) tea.Msg)
	m.execProcess = func(c tea.ExecCommand, cb tea.ExecCallback) tea.Cmd {
		proxy, ok := c.(*ptyproxy.Proxy)
		if !ok {
			panic("enter command is not proxied")
		}
		if proxy.Title != name+" - ctrl+] to come back" {
			panic("wrong proxy title: " + proxy.Title)
		}
		if proxy.Name != name {
			panic("wrong proxy name: " + proxy.Name)
		}
		cmd := proxy.Cmd
		*got, *dir, *env, *done = cmd.Args, cmd.Dir, cmd.Env, cb
		return func() tea.Msg { return nil }
	}
	return
}

func TestEnterKeyEntersTheSessionLikeE(t *testing.T) {
	for _, key := range []string{"enter", "e"} {
		t.Run(key, func(t *testing.T) {
			m, src := workerModel(120, 30)
			m = selectID(m, workerID)
			src.enterPlan = protocol.EnterPlan{Argv: []string{"opencode", "--session", "ses_9"}, Dir: "/work/tenebrae"}
			got, dir, _, _ := capture(&m)
			var cmd tea.Cmd
			if key == "enter" {
				m, cmd = press(m, tea.KeyMsg{Type: tea.KeyEnter})
			} else {
				m, cmd = pressE(m)
			}
			if cmd == nil {
				t.Fatal(key + " gave no command")
			}
			m, cmd = press2(m, cmd())
			if cmd == nil {
				t.Fatal(key + " did not run the plan")
			}
			if want := src.enterPlan.Argv; !reflect.DeepEqual(*got, want) || *dir != "/work/tenebrae" {
				t.Fatalf("%s ran %v in %q, want %v in /work/tenebrae", key, *got, *dir, want)
			}
		})
	}
}

func TestEnterOnAWaitingRowEntersItWithoutCompose(t *testing.T) {
	for _, inNeeds := range []bool{true, false} {
		row := "table"
		if inNeeds {
			row = "Needs you"
		}
		t.Run(row, func(t *testing.T) {
			m, src := waitingTableModel()
			m = selectWaitingRow(m, inNeeds)
			src.enterPlan = protocol.EnterPlan{Note: "cogforge-maintenance is not in a tmux pane agent-net can reach: open its window on build-vm"}

			m, cmd := press(m, tea.KeyMsg{Type: tea.KeyEnter})
			if m.mode != modeNormal {
				t.Fatalf("enter opened mode %v, want no compose form", m.mode)
			}
			if cmd == nil {
				t.Fatal("enter gave no enter command")
			}
			m, _ = press2(m, cmd())
			if len(src.enters) != 1 || src.enters[0].SessionID != cogforgeMaintenanceID {
				t.Fatalf("enter asked for %+v", src.enters)
			}
			if v := m.View(); !strings.Contains(v, "open its window on build-vm") {
				t.Errorf("the plan note is not on screen:\n%s", v)
			}
		})
	}
}

func TestEnterRunsThePlanOfTheDaemon(t *testing.T) {
	m, src := workerModel(100, 24)
	m = selectID(m, workerID)
	m.tmuxSocket = "/tmp/tmux-1/default"
	m.herdrSocket = "/tmp/herdr-1/herdr.sock"
	src.enterPlan = protocol.EnterPlan{Argv: []string{"opencode", "--session", "ses_9"}, Dir: "/work/tenebrae",
		Env: map[string]string{"OPENCODE_SERVER_PASSWORD": "pw"}, Note: "Press ctrl+b q to come back."}
	got, dir, env, done := capture(&m)

	m, cmd := pressE(m)
	if cmd == nil {
		t.Fatal("e gave no command")
	}
	if len(src.enters) != 0 {
		t.Fatal("the daemon is asked inside the command, not in Update")
	}
	if !strings.Contains(m.View(), "Looking up how to enter tenebrae (Mac)") {
		t.Errorf("no status line while asking:\n%s", m.View())
	}
	msg := cmd()
	if want := (protocol.EnterRequest{SessionID: workerID, TmuxSocket: "/tmp/tmux-1/default", HerdrSocket: "/tmp/herdr-1/herdr.sock"}); !reflect.DeepEqual(src.enters, []protocol.EnterRequest{want}) {
		t.Fatalf("asked %+v", src.enters)
	}
	m, cmd = press2(m, msg)
	if cmd == nil {
		t.Fatal("the plan did not lead to a command")
	}
	if want := []string{"opencode", "--session", "ses_9"}; !reflect.DeepEqual(*got, want) {
		t.Fatalf("ran %v, want %v", *got, want)
	}
	if *dir != "/work/tenebrae" || !contains(*env, "OPENCODE_SERVER_PASSWORD=pw") {
		t.Fatalf("dir %q env has the password: %v", *dir, contains(*env, "OPENCODE_SERVER_PASSWORD=pw"))
	}

	// The owner leaves the session: a status line and a refresh.
	m, cmd = press2(m, (*done)(nil))
	if !strings.Contains(m.View(), "Back from tenebrae (Mac).") || cmd == nil {
		t.Errorf("after the session:\n%s", m.View())
	}
	m, _ = press2(m, (*done)(errors.New("exit status 1")))
	if !strings.Contains(m.View(), "Left tenebrae (Mac): exit status 1. Press ctrl+b q to come back.") {
		t.Errorf("after a failed session:\n%s", m.View())
	}
}

func TestEnterWithNoCommandShowsTheNote(t *testing.T) {
	m, src := workerModel(100, 24)
	m = selectID(m, workerID)
	src.enterPlan = protocol.EnterPlan{Note: "tenebrae is not in a tmux pane agent-net can reach: open its window on Mac"}
	got, _, _, _ := capture(&m)
	m, cmd := pressE(m)
	m, cmd = press2(m, cmd())
	if len(*got) != 0 || cmd != nil {
		t.Fatalf("something ran: %v", *got)
	}
	if v := m.View(); !strings.Contains(v, "open its window on Mac") {
		t.Errorf("the note is not on screen:\n%s", v)
	}
}

// exitStatus is the error an exited command gives, with its code.
type exitStatus int

func (e exitStatus) Error() string { return fmt.Sprintf("exit status %d", int(e)) }
func (e exitStatus) ExitCode() int { return int(e) }

func TestEnterSSHFailureShowsTheFallback(t *testing.T) {
	m, src := workerModel(140, 30)
	m = selectID(m, workerID)
	src.enterPlan = protocol.EnterPlan{
		Argv:     []string{"ssh", "-t", "-o", "ConnectTimeout=5", "--", "research", "cd /work && exec claude attach x"},
		Note:     "Through ssh to research.",
		Fallback: "On research run: cd /work && exec claude attach x",
	}
	got, _, _, done := capture(&m)
	m, cmd := pressE(m)
	m, cmd = press2(m, cmd())
	if cmd == nil {
		t.Fatal("the ssh plan did not lead to a command")
	}
	if want := src.enterPlan.Argv; !reflect.DeepEqual(*got, want) {
		t.Fatalf("ran %v, want %v", *got, want)
	}

	// ssh could not connect or log in: the manual way is more useful than the
	// exit status.
	m, _ = press2(m, (*done)(exitStatus(255)))
	want := "Could not ssh to research. Set enter_ssh_hosts in node.json, or on research run: cd /work && exec claude attach x"
	if v := m.View(); !strings.Contains(v, want) {
		t.Errorf("after ssh exit 255:\n%s", v)
	}

	// Any other exit status is the session ending, as before.
	m, _ = press2(m, (*done)(exitStatus(1)))
	if v := m.View(); !strings.Contains(v, "Left tenebrae (Mac): exit status 1") {
		t.Errorf("after exit 1:\n%s", v)
	}
}

func TestEnterErrorsAreShown(t *testing.T) {
	m, src := workerModel(100, 24)
	m = selectID(m, workerID)
	src.enterErr = errors.New("no such session on this machine")
	capture(&m)
	m, cmd := pressE(m)
	m, _ = press2(m, cmd())
	if v := m.View(); !strings.Contains(v, "Cannot enter tenebrae (Mac)") || !strings.Contains(v, "no such session") {
		t.Errorf("the error is not on screen:\n%s", v)
	}
}

func TestEnterWithoutASelection(t *testing.T) {
	m, src := workerModel(100, 24)
	m.selected = ""
	m.snap.Agents = nil
	m, cmd := pressE(m)
	if cmd != nil || len(src.enters) != 0 {
		t.Fatal("asked with nothing selected")
	}
	if !strings.Contains(m.View(), "Nothing is selected") {
		t.Errorf("no message:\n%s", m.View())
	}
}

func TestEnterIsInTheHelpAndFooter(t *testing.T) {
	m, _ := workerModel(140, 30)
	if !strings.Contains(lines(m.View())[29], "e enter (ctrl+] back)") {
		t.Errorf("footer: %q", lines(m.View())[29])
	}
	m, _ = press(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("?")})
	if !strings.Contains(m.View(), "enter the session (ctrl+] to return)") {
		t.Errorf("help does not list e:\n%s", m.View())
	}
}

func TestHerdrSocketOf(t *testing.T) {
	for _, c := range []struct{ env, sock, want string }{{"1", "/h/herdr.sock", "/h/herdr.sock"}, {"", "/h/herdr.sock", ""}, {"1", "", ""}, {"0", "/h/s", ""}} {
		if got := herdrSocketOf(c.env, c.sock); got != c.want {
			t.Errorf("herdrSocketOf(%q, %q) = %q, want %q", c.env, c.sock, got, c.want)
		}
	}
}

func TestTmuxSocketOf(t *testing.T) {
	for in, want := range map[string]string{"": "", "/tmp/tmux-1/default,123,0": "/tmp/tmux-1/default", "/s": "/s"} {
		if got := tmuxSocketOf(in); got != want {
			t.Errorf("tmuxSocketOf(%q) = %q, want %q", in, got, want)
		}
	}
}

func press2(m Model, msg tea.Msg) (Model, tea.Cmd) {
	next, cmd := m.Update(msg)
	return next.(Model), cmd
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}
