package tui

import (
	"errors"
	"fmt"
	"github.com/niparis/agent-net/internal/ptyproxy"
	"io"
	"reflect"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/niparis/agent-net/internal/protocol"
)

// The key e hands the terminal to the selected session (decision 0002, W16).
// The daemon says what to run. These tests check that the TUI asks, runs
// exactly that, and shows the note when there is nothing to run.

func pressE(m Model) (Model, tea.Cmd) {
	return press(m, tea.KeyPressMsg{Code: tea.KeyEnter})
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

func TestEnterKeyEntersTheSession(t *testing.T) {
	for _, key := range []string{"enter"} {
		t.Run(key, func(t *testing.T) {
			m, src := workerModel(120, 30)
			m = selectID(m, workerID)
			src.enterPlan = protocol.EnterPlan{Argv: []string{"opencode", "--session", "ses_9"}, Dir: "/work/tenebrae"}
			got, dir, _, _ := capture(&m)
			var cmd tea.Cmd
			if key == "enter" {
				m, cmd = press(m, tea.KeyPressMsg{Code: tea.KeyEnter})
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
	m, src := waitingTableModel()
	m.selected = cogforgeMaintenanceID
	m.selIdx = tableOrdinal(m, cogforgeMaintenanceID)
	src.enterPlan = protocol.EnterPlan{Note: "cogforge-maintenance is not in a herdr pane agent-net can reach: open its window on build-vm"}

	m, cmd := press(m, tea.KeyPressMsg{Code: tea.KeyEnter})
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
	if v := m.testView(); !strings.Contains(v, "open its window on build-vm") {
		t.Errorf("the plan note is not on screen:\n%s", v)
	}
}

func TestEnterRunsThePlanOfTheDaemon(t *testing.T) {
	m, src := workerModel(100, 24)
	m = selectID(m, workerID)
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
	if !strings.Contains(m.testView(), "Looking up how to enter tenebrae (Mac)") {
		t.Errorf("no status line while asking:\n%s", m.testView())
	}
	msg := cmd()
	if want := (protocol.EnterRequest{SessionID: workerID, HerdrSocket: "/tmp/herdr-1/herdr.sock"}); !reflect.DeepEqual(src.enters, []protocol.EnterRequest{want}) {
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
	if !strings.Contains(m.testView(), "Back from tenebrae (Mac).") || cmd == nil {
		t.Errorf("after the session:\n%s", m.testView())
	}
	m, _ = press2(m, (*done)(errors.New("exit status 1")))
	if !strings.Contains(m.testView(), "Left tenebrae (Mac): exit status 1. Press ctrl+b q to come back.") {
		t.Errorf("after a failed session:\n%s", m.testView())
	}
}

// runProxy is the terminal hand-over for a test that runs the real proxy on
// pipes, so the entered command's output goes through it as on a terminal.
func runProxy(c tea.ExecCommand, cb tea.ExecCallback) tea.Cmd {
	proxy, ok := c.(*ptyproxy.Proxy)
	if !ok {
		panic("enter command is not proxied")
	}
	proxy.SetStdin(strings.NewReader(""))
	proxy.SetStdout(io.Discard)
	return func() tea.Msg { return cb(proxy.Run()) }
}

// A command that fails says why on the terminal it was handed, and the TUI
// used to show only its exit status after taking the screen back. herdr's
// reason (an attach is already open) was lost this way.
func TestEnterFailureShowsWhatTheCommandPrinted(t *testing.T) {
	m, src := workerModel(160, 30)
	m = selectID(m, workerID)
	src.enterPlan = protocol.EnterPlan{
		Argv: []string{"sh", "-c", "printf 'error: terminal already has an attached client\\n' >&2; exit 1"},
		Note: "Press ctrl+] to come back here; tenebrae (Mac) keeps running.",
	}
	m.execProcess = runProxy
	m, cmd := pressE(m)
	m, cmd = press2(m, cmd())
	if cmd == nil {
		t.Fatal("the plan did not lead to a command")
	}
	m, _ = press2(m, cmd())
	want := "Left tenebrae (Mac): error: terminal already has an attached client (exit status 1). Press ctrl+] to come back here; tenebrae (Mac) keeps running."
	if got := m.notice.text; !strings.Contains(got, want) {
		t.Fatalf("notice %q, want %q", got, want)
	}
}

func TestEnterWithNoCommandShowsTheNote(t *testing.T) {
	m, src := workerModel(100, 24)
	m = selectID(m, workerID)
	src.enterPlan = protocol.EnterPlan{Note: "tenebrae is not in a herdr pane agent-net can reach: open its window on Mac"}
	got, _, _, _ := capture(&m)
	m, cmd := pressE(m)
	m, cmd = press2(m, cmd())
	if len(*got) != 0 || cmd != nil {
		t.Fatalf("something ran: %v", *got)
	}
	if v := m.testView(); !strings.Contains(v, "open its window on Mac") {
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
	if v := m.testView(); !strings.Contains(v, want) {
		t.Errorf("after ssh exit 255:\n%s", v)
	}

	// Any other exit status is the session ending, as before.
	m, _ = press2(m, (*done)(exitStatus(1)))
	if v := m.testView(); !strings.Contains(v, "Left tenebrae (Mac): exit status 1") {
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
	if v := m.testView(); !strings.Contains(v, "Cannot enter tenebrae (Mac)") || !strings.Contains(v, "no such session") {
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
	if !strings.Contains(m.testView(), "Nothing is selected") {
		t.Errorf("no message:\n%s", m.testView())
	}
}

func TestEnterIsInTheHelpAndFooter(t *testing.T) {
	m, _ := workerModel(140, 30)
	if foot := lines(m.testView())[29]; !strings.Contains(foot, "enter open") || !strings.Contains(foot, "x stop") {
		t.Errorf("footer: %q", foot)
	}
	m, _ = press(m, tea.KeyPressMsg{Text: "?", Code: []rune("?")[0]})
	v := m.testView()
	if !strings.Contains(v, "go into the selected thing") {
		t.Errorf("help does not list enter:\n%s", v)
	}
	if strings.Contains(v, "alias for Enter") {
		t.Errorf("help still lists e as an alias:\n%s", v)
	}
	if strings.Contains(v, "open the conversation") {
		t.Errorf("help still lists the conversation view:\n%s", v)
	}
}

func TestHerdrSocketOf(t *testing.T) {
	for _, c := range []struct{ env, sock, want string }{{"1", "/h/herdr.sock", "/h/herdr.sock"}, {"", "/h/herdr.sock", ""}, {"1", "", ""}, {"0", "/h/s", ""}} {
		if got := herdrSocketOf(c.env, c.sock); got != c.want {
			t.Errorf("herdrSocketOf(%q, %q) = %q, want %q", c.env, c.sock, got, c.want)
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

func TestEnterFallbackShowsHerdrInstallNoticeOnReturn(t *testing.T) {
	m, _ := workerModel(80, 24)
	m.enterNote = "Press ctrl+] to come back here. Herdr was not found or failed; install: https://herdr.dev/tui/docs/install/."
	m, _ = m.onEnterDone(enterDoneMsg{name: "garden"})
	if !strings.Contains(m.notice.text, "Herdr was not found") || !strings.Contains(m.notice.text, "install") {
		t.Fatalf("notice %q", m.notice.text)
	}
	line, _ := m.noticeLine(80)
	if !strings.Contains(line, "herdr.dev/tui/docs/install/") {
		t.Fatalf("installation link is clipped: %q", line)
	}
}
