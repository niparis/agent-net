package daemon

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/harnesses"
)

// roomy is a /dev/shm tmpfs with more room than the minimum.
func roomy() shmInfo { return shmInfo{tmpfs: true, free: 8 << 30} }

// useGoTmpFolders points the Go temp folders and the RAM TMPDIR at temp folders
// of the test, so no test makes a folder under the real /dev/shm or /tmp.
func useGoTmpFolders(t *testing.T) (shm, disk string) {
	t.Helper()
	shm, disk = filepath.Join(t.TempDir(), "shm"), filepath.Join(t.TempDir(), "disk")
	prevShm, prevDisk, prevShmTmp := gotmpShmDir, gotmpDiskDir, gotmpShmTmpDir
	gotmpShmDir, gotmpDiskDir = shm, disk
	gotmpShmTmpDir = filepath.Join(t.TempDir(), "shm-tmp")
	t.Cleanup(func() { gotmpShmDir, gotmpDiskDir, gotmpShmTmpDir = prevShm, prevDisk, prevShmTmp })
	return shm, disk
}

// Pi and OpenCode workers run with the host /dev/shm, so their TMPDIR is the RAM
// folder when it has room. The sandboxed harnesses keep the TMPDIR they set.
func TestWorkerToolEnvPicksTheTmpDirPerHarness(t *testing.T) {
	useGoTmpFolders(t)
	t.Setenv("AGENT_NET_GOTMPDIR", "")
	for _, tc := range []struct {
		name    string
		harness string
		shm     shmInfo
		want    string
	}{
		{"pi with room", "pi", roomy(), gotmpShmTmpDir},
		{"opencode with room", "opencode", roomy(), gotmpShmTmpDir},
		{"pi without room", "pi", shmInfo{tmpfs: true, free: 1 << 20}, ""},
		{"claude with room", "claude", roomy(), ""},
		{"codex with room", "codex", roomy(), ""},
		{"agy with room", "agy", roomy(), ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := commitTestDaemon(t)
			d.cfg.WorkerGoTmp = ""
			d.cfg.testShm = func() shmInfo { return tc.shm }
			d.cfg.HarnessSearch = &harnesses.Options{LoginShellPATH: func(context.Context) string { return "" }}
			w := envWorker(t, d)
			w.harness = tc.harness
			got := d.workerToolEnv(w)["TMPDIR"]
			if got != tc.want {
				t.Fatalf("TMPDIR = %q, want %q", got, tc.want)
			}
			if tc.want != "" {
				if fi, err := os.Stat(got); err != nil || !fi.IsDir() {
					t.Fatalf("TMPDIR folder not made: %v", err)
				}
			}
		})
	}
}

// An owner or test that names a Go temp folder keeps the TMPDIR the daemon
// already had, so no run reaches the real /dev/shm through it.
func TestWorkerToolEnvLeavesTMPDIRAloneForAnExplicitGoTmp(t *testing.T) {
	useGoTmpFolders(t)
	t.Setenv("AGENT_NET_GOTMPDIR", "")
	d := commitTestDaemon(t)
	d.cfg.WorkerGoTmp = filepath.Join(t.TempDir(), "named")
	d.cfg.testShm = func() shmInfo { return roomy() }
	d.cfg.HarnessSearch = &harnesses.Options{LoginShellPATH: func(context.Context) string { return "" }}
	w := envWorker(t, d)
	w.harness = "pi"
	if got := d.workerToolEnv(w)["TMPDIR"]; got != "" {
		t.Fatalf("TMPDIR = %q with a named Go temp folder, want none", got)
	}
}

// The hourly cleanup removes old run folders from the RAM TMPDIR too, but only
// when no Go temp folder is named, the same condition that sets it.
func TestCleanWorkerGoTmpCleansTheRAMTmpDir(t *testing.T) {
	useGoTmpFolders(t)
	now := time.Now()
	oldTime := now.Add(-10 * time.Hour)
	makeOld := func(root string) string {
		sub := filepath.Join(root, "run_old")
		if err := os.MkdirAll(sub, 0o700); err != nil {
			t.Fatal(err)
		}
		file := filepath.Join(sub, "test.bin")
		if err := os.WriteFile(file, []byte("data"), 0o600); err != nil {
			t.Fatal(err)
		}
		_ = os.Chtimes(file, oldTime, oldTime)
		_ = os.Chtimes(sub, oldTime, oldTime)
		return sub
	}
	named := filepath.Join(t.TempDir(), "named")
	for _, tc := range []struct {
		name     string
		explicit string
		removed  bool
	}{
		{"no named folder", "", true},
		{"named folder", named, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("AGENT_NET_GOTMPDIR", "")
			sub := makeOld(gotmpShmTmpDir)
			d := &Daemon{cfg: Config{WorkerGoTmp: tc.explicit, testShm: func() shmInfo { return roomy() }}}
			d.cleanWorkerGoTmp(now, 6*time.Hour)
			_, err := os.Stat(sub)
			if tc.removed && !os.IsNotExist(err) {
				t.Fatalf("old run folder %s kept: %v", sub, err)
			}
			if !tc.removed && err != nil {
				t.Fatalf("old run folder %s removed with a named Go temp folder: %v", sub, err)
			}
		})
	}
}

func TestDefaultGoTmpDirUsesRAMOnlyWhenItHasRoom(t *testing.T) {
	shm, disk := useGoTmpFolders(t)
	for _, tc := range []struct {
		name string
		shm  shmInfo
		want string
	}{
		{"tmpfs with room", roomy(), shm},
		{"tmpfs with exactly the minimum", shmInfo{tmpfs: true, free: gotmpShmMinFree}, shm},
		{"tmpfs one byte short", shmInfo{tmpfs: true, free: gotmpShmMinFree - 1}, disk},
		{"not a tmpfs", shmInfo{tmpfs: false, free: 8 << 30}, disk},
		{"no /dev/shm to probe", shmInfo{}, disk},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := defaultGoTmpDir(tc.shm); got != tc.want {
				t.Fatalf("defaultGoTmpDir(%+v) = %s, want %s", tc.shm, got, tc.want)
			}
		})
	}
}

// Each worker start reads /dev/shm again, so a tmpfs that fills up moves the
// next worker to the disk folder.
func TestWorkerGoTmpDirFollowsTheRoomAtEachCall(t *testing.T) {
	shm, disk := useGoTmpFolders(t)
	t.Setenv("AGENT_NET_GOTMPDIR", "")
	probes := []shmInfo{roomy(), {tmpfs: true, free: 1 << 20}}
	d := &Daemon{cfg: Config{testShm: func() shmInfo {
		p := probes[0]
		probes = probes[1:]
		return p
	}}}
	if got := d.workerGoTmpDir(); got != shm {
		t.Fatalf("first worker: %s, want %s", got, shm)
	}
	if got := d.workerGoTmpDir(); got != disk {
		t.Fatalf("second worker: %s, want %s", got, disk)
	}
}

func TestExplicitGoTmpDirWinsOverRAM(t *testing.T) {
	useGoTmpFolders(t)
	d := &Daemon{cfg: Config{WorkerGoTmp: "/configured/gotmp", testShm: func() shmInfo { return roomy() }}}
	if got := d.workerGoTmpDir(); got != "/configured/gotmp" {
		t.Fatalf("config value lost: %s", got)
	}
	if got := d.workerSandboxGoTmpDir(); got != "/configured/gotmp" {
		t.Fatalf("config value lost for a sandbox: %s", got)
	}
	t.Setenv("AGENT_NET_GOTMPDIR", "/env/gotmp")
	d.cfg.WorkerGoTmp = ""
	if got := d.workerGoTmpDir(); got != "/env/gotmp" {
		t.Fatalf("AGENT_NET_GOTMPDIR lost: %s", got)
	}
}

// A worker's shell gets a Go temp folder it can see. A sandbox that mounts its
// own /dev cannot see the RAM folder the daemon makes, so those harnesses get
// the disk folder. The folder exists before the first go command runs in it.
func TestWorkerToolEnvPicksTheGoTempFolderPerHarness(t *testing.T) {
	shm, disk := useGoTmpFolders(t)
	t.Setenv("AGENT_NET_GOTMPDIR", "")
	for _, tc := range []struct{ harness, want string }{
		{"claude", disk},
		{"codex", disk},
		{"agy", disk},
		{"pi", shm},
		{"opencode", shm},
	} {
		t.Run(tc.harness, func(t *testing.T) {
			d := commitTestDaemon(t)
			d.cfg.WorkerGoTmp = ""
			d.cfg.testShm = func() shmInfo { return roomy() }
			d.cfg.HarnessSearch = &harnesses.Options{LoginShellPATH: func(context.Context) string { return "" }}
			w := envWorker(t, d)
			w.harness = tc.harness
			got := d.workerToolEnv(w)["GOTMPDIR"]
			if got != tc.want {
				t.Fatalf("GOTMPDIR = %q, want %q", got, tc.want)
			}
			if fi, err := os.Stat(got); err != nil || !fi.IsDir() {
				t.Fatalf("Go temp folder not made: %v", err)
			}
		})
	}
}

// Under the OS wrapper a worker's Go temp files stay in its private /tmp. The
// host's /dev/shm is not in the wrapper's view.
func TestOSSandboxEnvPutsGoTempInThePrivateTmp(t *testing.T) {
	set := osSandboxSetEnv("/tmp")
	if set["GOTMPDIR"] != "/tmp" || set["TMPDIR"] != "/tmp" {
		t.Fatalf("GOTMPDIR = %q, TMPDIR = %q, want /tmp for both", set["GOTMPDIR"], set["TMPDIR"])
	}
	if want := filepath.Join("/tmp", "cache", "uv"); set["UV_CACHE_DIR"] != want {
		t.Fatalf("UV_CACHE_DIR = %q, want %q", set["UV_CACHE_DIR"], want)
	}
}

// A sandboxed Claude or Codex worker may write the folder its shell uses, which
// is the disk folder, even when the RAM folder has room.
func TestSandboxedWorkersMayWriteTheDiskGoTempFolder(t *testing.T) {
	_, disk := useGoTmpFolders(t)
	t.Setenv("AGENT_NET_GOTMPDIR", "")
	d := &Daemon{cfg: Config{StateDir: t.TempDir(), testShm: func() shmInfo { return roomy() }}}
	w := &worker{id: "w_codex", path: t.TempDir()}
	if writes := d.claudeWorkerWrites(w.path); !hasString(writes, disk) {
		t.Fatalf("Claude writable folders %v lack the disk Go temp folder %s", writes, disk)
	}
	if roots := d.codexRootsFor(context.Background(), w); !hasString(roots, disk) {
		t.Fatalf("Codex writable roots %v lack the disk Go temp folder %s", roots, disk)
	}
}

// A Codex worker's sandbox writes the Go temp folder the owner names, because
// its scripts use it when the daemon does not set GOTMPDIR.
func TestCodexWorkerCanWriteTheGoTempFolder(t *testing.T) {
	goTmp := filepath.Join(t.TempDir(), "gotmp")
	d := &Daemon{cfg: Config{StateDir: t.TempDir(), WorkerGoTmp: goTmp}}
	w := &worker{id: "w_codex", path: t.TempDir()}
	roots := d.codexRootsFor(context.Background(), w)
	if !hasString(roots, goTmp) {
		t.Fatalf("writable roots %v lack the Go temp folder %s", roots, goTmp)
	}
	if fi, err := os.Stat(goTmp); err != nil || !fi.IsDir() {
		t.Fatalf("Go temp folder not made: %v", err)
	}
}

// The hourly cleanup empties old test folders in both Go temp folders, since
// an unsandboxed worker and a sandboxed one use different folders.
func TestGoTmpCleanupRemovesOldFoldersInBothFolders(t *testing.T) {
	shm, disk := useGoTmpFolders(t)
	t.Setenv("AGENT_NET_GOTMPDIR", "")
	now := time.Now()
	old := now.Add(-10 * time.Hour)
	var olds []string
	for _, root := range []string{shm, disk} {
		sub := filepath.Join(root, "run_old")
		if err := os.MkdirAll(sub, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(sub, old, old); err != nil {
			t.Fatal(err)
		}
		olds = append(olds, sub)
	}
	d := &Daemon{cfg: Config{StateDir: t.TempDir(), testShm: func() shmInfo { return roomy() }}}
	d.cleanWorkerGoTmp(now, 6*time.Hour)
	for _, sub := range olds {
		if _, err := os.Stat(sub); !os.IsNotExist(err) {
			t.Errorf("old test folder %s kept: %v", sub, err)
		}
	}
}
