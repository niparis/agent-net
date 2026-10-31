package daemon

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

// Entering a session that runs in herdr (decision 0002, W16). A fake herdr
// answers `pane list` and `pane process-info` from files and logs every call.
// No real herdr is started, and the real herdr of the machine is never asked.

// hideRealHerdr makes the tests blind to the herdr they run in: the
// environment of a process is shown without HERDR_* variables, unless the
// socket is under the temporary folder (a test made it).
func hideRealHerdr() {
	raw := osProcessEnviron
	processEnviron = func(pid int) []string {
		env := raw(pid)
		keep := false
		for _, kv := range env {
			if v, ok := strings.CutPrefix(kv, "HERDR_SOCKET_PATH="); ok && strings.HasPrefix(v, os.TempDir()) {
				keep = true
			}
		}
		out := env[:0:0]
		for _, kv := range env {
			if strings.HasPrefix(kv, "HERDR_") && !keep {
				continue
			}
			out = append(out, kv)
		}
		return out
	}
}

type fakeHerdr struct {
	dir, bin, log, sock string
}

// newFakeHerdr writes a herdr that prints <dir>/panes for `pane list`, and
// <dir>/info-<pane> for `pane process-info --pane <pane>`.
func newFakeHerdr(t *testing.T) *fakeHerdr {
	t.Helper()
	dir := t.TempDir()
	f := &fakeHerdr{dir: dir, bin: filepath.Join(dir, "herdr"), log: filepath.Join(dir, "log"), sock: filepath.Join(dir, "h.sock")}
	script := `#!/bin/sh
echo "sock=$HERDR_SOCKET_PATH session=${HERDR_SESSION:-none} $*" >> ` + f.log + `
[ -f ` + dir + `/fail ] && { echo "server down" >&2; exit 1; }
case "$1 $2" in
  "pane list") cat ` + dir + `/panes ;;
  "pane process-info") cat ` + dir + `/info-$4 2>/dev/null || exit 1 ;;
  *) exit 1 ;;
esac
`
	if err := os.WriteFile(f.bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return f
}

func (f *fakeHerdr) panes(t *testing.T, json string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(f.dir, "panes"), []byte(`{"result":{"panes":[`+json+`]}}`), 0o644); err != nil {
		t.Fatal(err)
	}
}

func (f *fakeHerdr) holds(t *testing.T, pane string, pid int) {
	t.Helper()
	body := `{"result":{"process_info":{"shell_pid":1,"foreground_processes":[{"pid":` + strconv.Itoa(pid) + `}]}}}`
	if err := os.WriteFile(filepath.Join(f.dir, "info-"+pane), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func (f *fakeHerdr) calls(t *testing.T) string {
	t.Helper()
	b, _ := os.ReadFile(f.log)
	return string(b)
}

// session starts a process that looks like a session in the pane `pane`.
func (f *fakeHerdr) session(t *testing.T, pane string) int {
	t.Helper()
	cmd := exec.Command("sleep", "30")
	cmd.Env = append(os.Environ(), "HERDR_ENV=1", "HERDR_PANE_ID="+pane, "HERDR_SOCKET_PATH="+f.sock, "HERDR_BIN_PATH="+f.bin, "HERDR_SESSION=other")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cmd.Process.Kill(); cmd.Wait() })
	waitProcessEnv(t, cmd.Process.Pid, "HERDR_ENV=1")
	return cmd.Process.Pid
}

// waitProcessEnv waits until /proc/<pid>/environ shows want. On Linux cmd.Start
// can return before the new program has set its environment area, and the file
// then reads empty for a moment.
func waitProcessEnv(t *testing.T, pid int, want string) {
	t.Helper()
	eventually(t, "environment of pid "+itoa(pid), func() bool {
		for _, kv := range osProcessEnviron(pid) {
			if kv == want {
				return true
			}
		}
		return false
	})
}

func herdrAgentPane(id, term string) string {
	return `{"pane_id":"` + id + `","terminal_id":"` + term + `","agent":"claude"}`
}

func herdrPlanFor(pid int, q protocol.EnterRequest, viaGateway bool) (protocol.EnterPlan, bool) {
	d := &Daemon{}
	return d.herdrPlan(protocol.SessionInfo{PID: pid}, q, protocol.EnterPlan{Host: "h"}, "s", viaGateway)
}

// From a TUI outside herdr, and from one inside any herdr: the same attach, with
// the socket in argv, and the detach key that works where the TUI runs.
func TestEnterHerdrAttach(t *testing.T) {
	f := newFakeHerdr(t)
	f.panes(t, herdrAgentPane("w9:p9", "term_9")+","+herdrAgentPane("w1:pB", "term_b"))
	f.holds(t, "w1:pB", 0)
	pid := f.session(t, "w1:pB")
	f.holds(t, "w1:pB", pid)

	p, ok := herdrPlanFor(pid, protocol.EnterRequest{}, false)
	want := []string{"env", "-u", "HERDR_SESSION", "HERDR_SOCKET_PATH=" + f.sock, f.bin, "agent", "attach", "w1:pB"}
	if !ok || !reflect.DeepEqual(p.Argv, want) {
		t.Fatalf("ok=%v argv %v, want %v", ok, p.Argv, want)
	}
	if !strings.Contains(p.Note, "Press ctrl+] to come back here;") {
		t.Errorf("note outside herdr: %q", p.Note)
	}
	// Never takes the terminal over from another attach.
	if strings.Contains(strings.Join(p.Argv, " "), "takeover") {
		t.Errorf("takeover in %v", p.Argv)
	}
	// The herdr commands ran against the session's server, not the inherited session.
	if c := f.calls(t); !strings.Contains(c, "sock="+f.sock+" session=none pane list") {
		t.Errorf("calls: %q", c)
	}

	// The TUI is itself in herdr (the same server or another one): the first
	// ctrl+b goes to the outer herdr. There is still one plan: focus would not
	// move the TUI's client.
	p, _ = herdrPlanFor(pid, protocol.EnterRequest{HerdrSocket: f.sock}, false)
	if !reflect.DeepEqual(p.Argv, want) || !strings.Contains(p.Note, "Press ctrl+] to come back here;") {
		t.Errorf("inside herdr: %v %q", p.Argv, p.Note)
	}
	// Asked by another machine: its terminal is not known.
	p, _ = herdrPlanFor(pid, protocol.EnterRequest{}, true)
	if !strings.Contains(p.Note, "Press ctrl+] to come back here;") {
		t.Errorf("via gateway: %q", p.Note)
	}
}

// The socket travels in argv, so the ssh wrapper keeps it (plan.Env does not
// cross the gateway).
func TestEnterHerdrRemoteWrapping(t *testing.T) {
	f := newFakeHerdr(t)
	f.sock = filepath.Join(f.dir, "my sock", "h.sock") // a space must survive the shell
	f.panes(t, herdrAgentPane("w1:pB", "term_b"))
	pid := f.session(t, "w1:pB")
	f.holds(t, "w1:pB", pid)
	p, ok := herdrPlanFor(pid, protocol.EnterRequest{}, true)
	if !ok || len(p.Argv) == 0 || len(p.Env) != 0 {
		t.Fatalf("plan %+v ok=%v", p, ok)
	}
	got := remoteCommand("/work", p.Argv)
	want := "cd /work && exec env -u HERDR_SESSION 'HERDR_SOCKET_PATH=" + f.sock + "' " + shellQuote(f.bin) + " agent attach w1:pB"
	if got != want {
		t.Fatalf("remote command\n got %s\nwant %s", got, want)
	}
}

// A pane that moved: HERDR_PANE_ID is old. The pane that holds the process now
// is found with process-info. When none does, a note says so.
func TestEnterHerdrStalePane(t *testing.T) {
	f := newFakeHerdr(t)
	f.panes(t, herdrAgentPane("w1:p1", "term_1")+","+herdrAgentPane("w3:p2", "term_3"))
	pid := f.session(t, "w1:pB") // w1:pB does not exist any more
	f.holds(t, "w1:p1", 999999)
	f.holds(t, "w3:p2", pid)
	p, ok := herdrPlanFor(pid, protocol.EnterRequest{}, false)
	if !ok || len(p.Argv) == 0 || p.Argv[len(p.Argv)-1] != "w3:p2" || p.Argv[len(p.Argv)-2] != "attach" {
		t.Fatalf("moved pane: %+v ok=%v", p, ok)
	}

	// The old ID exists but belongs to another process now.
	f.panes(t, herdrAgentPane("w1:pB", "term_b")+","+herdrAgentPane("w3:p2", "term_3"))
	f.holds(t, "w1:pB", 999999)
	if p, _ = herdrPlanFor(pid, protocol.EnterRequest{}, false); len(p.Argv) == 0 || p.Argv[len(p.Argv)-1] != "w3:p2" {
		t.Fatalf("reused id: %+v", p)
	}

	// Nothing holds it.
	f.holds(t, "w3:p2", 999999)
	p, ok = herdrPlanFor(pid, protocol.EnterRequest{}, false)
	if !ok || len(p.Argv) != 0 || !strings.Contains(p.Note, "has closed") || !strings.Contains(p.Note, "open its window on h") {
		t.Fatalf("closed pane: %+v ok=%v", p, ok)
	}
}

// An OpenCode session is not lost when its herdr pane closes: the session is
// still in the OpenCode service, so enter opens it there, not with a note.
// Only when the service cannot be reached either does the note say what was
// tried.
func TestEnterOpenCodeClosedHerdrPaneUsesTheService(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	bins := fakeBin(t, "opencode")
	cwd := t.TempDir()
	f := newFakeHerdr(t)
	f.panes(t, "") // the recorded pane w3:p2X has closed, and no pane holds it
	pid := f.session(t, "w3:p2X")
	info := protocol.SessionInfo{NativeID: "ses_1", PID: pid, Cwd: cwd, Name: "oc", Status: "idle"}

	n, _, id := enterNode(t, g, "opencode", info, bins, nil)
	p := enterOf(t, n, id, "")
	if want := []string{bins["opencode"], "--session", "ses_1"}; !reflect.DeepEqual(p.Argv, want) {
		t.Fatalf("closed herdr pane: %+v, want %v", p, want)
	}
	if p.Dir != cwd || len(p.Env) != 0 || strings.Contains(p.Note, "has closed") {
		t.Fatalf("closed herdr pane: %+v", p)
	}

	// Without the OpenCode program there is nothing left to open the session
	// with, and the note says what was tried.
	n, _, id = enterNode(t, g, "opencode", info, nil, nil)
	p = enterOf(t, n, id, "")
	if len(p.Argv) != 0 || !strings.Contains(p.Note, "has closed") || !strings.Contains(p.Note, "not installed") {
		t.Fatalf("no opencode binary: %+v", p)
	}
}

// A pane with no detected agent is attached as a terminal. A herdr that does
// not answer gives a note, not a tmux guess.
func TestEnterHerdrPlainTerminalAndDownServer(t *testing.T) {
	f := newFakeHerdr(t)
	f.panes(t, `{"pane_id":"w1:pB","terminal_id":"term_b"}`)
	pid := f.session(t, "w1:pB")
	f.holds(t, "w1:pB", pid)
	p, _ := herdrPlanFor(pid, protocol.EnterRequest{}, false)
	if got := strings.Join(p.Argv[len(p.Argv)-3:], " "); got != "terminal attach term_b" {
		t.Fatalf("argv %v", p.Argv)
	}
	if err := os.WriteFile(filepath.Join(f.dir, "fail"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	p, ok := herdrPlanFor(pid, protocol.EnterRequest{}, false)
	if !ok || len(p.Argv) != 0 || !strings.Contains(p.Note, "herdr server does not answer") {
		t.Fatalf("down: %+v ok=%v", p, ok)
	}
	// Not a herdr session at all.
	if _, ok := herdrPlanFor(os.Getpid(), protocol.EnterRequest{}, false); ok {
		t.Error("the test process is not in herdr")
	}
}

// herdr is asked before tmux: a herdr pane is not a tmux pane, and a session
// that is in both (tmux inside a herdr pane) is shown by herdr.
func TestEnterHerdrBeforeTmux(t *testing.T) {
	f := newFakeHerdr(t)
	f.panes(t, herdrAgentPane("w1:pB", "term_b"))
	pid := f.session(t, "w1:pB")
	f.holds(t, "w1:pB", pid)
	argsFile := fakeTmux(t, strconv.Itoa(pid)+"|%7|work")
	d := &Daemon{}
	p, err := d.terminalPlan(protocol.SessionInfo{PID: pid}, protocol.EnterRequest{TmuxSocket: "/tmp/tmux-1/default"}, protocol.EnterPlan{Host: "h"}, "s", false)
	if err != nil || len(p.Argv) < 4 || p.Argv[len(p.Argv)-3] != "agent" {
		t.Fatalf("plan %+v err %v", p, err)
	}
	if b, _ := os.ReadFile(argsFile); len(b) != 0 {
		t.Errorf("tmux was asked: %q", b)
	}
}

// The macOS buffer of sysctl kern.procargs2: argc, path, padding, arguments,
// then the environment. Values keep their spaces; the list ends at an empty
// string, and what follows (a second copy of strings the kernel keeps) is not read.
func TestParseProcArgs2(t *testing.T) {
	buf := []byte{2, 0, 0, 0}
	buf = append(buf, "/usr/local/bin/claude\x00\x00\x00\x00"...)
	buf = append(buf, "claude\x00--resume\x00"...)
	buf = append(buf, "HERDR_ENV=1\x00HERDR_PANE_ID=w1:pB\x00HERDR_SOCKET_PATH=/Users/me/Library/App Support/herdr/herdr.sock\x00PATH=/a:/b\x00EMPTY=\x00\x00"...)
	buf = append(buf, "junk=after\x00"...)
	want := []string{"HERDR_ENV=1", "HERDR_PANE_ID=w1:pB", "HERDR_SOCKET_PATH=/Users/me/Library/App Support/herdr/herdr.sock", "PATH=/a:/b", "EMPTY="}
	if got := parseProcArgs2(buf); !reflect.DeepEqual(got, want) {
		t.Fatalf("env %q, want %q", got, want)
	}
	// An env var with a name is found the same way as on Linux.
	old := processEnviron
	processEnviron = func(int) []string { return parseProcArgs2(buf) }
	defer func() { processEnviron = old }()
	if v := processEnvVar(1, "HERDR_SOCKET_PATH"); v != "/Users/me/Library/App Support/herdr/herdr.sock" {
		t.Errorf("value %q", v)
	}

	// No environment, a cut buffer, and nonsense do not panic.
	for _, b := range [][]byte{nil, {2, 0, 0}, {0, 0, 0, 0}, {5, 0, 0, 0, 'a', 0}, {1, 0, 0, 0, 'a', 0, 0, 'b', 0}, buf[:30], buf[:60]} {
		_ = parseProcArgs2(b)
	}
	if got := parseProcArgs2([]byte{1, 0, 0, 0, 'a', 0, 0, 'b', 0}); len(got) != 0 {
		t.Errorf("argv only: %q", got)
	}
}

// The whole path: session_enter on a node answers a herdr plan for an
// interactive Claude session, and the TUI's herdr socket is read from the request.
func TestEnterHerdrThroughSessionEnter(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	f := newFakeHerdr(t)
	f.panes(t, herdrAgentPane("w1:pB", "term_b"))
	pid := f.session(t, "w1:pB")
	f.holds(t, "w1:pB", pid)
	ea := &enterAdapter{fakeAdapter: &fakeAdapter{harness: "claude"}, kind: "interactive"}
	n := startNode(t, g, nodeOpts{cfg: func(c *Config) { c.Adapters = []Adapter{ea} }})
	ea.setSessions(protocol.SessionInfo{Harness: "claude", NativeID: "i1", PID: pid, Cwd: "/nope", Name: "inter", Status: "idle", Mode: protocol.ModeForeground})
	var id string
	eventually(t, "session id", func() bool {
		if s := n.d.Sessions(); len(s) == 1 {
			id = s[0].SessionID
		}
		return id != ""
	})
	raw, err := cliClient(t, n).call("session_enter", protocol.EnterRequest{SessionID: id, HerdrSocket: f.sock})
	if err != nil {
		t.Fatal(err)
	}
	var p protocol.EnterPlan
	if err := json.Unmarshal(raw, &p); err != nil {
		t.Fatal(err)
	}
	want := []string{"env", "-u", "HERDR_SESSION", "HERDR_SOCKET_PATH=" + f.sock, f.bin, "agent", "attach", "w1:pB"}
	if !reflect.DeepEqual(p.Argv, want) || !strings.Contains(p.Note, "Press ctrl+] to come back here;") {
		t.Fatalf("plan %+v", p)
	}
}
