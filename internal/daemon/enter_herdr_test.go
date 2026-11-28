package daemon

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

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
			if v, ok := strings.CutPrefix(kv, "HERDR_SOCKET_PATH="); ok && testTempFolder(v) {
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
	// The server fallback reads command lines up the process tree. Hide a
	// real herdr server too, unless its binary is under the temporary folder.
	rawArgs := osProcessArgs
	// The test process itself may run under a real agent host (a worker under
	// `opencode serve`, a Codex app server): the owner gate would then refuse
	// every CLI call of the tests. Hide the real ancestors of this process.
	ancestors := map[int]bool{}
	for p, depth := os.Getpid(), 0; p > 1 && depth < 64; depth++ {
		ancestors[p] = true
		parent, err := parentPID(p)
		if err != nil {
			break
		}
		p = parent
	}
	processArgs = func(pid int) []string {
		args := rawArgs(pid)
		if len(args) > 0 && filepath.Base(args[0]) == "herdr" && !testTempFolder(args[0]) {
			return nil
		}
		if ancestors[pid] && sessionHostCommand(args) {
			return nil
		}
		return args
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
[ ! -f ` + dir + `/socket ] || [ "$HERDR_SOCKET_PATH" = "$(cat ` + dir + `/socket)" ] || { echo "wrong socket" >&2; exit 1; }
case "$1 $2" in
  "pane list") cat ` + dir + `/panes ;;
  "pane process-info") cat ` + dir + `/info-$4 2>/dev/null || exit 1 ;;
  "tab create") printf '%s\n' '{"result":{"tab":{"tab_id":"w1:tNew"},"root_pane":{"pane_id":"w1:pNew"}}}' ;;
  "pane run"|"agent focus"|"tab focus"|"pane close") exit 0 ;;
  *) exit 1 ;;
esac
`
	if err := os.WriteFile(f.bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return f
}

func (f *fakeHerdr) answersAt(t *testing.T, socket string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(f.dir, "socket"), []byte(socket), 0o644); err != nil {
		t.Fatal(err)
	}
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

// readOnly fails the test if the daemon sent this herdr, which plays the
// owner's own herdr, anything but a read (decision 0154).
func (f *fakeHerdr) readOnly(t *testing.T) {
	t.Helper()
	for _, line := range strings.Split(strings.TrimSpace(f.calls(t)), "\n") {
		fields := strings.SplitN(line, " ", 3)
		if len(fields) < 3 {
			continue
		}
		command := fields[2]
		if !strings.HasPrefix(command, "pane list") && !strings.HasPrefix(command, "pane process-info") && !strings.HasPrefix(command, "session list") {
			t.Errorf("the owner's herdr got %q", command)
		}
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
	// A blocked pipe keeps the fake session alive without a timed sleep.
	cmd := exec.Command("cat")
	cmd.Env = append(os.Environ(), "HERDR_ENV=1", "HERDR_PANE_ID="+pane, "HERDR_SOCKET_PATH="+f.sock, "HERDR_BIN_PATH="+f.bin, "HERDR_SESSION=other")
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { stdin.Close(); cmd.Process.Kill(); cmd.Wait() })
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

// testHerdrPane is the pane of the asking TUI in these tests: the daemon opens
// the new view in a tab.
const testHerdrPane = "w1:p0"

// fakeHerdrSocket is the absolute socket of the fake herdr whose call log is at
// log. The fake does not read the socket; the tests assert on the calls.
func fakeHerdrSocket(log string) string {
	return filepath.Join(filepath.Dir(log), "h.sock")
}

func inHerdrEnter(q protocol.EnterRequest, socket string) protocol.EnterRequest {
	q.HerdrSocket, q.HerdrPaneID = socket, testHerdrPane
	return q
}

func inHerdrStart(q protocol.SessionStartRequest, socket string) protocol.SessionStartRequest {
	q.HerdrSocket, q.HerdrPaneID = socket, testHerdrPane
	return q
}

func inHerdrResume(q protocol.SessionResumeRequest, socket string) protocol.SessionResumeRequest {
	q.HerdrSocket, q.HerdrPaneID = socket, testHerdrPane
	return q
}

func herdrPlanFor(pid int, q protocol.EnterRequest) (protocol.EnterPlan, bool) {
	d := &Daemon{}
	return d.herdrPlan(protocol.SessionInfo{PID: pid}, q, protocol.EnterPlan{Host: "h"}, "s")
}

// fakeProcessTree supplies a process tree and its command lines without
// depending on the test process's real environment or ancestors.
func fakeProcessTree(t *testing.T, parents map[int]int, args map[int][]string) {
	t.Helper()
	oldEnviron, oldArgs, oldParent := processEnviron, processArgs, processParentPID
	processEnviron = func(int) []string { return nil }
	processArgs = func(pid int) []string { return args[pid] }
	processParentPID = func(pid int) (int, error) {
		if parent := parents[pid]; parent != 0 {
			return parent, nil
		}
		return 1, nil
	}
	t.Cleanup(func() {
		processEnviron, processArgs, processParentPID = oldEnviron, oldArgs, oldParent
	})
}

func TestEnterHerdrFallbackFromServerWithoutEnvironment(t *testing.T) {
	f := newFakeHerdr(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", "")
	socket := filepath.Join(home, ".config", "herdr", "herdr.sock")
	f.answersAt(t, socket)
	f.panes(t, herdrAgentPane("w1:p1", "term_1"))
	f.holds(t, "w1:p1", 31870)

	fakeProcessTree(t,
		map[int]int{31870: 25104, 25104: 1024, 1024: 1},
		map[int][]string{31870: {"pi"}, 25104: {"-zsh"}, 1024: {f.bin, "server"}},
	)
	p, ok := herdrPlanFor(31870, protocol.EnterRequest{})
	want := []string{"env", "-u", "HERDR_SESSION", "HERDR_SOCKET_PATH=" + socket, f.bin, "agent", "attach", "w1:p1", "--takeover"}
	if !ok || !reflect.DeepEqual(p.Argv, want) {
		t.Fatalf("fallback plan %+v ok=%v, want argv %v", p, ok, want)
	}
	if calls := f.calls(t); !strings.Contains(calls, "sock="+socket+" session=none pane list") {
		t.Fatalf("fake herdr calls %q", calls)
	}
}

func TestEnterHerdrFallbackUsesServerSocketArgument(t *testing.T) {
	f := newFakeHerdr(t)
	socket := filepath.Join(f.dir, "server.sock")
	f.answersAt(t, socket)
	f.panes(t, herdrAgentPane("w1:p1", "term_1"))
	f.holds(t, "w1:p1", 31870)
	fakeProcessTree(t,
		map[int]int{31870: 25104, 25104: 1024, 1024: 1},
		map[int][]string{31870: {"pi"}, 25104: {"-zsh"}, 1024: {f.bin, "server", "--socket", socket}},
	)
	p, ok := herdrPlanFor(31870, protocol.EnterRequest{})
	if !ok || !strings.Contains(strings.Join(p.Argv, " "), "HERDR_SOCKET_PATH="+socket) {
		t.Fatalf("server socket argument was not used: %+v ok=%v", p, ok)
	}
}

func TestEnterNoHerdrOrTmuxFailureExplainsChecks(t *testing.T) {
	fakeProcessTree(t,
		map[int]int{31870: 25104, 25104: 1},
		map[int][]string{31870: {"pi"}, 25104: {"-zsh"}},
	)
	d := &Daemon{}
	p, err := d.terminalPlan(protocol.SessionInfo{PID: 31870}, protocol.EnterRequest{}, protocol.EnterPlan{Host: "h"}, "pi")
	if err != nil || len(p.Argv) != 0 || !strings.Contains(p.Note, "no herdr variables and no herdr server above the process") {
		t.Fatalf("missing diagnostic: %+v err=%v", p, err)
	}
}

// From a TUI outside herdr, and from one inside any herdr: the same attach, with
// the socket in argv, and the detach key that works where the TUI runs.
func TestEnterHerdrAttach(t *testing.T) {
	f := newFakeHerdr(t)
	f.panes(t, herdrAgentPane("w9:p9", "term_9")+","+herdrAgentPane("w1:pB", "term_b"))
	f.holds(t, "w1:pB", 0)
	pid := f.session(t, "w1:pB")
	f.holds(t, "w1:pB", pid)

	p, ok := herdrPlanFor(pid, protocol.EnterRequest{})
	want := []string{"env", "-u", "HERDR_SESSION", "HERDR_SOCKET_PATH=" + f.sock, f.bin, "agent", "attach", "w1:pB", "--takeover"}
	if !ok || !reflect.DeepEqual(p.Argv, want) {
		t.Fatalf("ok=%v argv %v, want %v", ok, p.Argv, want)
	}
	if !strings.Contains(p.Note, "Press ctrl+] to come back here;") {
		t.Errorf("note outside herdr: %q", p.Note)
	}
	// A stale attach must not block Enter: the newest one takes the terminal
	// over, so the old client is detached (decision 0167).
	if !strings.Contains(strings.Join(p.Argv, " "), "--takeover") {
		t.Errorf("no takeover in %v", p.Argv)
	}
	// The herdr commands ran against the session's server, not the inherited session.
	if c := f.calls(t); !strings.Contains(c, "sock="+f.sock+" session=none pane list") {
		t.Errorf("calls: %q", c)
	}

	// The TUI is itself in herdr (the same server or another one): the first
	// ctrl+b goes to the outer herdr. There is still one plan: focus would not
	// move the TUI's client.
	p, _ = herdrPlanFor(pid, protocol.EnterRequest{HerdrSocket: f.sock})
	if !reflect.DeepEqual(p.Argv, want) || !strings.Contains(p.Note, "Press ctrl+] to come back here;") {
		t.Errorf("inside herdr: %v %q", p.Argv, p.Note)
	}
	// Asked by another machine: its terminal is not known.
	p, _ = herdrPlanFor(pid, protocol.EnterRequest{})
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
	p, ok := herdrPlanFor(pid, protocol.EnterRequest{})
	if !ok || len(p.Argv) == 0 || len(p.Env) != 0 {
		t.Fatalf("plan %+v ok=%v", p, ok)
	}
	got := remoteCommand("/work", p.Argv)
	want := "cd /work && exec env -u HERDR_SESSION 'HERDR_SOCKET_PATH=" + f.sock + "' " + shellQuote(f.bin) + " agent attach w1:pB --takeover"
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
	p, ok := herdrPlanFor(pid, protocol.EnterRequest{})
	if !ok || len(p.Argv) == 0 || p.Argv[len(p.Argv)-2] != "w3:p2" || p.Argv[len(p.Argv)-3] != "attach" {
		t.Fatalf("moved pane: %+v ok=%v", p, ok)
	}

	// The old ID exists but belongs to another process now.
	f.panes(t, herdrAgentPane("w1:pB", "term_b")+","+herdrAgentPane("w3:p2", "term_3"))
	f.holds(t, "w1:pB", 999999)
	if p, _ = herdrPlanFor(pid, protocol.EnterRequest{}); len(p.Argv) == 0 || p.Argv[len(p.Argv)-2] != "w3:p2" {
		t.Fatalf("reused id: %+v", p)
	}

	// Nothing holds it.
	f.holds(t, "w3:p2", 999999)
	p, ok = herdrPlanFor(pid, protocol.EnterRequest{})
	if !ok || len(p.Argv) != 0 || !strings.Contains(p.Note, "has closed") || !strings.Contains(p.Note, "open its window on h") {
		t.Fatalf("closed pane: %+v ok=%v", p, ok)
	}
}

// An OpenCode session is not lost when its herdr pane closes: the session is
// still in the OpenCode service, so enter opens it there with --server. When
// the service is not running there is no plan: a bare `opencode --session`
// would host the session in a terminal of its own, outside the daemon.
func TestEnterOpenCodeClosedHerdrPaneUsesTheService(t *testing.T) {
	state := t.TempDir()
	t.Setenv("XDG_STATE_HOME", state)
	g := startGateway(t, t.TempDir(), "")
	bins := fakeBin(t, "opencode")
	cwd := t.TempDir()
	f := newFakeHerdr(t)
	f.panes(t, "") // the recorded pane w3:p2X has closed, and no pane holds it
	pid := f.session(t, "w3:p2X")
	info := protocol.SessionInfo{NativeID: "ses_1", PID: pid, Cwd: cwd, Name: "oc", Status: "idle"}

	n, _, id := enterNode(t, g, "opencode", info, bins, nil)
	p := enterOf(t, n, id, "")
	if len(p.Argv) != 0 || !strings.Contains(p.Note, "OpenCode service is not running") {
		t.Fatalf("no service: %+v", p)
	}

	writeOwnerOpenCodeService(t, state, "http://127.0.0.1:9999")
	p = enterOf(t, n, id, "")
	if want := []string{bins["opencode"], "--server", "http://127.0.0.1:9999", "--session", "ses_1"}; !reflect.DeepEqual(p.Argv, want) {
		t.Fatalf("closed herdr pane: %+v, want %v", p, want)
	}
	if p.Dir != cwd || strings.Contains(p.Note, "has closed") {
		t.Fatalf("closed herdr pane: %+v", p)
	}

	// Without the OpenCode program there is nothing left to open the session
	// with, and the note says what was tried.
	n, _, id = enterNode(t, g, "opencode", info, nil, nil)
	p = enterOf(t, n, id, "")
	if len(p.Argv) != 0 || !strings.Contains(p.Note, "not installed") {
		t.Fatalf("no opencode binary: %+v", p)
	}
}

// A pane with no detected agent is attached as a terminal. A herdr that does
// not answer gives a note.
func TestEnterHerdrPlainTerminalAndDownServer(t *testing.T) {
	f := newFakeHerdr(t)
	f.panes(t, `{"pane_id":"w1:pB","terminal_id":"term_b"}`)
	pid := f.session(t, "w1:pB")
	f.holds(t, "w1:pB", pid)
	p, _ := herdrPlanFor(pid, protocol.EnterRequest{})
	if got := strings.Join(p.Argv[len(p.Argv)-4:], " "); got != "terminal attach term_b --takeover" {
		t.Fatalf("argv %v", p.Argv)
	}
	if err := os.WriteFile(filepath.Join(f.dir, "fail"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	p, ok := herdrPlanFor(pid, protocol.EnterRequest{})
	if !ok || len(p.Argv) != 0 || !strings.Contains(p.Note, "herdr server does not answer") {
		t.Fatalf("down: %+v ok=%v", p, ok)
	}
	// Not a herdr session at all.
	if _, ok := herdrPlanFor(os.Getpid(), protocol.EnterRequest{}); ok {
		t.Error("the test process is not in herdr")
	}
}

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
	if got := parseProcArgs2Argv(buf); !reflect.DeepEqual(got, []string{"claude", "--resume"}) {
		t.Fatalf("argv %q", got)
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

// withoutLocalHerdr makes herdrBinary answer "" on this machine: the test's
// PATH has no herdr and its home has no ~/.local/bin/herdr.
func withoutLocalHerdr(t *testing.T) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	var keep []string
	for _, dir := range filepath.SplitList(os.Getenv("PATH")) {
		if isExecutable(filepath.Join(dir, "herdr")) {
			continue
		}
		keep = append(keep, dir)
	}
	t.Setenv("PATH", strings.Join(keep, string(os.PathListSeparator)))
	if bin := (&Daemon{}).herdrBinary(""); bin != "" {
		t.Fatalf("a herdr is still found: %s", bin)
	}
}

// enterRemoteHerdr starts a remote node whose session runs in a fake herdr pane
// and returns it with the session's gateway ID. The fake herdr is the one on
// the remote machine.
func enterRemoteHerdr(t *testing.T, g *testGateway, pane string) (*node, *fakeHerdr, string) {
	t.Helper()
	f := newFakeHerdr(t)
	f.panes(t, herdrAgentPane(pane, "term_b"))
	pid := f.session(t, pane)
	f.holds(t, pane, pid)
	ea := &enterAdapter{fakeAdapter: &fakeAdapter{harness: "pi"}}
	n := startNode(t, g, nodeOpts{cfg: func(c *Config) { c.Adapters = []Adapter{ea} }})
	ea.setSessions(protocol.SessionInfo{Harness: "pi", NativeID: "i1", PID: pid, Cwd: "/nope", Name: "inter", Status: "idle", Mode: protocol.ModeForeground})
	var id string
	eventually(t, "remote session id", func() bool {
		if s := n.d.Sessions(); len(s) == 1 {
			id = s[0].SessionID
		}
		return id != ""
	})
	return n, f, id
}

// waitSessionOnGateway waits until the local node sees a session another node
// reported.
func waitSessionOnGateway(t *testing.T, n *node, id string) {
	t.Helper()
	eventually(t, "session on the gateway", func() bool {
		a, err := n.d.agentView(context.Background(), id)
		return err == nil && a.SessionID == id
	})
}

// A session in herdr on another machine: when this machine has herdr, the
// owner enters it with herdr's own remote client, which carries the clipboard
// and image paste. The TUI runs that client in its own pane.
func TestEnterRemoteHerdrUsesLocalClient(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	remote, remoteHerdr, id := enterRemoteHerdr(t, g, "w1:pB")
	localHerdr, localLog := fakeOpenHerdr(t)
	local := startNode(t, g, nodeOpts{cfg: func(c *Config) {
		c.HarnessPaths = map[string]string{"herdr": localHerdr}
	}})
	waitSessionOnGateway(t, local, id)

	p := enterOfHerdr(t, local, id, fakeHerdrSocket(localLog))
	if want := []string{localHerdr, "--remote", remote.cfg.Hostname}; p.OpenedHerdr || !reflect.DeepEqual(p.Argv, want) || p.Dir != "" || p.Host != remote.cfg.Hostname || !strings.Contains(p.Note, "w1:pB") {
		t.Fatalf("plan %+v, want argv %v", p, want)
	}
	requireHerdrRemoteEnv(t, p)
	// The pane is in the owner's own herdr there, which is read-only: it is
	// found, named in the note, and not focused.
	if calls := remoteHerdr.calls(t); !strings.Contains(calls, "pane process-info --pane w1:pB") {
		t.Errorf("remote herdr calls %q", calls)
	}
	remoteHerdr.readOnly(t)
	requireHerdrRules(t, localLog)
	// The ssh target from enter_ssh_hosts is the one herdr's client gets.
	alias := startNode(t, g, nodeOpts{cfg: func(c *Config) {
		c.HarnessPaths = map[string]string{"herdr": localHerdr}
		c.EnterSSHHosts = map[string]string{remote.cfg.Hostname: "me@box"}
	}})
	waitSessionOnGateway(t, alias, id)
	p = enterOfHerdr(t, alias, id, fakeHerdrSocket(localLog))
	if want := []string{localHerdr, "--remote", "me@box"}; !reflect.DeepEqual(p.Argv, want) {
		t.Fatalf("alias plan %+v, want argv %v", p, want)
	}
}

// The CTO on another machine opens the same way a session does: the CTO's
// machine opens or focuses a pane in agent-net's herdr there, and this
// machine runs herdr's remote client in the TUI's own terminal or pane. Plain ssh loses the clipboard, so image paste into
// the CTO fails (decision 0046).
func TestEnterRemoteCTOUsesHerdrRemoteClient(t *testing.T) {
	remoteBin, remoteLog := fakeOpenHerdr(t)
	localBin, localLog := fakeOpenHerdr(t)
	t.Setenv("AGENT_NET_CONFIG_DIR", t.TempDir())
	sessions, _ := useFakeClaude(t)
	g := startGateway(t, t.TempDir(), "")
	remote := startNode(t, g, nodeOpts{detect: fakeInventory, interval: time.Hour, cfg: func(c *Config) {
		c.ClaudeSessionsDir = sessions
		c.HarnessPaths = map[string]string{"claude": os.Args[0], "herdr": remoteBin}
	}})
	cli := cliClient(t, remote)
	harness := "claude"
	cli.mustCall("cto_settings", protocol.CTOSettingsRequest{Harness: &harness})
	cli.mustCall("cto_enter", nil) // register the CTO on this machine
	ctoID := remote.d.ctoSessionID()
	if ctoID == "" {
		t.Fatal("the CTO is not registered")
	}
	local := startNode(t, g, nodeOpts{cfg: func(c *Config) {
		c.HarnessPaths = map[string]string{"herdr": localBin}
	}})
	waitSessionOnGateway(t, local, ctoID)

	// A TUI in a plain terminal runs the remote client itself.
	p := enterOf(t, local, ctoID, "")
	if want := []string{localBin, "--remote", remote.cfg.Hostname, "--session", "agent-net"}; !reflect.DeepEqual(p.Argv, want) {
		t.Fatalf("argv %v, want %v (plan %+v)", p.Argv, want, p)
	}
	if p.OpenedHerdr {
		t.Fatalf("a TUI outside herdr got a pane it cannot see: %+v", p)
	}
	// The CTO's machine made the pane in agent-net's herdr, titled CTO, and
	// ran the attach there.
	raw, _ := os.ReadFile(remoteLog)
	calls := string(raw)
	requireHerdrRules(t, remoteLog)
	if !hasOwnHerdrCall(t, remoteLog, "workspace create --label CTO") || !hasOwnHerdrCall(t, remoteLog, "pane run") || !strings.Contains(calls, "attach") {
		t.Fatalf("the CTO's machine did not open a pane: %s", calls)
	}
	if strings.Contains(strings.Join(p.Argv, " "), "ssh") || strings.Contains(calls, "ssh") {
		t.Fatalf("ssh on the CTO path: argv %v calls %s", p.Argv, calls)
	}

	// A TUI inside the owner's herdr runs the same client in its own pane.
	q := enterOfHerdr(t, local, ctoID, fakeHerdrSocket(localLog))
	if q.OpenedHerdr || !reflect.DeepEqual(q.Argv, p.Argv) {
		t.Fatalf("plan in herdr %+v, want argv %v", q, p.Argv)
	}
	requireHerdrRules(t, localLog)
}

// A TUI outside herdr (a plain terminal such as Ghostty) still enters a remote
// herdr session with herdr's remote client, run in the TUI's own terminal: it
// is the only path that carries the clipboard, so image paste works. Plain ssh
// was used from v0.4.72 to v0.4.90, and pasting an image failed.
func TestEnterRemoteHerdrFromPlainTerminalUsesLocalClient(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	remote, remoteHerdr, id := enterRemoteHerdr(t, g, "w1:pB")
	localHerdr, _ := fakeOpenHerdr(t)
	local := startNode(t, g, nodeOpts{cfg: func(c *Config) {
		c.HarnessPaths = map[string]string{"herdr": localHerdr}
	}})
	waitSessionOnGateway(t, local, id)

	p := enterOf(t, local, id, "")
	if want := []string{localHerdr, "--remote", remote.cfg.Hostname}; !reflect.DeepEqual(p.Argv, want) {
		t.Fatalf("argv %v, want %v (plan %+v)", p.Argv, want, p)
	}
	if p.OpenedHerdr {
		t.Fatalf("a TUI outside herdr got a pane it cannot see: %+v", p)
	}
	remoteHerdr.readOnly(t)
}

// Without a local herdr there is no client to carry the clipboard, so the plan
// stays the ssh attach it is today, and the remote pane is not focused.
func TestEnterRemoteHerdrWithoutLocalClientKeepsSSH(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	remote, remoteHerdr, id := enterRemoteHerdr(t, g, "w1:pB")
	local := startNode(t, g, nodeOpts{})
	withoutLocalHerdr(t) // before the first plan, which would otherwise focus the pane
	waitSessionOnGateway(t, local, id)

	// What the remote machine plans on its own is the command ssh runs.
	localEnter, err := remote.d.localEnterBase(context.Background(), protocol.EnterRequest{SessionID: id}, true, nil)
	if err != nil || len(localEnter.Argv) == 0 {
		t.Fatalf("remote plan %+v err %v", localEnter, err)
	}
	want := []string{"ssh", "-t", "-o", "ConnectTimeout=5", "--", remote.cfg.Hostname, remoteCommand(localEnter.Dir, localEnter.Argv)}
	p := enterOf(t, local, id, "")
	if !reflect.DeepEqual(p.Argv, want) {
		t.Fatalf("argv %v, want %v", p.Argv, want)
	}
	if p.HerdrPane != "" || strings.Contains(p.Note, "Opens herdr on") {
		t.Fatalf("plan names a local herdr client: %+v", p)
	}
	if calls := remoteHerdr.calls(t); strings.Contains(calls, "agent focus") {
		t.Errorf("the remote pane was focused without a local client: %q", calls)
	}
}

// A session in herdr on this machine still gets the direct attach: the local
// herdr client is for a session on another machine.
func TestEnterLocalHerdrStaysAttach(t *testing.T) {
	f := newFakeHerdr(t)
	f.panes(t, herdrAgentPane("w1:pB", "term_b"))
	pid := f.session(t, "w1:pB")
	f.holds(t, "w1:pB", pid)
	p, ok := herdrPlanFor(pid, protocol.EnterRequest{})
	if !ok || p.HerdrPane != "" {
		t.Fatalf("plan %+v ok=%v", p, ok)
	}
	if got := strings.Join(p.Argv[len(p.Argv)-4:], " "); got != "agent attach w1:pB --takeover" {
		t.Fatalf("argv %v", p.Argv)
	}
	if calls := f.calls(t); strings.Contains(calls, "agent focus") {
		t.Errorf("a local attach focused the pane: %q", calls)
	}
}

// A hand-started Claude session has no background job, so session_enter from
// a TUI inside herdr opens nothing: it offers the move to the background and
// leaves herdr alone.
func TestEnterHerdrHandStartedClaudeOpensNothing(t *testing.T) {
	_, log := fakeOpenHerdr(t)
	g := startGateway(t, t.TempDir(), "")
	bins := fakeBin(t, "claude")
	ea := &enterAdapter{fakeAdapter: &fakeAdapter{harness: "claude"}, kind: "interactive"}
	n := startNode(t, g, nodeOpts{cfg: func(c *Config) { c.HarnessPaths = bins; c.Adapters = []Adapter{ea} }})
	ea.setSessions(protocol.SessionInfo{Harness: "claude", NativeID: "i1", PID: 1, Name: "inter"})
	var id string
	eventually(t, "session id", func() bool {
		if sessions := n.d.Sessions(); len(sessions) == 1 {
			id = sessions[0].SessionID
		}
		return id != ""
	})
	p := enterOfHerdr(t, n, id, fakeHerdrSocket(log))
	if p.OpenedHerdr || !p.MoveToBackground {
		t.Fatalf("plan %+v", p)
	}
	if calls, _ := os.ReadFile(log); strings.Contains(string(calls), "tab create") {
		t.Fatalf("herdr opened a tab: %s", calls)
	}
}

// A Pi session in the owner's own herdr is stopped by signalling its process:
// closing its pane would write to a herdr agent-net does not own.
func TestSessionStopPiInOwnerHerdrSignalsProcess(t *testing.T) {
	f := newFakeHerdr(t)
	pid := f.session(t, "w3:p2")
	f.panes(t, herdrAgentPane("w3:p2", "w3:t1"))
	f.holds(t, "w3:p2", pid)
	g := startGateway(t, t.TempDir(), "")
	n, _, id := enterNode(t, g, "pi", protocol.SessionInfo{NativeID: "pi_owner", Name: "shop", PID: pid, Status: "idle"}, nil, nil)
	cliClient(t, n).mustCall("session_stop", map[string]string{"session_id": id})
	eventually(t, "the Pi process ends", func() bool { _, live := procToken(pid); return !live })
	f.readOnly(t)
}

// A harness the owner runs in his own herdr is discovered, listed and
// entered, with reads only: the TUI attaches to its pane.
func TestOwnerHerdrSessionIsListedAndEntered(t *testing.T) {
	f := newFakeHerdr(t)
	f.panes(t, herdrAgentPane("w2:p4", "term_4"))
	pid := f.session(t, "w2:p4")
	f.holds(t, "w2:p4", pid)
	g := startGateway(t, t.TempDir(), "")
	n, _, id := enterNode(t, g, "pi", protocol.SessionInfo{NativeID: "pi_mine", Name: "shop", PID: pid, Status: "idle"}, map[string]string{"herdr": f.bin}, nil)
	if a, err := n.d.agentView(context.Background(), id); err != nil || a.Harness != "pi" {
		t.Fatalf("not listed: %+v %v", a, err)
	}
	p := enterOfHerdr(t, n, id, f.sock)
	if want := []string{"env", "-u", "HERDR_SESSION", "HERDR_SOCKET_PATH=" + f.sock, f.bin, "agent", "attach", "w2:p4", "--takeover"}; p.OpenedHerdr || !reflect.DeepEqual(p.Argv, want) {
		t.Fatalf("plan %+v, want argv %v", p, want)
	}
	f.readOnly(t)
}

// A sign-in ask runs the allowlisted login command through the one view path:
// the TUI hands over its terminal, or its pane in the owner's herdr. No ask
// or agent supplies the argv.
func TestSignInOpenLocalPaneRunsAllowlistedArgv(t *testing.T) {
	_, log := fakeOpenHerdr(t)
	call := func(_ context.Context, typ string, _ any) (json.RawMessage, bool, error) {
		if typ != "query.sign_in" {
			return nil, false, nil
		}
		b, _ := json.Marshal(protocol.SignInTarget{AskID: "i_signin", Provider: "alibaba", Label: "Alibaba Bailian CLI", NodeID: "n_research", Hostname: "research"})
		return b, true, nil
	}
	d := &Daemon{cfg: Config{NodeID: "n_research", Hostname: "research", testGatewayCall: func(ctx context.Context, typ string, p any) (json.RawMessage, bool, error) {
		return call(ctx, typ, p)
	}}}
	// A TUI outside herdr hands its terminal the same allowlisted command.
	plan, err := d.signInOpen(context.Background(), protocol.SignInOpenRequest{AskID: "i_signin"})
	if err != nil {
		t.Fatal(err)
	}
	if plan.OpenedHerdr || !reflect.DeepEqual(plan.Argv, []string{"bl", "auth", "login", "--console", "--no-browser", "--console-site", "international"}) {
		t.Fatalf("plan %+v", plan)
	}
	if calls, _ := os.ReadFile(log); len(calls) != 0 {
		t.Fatalf("outside herdr should hand over the terminal: %s", calls)
	}

	// A TUI inside the owner's herdr gets the same handover.
	plan, err = d.signInOpen(context.Background(), protocol.SignInOpenRequest{AskID: "i_signin", HerdrSocket: "/tmp/fake-herdr.sock", HerdrPaneID: testHerdrPane})
	if err != nil || plan.OpenedHerdr || !reflect.DeepEqual(plan.Argv, []string{"bl", "auth", "login", "--console", "--no-browser", "--console-site", "international"}) {
		t.Fatalf("in herdr: plan %+v, error %v", plan, err)
	}
	requireNoHerdrCalls(t, log)
}

// The browser and listener stay on the owner's machine for a remote ask.
func TestSignInOpenRemoteRunsOnBrowserMachine(t *testing.T) {
	_, log := fakeOpenHerdr(t)
	d := &Daemon{cfg: Config{NodeID: "local", Hostname: "browser", testGatewayCall: func(_ context.Context, typ string, _ any) (json.RawMessage, bool, error) {
		if typ == "query.sign_in" {
			b, _ := json.Marshal(protocol.SignInTarget{AskID: "i_signin", Provider: "alibaba", NodeID: "remote", Hostname: "remote-host"})
			return b, true, nil
		}
		return nil, false, nil
	}}}
	plan, err := d.signInOpen(context.Background(), protocol.SignInOpenRequest{AskID: "i_signin"})
	if err != nil || plan.OpenedHerdr || !reflect.DeepEqual(plan.Argv, []string{"bl", "auth", "login", "--console", "--no-browser", "--console-site", "international"}) {
		t.Fatalf("remote sign-in plan %+v, error %v", plan, err)
	}
	if plan.Env["BAILIAN_CONFIG_DIR"] == "" {
		t.Fatal("remote sign-in needs an isolated credential directory")
	}
	requireNoHerdrCalls(t, log)
}

// The target daemon opens the pane from a provider key only. A command, or an
// unknown provider, is refused at that boundary.
func TestSignInPrepareOpensAllowlistedArgv(t *testing.T) {
	_, log := fakeOpenHerdr(t)
	d := watchedDaemon(t, Config{NodeID: "n_mac", Hostname: "Nicolas-MBP"})
	raw, _ := json.Marshal(protocol.SignInPrepare{Provider: "alibaba"})
	res, err := d.signInPrepare(raw)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Opened || res.Host != "Nicolas-MBP" || res.Pane != "w1:p1" {
		t.Fatalf("prepare result %+v", res)
	}
	requireHerdrOpened(t, log, "sign in: Alibaba Bailian CLI - Firefox, not Safari", "bl auth login --console --no-browser --console-site international")
	if calls := string(mustRead(t, log)); strings.Contains(calls, "ssh") {
		t.Fatalf("herdr calls %q", calls)
	}
	bad, _ := json.Marshal(protocol.SignInPrepare{Provider: "sh -c curl"})
	if _, err := d.signInPrepare(bad); err == nil {
		t.Fatal("an unknown provider was accepted")
	}
}

// A sign-in watch belongs to the daemon's lifetime: once that ends, the watch
// returns without asking herdr again.
func TestSignInWatchStopsWithDaemon(t *testing.T) {
	bin, log := fakeOpenHerdr(t)
	// The fake keeps reporting the pane, so the watch would run its full 30
	// minutes unless the daemon's lifetime ends it.
	if err := os.WriteFile(filepath.Join(filepath.Dir(log), "created"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	d := &Daemon{cfg: Config{NodeID: "n_mac", Hostname: "mac"}, ctoCtx: ctx}
	done := make(chan struct{})
	go func() {
		d.watchSignInPane(ctx, "alibaba", bin, "", "w1:p1")
		close(done)
	}()
	eventually(t, "the watch to check its pane", func() bool { return len(herdrCalls(t, log)) > 0 })
	cancel()
	eventually(t, "the watch to stop with the daemon", func() bool {
		select {
		case <-done:
			return true
		default:
			return false
		}
	})
}

func TestEnterRemoteHerdrClientNeverFallsBackToSSH(t *testing.T) {
	bin, _ := fakeOpenHerdr(t)
	d := &Daemon{cfg: Config{EnterSSH: true, HarnessPaths: map[string]string{"herdr": bin}}}
	p := d.wrapRemoteEnter("remote-host", protocol.EnterPlan{Host: "remote-host", Argv: []string{"claude", "attach", "job"}})
	if len(p.Argv) > 0 && p.Argv[0] == "ssh" {
		t.Fatalf("local herdr must prevent ssh fallback: %+v", p)
	}
}

// A remote open runs herdr's client in a session folder of its own. herdr keeps
// a client's log and sockets under <XDG_CONFIG_HOME>/herdr/sessions/<name>, so
// a client for the remote "agent-net" session would otherwise share the local
// agent-net server's folder and could remove its sockets.
func TestRemoteEnterKeepsHerdrClientOutOfOwnerSessions(t *testing.T) {
	bin, _ := fakeOpenHerdr(t)
	state, owner := t.TempDir(), t.TempDir()
	t.Setenv("AGENT_NET_STATE_DIR", state)
	t.Setenv("XDG_CONFIG_HOME", owner)
	t.Setenv("HERDR_CONFIG_PATH", "")
	config := filepath.Join(owner, "herdr", "config.toml")
	if err := os.MkdirAll(filepath.Dir(config), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(config, []byte("[ui]\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	d := &Daemon{cfg: Config{EnterSSH: true, HarnessPaths: map[string]string{"herdr": bin}}}
	p := d.wrapRemoteEnter("research", protocol.EnterPlan{Argv: []string{"true"}, HerdrPane: "w1:p1", HerdrSession: agentNetHerdrSession, Note: "remote"})
	wantArgv := []string{bin, "--remote", "research", "--session", agentNetHerdrSession}
	if !reflect.DeepEqual(p.Argv, wantArgv) {
		t.Fatalf("plan %+v, want argv %v", p, wantArgv)
	}
	if want := filepath.Join(state, "herdr-remote"); p.Env["XDG_CONFIG_HOME"] != want {
		t.Fatalf("client config folder %q, want %q", p.Env["XDG_CONFIG_HOME"], want)
	}
	if p.Env["HERDR_CONFIG_PATH"] != config {
		t.Fatalf("client config %q, want the owner's %q", p.Env["HERDR_CONFIG_PATH"], config)
	}
	if strings.HasPrefix(p.Env["XDG_CONFIG_HOME"], filepath.Join(owner, "herdr")) {
		t.Fatalf("remote client writes into the owner's herdr folder: %q", p.Env["XDG_CONFIG_HOME"])
	}
}

// A herdr server that is alive after its socket file disappeared cannot be
// reached again. When it is agent-net's own, agent-net starts a new server and
// says so, with herdr's own error. The owner's server is never replaced.
func TestEnterHerdrLostServerSocket(t *testing.T) {
	for _, tc := range []struct {
		name     string
		owner    bool
		restarts bool
	}{
		{"agent-net's server", false, true},
		{"the owner's server", true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			t.Setenv("XDG_CONFIG_HOME", "")
			t.Setenv("AGENT_NET_STATE_DIR", t.TempDir())
			dir := t.TempDir()
			bin, log := filepath.Join(dir, "herdr"), filepath.Join(dir, "log")
			running, defaults := filepath.Join(dir, "running"), filepath.Join(dir, "default-config")
			if err := os.WriteFile(defaults, []byte("[ui]\n# sidebar_collapsed_mode = \"hidden\"\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			ownSocket := filepath.Join(home, ".config", "herdr", "sessions", agentNetHerdrSession, "herdr.sock")
			socket := ownSocket
			sessionArgs := []string{bin, "--session", agentNetHerdrSession, "server"}
			if tc.owner {
				socket = filepath.Join(home, ".config", "herdr", "herdr.sock")
				sessionArgs = []string{bin, "server"}
			}
			script := `#!/bin/sh
printf '%s\n' "$*" >> ` + shellQuote(log) + `
case "$1 $2" in
  "--default-config ") cat ` + shellQuote(defaults) + ` ;;
  "session list") if [ -f ` + shellQuote(running) + ` ]; then r=true; else r=false; fi
    printf '{"sessions":[{"name":"agent-net","running":%s,"socket_path":"%s"}]}\n' "$r" ` + shellQuote(ownSocket) + ` ;;
  "--session agent-net") [ "$3" = server ] && : > ` + shellQuote(running) + ` ;;
  "pane list") if [ -f ` + shellQuote(running) + ` ]; then printf '%s\n' '{"result":{"panes":[]}}'; else echo "server down" >&2; exit 1; fi ;;
  *) exit 1 ;;
esac
`
			if err := os.WriteFile(bin, []byte(script), 0o700); err != nil {
				t.Fatal(err)
			}
			const pid, serverPID = 31870, 1024
			fakeProcessTree(t,
				map[int]int{pid: 25104, 25104: serverPID, serverPID: 1},
				map[int][]string{pid: {"pi"}, 25104: {"-zsh"}, serverPID: sessionArgs},
			)
			processEnviron = func(p int) []string {
				if p != pid {
					return nil
				}
				return []string{"HERDR_ENV=1", "HERDR_PANE_ID=w1:p1", "HERDR_SOCKET_PATH=" + socket, "HERDR_BIN_PATH=" + bin}
			}
			plan, ok := herdrPlanFor(pid, protocol.EnterRequest{})
			if !ok || len(plan.Argv) != 0 {
				t.Fatalf("plan %+v ok=%v", plan, ok)
			}
			if !strings.Contains(plan.Note, "server down") {
				t.Fatalf("note %q does not carry herdr's error", plan.Note)
			}
			if tc.restarts && !strings.Contains(plan.Note, "lost its socket") {
				t.Fatalf("note %q does not say the server lost its socket", plan.Note)
			}
			calls := string(mustRead(t, log))
			started := strings.Contains(calls, "--session agent-net server")
			if started != tc.restarts {
				t.Fatalf("restarted=%v, want %v: %s", started, tc.restarts, calls)
			}
			if tc.restarts && !strings.Contains(plan.Note, "a new one started") {
				t.Fatalf("note %q does not say a new server started", plan.Note)
			}
			if !tc.restarts && !strings.Contains(plan.Note, "does not answer") {
				t.Fatalf("note %q does not say the server does not answer", plan.Note)
			}
		})
	}
}

// herdrPaneEnv is the environment of a process that runs in pane of f's herdr.
func herdrPaneEnv(f *fakeHerdr, pane string) []string {
	return []string{"HERDR_ENV=1", "HERDR_PANE_ID=" + pane, "HERDR_SOCKET_PATH=" + f.sock, "HERDR_BIN_PATH=" + f.bin}
}

// A running agy that lives in a herdr pane is shown by attaching to that pane.
// Enter starts no second agy on its conversation, because ctrl+] would end it.
func TestEnterAgyRunningInHerdrPaneAttachesWithoutSecondAgy(t *testing.T) {
	f := newFakeHerdr(t)
	f.panes(t, `{"pane_id":"w1:pB","terminal_id":"term_b"}`)
	cmd, _ := startFakeAgy(t, t.TempDir(), herdrPaneEnv(f, "w1:pB"))
	pid := cmd.Process.Pid
	f.holds(t, "w1:pB", pid)
	g := startGateway(t, t.TempDir(), "")
	bins := fakeBin(t, "agy")
	bins["herdr"] = f.bin
	n, _, id := enterNode(t, g, "agy", protocol.SessionInfo{NativeID: "conversation-live", PID: pid, Cwd: t.TempDir(), Name: "lead"}, bins, nil)
	p := enterOf(t, n, id, "")
	want := []string{"env", "-u", "HERDR_SESSION", "HERDR_SOCKET_PATH=" + f.sock, f.bin, "terminal", "attach", "term_b", "--takeover"}
	if !reflect.DeepEqual(p.Argv, want) {
		t.Fatalf("argv %v, want %v (note %q)", p.Argv, want, p.Note)
	}
	if !strings.Contains(p.Note, "Press ctrl+] to come back here;") {
		t.Errorf("note: %q", p.Note)
	}
}

// From another machine, a running agy in a herdr pane opens with herdr's
// remote client on that machine, and its pane is named (decision 0046).
func TestEnterRemoteAgyRunningInHerdrPaneUsesLocalClient(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	remoteHerdr := newFakeHerdr(t)
	remoteHerdr.panes(t, `{"pane_id":"w1:pB","terminal_id":"term_b"}`)
	cmd, _ := startFakeAgy(t, t.TempDir(), herdrPaneEnv(remoteHerdr, "w1:pB"))
	pid := cmd.Process.Pid
	remoteHerdr.holds(t, "w1:pB", pid)
	ea := &enterAdapter{fakeAdapter: &fakeAdapter{harness: "agy"}}
	remoteBins := fakeBin(t, "agy")
	remoteBins["herdr"] = remoteHerdr.bin
	remote := startNode(t, g, nodeOpts{cfg: func(c *Config) {
		c.Adapters = []Adapter{ea}
		c.HarnessPaths = remoteBins
	}})
	ea.setSessions(protocol.SessionInfo{Harness: "agy", NativeID: "conversation-remote", PID: pid, Cwd: "/nope", Name: "lead", Status: "idle"})
	var id string
	eventually(t, "remote session id", func() bool {
		if s := remote.d.Sessions(); len(s) == 1 {
			id = s[0].SessionID
		}
		return id != ""
	})
	localHerdr, localLog := fakeOpenHerdr(t)
	local := startNode(t, g, nodeOpts{cfg: func(c *Config) {
		c.HarnessPaths = map[string]string{"herdr": localHerdr}
	}})
	waitSessionOnGateway(t, local, id)

	p := enterOfHerdr(t, local, id, fakeHerdrSocket(localLog))
	if want := []string{localHerdr, "--remote", remote.cfg.Hostname}; p.OpenedHerdr || !reflect.DeepEqual(p.Argv, want) || !strings.Contains(p.Note, "w1:pB") {
		t.Fatalf("plan %+v, want argv %v", p, want)
	}
	requireHerdrRemoteEnv(t, p)
	remoteHerdr.readOnly(t)
}

// A running agy outside every herdr pane cannot be shown from here, and Enter
// starts no second agy on the conversation. The note says where it runs.
func TestEnterAgyRunningWithoutPaneStartsNothing(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	started := filepath.Join(t.TempDir(), "started")
	agy := filepath.Join(t.TempDir(), "agy")
	if err := os.WriteFile(agy, []byte("#!/bin/sh\necho \"$@\" >> "+started+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	cwd := t.TempDir()
	cmd, _ := startFakeAgy(t, t.TempDir(), nil)
	pid := cmd.Process.Pid
	n, _, id := enterNode(t, g, "agy", protocol.SessionInfo{NativeID: "conversation-idle", PID: pid, Cwd: cwd, Name: "lead"}, map[string]string{"agy": agy}, nil)
	p := enterOf(t, n, id, "")
	if len(p.Argv) != 0 {
		t.Fatalf("argv %v, want none", p.Argv)
	}
	for _, want := range []string{strconv.Itoa(pid), cwd} {
		if !strings.Contains(p.Note, want) {
			t.Errorf("note %q does not name %q", p.Note, want)
		}
	}
	if _, err := os.Stat(started); err == nil {
		t.Fatal("agy was started on the conversation")
	}
}
