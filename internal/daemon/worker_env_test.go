package daemon

import (
	"context"
	"crypto/sha1"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/harnesses"
	"github.com/niparis/agent-net/internal/protocol"
)

// fakeLoginShell is a login shell (named sh, as the lookup requires a known
// shell) that prints noise as a profile would, counts its runs, and answers the
// PATH probe with a PATH of its own. It sets $SHELL to it.
func fakeLoginShell(t *testing.T, path string) (shell, counter string) {
	t.Helper()
	dir := t.TempDir()
	counter = filepath.Join(dir, "runs")
	shell = filepath.Join(dir, "sh")
	script := fmt.Sprintf("#!/bin/sh\necho run >> %s\necho 'profile noise'\nPATH=%s exec /bin/sh -c \"$3\"\n", testShellQuote(counter), testShellQuote(path))
	if err := os.WriteFile(shell, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SHELL", shell)
	return shell, counter
}

func envWorker(t *testing.T, d *Daemon) *worker {
	t.Helper()
	return &worker{id: protocol.NewID("w"), harness: "opencode", project: "shop"}
}

func TestWorkerPATHHasTheLoginShellPATH(t *testing.T) {
	_, counter := fakeLoginShell(t, "/opt/fake-homebrew/bin:/opt/fake-go/bin:/usr/bin")
	t.Setenv("PATH", "/usr/bin:/bin")
	d := commitTestDaemon(t)
	w := envWorker(t, d)
	got := d.workerToolEnv(w)["PATH"]
	if want := "/usr/bin:/bin:/opt/fake-homebrew/bin:/opt/fake-go/bin"; got != want {
		t.Fatalf("PATH = %q, want %q", got, want)
	}
	// Found once per daemon start, not once per worker.
	d.workerToolEnv(envWorker(t, d))
	runs, _ := os.ReadFile(counter)
	if n := strings.Count(string(runs), "run"); n != 1 {
		t.Fatalf("the login shell ran %d times, want 1", n)
	}
}

func TestWorkerPATHKeepsTheDaemonPATHWhenTheLoginShellFails(t *testing.T) {
	t.Setenv("PATH", "/usr/bin:/bin")
	t.Setenv("SHELL", "/nonexistent/sh")
	d := commitTestDaemon(t)
	if got := d.workerToolEnv(envWorker(t, d))["PATH"]; got != "/usr/bin:/bin" {
		t.Fatalf("PATH = %q", got)
	}
}

func TestWorkerCachesAreInTheWorkersOwnFolder(t *testing.T) {
	d := commitTestDaemon(t)
	d.cfg.HarnessSearch = &harnesses.Options{LoginShellPATH: func(context.Context) string { return "" }}
	w := envWorker(t, d)
	env := d.workerToolEnv(w)
	root := filepath.Join(d.cfg.StateDir, "workers", w.id, "cache")
	for key, sub := range map[string]string{"UV_CACHE_DIR": "uv", "npm_config_cache": "npm", "PIP_CACHE_DIR": "pip"} {
		if got, want := env[key], filepath.Join(root, sub); got != want {
			t.Errorf("%s = %q, want %q", key, got, want)
		}
	}
	// The folder is made, so a sandbox that lists it as writable can bind it.
	if fi, err := os.Stat(root); err != nil || !fi.IsDir() {
		t.Fatalf("cache folder not made: %v", err)
	}
	// The daemon removes workers/<id> with the worker, which takes the caches.
	if !strings.HasPrefix(root, filepath.Join(d.cfg.StateDir, "workers", w.id)+string(filepath.Separator)) {
		t.Fatal("the caches are outside the worker's folder")
	}
}

func TestWorkerCachesMoveIntoThePrivateTmpInTheWrapper(t *testing.T) {
	if _, err := exec.LookPath("bwrap"); err != nil && runtime.GOOS == "linux" {
		t.Log("no bwrap here: the wrapper's command is not run, only its environment is read")
	}
	d := commitTestDaemon(t)
	repo := newRepo(t, "shop")
	wt := filepath.Join(d.cfg.StateDir, "worktrees", "w_x")
	gitIn(t, repo, "worktree", "add", "-q", "-b", "agent-net/w_x", wt)
	w := &worker{id: "w_x", harness: "opencode", project: "shop", path: wt, permissions: protocol.WorkersSandboxed, osSandbox: osSandboxName}
	if _, err := osSandboxTool(); err != nil {
		t.Skip("no sandbox tool here")
	}
	_, _, env, err := d.osSandboxWrap(w, "/bin/true", nil, os.Environ())
	if err != nil {
		t.Fatal(err)
	}
	have := map[string]string{}
	for _, kv := range env {
		k, v, _ := strings.Cut(kv, "=")
		have[k] = v
	}
	tmp := osSandboxTmpEnv(filepath.Join(resolved(filepath.Join(d.cfg.StateDir, "workers", "w_x")), "tmp"))
	for key, sub := range map[string]string{"UV_CACHE_DIR": "uv", "npm_config_cache": "npm", "PIP_CACHE_DIR": "pip"} {
		if want := filepath.Join(tmp, "cache", sub); have[key] != want {
			t.Errorf("%s = %q, want %q", key, have[key], want)
		}
	}
	if have["TMPDIR"] != tmp {
		t.Errorf("TMPDIR = %q, want %q", have["TMPDIR"], tmp)
	}
}

func TestWorkerUsesTheSystemTrustStore(t *testing.T) {
	bundle := filepath.Join(t.TempDir(), "ca.pem")
	if err := os.WriteFile(bundle, []byte("-----BEGIN CERTIFICATE-----\nx\n-----END CERTIFICATE-----\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	old := systemCABundles
	systemCABundles = []string{"/nonexistent/ca.pem", bundle}
	t.Cleanup(func() { systemCABundles = old })
	oldKeychain := keychainBundle
	keychainBundle = func(context.Context) ([]byte, error) { return nil, fmt.Errorf("no keychain") }
	t.Cleanup(func() { keychainBundle = oldKeychain })
	t.Setenv("PIP_CERT", "")
	t.Setenv("SSL_CERT_FILE", "")
	d := commitTestDaemon(t)
	d.cfg.HarnessSearch = &harnesses.Options{LoginShellPATH: func(context.Context) string { return "" }}
	env := d.workerToolEnv(envWorker(t, d))
	if env["UV_NATIVE_TLS"] != "1" {
		t.Errorf("UV_NATIVE_TLS = %q", env["UV_NATIVE_TLS"])
	}
	for _, key := range []string{"PIP_CERT", "SSL_CERT_FILE"} {
		if env[key] != bundle {
			t.Errorf("%s = %q, want %q", key, env[key], bundle)
		}
	}
}

func TestWorkerKeepsTheOwnersCertificateChoice(t *testing.T) {
	bundle := filepath.Join(t.TempDir(), "ca.pem")
	if err := os.WriteFile(bundle, []byte("pem"), 0o644); err != nil {
		t.Fatal(err)
	}
	old := systemCABundles
	systemCABundles = []string{bundle}
	t.Cleanup(func() { systemCABundles = old })
	t.Setenv("PIP_CERT", "/owner/pip.pem")
	t.Setenv("SSL_CERT_FILE", "/owner/ssl.pem")
	d := commitTestDaemon(t)
	d.cfg.HarnessSearch = &harnesses.Options{LoginShellPATH: func(context.Context) string { return "" }}
	env := d.workerToolEnv(envWorker(t, d))
	if _, ok := env["PIP_CERT"]; ok {
		t.Errorf("PIP_CERT was replaced: %q", env["PIP_CERT"])
	}
	if _, ok := env["SSL_CERT_FILE"]; ok {
		t.Errorf("SSL_CERT_FILE was replaced: %q", env["SSL_CERT_FILE"])
	}
}

func TestWorkerGetsItsOwnPortRange(t *testing.T) {
	d := commitTestDaemon(t)
	d.workers = map[string]*worker{}
	var bases []int
	for i := 0; i < 3; i++ {
		w := envWorker(t, d)
		d.workers[w.id] = w
		base := d.workerPortBase(w)
		if base < workerPortFirst || base+workerPortCount-1 > workerPortLast {
			t.Fatalf("base %d is outside the pool", base)
		}
		if again := d.workerPortBase(w); again != base {
			t.Fatalf("the range of a worker changed from %d to %d", base, again)
		}
		bases = append(bases, base)
	}
	for i := range bases {
		for j := range bases {
			if i != j && bases[i] < bases[j]+workerPortCount && bases[j] < bases[i]+workerPortCount {
				t.Fatalf("ranges %d and %d overlap", bases[i], bases[j])
			}
		}
	}
	env := d.workerToolEnv(d.workers[firstKey(d.workers)])
	if env["WORKER_PORT_BASE"] == "" {
		t.Fatal("WORKER_PORT_BASE is not set")
	}
}

func firstKey(m map[string]*worker) string {
	for k := range m {
		return k
	}
	return ""
}

// A range with a listener on it is skipped, so a worker never gets a port that
// something else holds.
func TestWorkerPortRangeSkipsABusyPort(t *testing.T) {
	d := commitTestDaemon(t)
	d.workers = map[string]*worker{}
	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", workerPortFirst+7))
	if err != nil {
		t.Skipf("port %d is in use already: %v", workerPortFirst+7, err)
	}
	defer ln.Close()
	w := envWorker(t, d)
	d.workers[w.id] = w
	if base := d.workerPortBase(w); base == workerPortFirst {
		t.Fatalf("got the range %d, which has a busy port", base)
	}
}

// The worker's environment must stop git from running gc or maintenance on its
// own: they take gc.pid.lock and pack refs in the shared .git, which the
// worker's sandbox keeps read-only. The triggers are run by name (git gc --auto
// and git maintenance run --auto, which is what git commit calls), so the test
// does not depend on how one git version decides to start a gc after a commit.
// The repository asks for a gc whenever there are loose objects: git counts the
// objects in objects/17, and two are over the limit in every version.
func TestWorkerCommitRunsNoAutoGc(t *testing.T) {
	d := commitTestDaemon(t)
	w, dir := commitTestWorker(t, d)
	main := w.mainPath
	version := gitWithEnv(t, dir, nil, "--version")
	gitIn(t, main, "config", "gc.auto", "1")
	gitIn(t, main, "config", "gc.autoPackLimit", "1")
	// gc must finish before the test looks at the result.
	gitIn(t, main, "config", "gc.autoDetach", "false")
	gitIn(t, main, "config", "maintenance.autoDetach", "false")
	plantLooseObjectsIn17(t, main, 2)
	// A gc leaves a pack or packed refs in the shared .git.
	gcTraces := func() []string {
		var found []string
		packs, _ := filepath.Glob(filepath.Join(main, ".git", "objects", "pack", "*.pack"))
		found = append(found, packs...)
		for _, f := range []string{"packed-refs", "gc.pid", "gc.log"} {
			if _, err := os.Stat(filepath.Join(main, ".git", f)); err == nil {
				found = append(found, f)
			}
		}
		return found
	}
	if before := gcTraces(); len(before) != 0 {
		t.Fatalf("the repository starts with gc traces %v", before)
	}
	// The same triggers in both arms: a commit, then both auto commands.
	trigger := func(env map[string]string, name string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(name), 0o644); err != nil {
			t.Fatal(err)
		}
		fakeHarnessCommit(t, dir, env, "add "+name)
		gitWithEnv(t, dir, env, "gc", "--auto")
		gitWithEnv(t, dir, env, "maintenance", "run", "--auto")
	}
	trigger(d.workerCommitEnv(w), "a.txt")
	if traces := gcTraces(); len(traces) != 0 {
		t.Fatalf("%s ran gc or maintenance in the worker's environment: %v", version, traces)
	}
	// Control: the same repository and the same commands without the worker's
	// environment do gc.
	trigger(nil, "b.txt")
	if len(gcTraces()) == 0 {
		t.Fatalf("%s: the control arm did not run gc, so the test proves nothing", version)
	}
}

// plantLooseObjectsIn17 writes n real blobs whose id starts with 17. git
// estimates the loose objects of a repository from that one folder.
func plantLooseObjectsIn17(t *testing.T, repo string, n int) {
	t.Helper()
	for i, planted := 0, 0; planted < n; i++ {
		content := fmt.Sprintf("loose object %d", i)
		sum := sha1.Sum([]byte(fmt.Sprintf("blob %d\x00%s", len(content), content)))
		if sum[0] != 0x17 {
			continue
		}
		cmd := exec.Command("git", "hash-object", "-w", "--stdin")
		cmd.Dir = repo
		cmd.Env = gitTestEnv(nil)
		cmd.Stdin = strings.NewReader(content)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git hash-object: %v\n%s", err, out)
		}
		planted++
	}
}

func TestWorkerCommitWorksWithAReadOnlySharedGit(t *testing.T) {
	d := commitTestDaemon(t)
	w, dir := commitTestWorker(t, d)
	main := w.mainPath
	gitIn(t, main, "config", "gc.auto", "1")
	gitIn(t, main, "config", "gc.autoPackLimit", "1")
	// What the worker's sandbox leaves writable: objects, refs, logs and its own
	// worktree folder. The rest of the shared .git (the folder itself, config,
	// packed-refs, gc.pid) is read-only.
	shared := filepath.Join(main, ".git")
	if err := os.Chmod(shared, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(shared, 0o755) })
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("a"), 0o644); err != nil {
		t.Fatal(err)
	}
	fakeHarnessCommit(t, dir, d.workerCommitEnv(w), "commit with a read-only shared .git")
	for _, f := range []string{"gc.pid", "gc.log", "packed-refs"} {
		if _, err := os.Stat(filepath.Join(shared, f)); err == nil {
			t.Errorf("the worker's commit wrote %s in the shared .git", f)
		}
	}
}

// The daemon sets origin/<branch> as the upstream, so the worker can push
// without -u, which writes the shared config that its sandbox keeps read-only.
func TestWorkerBranchPushesWithoutDashU(t *testing.T) {
	d := commitTestDaemon(t)
	repo := newRepo(t, "shop")
	origin := filepath.Join(t.TempDir(), "origin.git")
	gitIn(t, filepath.Dir(origin), "init", "-q", "--bare", origin)
	gitIn(t, repo, "remote", "add", "origin", origin)
	w := &worker{id: protocol.NewID("w"), harness: "codex", project: "shop", path: repo, owner: "s_x", sessionID: "s_y"}
	if err := d.createWorktree(context.Background(), w); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(w.worktree, "a.txt"), []byte("a"), 0o644); err != nil {
		t.Fatal(err)
	}
	fakeHarnessCommit(t, w.worktree, d.workerCommitEnv(w), "work")
	// The shared config cannot be written, as in the worker's sandbox.
	cfg := filepath.Join(repo, ".git", "config")
	if err := os.Chmod(cfg, 0o444); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(cfg, 0o644) })
	gitWithEnv(t, w.worktree, nil, "push", "-q")
	if got := gitWithEnv(t, w.worktree, nil, "rev-parse", "--abbrev-ref", "@{upstream}"); strings.TrimSpace(got) != "origin/"+w.branch {
		t.Fatalf("upstream = %q, want origin/%s", got, w.branch)
	}
}

func TestWorkerBranchWithoutOriginGetsNoUpstreamConfig(t *testing.T) {
	d := commitTestDaemon(t)
	w, _ := commitTestWorker(t, d)
	if out, err := exec.Command("git", "-C", w.mainPath, "config", "--get", "branch."+w.branch+".remote").CombinedOutput(); err == nil {
		t.Fatalf("a branch without an origin got an upstream: %s", out)
	}
}

// A Claude worker's shell reads its environment from the launch settings, so
// the tool environment must be there, and its sandbox must be able to write the
// caches that the environment names.
func TestClaudeWorkerGetsTheToolEnvironmentAndCacheWrite(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	c := startClaudeNode(t, g, shortSet)
	repo := newRepo(t, "shop")
	c.register(t, repo)
	res := c.mustStart(t, protocol.WorkerStartRequest{Project: "shop", Harness: "claude", Model: "fake/m1",
		Task: "FAKE:ignore hold the turn", Isolation: protocol.IsolationWorktree})
	rec := readClaude(t, res.Worktree)
	env, ok := rec.Settings["env"].(map[string]any)
	if !ok {
		t.Fatalf("no env in the launch settings: %v", rec.Settings)
	}
	cache, _ := env["UV_CACHE_DIR"].(string)
	if cache == "" || env["WORKER_PORT_BASE"] == nil || env["PATH"] == nil {
		t.Fatalf("tool environment missing from the launch settings: %v", env)
	}
	files := rec.Settings["sandbox"].(map[string]any)["filesystem"].(map[string]any)
	writes := stringList(t, files["allowWrite"], "sandbox.filesystem.allowWrite")
	if !hasString(writes, filepath.Dir(cache)) {
		t.Errorf("the sandbox cannot write the cache folder %s: %v", filepath.Dir(cache), writes)
	}
}
