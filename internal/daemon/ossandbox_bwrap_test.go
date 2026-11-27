//go:build linux

package daemon

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// These tests run the real bubblewrap. They are skipped where it is not
// installed or cannot make a sandbox (user namespaces off): a skip proves
// nothing about the sandbox.

// requireBwrap skips the test when bubblewrap cannot make a sandbox. Otherwise it
// points TMPDIR at a folder under /var/tmp, so t.TempDir, the repositories and the
// state folders the test makes can be bound into the sandbox. Go may run the test
// with TMPDIR on /dev/shm, and bwrap cannot bind anything from there. So the test
// must not run in parallel.
func requireBwrap(t *testing.T) {
	t.Helper()
	if err := osSandboxReady(); err != nil {
		t.Skipf("bubblewrap cannot run here: %v", err)
	}
	t.Setenv("TMPDIR", sandboxFolder(t, "tmp"))
}

// The write fence of decision 0197 under bubblewrap.
func TestOSSandboxBwrapWriteFence(t *testing.T) {
	requireBwrap(t)
	checkWriteFence(t)
}

// groupMembers lists the processes in a process group, as the daemon sees
// them.
func groupMembers(pgid int) []int {
	var out []int
	entries, _ := os.ReadDir("/proc")
	for _, e := range entries {
		pid, err := strconv.Atoi(e.Name())
		if err != nil {
			continue
		}
		if g, err := syscall.Getpgid(pid); err == nil && g == pgid {
			out = append(out, pid)
		}
	}
	return out
}

// Without --die-with-parent and --new-session, the harness and its children
// stay in bubblewrap's process group: the daemon's stop reaches all of them.
func TestOSSandboxGroupStopReachesTheHarness(t *testing.T) {
	requireBwrap(t)
	d, w, _ := sandboxWorker(t, protocol.WorkersSandboxed)
	if err := d.spawnWorker(w, "/bin/sh", []string{"-c", "sleep 60 & wait"}, os.Environ()); err != nil {
		t.Fatal(err)
	}
	eventually(t, "bwrap, its init, the shell and sleep share one group", func() bool { return len(groupMembers(w.pgid)) >= 4 })
	d.killGroup(w)
	eventually(t, "the whole group is gone", func() bool {
		return errors.Is(syscall.Kill(-w.pgid, 0), syscall.ESRCH)
	})
}

// A sandboxed worker keeps its connection through a daemon restart. The
// sandbox binds the folder of the daemon socket, so the socket the new daemon
// creates at the same path appears in the worker's namespace. A bind of the
// socket file would keep the socket of the daemon that stopped, and the worker
// would never report or hand back.
func TestOSSandboxSocketSurvivesDaemonRestart(t *testing.T) {
	requireBwrap(t)
	d, w, _ := sandboxWorker(t, protocol.WorkersSandboxed)
	dir := filepath.Join(d.cfg.StateDir, "workers", w.id)
	first, err := listenLocal(d, d.cfg.SocketPath)
	if err != nil {
		t.Fatal(err)
	}
	open := first
	t.Cleanup(func() { open.close() })

	env := workerEnv(os.Environ(), map[string]string{
		envFakeSocketProbe:      "1",
		envFakeSocketProbeReady: filepath.Join(dir, "tmp", "ready"),
		envFakeSocketProbeGate:  filepath.Join(dir, "tmp", "gate"),
		envFakeSocketProbeOut:   filepath.Join(dir, "tmp", "result"),
	})
	if err := d.spawnWorker(w, sandboxHarnessCopy(t), nil, env); err != nil {
		t.Fatal(err)
	}
	eventually(t, "the probe waits inside the sandbox", func() bool {
		return fileExists(filepath.Join(dir, "tmp", "ready"))
	})

	// The daemon stops and its socket goes; the new daemon listens at the same
	// path, with a new socket file and a new inode.
	first.close()
	open, err = listenLocal(d, d.cfg.SocketPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "tmp", "gate"), []byte("go"), 0o600); err != nil {
		t.Fatal(err)
	}

	waitExit(t, w)
	raw, err := os.ReadFile(filepath.Join(dir, "tmp", "result"))
	if err != nil {
		t.Fatalf("no result from the sandbox: %v (log: %s)", err, d.workerLogTail(w))
	}
	if got := strings.TrimSpace(string(raw)); got != "connected" {
		t.Fatalf("the worker reached %q after the restart, want connected (log: %s)", got, d.workerLogTail(w))
	}
}

// A Pi worker of a project with the switch on starts through the gateway as
// usual, inside bubblewrap: its extension reaches the daemon through the
// bound socket and the worker answers, but it cannot write in the home folder
// or add an extension to the owner's Pi, and it uses a private agent folder
// with a copy of the credentials.
func TestOSSandboxPiWorkerEndToEnd(t *testing.T) {
	requireBwrap(t)
	home := outsideHome(t)
	agent := filepath.Join(home, ".pi", "agent")
	if err := os.MkdirAll(filepath.Join(agent, "extensions"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(agent, "auth.json"), []byte(`{"openai":{"type":"api_key","key":"sk-test"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PI_CODING_AGENT_DIR", agent)
	t.Setenv(envFakePiProbeHome, home)
	t.Setenv(envFakePiProbeExt, "1")
	// The test binary is the fake `pi` itself, as in the OpenCode test.
	t.Setenv(envFakePi, "1")
	sessions := t.TempDir()
	t.Setenv("PI_CODING_AGENT_SESSION_DIR", sessions)
	g := startGateway(t, t.TempDir(), "")
	set := shortSet
	set.OSSandbox = []string{"shop"}
	w := startWorkerNode(t, g, set, func(c *Config) {
		c.HarnessPaths = map[string]string{"pi": sandboxHarnessCopy(t)}
		c.PiSessionsDir = sessions
	})
	w.register(t, newRepo(t, "shop"))
	owner := w.ownerID()

	res := w.mustStart(t, protocol.WorkerStartRequest{Project: "shop", Harness: "pi", Model: "openai/gpt-4.1-nano:low", Task: "say ok", Isolation: protocol.IsolationWorktree})
	rec := readPi(t, res.Worktree)
	// The start result, the brief and the worker view name the wrapper.
	if res.OSSandbox != osSandboxName {
		t.Errorf("start result os_sandbox %q", res.OSSandbox)
	}
	if !strings.Contains(res.Task.Body, "(sandboxed by bubblewrap): you may write only in your worktree, $TMPDIR") {
		t.Errorf("the brief does not name the wrapper:\n%s", res.Task.Body)
	}
	if a := w.agentOf(t, res.SessionID); a == nil || a.Worker == nil || a.Worker.OSSandbox != osSandboxName {
		t.Errorf("worker view: %+v", a)
	}
	if !strings.Contains(rec.HomeWrite, "read-only file system") {
		t.Errorf("home write: %q, want a read-only refusal", rec.HomeWrite)
	}
	if !strings.Contains(rec.ExtWrite, "read-only file system") || fileExists(filepath.Join(agent, "extensions", "evil.ts")) {
		t.Errorf("extension write: %q, want a read-only refusal", rec.ExtWrite)
	}
	if rec.TmpWrite != "" {
		t.Errorf("TMPDIR write: %q", rec.TmpWrite)
	}
	rows, _ := w.d.st.workers()
	if len(rows) != 1 {
		t.Fatalf("rows: %+v", rows)
	}
	dir := filepath.Join(w.d.cfg.StateDir, "workers", rows[0].ID)
	if rec.AgentDir != resolved(filepath.Join(dir, "pi-agent")) {
		t.Errorf("Pi agent folder %q, want the private one in %s", rec.AgentDir, dir)
	}
	if b, err := os.ReadFile(filepath.Join(dir, "pi-agent", "auth.json")); err != nil || !strings.Contains(string(b), "sk-test") {
		t.Errorf("the private agent folder has no copy of the credentials: %q %v", b, err)
	}
	if !fileExists(filepath.Join(dir, "tmp", "fake-pi-probe")) {
		t.Error("TMPDIR is not the worker's tmp folder")
	}
	if fileExists(filepath.Join(home, "probe")) {
		t.Error("the worker wrote in the home folder")
	}
	reply := w.replyTo(t, owner, res.Task.ID)
	if !strings.HasPrefix(reply.Body, "ok from worker") {
		t.Errorf("reply: %+v", reply)
	}
	w.cli.mustCall("worker_stop", protocol.WorkerStopRequest{Session: res.SessionID})
	eventually(t, "the worker ends", func() bool { return w.d.workerCount() == 0 })
	notices := w.waitNotice(t, 1)
	if body := notices[len(notices)-1].Body; !strings.Contains(body, "(sandboxed by bubblewrap)") {
		t.Errorf("the completion notice does not name the wrapper:\n%s", body)
	}
	if pgid := rows[0].PGID; pgid > 0 {
		eventually(t, "the sandbox's process group is gone", func() bool { return errors.Is(syscall.Kill(-pgid, 0), syscall.ESRCH) })
	}
}

// An agy worker of a project with the switch on runs inside bubblewrap without
// agy's own --sandbox, which cannot run a command on the owner's machines. It
// writes in its worktree and in agy's runtime folder (the fake keeps its
// records there), a write outside them is refused, and it hands back through
// the bound socket.
func TestOSSandboxAgyWorkerEndToEnd(t *testing.T) {
	requireBwrap(t)
	home := outsideHome(t)
	records := filepath.Join(home, ".gemini", "antigravity-cli", "records")
	if err := os.MkdirAll(records, 0o700); err != nil {
		t.Fatal(err)
	}
	outside := sandboxFolder(t, "outside")
	bin := filepath.Join(sandboxFolder(t, "agy"), "agy")
	script := "#!/bin/sh\nFAKE_AGY_RECORDS=" + shellQuote(records) + " FAKE_AGY_FENCE_OUTSIDE=" + shellQuote(outside) +
		" FAKE_AGY_PROCESS=1 exec " + shellQuote(sandboxHarnessCopy(t)) + " -test.run '^TestAgyProcess$' -- \"$@\"\n"
	if err := os.WriteFile(bin, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	g := startGateway(t, t.TempDir(), "")
	set := shortSet
	set.OSSandbox = []string{"shop"}
	set.StartTimeout = scaled(30 * time.Second)
	w := startWorkerNodeWithDetect(t, g, set, agyInventory, func(c *Config) {
		c.HarnessPaths = map[string]string{"agy": bin}
		c.WorkerGoTmp = filepath.Join(t.TempDir(), "gotmp")
	})
	w.register(t, newRepo(t, "shop"))
	// The daemon finds agy's folders in the home folder when the worker starts.
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))

	res := w.mustStart(t, protocol.WorkerStartRequest{Project: "shop", Harness: "agy", Model: "gemini-3.8-flash-low", Task: "FAKE:fence FAKE:handback", Isolation: protocol.IsolationWorktree})
	if res.OSSandbox != osSandboxName {
		t.Errorf("start result os_sandbox %q", res.OSSandbox)
	}
	n := w.waitNotice(t, 1)[0]
	for _, want := range []string{"status: done", "finished agy task", "(sandboxed by bubblewrap)"} {
		if !strings.Contains(n.Body, want) {
			t.Errorf("notice lacks %q:\n%s", want, n.Body)
		}
	}
	recs := agyRecords(records)
	if len(recs) != 1 {
		t.Fatalf("records in agy's runtime folder: %+v (log: %s)", recs, w.logs)
	}
	r := recs[0]
	if slices.Contains(r.Args, "--sandbox") {
		t.Errorf("agy runs with its own sandbox inside the wrapper: %q", r.Args)
	}
	if r.MCPError != "" {
		t.Errorf("MCP: %s", r.MCPError)
	}
	if r.FenceInside != "" || !fileExists(filepath.Join(res.Worktree, "fence-inside.txt")) {
		t.Errorf("a write in the worktree failed: %q", r.FenceInside)
	}
	if !strings.Contains(r.FenceOutside, "read-only file system") || fileExists(filepath.Join(outside, "fence-outside.txt")) {
		t.Errorf("a write outside the fence: %q, want a read-only refusal", r.FenceOutside)
	}
	if m := readOSSandboxMarker(filepath.Join(w.d.cfg.StateDir, "workers", r.Worker)); m != osSandboxName {
		t.Errorf("marker %q", m)
	}
}

// An OpenCode worker of a project with the switch on: the daemon reaches its
// private server over loopback (the network level keeps the host network),
// its plugin registers through the bound socket, and it answers.
func TestOSSandboxOpenCodeWorkerEndToEnd(t *testing.T) {
	requireBwrap(t)
	g := startGateway(t, t.TempDir(), "")
	set := shortSet
	set.OSSandbox = []string{"shop"}
	harness := sandboxHarnessCopy(t)
	w := startWorkerNode(t, g, set, func(c *Config) { c.HarnessPaths = map[string]string{"opencode": harness} })
	w.register(t, newRepo(t, "shop"))
	owner := w.ownerID()
	res := w.mustStart(t, protocol.WorkerStartRequest{Project: "shop", Harness: "opencode", Model: "fake/m1", Task: "say ok", Isolation: protocol.IsolationWorktree})
	rec := readFake(t, res.Worktree)
	if rec.Cwd != res.Worktree {
		t.Errorf("the worker runs in %s, not its worktree", rec.Cwd)
	}
	id := rec.Env["AGENT_NET_WORKER"]
	if m := readOSSandboxMarker(filepath.Join(w.cfg.StateDir, "workers", id)); m != osSandboxName {
		t.Errorf("marker %q", m)
	}
	reply := w.replyTo(t, owner, res.Task.ID)
	if reply.Body == "" {
		t.Errorf("reply: %+v", reply)
	}
	w.cli.mustCall("worker_stop", protocol.WorkerStopRequest{Session: res.SessionID})
	eventually(t, "the worker ends", func() bool { return w.d.workerCount() == 0 })
}
