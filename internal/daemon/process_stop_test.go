package daemon

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// The CTO stops processes on a machine on the owner's instruction (decision
// 0102). These tests start real throwaway children and stop them. A signal
// guard stands between the code and the kernel: it passes a signal only to a
// child that the test started, so no test can touch any other process.

type throwaway struct {
	cmd  *exec.Cmd
	pid  int
	done chan struct{}
}

func (s *throwaway) exited() bool {
	select {
	case <-s.done:
		return true
	default:
		return false
	}
}

// startThrowaway starts a throwaway child that sleeps. With ignoreTerm it
// ignores SIGTERM, so only SIGKILL stops it. The test cleanup kills it.
func startThrowaway(t *testing.T, ignoreTerm bool) *throwaway {
	t.Helper()
	cmd := exec.Command("sleep", "300")
	var ready *bufio.Reader
	if ignoreTerm {
		// An ignored signal stays ignored across exec, so sleep ignores TERM.
		cmd = exec.Command("sh", "-c", `trap "" TERM; echo ready; exec sleep 300`)
		out, err := cmd.StdoutPipe()
		if err != nil {
			t.Fatal(err)
		}
		ready = bufio.NewReader(out)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	s := &throwaway{cmd: cmd, pid: cmd.Process.Pid, done: make(chan struct{})}
	go func() {
		if ready != nil {
			io.Copy(io.Discard, ready) // reads "ready", then EOF when the process ends
		}
		cmd.Wait()
		close(s.done)
	}()
	t.Cleanup(func() {
		cmd.Process.Kill()
		select {
		case <-s.done:
		case <-time.After(5 * time.Second):
			t.Errorf("throwaway process %d did not exit", s.pid)
		}
	})
	if ignoreTerm {
		eventually(t, "the throwaway process ignores TERM", func() bool {
			raw, err := os.ReadFile("/proc/" + strconv.Itoa(s.pid) + "/status")
			if err != nil {
				return false
			}
			for _, line := range strings.Split(string(raw), "\n") {
				if rest, ok := strings.CutPrefix(line, "SigIgn:"); ok {
					mask, _ := strconv.ParseUint(strings.TrimSpace(rest), 16, 64)
					return mask&(1<<(uint(syscall.SIGTERM)-1)) != 0
				}
			}
			return false
		})
	}
	return s
}

// signalGuard records every signal and sends it only to the test's children.
type signalGuard struct {
	mu      sync.Mutex
	allowed map[int]bool
	sent    []string
	unsafe  []int
}

func newSignalGuard(children ...*throwaway) *signalGuard {
	g := &signalGuard{allowed: map[int]bool{}}
	for _, c := range children {
		g.allowed[c.pid] = true
	}
	return g
}

func (g *signalGuard) allow(children ...*throwaway) {
	g.mu.Lock()
	defer g.mu.Unlock()
	for _, c := range children {
		g.allowed[c.pid] = true
	}
}

func (g *signalGuard) signal(pid int, sig syscall.Signal) error {
	g.mu.Lock()
	ok := g.allowed[pid]
	if ok {
		g.sent = append(g.sent, fmt.Sprintf("%d:%d", pid, sig))
	} else {
		g.unsafe = append(g.unsafe, pid)
	}
	g.mu.Unlock()
	if !ok {
		return fmt.Errorf("the test refused to signal pid %d", pid)
	}
	return syscall.Kill(pid, sig)
}

func (g *signalGuard) check(t *testing.T) {
	t.Helper()
	g.mu.Lock()
	defer g.mu.Unlock()
	if len(g.unsafe) > 0 {
		t.Fatalf("the code tried to signal pids that are not the test's children: %v", g.unsafe)
	}
}

func (g *signalGuard) log() []string {
	g.mu.Lock()
	defer g.mu.Unlock()
	return append([]string(nil), g.sent...)
}

// processStopDaemon is a bare daemon: no gateway, only what a local stop uses.
func processStopDaemon(g *signalGuard) *Daemon {
	return &Daemon{
		cfg: Config{
			NodeID: "n_local", Hostname: "localhost-test",
			testProcessSignal: g.signal, testProcessStopGrace: 300 * time.Millisecond,
			// No roster: a Claude background process is refused, as with a missing roster.
			claudeRosterPath: os.DevNull,
		},
		log:      log.New(io.Discard, "", 0),
		sessions: map[string]*localSession{},
		workers:  map[string]*worker{},
	}
}

func stopLocal(t *testing.T, d *Daemon, pids ...int) []protocol.ProcessOutcome {
	t.Helper()
	res, err := d.processStop(context.Background(), protocol.ProcessStopRequest{NodeID: d.cfg.NodeID, PIDs: pids})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Processes) != len(pids) {
		t.Fatalf("want %d results, got %+v", len(pids), res.Processes)
	}
	if res.NodeID != d.cfg.NodeID || res.Hostname == "" {
		t.Fatalf("the result must name the machine: %+v", res)
	}
	return res.Processes
}

func waitExited(t *testing.T, s *throwaway) {
	t.Helper()
	eventually(t, fmt.Sprintf("pid %d exited", s.pid), s.exited)
}

func TestProcessStopTermStopsAProcessAndReportsWhatItWas(t *testing.T) {
	s := startThrowaway(t, false)
	g := newSignalGuard(s)
	d := processStopDaemon(g)
	before := time.Now()

	got := stopLocal(t, d, s.pid)[0]
	g.check(t)
	if got.PID != s.pid || got.Outcome != protocol.ProcessStoppedTerm {
		t.Fatalf("want stopped by TERM, got %+v", got)
	}
	waitExited(t, s)
	// Read before the signal: the command line, the parent and the start time.
	if !strings.Contains(got.Command, "sleep") || !strings.Contains(got.Command, "300") {
		t.Errorf("command line %q", got.Command)
	}
	if got.ParentPID != os.Getpid() {
		t.Errorf("parent %d, want %d", got.ParentPID, os.Getpid())
	}
	started, err := time.Parse(time.RFC3339, got.Started)
	if err != nil {
		t.Fatalf("started %q: %v", got.Started, err)
	}
	if started.Before(before.Add(-2*time.Minute)) || started.After(time.Now().Add(2*time.Second)) {
		t.Errorf("start time %s is not recent", got.Started)
	}
	if sent := g.log(); len(sent) != 1 || sent[0] != fmt.Sprintf("%d:%d", s.pid, syscall.SIGTERM) {
		t.Errorf("only one TERM was due, sent %v", sent)
	}
	sample, _ := json.Marshal(got)
	t.Logf("sample result: %s", sample)
}

func TestProcessStopKillsWhatIgnoresTerm(t *testing.T) {
	s := startThrowaway(t, true)
	g := newSignalGuard(s)
	d := processStopDaemon(g)

	got := stopLocal(t, d, s.pid)[0]
	g.check(t)
	if got.Outcome != protocol.ProcessStoppedKill {
		t.Fatalf("want stopped by KILL, got %+v", got)
	}
	waitExited(t, s)
	want := []string{fmt.Sprintf("%d:%d", s.pid, syscall.SIGTERM), fmt.Sprintf("%d:%d", s.pid, syscall.SIGKILL)}
	if sent := g.log(); strings.Join(sent, ",") != strings.Join(want, ",") {
		t.Errorf("signals %v, want TERM then KILL %v", sent, want)
	}
}

func TestProcessStopWaitsTheGraceTimeBeforeKill(t *testing.T) {
	s := startThrowaway(t, true)
	g := newSignalGuard(s)
	d := processStopDaemon(g)
	d.cfg.testProcessStopGrace = 700 * time.Millisecond

	started := time.Now()
	got := stopLocal(t, d, s.pid)[0]
	if got.Outcome != protocol.ProcessStoppedKill {
		t.Fatalf("%+v", got)
	}
	if took := time.Since(started); took < 650*time.Millisecond {
		t.Errorf("KILL came after %s, before the grace time", took)
	}
}

func TestProcessStopReportsAProcessThatIsNotThere(t *testing.T) {
	gone := exec.Command("true")
	if err := gone.Run(); err != nil {
		t.Fatal(err)
	}
	g := newSignalGuard()
	d := processStopDaemon(g)
	got := stopLocal(t, d, gone.Process.Pid)[0]
	g.check(t)
	if got.Outcome != protocol.ProcessNotFound {
		t.Fatalf("want not found, got %+v", got)
	}
	if sent := g.log(); len(sent) != 0 {
		t.Errorf("nothing to signal, sent %v", sent)
	}
}

func TestProcessStopAnswersEveryPidInOrderAndDropsDuplicates(t *testing.T) {
	term := startThrowaway(t, false)
	kill := startThrowaway(t, true)
	gone := exec.Command("true")
	if err := gone.Run(); err != nil {
		t.Fatal(err)
	}
	g := newSignalGuard(term, kill)
	d := processStopDaemon(g)

	res, err := d.processStop(context.Background(), protocol.ProcessStopRequest{NodeID: "n_local", PIDs: []int{term.pid, 1, kill.pid, term.pid, gone.Process.Pid}})
	if err != nil {
		t.Fatal(err)
	}
	g.check(t)
	var order []int
	outcomes := map[int]string{}
	for _, p := range res.Processes {
		order = append(order, p.PID)
		outcomes[p.PID] = p.Outcome
	}
	if fmt.Sprint(order) != fmt.Sprint([]int{term.pid, 1, kill.pid, gone.Process.Pid}) {
		t.Errorf("order %v", order)
	}
	for pid, want := range map[int]string{term.pid: protocol.ProcessStoppedTerm, 1: protocol.ProcessRefused, kill.pid: protocol.ProcessStoppedKill, gone.Process.Pid: protocol.ProcessNotFound} {
		if outcomes[pid] != want {
			t.Errorf("pid %d: %s, want %s", pid, outcomes[pid], want)
		}
	}
	waitExited(t, term)
	waitExited(t, kill)
}

func TestProcessStopRefusesTheDaemonPid1AndBadPids(t *testing.T) {
	g := newSignalGuard()
	d := processStopDaemon(g)
	own := os.Getpid()
	got := stopLocal(t, d, own, 1, 0, -1, -own)
	g.check(t)
	reasons := map[int]string{own: "daemon", 1: "init", 0: "not a valid", -1: "not a valid", -own: "not a valid"}
	for _, p := range got {
		if p.Outcome != protocol.ProcessRefused {
			t.Errorf("pid %d must be refused: %+v", p.PID, p)
		}
		if !strings.Contains(p.Reason, reasons[p.PID]) {
			t.Errorf("pid %d reason %q lacks %q", p.PID, p.Reason, reasons[p.PID])
		}
	}
	if sent := g.log(); len(sent) != 0 {
		t.Errorf("a refused pid is never signalled, sent %v", sent)
	}
}

func TestProcessStopRefusesLiveSessionsAndWorkers(t *testing.T) {
	session := startThrowaway(t, false)
	worker1 := startThrowaway(t, false)
	plain := startThrowaway(t, false)
	g := newSignalGuard(session, worker1, plain)
	d := processStopDaemon(g)
	d.sessions["claude:abc"] = &localSession{sessionID: "s_abc", info: protocol.SessionInfo{Harness: "claude", NativeID: "abc", PID: session.pid}}
	d.workers["w_1"] = &worker{id: "w_1", pid: worker1.pid}

	got := stopLocal(t, d, session.pid, worker1.pid, plain.pid)
	g.check(t)
	if got[0].Outcome != protocol.ProcessRefused || !strings.Contains(got[0].Reason, "session") {
		t.Errorf("a live session must be refused: %+v", got[0])
	}
	if got[1].Outcome != protocol.ProcessRefused || !strings.Contains(got[1].Reason, "worker") {
		t.Errorf("a live worker must be refused: %+v", got[1])
	}
	if got[2].Outcome != protocol.ProcessStoppedTerm {
		t.Errorf("an ordinary process is stopped: %+v", got[2])
	}
	waitExited(t, plain)
	time.Sleep(100 * time.Millisecond)
	if session.exited() || worker1.exited() {
		t.Fatal("a refused process was stopped")
	}
	// Refused processes still report what they are.
	if !strings.Contains(got[0].Command, "sleep") || got[0].ParentPID != os.Getpid() || got[0].Started == "" {
		t.Errorf("a refused pid still reports command, parent and start: %+v", got[0])
	}
}

func TestProcessStopRefusesAProcessOfAnotherUser(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("every process is the daemon user's when the tests run as root")
	}
	// Any process that another user owns, for example a system service.
	foreign := 0
	entries, _ := os.ReadDir("/proc")
	for _, e := range entries {
		pid, err := strconv.Atoi(e.Name())
		if err != nil || pid == 1 {
			continue
		}
		if v, err := readProcView(pid); err == nil && v.UID != os.Getuid() {
			foreign = pid
			break
		}
	}
	if foreign == 0 {
		t.Skip("no process of another user is visible here")
	}
	g := newSignalGuard()
	d := processStopDaemon(g)
	got := stopLocal(t, d, foreign)[0]
	g.check(t)
	if got.Outcome != protocol.ProcessRefused || !strings.Contains(got.Reason, "another user") {
		t.Fatalf("want a refusal for another user, got %+v", got)
	}
	if sent := g.log(); len(sent) != 0 {
		t.Errorf("sent %v", sent)
	}
}

func TestProcessRefusalByOwnerAndByAgentNetServiceCommand(t *testing.T) {
	d := processStopDaemon(newSignalGuard())
	me := os.Getuid()
	cases := []struct {
		name string
		view procView
		want string
	}{
		{"another user", procView{PID: 4242, UID: me + 1, EUID: me + 1, Command: "nginx"}, "another user"},
		{"another effective user", procView{PID: 4242, UID: me, EUID: me + 1, Command: "su"}, "another user"},
		{"the daemon service", procView{PID: 4242, UID: me, EUID: me, Command: "/home/x/.local/bin/agent-net daemon"}, "daemon or gateway"},
		{"the gateway service", procView{PID: 4242, UID: me, EUID: me, Command: "/usr/local/bin/agent-net gateway --listen :8443"}, "daemon or gateway"},
		{"a go run daemon", procView{PID: 4242, UID: me, EUID: me, Command: "agent-net daemon --foo"}, "daemon or gateway"},
		{"an mcp shim is not the service", procView{PID: 4242, UID: me, EUID: me, Command: "/home/x/.local/bin/agent-net mcp"}, ""},
		{"a user mentioning it", procView{PID: 4242, UID: me, EUID: me, Command: "vim agent-net daemon.md"}, ""},
		{"an ordinary process", procView{PID: 4242, UID: me, EUID: me, Command: "python3 server.py"}, ""},
	}
	for _, tc := range cases {
		got := d.processRefusal(tc.view)
		if tc.want == "" && got != "" {
			t.Errorf("%s: refused: %s", tc.name, got)
		}
		if tc.want != "" && !strings.Contains(got, tc.want) {
			t.Errorf("%s: reason %q lacks %q", tc.name, got, tc.want)
		}
	}
}

func TestProcessStopNeedsAMachineAndPids(t *testing.T) {
	d := processStopDaemon(newSignalGuard())
	for name, q := range map[string]protocol.ProcessStopRequest{
		"no machine": {PIDs: []int{12345}},
		"no pids":    {NodeID: "n_local"},
		"empty pids": {NodeID: "n_local", PIDs: []int{}},
	} {
		if _, err := d.processStop(context.Background(), q); err == nil {
			t.Errorf("%s: want an error", name)
		}
	}
}

// ---- through the CTO tool, the gateway and a second machine ----

func ctoProcessStop(t *testing.T, d *Daemon, node string, pids ...int) (protocol.ProcessStopResult, error) {
	t.Helper()
	raw, _ := json.Marshal(map[string]any{"area": "process_stop", "node_id": node, "pids": pids})
	res, err := d.runCTOToolRaw(context.Background(), "settings_change", raw)
	if err != nil {
		return protocol.ProcessStopResult{}, err
	}
	b, _ := json.Marshal(res)
	var out protocol.ProcessStopResult
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatalf("result %s: %v", b, err)
	}
	return out, nil
}

func TestCTOProcessStopRelaysToTheMachineAndAuditsWithoutUndo(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	pathTestHome(t)
	term := startThrowaway(t, false)
	stubborn := startThrowaway(t, true)
	guard := newSignalGuard(term, stubborn)
	a := startWorkerNode(t, g, shortSet, nil)
	b := startWorkerNode(t, g, shortSet, func(c *Config) {
		c.testProcessSignal, c.testProcessStopGrace = guard.signal, 300*time.Millisecond
		c.claudeRosterPath = os.DevNull
	})
	if err := a.d.registerCTOSession(context.Background()); err != nil {
		t.Fatal(err)
	}

	res, err := ctoProcessStop(t, a.d, b.d.NodeID(), term.pid, stubborn.pid, 1)
	if err != nil {
		t.Fatal(err)
	}
	guard.check(t)
	if res.NodeID != b.d.NodeID() || res.Hostname == "" {
		t.Fatalf("the result must name the target machine: %+v", res)
	}
	want := []string{protocol.ProcessStoppedTerm, protocol.ProcessStoppedKill, protocol.ProcessRefused}
	for i, p := range res.Processes {
		if p.Outcome != want[i] {
			t.Errorf("pid %d: %s, want %s (%+v)", p.PID, p.Outcome, want[i], p)
		}
	}
	waitExited(t, term)
	waitExited(t, stubborn)

	item := lastInbox(t, g)
	if len(item.Options) != 0 {
		t.Fatalf("a stopped process has no undo: %+v", item)
	}
	notice := item.Text + "\n" + item.Recommendation
	t.Logf("sample Inbox notice:\n%s", notice)
	for _, want := range []string{res.Hostname, strconv.Itoa(term.pid), strconv.Itoa(stubborn.pid), "sleep 300", "stopped by TERM", "stopped by KILL", "refused", "No undo", "ppid " + strconv.Itoa(os.Getpid())} {
		if !strings.Contains(notice, want) {
			t.Errorf("the Inbox notice lacks %q:\n%s", want, notice)
		}
	}
	if items, err := a.d.proposalRunner().list(); err != nil || len(items) != 0 {
		t.Fatalf("stopping processes is not a proposal: %+v %v", items, err)
	}
}

func TestCTOProcessStopOnThisMachineAndByHostname(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	pathTestHome(t)
	s := startThrowaway(t, false)
	guard := newSignalGuard(s)
	a := startWorkerNode(t, g, shortSet, func(c *Config) {
		c.testProcessSignal, c.testProcessStopGrace = guard.signal, 300*time.Millisecond
		c.claudeRosterPath = os.DevNull
	})
	if err := a.d.registerCTOSession(context.Background()); err != nil {
		t.Fatal(err)
	}
	res, err := ctoProcessStop(t, a.d, a.d.NodeID(), s.pid)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Processes) != 1 || res.Processes[0].Outcome != protocol.ProcessStoppedTerm {
		t.Fatalf("%+v", res)
	}
	waitExited(t, s)
	if item := lastInbox(t, g); !strings.Contains(item.Text+item.Recommendation, "No undo") {
		t.Fatalf("the notice must say there is no undo: %+v", item)
	}
	// A machine that does not exist is an error, and nothing is signalled.
	if _, err := ctoProcessStop(t, a.d, "no-such-machine", s.pid); err == nil {
		t.Fatal("an unknown machine must be refused")
	}
	// The machine is required: a missing node_id is never "this machine".
	if _, err := ctoProcessStop(t, a.d, "", s.pid); err == nil {
		t.Fatal("a missing node_id must be refused")
	}
	guard.check(t)
}

func TestProcessStopIsRefusedToAgentSessionsAndListedForTheOwner(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	s := startThrowaway(t, false)
	guard := newSignalGuard(s)
	n := startNodeCfg(t, g, nodeOpts{cmdline: "/usr/bin/pi", interval: time.Hour}, func(c *Config) {
		c.testProcessSignal, c.testProcessStopGrace = guard.signal, 300*time.Millisecond
	})
	agent, _ := helloPi(t, n, "stop-agent", "/w/stop", true)
	_, err := agent.call(protocol.OpProcessStop, protocol.ProcessStopRequest{NodeID: n.d.NodeID(), PIDs: []int{s.pid}})
	if err == nil || !strings.Contains(err.Error(), "only available to the owner") {
		t.Fatalf("an agent session must not stop processes: %v", err)
	}
	time.Sleep(100 * time.Millisecond)
	if s.exited() || len(guard.log()) != 0 {
		t.Fatalf("a refused call signalled %v", guard.log())
	}
	found := false
	for _, m := range protocol.OperatorMethods {
		found = found || m == protocol.OpProcessStop
	}
	if !found {
		t.Fatal("process_stop is not listed in OperatorMethods")
	}
}

// The production path has no test hook: it signals with the kernel. Only the
// test's own child is ever named, so this is safe.
func TestProcessStopSignalsForRealWithoutTheTestHook(t *testing.T) {
	s := startThrowaway(t, false)
	d := processStopDaemon(newSignalGuard())
	d.cfg.testProcessSignal = nil
	got := stopLocal(t, d, s.pid)[0]
	if got.Outcome != protocol.ProcessStoppedTerm {
		t.Fatalf("%+v", got)
	}
	waitExited(t, s)
}

// ---- Claude Code background sessions (the roster, process_stop_claude.go) ----

// writeClaudeRoster writes a roster file in a temp folder, in the shape the
// Claude supervisor uses, and returns its path.
func writeClaudeRoster(t *testing.T, supervisor int, updated time.Time, workers map[string]any) string {
	t.Helper()
	raw, err := json.Marshal(map[string]any{"supervisorPid": supervisor, "updatedAt": updated.UnixMilli(), "workers": workers})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "roster.json")
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// startNamedThrowaway starts a sleeping child whose command line reads as name,
// such as "claude bg-spare". Only the command line differs from startThrowaway.
func startNamedThrowaway(t *testing.T, name string) *throwaway {
	t.Helper()
	cmd := exec.Command("sleep", "300")
	cmd.Args[0] = name
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	s := &throwaway{cmd: cmd, pid: cmd.Process.Pid, done: make(chan struct{})}
	go func() {
		cmd.Wait()
		close(s.done)
	}()
	t.Cleanup(func() {
		cmd.Process.Kill()
		select {
		case <-s.done:
		case <-time.After(5 * time.Second):
			t.Errorf("throwaway process %d did not exit", s.pid)
		}
	})
	return s
}

// startParentWithChild starts a shell that starts one sleeping child, as a
// background host starts a session's processes. It returns the shell and the
// child's process ID. The child's output goes to /dev/null, so it does not
// hold the shell's pipe open.
func startParentWithChild(t *testing.T) (*throwaway, int) {
	t.Helper()
	cmd := exec.Command("sh", "-c", "sleep 300 >/dev/null 2>&1 & echo $!; wait")
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	r := bufio.NewReader(out)
	line, err := r.ReadString('\n')
	if err != nil {
		t.Fatal(err)
	}
	child, err := strconv.Atoi(strings.TrimSpace(line))
	if err != nil {
		t.Fatalf("child pid %q: %v", line, err)
	}
	s := &throwaway{cmd: cmd, pid: cmd.Process.Pid, done: make(chan struct{})}
	go func() {
		io.Copy(io.Discard, r)
		cmd.Wait()
		close(s.done)
	}()
	t.Cleanup(func() {
		syscall.Kill(child, syscall.SIGKILL) // the test's own child; the shell does not end it
		cmd.Process.Kill()
		select {
		case <-s.done:
		case <-time.After(5 * time.Second):
			t.Errorf("throwaway shell %d did not exit", s.pid)
		}
	})
	return s, child
}

func TestProcessStopRefusesARosterSessionAndItsSessionProcess(t *testing.T) {
	host := startThrowaway(t, false)    // stands for the background host (pty host)
	session := startThrowaway(t, false) // stands for the session process
	plain := startThrowaway(t, false)
	g := newSignalGuard(host, session, plain)
	d := processStopDaemon(g)
	d.cfg.claudeRosterPath = writeClaudeRoster(t, 0, time.Now(), map[string]any{
		"ab12cd34": map[string]any{"pid": host.pid, "replPid": session.pid, "cwd": "/work/finget", "sessionId": "sess-1"},
	})

	got := stopLocal(t, d, host.pid, session.pid, plain.pid)
	g.check(t)
	for _, p := range got[:2] {
		if p.Outcome != protocol.ProcessRefused {
			t.Fatalf("a live Claude session must be refused: %+v", p)
		}
		if !strings.Contains(p.Reason, "ab12cd34") || !strings.Contains(p.Reason, "/work/finget") {
			t.Errorf("the reason must name the session (short id and cwd): %q", p.Reason)
		}
	}
	if got[2].Outcome != protocol.ProcessStoppedTerm {
		t.Errorf("an ordinary process is stopped: %+v", got[2])
	}
	waitExited(t, plain)
	time.Sleep(100 * time.Millisecond)
	if host.exited() || session.exited() {
		t.Fatal("a refused Claude process was stopped")
	}
	if sent := g.log(); len(sent) != 1 || sent[0] != fmt.Sprintf("%d:%d", plain.pid, syscall.SIGTERM) {
		t.Errorf("only the ordinary process may be signalled, sent %v", sent)
	}
}

func TestProcessStopRefusesTheClaudeSupervisor(t *testing.T) {
	supervisor := startThrowaway(t, false)
	g := newSignalGuard(supervisor)
	d := processStopDaemon(g)
	d.cfg.claudeRosterPath = writeClaudeRoster(t, supervisor.pid, time.Now(), map[string]any{})

	got := stopLocal(t, d, supervisor.pid)[0]
	g.check(t)
	if got.Outcome != protocol.ProcessRefused || !strings.Contains(got.Reason, "Claude supervisor") {
		t.Fatalf("the Claude supervisor must be refused: %+v", got)
	}
	time.Sleep(100 * time.Millisecond)
	if supervisor.exited() || len(g.log()) != 0 {
		t.Fatalf("the supervisor was signalled: %v", g.log())
	}
}

func TestProcessStopRefusesADescendantOfAPtyHost(t *testing.T) {
	host, child := startParentWithChild(t)
	g := newSignalGuard(host)
	g.allow(&throwaway{pid: child})
	d := processStopDaemon(g)
	d.cfg.claudeRosterPath = writeClaudeRoster(t, 0, time.Now(), map[string]any{
		"ef56gh78": map[string]any{"pid": host.pid, "replPid": 0, "cwd": "/work/slitherswap", "sessionId": "sess-2"},
	})

	got := stopLocal(t, d, child)[0]
	g.check(t)
	if got.Outcome != protocol.ProcessRefused {
		t.Fatalf("a process inside a live session must be refused: %+v", got)
	}
	if !strings.Contains(got.Reason, "ef56gh78") || !strings.Contains(got.Reason, "/work/slitherswap") {
		t.Errorf("the reason must name the session (short id and cwd): %q", got.Reason)
	}
	if got.ParentPID != host.pid {
		t.Errorf("the refused child reports its parent %d, want the host %d", got.ParentPID, host.pid)
	}
	time.Sleep(100 * time.Millisecond)
	if len(g.log()) != 0 {
		t.Fatalf("a refused descendant was signalled: %v", g.log())
	}
	if err := syscall.Kill(child, 0); err != nil {
		t.Fatalf("the child must still run: %v", err)
	}
}

func TestProcessStopRefusesEveryClaudeBackgroundProcessWhenTheRosterIsNotFresh(t *testing.T) {
	cases := map[string]string{
		"a stale roster":   writeClaudeRoster(t, 0, time.Now().Add(-5*time.Minute), map[string]any{}),
		"a missing roster": filepath.Join(t.TempDir(), "no-roster.json"),
	}
	for name, path := range cases {
		t.Run(name, func(t *testing.T) {
			bg := startNamedThrowaway(t, "claude bg-spare")
			daemonProc := startNamedThrowaway(t, "claude daemon")
			g := newSignalGuard(bg, daemonProc)
			d := processStopDaemon(g)
			d.cfg.claudeRosterPath = path

			got := stopLocal(t, d, bg.pid, daemonProc.pid)
			g.check(t)
			for _, p := range got {
				if p.Outcome != protocol.ProcessRefused || !strings.Contains(p.Reason, "Claude roster not fresh; cannot tell live sessions") {
					t.Errorf("pid %d must be refused for the roster: %+v", p.PID, p)
				}
			}
			time.Sleep(100 * time.Millisecond)
			if bg.exited() || daemonProc.exited() || len(g.log()) != 0 {
				t.Fatalf("a Claude background process was signalled: %v", g.log())
			}
		})
	}
}

func TestProcessStopStillStopsANonClaudeOrphan(t *testing.T) {
	cases := map[string]func(t *testing.T) string{
		"with a stale roster": func(t *testing.T) string {
			return writeClaudeRoster(t, 0, time.Now().Add(-5*time.Minute), map[string]any{})
		},
		"with a fresh roster of other sessions": func(t *testing.T) string {
			live := startThrowaway(t, false)
			return writeClaudeRoster(t, 0, time.Now(), map[string]any{
				"ij90kl12": map[string]any{"pid": live.pid, "replPid": 0, "cwd": "/work/other", "sessionId": "sess-3"},
			})
		},
	}
	for name, roster := range cases {
		t.Run(name, func(t *testing.T) {
			orphan := startThrowaway(t, false)
			g := newSignalGuard(orphan)
			d := processStopDaemon(g)
			d.cfg.claudeRosterPath = roster(t)

			got := stopLocal(t, d, orphan.pid)[0]
			g.check(t)
			if got.Outcome != protocol.ProcessStoppedTerm {
				t.Fatalf("a non-Claude orphan must still be stopped: %+v", got)
			}
			waitExited(t, orphan)
		})
	}
}

func TestClaudeRosterFreshnessIsSixtySeconds(t *testing.T) {
	me := os.Getuid()
	now := time.Now()
	cases := []struct {
		name    string
		updated time.Time
		fresh   bool
	}{
		{"written five seconds ago", now.Add(-5 * time.Second), true},
		{"written a minute and a second ago", now.Add(-61 * time.Second), false},
		{"dated two minutes ahead", now.Add(2 * time.Minute), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := processStopDaemon(newSignalGuard())
			d.cfg.claudeRosterPath = writeClaudeRoster(t, 0, tc.updated, map[string]any{})
			got := d.processRefusal(procView{PID: 4242, UID: me, EUID: me, Command: "claude bg-spare"})
			refused := strings.Contains(got, "Claude roster not fresh")
			if refused == tc.fresh {
				t.Errorf("fresh=%v: refusal %q", tc.fresh, got)
			}
		})
	}
}

func TestProcessRefusalForClaudeCommandLines(t *testing.T) {
	me := os.Getuid()
	d := processStopDaemon(newSignalGuard())
	d.cfg.claudeRosterPath = os.DevNull
	cases := map[string]bool{
		"claude bg-spare":                        true,
		"/home/x/.local/bin/claude daemon start": true,
		"claude":                                 false,
		"sleep 300":                              false,
		"/usr/bin/python3 claude_helper.py bg-run": false,
	}
	for command, refused := range cases {
		got := d.processRefusal(procView{PID: 4242, UID: me, EUID: me, Command: command})
		if refused != strings.Contains(got, "Claude roster not fresh") {
			t.Errorf("%q: refusal %q", command, got)
		}
	}
}
