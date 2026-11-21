package daemon

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/harnesses"
)

// roomy is a /dev/shm tmpfs with more room than the minimum.
func roomy() shmInfo { return shmInfo{tmpfs: true, free: 8 << 30} }

func TestDefaultGoTmpDirUsesRAMOnlyWhenItHasRoom(t *testing.T) {
	for _, tc := range []struct {
		name string
		shm  shmInfo
		want string
	}{
		{"tmpfs with room", roomy(), gotmpShmDir},
		{"tmpfs with exactly the minimum", shmInfo{tmpfs: true, free: gotmpShmMinFree}, gotmpShmDir},
		{"tmpfs one byte short", shmInfo{tmpfs: true, free: gotmpShmMinFree - 1}, gotmpDiskDir},
		{"not a tmpfs", shmInfo{tmpfs: false, free: 8 << 30}, gotmpDiskDir},
		{"no /dev/shm to probe", shmInfo{}, gotmpDiskDir},
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
	t.Setenv("AGENT_NET_GOTMPDIR", "")
	probes := []shmInfo{roomy(), {tmpfs: true, free: 1 << 20}}
	d := &Daemon{cfg: Config{testShm: func() shmInfo {
		p := probes[0]
		probes = probes[1:]
		return p
	}}}
	if got := d.workerGoTmpDir(); got != gotmpShmDir {
		t.Fatalf("first worker: %s, want %s", got, gotmpShmDir)
	}
	if got := d.workerGoTmpDir(); got != gotmpDiskDir {
		t.Fatalf("second worker: %s, want %s", got, gotmpDiskDir)
	}
}

func TestExplicitGoTmpDirWinsOverRAM(t *testing.T) {
	d := &Daemon{cfg: Config{WorkerGoTmp: "/configured/gotmp", testShm: func() shmInfo { return roomy() }}}
	if got := d.workerGoTmpDir(); got != "/configured/gotmp" {
		t.Fatalf("config value lost: %s", got)
	}
	t.Setenv("AGENT_NET_GOTMPDIR", "/env/gotmp")
	d.cfg.WorkerGoTmp = ""
	if got := d.workerGoTmpDir(); got != "/env/gotmp" {
		t.Fatalf("AGENT_NET_GOTMPDIR lost: %s", got)
	}
}

// A worker's shell gets the folder as GOTMPDIR, and the folder exists before the
// first go command runs in it.
func TestWorkerToolEnvSetsGOTMPDIR(t *testing.T) {
	d := commitTestDaemon(t)
	d.cfg.HarnessSearch = &harnesses.Options{LoginShellPATH: func(context.Context) string { return "" }}
	w := envWorker(t, d)
	got := d.workerToolEnv(w)["GOTMPDIR"]
	if want := d.workerGoTmpDir(); got != want {
		t.Fatalf("GOTMPDIR = %q, want %q", got, want)
	}
	if fi, err := os.Stat(got); err != nil || !fi.IsDir() {
		t.Fatalf("Go temp folder not made: %v", err)
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

// The Go temp folder is written by a merger's go test, so a merger's Claude
// settings name it, next to the shared build cache.
func TestMergerSettingsNameTheGoTempFolder(t *testing.T) {
	settings := mergerClaudeSettings("/cache", "/shm/gotmp")
	if !strings.Contains(settings, `"GOTMPDIR":"/shm/gotmp"`) || !strings.Contains(settings, `"GOCACHE":"/cache"`) {
		t.Fatalf("merger settings = %s", settings)
	}
}

// A Codex worker's sandbox writes the Go temp folder, because its scripts use
// it when the daemon does not set GOTMPDIR.
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
