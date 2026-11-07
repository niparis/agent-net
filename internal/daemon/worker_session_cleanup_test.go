package daemon

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestWorkerCodexSessionCleanup(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	id := "12345678-1234-1234-1234-123456789abc"
	owner := filepath.Join(home, "owner")
	target := filepath.Join(home, id)
	os.WriteFile(owner, []byte("owner"), 0600)
	os.WriteFile(target, []byte("worker"), 0600)
	bin := filepath.Join(t.TempDir(), "codex")
	os.WriteFile(bin, []byte("#!/bin/sh\n[ \"$1\" = delete ] && [ \"$2\" = --force ] || exit 2\nrm -- \"$CODEX_HOME/$3\"\n"), 0700)
	d := &Daemon{cfg: Config{HarnessPaths: map[string]string{"codex": bin}}}
	r := workerRow{ID: "w_test", Harness: "codex", NativeID: id, EndedReason: "done"}
	if err := d.deleteWorkerHarnessSession(context.Background(), r); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Fatalf("worker remains: %v", err)
	}
	if _, err := os.Stat(owner); err != nil {
		t.Fatal(err)
	}
}

func TestWorkerSessionCleanupRefusesLiveAndRetries(t *testing.T) {
	t.Setenv("CODEX_HOME", t.TempDir())
	bin := filepath.Join(t.TempDir(), "codex")
	d := &Daemon{cfg: Config{HarnessPaths: map[string]string{"codex": bin}}}
	r := workerRow{ID: "w_test", Harness: "codex", NativeID: "12345678-1234-1234-1234-123456789abc"}
	if err := d.deleteWorkerHarnessSession(context.Background(), r); err == nil {
		t.Fatal("live worker accepted")
	}
	r.EndedReason = "done"
	if err := d.deleteWorkerHarnessSession(context.Background(), r); err == nil {
		t.Fatal("unreachable harness accepted")
	}
	os.WriteFile(bin, []byte("#!/bin/sh\nexit 0\n"), 0700)
	if err := d.deleteWorkerHarnessSession(context.Background(), r); err != nil {
		t.Fatal(err)
	}
}

func TestWorkerOpenCodeSessionCleanup(t *testing.T) {
	withFakeOpenCodeRouteSets(t, testWorkerOpenCodeSessionCleanup)
}

func testWorkerOpenCodeSessionCleanup(t *testing.T) {
	t.Setenv(envFakeOpenCode, "1")
	root := t.TempDir()
	dir := filepath.Join(root, "workers", "w_test")
	data := filepath.Join(dir, "data")
	if err := os.MkdirAll(data, 0700); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(data, "session-to-delete")
	os.WriteFile(target, []byte("worker"), 0600)
	owner := filepath.Join(root, "owner-session")
	os.WriteFile(owner, []byte("owner"), 0600)
	d := &Daemon{cfg: Config{StateDir: root, HarnessPaths: map[string]string{"opencode": os.Args[0]}}}
	r := workerRow{ID: "w_test", Harness: "opencode", NativeID: "session-to-delete", EndedReason: "done"}
	if err := d.deleteWorkerHarnessSession(context.Background(), r); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Fatalf("worker session remains: %v", err)
	}
	if _, err := os.Stat(owner); err != nil {
		t.Fatal(err)
	}
}

func TestWorkerRecordCleanupRetriesAndSetting(t *testing.T) {
	t.Setenv("CODEX_HOME", t.TempDir())
	for _, disabled := range []bool{false, true} {
		t.Run(fmt.Sprint(disabled), func(t *testing.T) {
			root := t.TempDir()
			bin := filepath.Join(root, "codex")
			st, err := openState(filepath.Join(root, "db"))
			if err != nil {
				t.Fatal(err)
			}
			defer st.close()
			enabled := !disabled
			d := &Daemon{st: st, log: log.New(io.Discard, "", 0), cfg: Config{StateDir: root, HarnessPaths: map[string]string{"codex": bin}, Workers: WorkerSettings{DeleteSessions: &enabled}}}
			r := workerRow{ID: "w_test", Harness: "codex", NativeID: "12345678-1234-1234-1234-123456789abc", EndedReason: "done", Reported: true, StartedAt: time.Now().Add(-48 * time.Hour)}
			if err := st.saveWorker(r); err != nil {
				t.Fatal(err)
			}
			d.cleanupWorkerSessionRow(context.Background(), r)
			rows, _ := st.workers()
			if disabled {
				if len(rows) != 0 {
					t.Fatal("disabled cleanup retained row")
				}
				return
			}
			if len(rows) != 1 {
				t.Fatal("failed deletion dropped retry record")
			}
			if err := os.WriteFile(bin, []byte("#!/bin/sh\nexit 0\n"), 0700); err != nil {
				t.Fatal(err)
			}
			d.cleanupWorkerSessionRow(context.Background(), r)
			rows, _ = st.workers()
			if len(rows) != 0 {
				t.Fatal("successful retry retained row")
			}
		})
	}
}

func TestWorkerCodexConfiguredHome(t *testing.T) {
	inherited, configured := t.TempDir(), t.TempDir()
	t.Setenv("CODEX_HOME", inherited)
	id := "12345678-1234-1234-1234-123456789abc"
	for _, home := range []string{inherited, configured} {
		if err := os.MkdirAll(filepath.Join(home, "sessions"), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(home, "sessions", "rollout-test-"+id+".jsonl"), []byte("worker"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	bin := filepath.Join(t.TempDir(), "codex")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\nrm -- \"$CODEX_HOME/sessions/rollout-test-$3.jsonl\"\n"), 0700); err != nil {
		t.Fatal(err)
	}
	var logs bytes.Buffer
	d := &Daemon{log: log.New(&logs, "", 0), cfg: Config{CodexHome: configured, HarnessPaths: map[string]string{"codex": bin}}}
	r := workerRow{ID: "w_test", Harness: "codex", NativeID: id, EndedReason: "done"}
	if err := d.deleteWorkerHarnessSession(context.Background(), r); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(configured, "sessions", "rollout-test-"+id+".jsonl")); !os.IsNotExist(err) {
		t.Fatalf("configured thread remains: %v", err)
	}
	if _, err := os.Stat(filepath.Join(inherited, "sessions", "rollout-test-"+id+".jsonl")); err != nil {
		t.Fatalf("inherited home touched: %v", err)
	}
	if !strings.Contains(logs.String(), "bytes_freed=6") {
		t.Fatalf("wrong home counted: %s", logs.String())
	}
}

func TestWorkerCodexDeletionFailureRetention(t *testing.T) {
	t.Setenv("CODEX_HOME", t.TempDir())
	for _, tc := range []struct {
		name, output  string
		age           time.Duration
		drop, skipped bool
	}{
		{"missing", "Error: thread not found", time.Hour, true, false},
		{"missing_with_id", "Error: session 12345678-1234-1234-1234-123456789abc not found", time.Hour, true, false},
		{"unsupported", "error: unrecognized subcommand 'delete'", time.Hour, true, true},
		{"retry", "Error: failed to delete session", time.Hour, false, false},
		{"expired", "Error: failed to delete session", 7*24*time.Hour + time.Minute, true, true},
		{"config_missing", "Error: config file not found", time.Hour, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			bin := filepath.Join(root, "codex")
			if err := os.WriteFile(bin, []byte("#!/bin/sh\nprintf '%s\\n' \""+tc.output+"\" >&2\nexit 1\n"), 0700); err != nil {
				t.Fatal(err)
			}
			st, err := openState(filepath.Join(root, "db"))
			if err != nil {
				t.Fatal(err)
			}
			defer st.close()
			var logs bytes.Buffer
			d := &Daemon{st: st, log: log.New(&logs, "", 0), cfg: Config{StateDir: root, HarnessPaths: map[string]string{"codex": bin}, Workers: WorkerSettings{MaxAge: time.Hour}}}
			r := workerRow{ID: "w_test", Harness: "codex", NativeID: "12345678-1234-1234-1234-123456789abc", EndedReason: "done", Reported: true, StartedAt: time.Now().Add(-time.Hour - tc.age)}
			if err := st.saveWorker(r); err != nil {
				t.Fatal(err)
			}
			if got := d.cleanupWorkerSessionRow(context.Background(), r); got != tc.drop {
				t.Fatalf("dropped=%v want %v; %s", got, tc.drop, logs.String())
			}
			rows, err := st.workers()
			if err != nil {
				t.Fatal(err)
			}
			if (len(rows) == 0) != tc.drop {
				t.Fatalf("unexpected retained rows: %d", len(rows))
			}
			if strings.Contains(logs.String(), "worker.session.delete_skipped") != tc.skipped {
				t.Fatalf("unexpected skip log: %s", logs.String())
			}
		})
	}
}

func TestWorkerOpenCodeDeletionRetryExpired(t *testing.T) {
	root := t.TempDir()
	st, err := openState(filepath.Join(root, "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.close()
	dir := filepath.Join(root, "workers", "w_old")
	if err := os.MkdirAll(filepath.Join(dir, "data"), 0700); err != nil {
		t.Fatal(err)
	}
	var logs bytes.Buffer
	d := &Daemon{st: st, log: log.New(&logs, "", 0), cfg: Config{StateDir: root, HarnessPaths: map[string]string{"opencode": filepath.Join(root, "missing")}, Workers: WorkerSettings{MaxAge: time.Hour}}}
	r := workerRow{ID: "w_old", Harness: "opencode", NativeID: "ses_old", EndedReason: "done", Reported: true, StartedAt: time.Now().Add(-8 * 24 * time.Hour)}
	if err := st.saveWorker(r); err != nil {
		t.Fatal(err)
	}
	// Use the current cleanup entry point; this must apply to OpenCode too.
	if !d.cleanupWorkerSessionRow(context.Background(), r) {
		t.Fatalf("expired OpenCode failure retained: %s", logs.String())
	}
	rows, err := st.workers()
	if err != nil || len(rows) != 0 {
		t.Fatalf("rows=%v err=%v", rows, err)
	}
	if !strings.Contains(logs.String(), "worker.session.delete_skipped") || !strings.Contains(logs.String(), "reason=retry_expired") {
		t.Fatal(logs.String())
	}
}

func TestWorkerCleanupDoesNotWaitForHarness(t *testing.T) {
	root := t.TempDir()
	st, err := openState(filepath.Join(root, "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.close()
	bin := filepath.Join(root, "codex")
	marker := filepath.Join(root, "started")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\necho started > \"$CODEX_HOME/started\"\nexec sleep 600\n"), 0700); err != nil {
		t.Fatal(err)
	}
	d := &Daemon{st: st, log: log.New(io.Discard, "", 0), cfg: Config{StateDir: root, CodexHome: root, HarnessPaths: map[string]string{"codex": bin}, Workers: WorkerSettings{MaxAge: time.Hour}}}
	r := workerRow{ID: "w_hang", Harness: "codex", NativeID: "12345678-1234-1234-1234-123456789abc", EndedReason: "done", Reported: true, StartedAt: time.Now().Add(-2 * time.Hour)}
	if err := st.saveWorker(r); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	passDone := make(chan struct{})
	go func() { defer close(passDone); d.cleanupWorkerSessions(ctx) }()
	defer func() { cancel(); <-passDone }()
	eventually(t, "harness deletion started", func() bool { _, err := os.Stat(marker); return err == nil })
	done := make(chan struct{})
	go func() { d.cleanupWorkers(ctx); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("cleanupWorkers blocked on harness deletion")
	}
	rows, err := st.workers()
	if err != nil || len(rows) != 1 {
		t.Fatalf("hanging deletion lost row: %v %v", rows, err)
	}
	// A second pass must also return immediately while the first is hanging.
	again := make(chan struct{})
	go func() { d.cleanupWorkerSessions(ctx); close(again) }()
	select {
	case <-again:
	case <-time.After(time.Second):
		t.Fatal("overlapping cleanup pass blocked")
	}
}

func TestWorkerSessionCleanupPassBudget(t *testing.T) {
	root := t.TempDir()
	st, err := openState(filepath.Join(root, "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.close()
	d := &Daemon{st: st, log: log.New(io.Discard, "", 0), cfg: Config{StateDir: root, Workers: WorkerSettings{MaxAge: time.Hour}}}
	for i := 0; i < 12; i++ {
		r := workerRow{ID: fmt.Sprintf("w_%02d", i), Harness: "claude", EndedReason: "done", Reported: true, StartedAt: time.Now().Add(-2 * time.Hour)}
		if err := st.saveWorker(r); err != nil {
			t.Fatal(err)
		}
	}
	d.cleanupWorkerSessions(context.Background())
	rows, err := st.workers()
	if err != nil || len(rows) != 2 {
		t.Fatalf("budget left %d rows: %v", len(rows), err)
	}
	d.cleanupWorkerSessions(context.Background())
	rows, err = st.workers()
	if err != nil || len(rows) != 0 {
		t.Fatalf("next pass left %d rows: %v", len(rows), err)
	}
}

func TestWorkerSessionCleanupSafetyNeverExpires(t *testing.T) {
	for _, tc := range []string{"live", "registered", "invalid_worker", "invalid_thread"} {
		t.Run(tc, func(t *testing.T) {
			root := t.TempDir()
			st, err := openState(filepath.Join(root, "db"))
			if err != nil {
				t.Fatal(err)
			}
			defer st.close()
			d := &Daemon{st: st, log: log.New(io.Discard, "", 0), cfg: Config{StateDir: root, Workers: WorkerSettings{MaxAge: time.Hour}}}
			r := workerRow{ID: "w_safe", Harness: "codex", NativeID: "12345678-1234-1234-1234-123456789abc", EndedReason: "done", Reported: true, StartedAt: time.Now().Add(-9 * 24 * time.Hour)}
			switch tc {
			case "live":
				r.EndedReason = ""
			case "registered":
				d.workers = map[string]*worker{r.ID: {}}
			case "invalid_worker":
				r.ID = ".."
			case "invalid_thread":
				r.NativeID = "invalid"
			}
			if err := st.saveWorker(r); err != nil {
				t.Fatal(err)
			}
			if d.cleanupWorkerSessionRow(context.Background(), r) {
				t.Fatal("safety refusal expired")
			}
			rows, err := st.workers()
			if err != nil || len(rows) != 1 {
				t.Fatalf("safety row dropped: %v %v", rows, err)
			}
		})
	}
}

func TestWorkerSessionCleanupPassMovesPastFailures(t *testing.T) {
	root := t.TempDir()
	st, err := openState(filepath.Join(root, "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.close()
	d := &Daemon{st: st, log: log.New(io.Discard, "", 0), cfg: Config{StateDir: root, Workers: WorkerSettings{MaxAge: time.Hour}}}
	for i := 0; i < 12; i++ {
		r := workerRow{ID: fmt.Sprintf("w_%02d", i), Harness: "claude", EndedReason: "done", Reported: true, StartedAt: time.Now().Add(-2 * time.Hour).Add(time.Duration(i) * time.Second)}
		if i < 10 {
			r.Harness = "codex"
			r.NativeID = "invalid"
		}
		if err := st.saveWorker(r); err != nil {
			t.Fatal(err)
		}
	}
	d.cleanupWorkerSessions(context.Background())
	rows, err := st.workers()
	if err != nil || len(rows) != 12 {
		t.Fatalf("first pass left %d rows: %v", len(rows), err)
	}
	d.cleanupWorkerSessions(context.Background())
	rows, err = st.workers()
	if err != nil || len(rows) != 10 {
		t.Fatalf("later workers starved: %d rows: %v", len(rows), err)
	}
}
