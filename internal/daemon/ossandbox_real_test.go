package daemon

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// The real sandbox check, on Linux (bubblewrap) and macOS (sandbox-exec). It
// runs a real shell inside the wrapper, so it is opt-in: set
// AGENT_NET_TEST_REAL_SANDBOX=1. It never touches the real setup: the daemon
// state and the worktree are temporary.

func requireRealSandbox(t *testing.T) {
	t.Helper()
	if os.Getenv("AGENT_NET_TEST_REAL_SANDBOX") != "1" {
		t.Skip("set AGENT_NET_TEST_REAL_SANDBOX=1 to run the real OS sandbox")
	}
	if err := osSandboxReady(); err != nil {
		t.Fatalf("AGENT_NET_TEST_REAL_SANDBOX=1, but the sandbox cannot run here: %v", err)
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

// A worker's write outside its worktree fails, and a write inside works. The
// script reports to $TMPDIR/result, which is the worker's private tmp folder
// on both OSes.
func TestRealSandboxWriteBoundary(t *testing.T) {
	requireRealSandbox(t)
	outside := outsideHome(t)
	d, w, _ := sandboxWorker(t, protocol.WorkersSandboxed)
	script := `
try() { if out=$("$@" 2>&1); then echo written; else echo "$out"; fi; }
{
echo "outside: $(try touch "$OUTSIDE/probe")"
echo "inside: $(try touch "$PWD/probe")"
} > "$TMPDIR/result" 2>&1
`
	env := workerEnv(os.Environ(), map[string]string{"OUTSIDE": outside})
	if err := d.spawnWorker(w, "/bin/sh", []string{"-c", script}, env); err != nil {
		t.Fatal(err)
	}
	waitExit(t, w)
	got := probeResults(t, d, w)
	if got["inside"] != "written" || !fileExists(filepath.Join(w.path, "probe")) {
		t.Errorf("a write inside the worktree failed: %v", got)
	}
	if got["outside"] == "written" || fileExists(filepath.Join(outside, "probe")) {
		t.Errorf("a write outside the worktree worked: %v", got)
	}
}
