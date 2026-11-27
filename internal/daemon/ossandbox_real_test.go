package daemon

import (
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
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
	if runtime.GOOS == "linux" {
		// bubblewrap cannot bind from /dev/shm, so the test's folders go elsewhere
		// (see requireBwrap).
		t.Setenv("TMPDIR", sandboxFolder(t, "tmp"))
	}
}

// outsideHome is a folder the test can write and the sandbox can see but must
// not write.
func outsideHome(t *testing.T) string {
	t.Helper()
	return sandboxFolder(t, "home")
}

// sandboxFolder makes a folder under /var/tmp for a sandbox test and removes it
// with the test. It is not on /dev/shm: bubblewrap mounts a fresh /dev, so
// nothing there can be bound into the sandbox.
func sandboxFolder(t *testing.T, name string) string {
	t.Helper()
	d, err := os.MkdirTemp("/var/tmp", "agent-net-osbox-"+name+"-")
	if err != nil {
		t.Skipf("no /var/tmp to make a %s folder for the sandbox test: %v", name, err)
	}
	t.Cleanup(func() { os.RemoveAll(d) })
	return resolved(d)
}

// sandboxWorker makes a daemon, a repository with a linked worktree, and an
// OpenCode worker of the given level in that worktree, in the OS wrapper.
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

// sandboxHarnessCopy copies the running test binary to a folder under /var/tmp
// and returns the copy's path. The fake harnesses are this binary, and go test
// runs it from the Go temp folder, which may be on /dev/shm, out of bubblewrap's
// reach.
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

// checkWriteFence runs the fence probe as the harness of a worker in the OS
// wrapper and checks decision 0197: commit, switch -c, worktree add, a write to
// TMPDIR, a unix socket in TMPDIR and a loopback listener all work, and a
// write in a home folder outside the worktree, in the git hooks and config, or
// in another linked worktree's git folder fails.
func checkWriteFence(t *testing.T) {
	t.Helper()
	home := outsideHome(t)
	d, w, repo := sandboxWorker(t, protocol.WorkersSandboxed)
	gitIn(t, repo, "worktree", "add", "-q", "-b", "other", filepath.Join(sandboxFolder(t, "other"), "wt"))
	env := workerEnv(os.Environ(), map[string]string{envFakeFenceProbe: "1", envFakeFenceHome: home, "HOME": home})
	if err := d.spawnWorker(w, sandboxHarnessCopy(t), nil, env); err != nil {
		t.Fatal(err)
	}
	waitExit(t, w)
	raw, err := os.ReadFile(filepath.Join(d.cfg.StateDir, "workers", w.id, "tmp", "fence.json"))
	if err != nil {
		t.Fatalf("no result from the sandboxed probe: %v (log: %s)", err, d.workerLogTail(w))
	}
	var got fenceProbe
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	for name, res := range map[string]string{"commit": got.Commit, "switch -c": got.SwitchC, "TMPDIR write": got.TmpWrite,
		"unix socket in TMPDIR": got.UnixSocket, "listen on 127.0.0.1": got.Loopback, "worktree add": got.WorktreeAdd} {
		if res != "" {
			t.Errorf("%s failed in the sandbox: %s", name, res)
		}
	}
	for name, res := range map[string]string{".git/hooks/pre-commit": got.HookWrite, ".git/config": got.ConfigWrite,
		"another worktree's commondir": got.OtherWorktree} {
		if res == "" {
			t.Errorf("a write to %s worked in the sandbox; git runs code from it outside", name)
		}
	}
	if b, err := os.ReadFile(filepath.Join(repo, ".git", "config")); err != nil || strings.Contains(string(b), "fsmonitor") {
		t.Errorf("the git config was changed from the sandbox (%v):\n%s", err, b)
	}
	if got.HomeWrite == "" || fileExists(filepath.Join(home, "probe")) {
		t.Errorf("a write in the home folder outside the worktree worked: %+v", got)
	}
	if b, err := exec.Command("git", "-C", w.path, "branch", "--show-current").Output(); err != nil || strings.TrimSpace(string(b)) != "fence-probe" {
		t.Errorf("the worktree is on %q after switch -c, want fence-probe (%v)", b, err)
	}
	if m := readOSSandboxMarker(filepath.Join(d.cfg.StateDir, "workers", w.id)); m != osSandboxName {
		t.Errorf("marker %q, want %q", m, osSandboxName)
	}
}

// The write fence on the real wrapper of this OS: sandbox-exec on macOS,
// bubblewrap on Linux (which TestOSSandboxBwrapWriteFence also runs without
// the opt-in).
func TestRealSandboxWriteFence(t *testing.T) {
	requireRealSandbox(t)
	checkWriteFence(t)
}

// A DNS lookup and an HTTPS connection work inside the wrapper, so a worker can
// reach its model. On macOS the lookup goes through the system resolver, whose
// queries the profile allows through the mDNSResponder socket: that allowance
// was a guess until this check.
func TestRealSandboxNetwork(t *testing.T) {
	requireRealSandbox(t)
	d, w, _ := sandboxWorker(t, protocol.WorkersSandboxed)
	// A tool that asks the resolver the harnesses use on this OS: the system
	// resolver (mDNSResponder) on macOS, libc on Linux.
	dns := "/usr/bin/dscacheutil -q host -a name"
	if runtime.GOOS == "linux" {
		dns = "getent hosts"
	}
	script := `
try() { if out=$("$@" 2>&1); then echo "ok, $(echo "$out" | head -n 1)"; else echo "failed: $out"; fi; }
{
echo "dns: $(try ` + dns + ` github.com)"
echo "https: $(try /usr/bin/curl -fsS --max-time 10 -o /dev/null -w '%{http_code}' https://github.com/)"
} > "$TMPDIR/result" 2>&1
`
	if err := d.spawnWorker(w, "/bin/sh", []string{"-c", script}, os.Environ()); err != nil {
		t.Fatal(err)
	}
	waitExit(t, w)
	got := probeResults(t, d, w)
	t.Logf("in the sandbox: dns %s; https %s", got["dns"], got["https"])
	if dns := got["dns"]; !strings.HasPrefix(dns, "ok") || !strings.Contains(dns, "github.com") {
		t.Errorf("DNS inside the sandbox: %q (log: %s)", dns, d.workerLogTail(w))
	}
	if https := got["https"]; !strings.HasPrefix(https, "ok") {
		t.Errorf("HTTPS inside the sandbox: %q (log: %s)", https, d.workerLogTail(w))
	}
}
