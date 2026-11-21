//go:build linux

package daemon

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"

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

// sandboxHarnessCopy copies the running test binary to a folder under /var/tmp
// and returns the copy's path. The fake harnesses are this binary, and go test
// runs it from the Go temp folder, which may be on /dev/shm. The sandbox binds the
// harness by its path, so it must come from a folder the sandbox can see.
func sandboxHarnessCopy(t *testing.T) string {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	src, err := os.Open(exe)
	if err != nil {
		t.Fatal(err)
	}
	defer src.Close()
	dst := filepath.Join(sandboxFolder(t, "bin"), filepath.Base(exe))
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o755)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.Copy(out, src); err != nil {
		out.Close()
		t.Fatal(err)
	}
	if err := out.Close(); err != nil {
		t.Fatal(err)
	}
	return dst
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
	// The test binary is the fake `pi` itself, as in the OpenCode test. A
	// launcher script would not do: the test binary usually sits under /tmp,
	// which the sandbox replaces with the worker's private tmp, so a launcher
	// that execs it finds nothing there. The sandbox binds the harness binary
	// itself, so pointing at the binary keeps it visible.
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
