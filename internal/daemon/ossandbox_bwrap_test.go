//go:build linux

package daemon

import (
	"errors"
	"os"
	"path/filepath"
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

func requireBwrap(t *testing.T) {
	t.Helper()
	if err := osSandboxReady(); err != nil {
		t.Skipf("bubblewrap cannot run here: %v", err)
	}
}

// outsideHome is a folder the test can write and the sandbox can see but must
// not write: not under /tmp, which the sandbox replaces with a private one.
func outsideHome(t *testing.T) string {
	t.Helper()
	d, err := os.MkdirTemp("/var/tmp", "agent-net-osbox-home-")
	if err != nil {
		t.Skipf("no /var/tmp to make a home outside the sandbox: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(d) })
	return resolved(d)
}

// sandboxWorker makes a daemon, a repository with a linked worktree, and an
// OpenCode worker of the given level in that worktree, wrapped by bubblewrap.
func sandboxWorker(t *testing.T, level string) (*Daemon, *worker, string) {
	t.Helper()
	state := seededStateDir(t)
	d, err := New(Config{StateDir: state, SocketPath: filepath.Join(shortDir(t), "d.sock")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.st.close() })
	repo := newRepo(t, "shop")
	id := "w_osbox" + strconv.Itoa(int(time.Now().UnixNano()%1e6))
	wt := filepath.Join(state, "worktrees", id)
	gitIn(t, repo, "worktree", "add", "-q", "-b", "agent-net/"+id, wt)
	w := &worker{id: id, harness: "opencode", project: "shop", path: wt, permissions: level,
		osSandbox: osSandboxName, ready: make(chan struct{}), startedAt: time.Now()}
	t.Cleanup(func() { d.killGroup(w) })
	return d, w, repo
}

func waitExit(t *testing.T, w *worker) {
	t.Helper()
	select {
	case <-w.exited:
	case <-time.After(20 * time.Second):
		t.Fatal("the sandboxed process did not end")
	}
}

// The probe script writes its findings to /tmp/result, which is the private
// /tmp of the sandbox: the tmp folder in the worker's own folder.
const sandboxProbeScript = `
try() { if out=$("$@" 2>&1); then echo written; else echo "$out"; fi; }
{
echo "home: $(try touch "$HOME/probe")"
echo "worktree: $(try touch "$PWD/probe")"
echo "tmp: $(try touch /tmp/probe)"
echo "gitconfig: $(try touch "$COMMON/config")"
echo "hooks: $(try touch "$COMMON/hooks/probe")"
echo "dotgit: $(try sh -c 'echo x >> .git')"
if git add probe >/dev/null 2>&1 && git -c user.name=t -c user.email=t@example.com commit -qm probe >/dev/null 2>&1; then echo "commit: ok"; else echo "commit: failed"; fi
} > /tmp/result 2>&1
`

func probeResults(t *testing.T, d *Daemon, w *worker) map[string]string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(d.cfg.StateDir, "workers", w.id, "tmp", "result"))
	if err != nil {
		t.Fatalf("no result from the sandboxed harness: %v (log: %s)", err, d.workerLogTail(w))
	}
	out := map[string]string{}
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		k, v, _ := strings.Cut(line, ": ")
		out[k] = v
	}
	return out
}

// A sandboxed harness writes in its worktree, its private /tmp and the git
// folders of a commit, and nowhere else: not in the home folder, not in the
// main repository's config or hooks, not in the worktree's .git file.
func TestOSSandboxKeepsWritesInTheWorktree(t *testing.T) {
	requireBwrap(t)
	home := outsideHome(t)
	d, w, repo := sandboxWorker(t, protocol.WorkersSandboxed)
	env := workerEnv(os.Environ(), map[string]string{"HOME": home, "COMMON": filepath.Join(resolved(repo), ".git")})
	if err := d.spawnWorker(w, "/bin/sh", []string{"-c", sandboxProbeScript}, env); err != nil {
		t.Fatal(err)
	}
	waitExit(t, w)
	got := probeResults(t, d, w)
	for _, k := range []string{"home", "gitconfig", "hooks", "dotgit"} {
		if !strings.Contains(got[k], "Read-only file system") {
			t.Errorf("%s: %q, want a read-only refusal (all: %v)", k, got[k], got)
		}
	}
	for _, k := range []string{"worktree", "tmp"} {
		if got[k] != "written" {
			t.Errorf("%s: %q, want written", k, got[k])
		}
	}
	if got["commit"] != "ok" {
		t.Errorf("a commit in the worktree failed: %v", got)
	}
	if fileExists(filepath.Join(home, "probe")) {
		t.Error("the sandboxed harness wrote in the home folder")
	}
	if !fileExists(filepath.Join(w.path, "probe")) {
		t.Error("the worktree write did not reach the worktree")
	}
	if m := readOSSandboxMarker(filepath.Join(d.cfg.StateDir, "workers", w.id)); m != osSandboxName {
		t.Errorf("marker %q, want %q", m, osSandboxName)
	}
}

// The read-only level writes nothing in the project, and still has its
// private /tmp.
func TestOSSandboxReadOnlyLevel(t *testing.T) {
	requireBwrap(t)
	home := outsideHome(t)
	d, w, repo := sandboxWorker(t, protocol.WorkersReadOnly)
	env := workerEnv(os.Environ(), map[string]string{"HOME": home, "COMMON": filepath.Join(resolved(repo), ".git")})
	if err := d.spawnWorker(w, "/bin/sh", []string{"-c", sandboxProbeScript}, env); err != nil {
		t.Fatal(err)
	}
	waitExit(t, w)
	got := probeResults(t, d, w)
	for _, k := range []string{"home", "worktree", "gitconfig"} {
		if !strings.Contains(got[k], "Read-only file system") {
			t.Errorf("%s: %q, want a read-only refusal", k, got[k])
		}
	}
	if got["tmp"] != "written" || got["commit"] != "failed" {
		t.Errorf("tmp %q (want written), commit %q (want failed)", got["tmp"], got["commit"])
	}
	if fileExists(filepath.Join(w.path, "probe")) || fileExists(filepath.Join(home, "probe")) {
		t.Error("the read-only harness wrote in the worktree or the home folder")
	}
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
	t.Parallel()
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
	g := startGateway(t, t.TempDir(), "")
	set := shortSet
	set.OSSandbox = []string{"shop"}
	w := startPiNode(t, g, set)
	w.register(t, newRepo(t, "shop"))
	owner := w.ownerID()

	res := w.mustStart(t, protocol.WorkerStartRequest{Project: "shop", Harness: "pi", Model: "openai/gpt-4.1-nano:low", Task: "say ok", Isolation: protocol.IsolationWorktree})
	rec := readPi(t, res.Worktree)
	// The start result, the brief and the worker view name the wrapper.
	if res.OSSandbox != osSandboxName {
		t.Errorf("start result os_sandbox %q", res.OSSandbox)
	}
	if !strings.Contains(res.Task.Body, "(sandboxed by bubblewrap): writes outside your worktree and /tmp fail") {
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
		t.Errorf("private /tmp write: %q", rec.TmpWrite)
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
		t.Error("the private /tmp is not the worker's tmp folder")
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

// An OpenCode worker of a project with the switch on: the daemon reaches its
// private server over loopback (the network level keeps the host network),
// its plugin registers through the bound socket, and it answers.
func TestOSSandboxOpenCodeWorkerEndToEnd(t *testing.T) {
	t.Parallel()
	requireBwrap(t)
	g := startGateway(t, t.TempDir(), "")
	set := shortSet
	set.OSSandbox = []string{"shop"}
	w := startWorkerNode(t, g, set, nil)
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
