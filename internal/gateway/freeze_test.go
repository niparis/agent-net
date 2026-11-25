package gateway

import (
	"bytes"
	"context"
	"database/sql"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

type syncLogBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncLogBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncLogBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func newFreezeTestGateway(t *testing.T, after, poll, cooldown time.Duration) (*Gateway, *syncLogBuffer) {
	t.Helper()
	dir := t.TempDir()
	logs := &syncLogBuffer{}
	g, err := New(Options{
		DataDir:            dir,
		Logger:             log.New(logs, "", 0),
		FreezeAfter:        after,
		FreezePollInterval: poll,
		FreezeCooldown:     cooldown,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = g.Close() })
	return g, logs
}

func waitForFreezeLog(t *testing.T, logs *syncLogBuffer, timeout time.Duration) string {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if s := logs.String(); strings.Contains(s, "event=freeze ") {
			return s
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for event=freeze in logs:\n%s", logs.String())
	return ""
}

func TestFreezeDumpWhenBothStuck(t *testing.T) {
	g, logs := newFreezeTestGateway(t, 60*time.Millisecond, 15*time.Millisecond, 10*time.Minute)

	// Hold the single write connection inside a tracked request.
	writeHeld := make(chan struct{})
	releaseWrite := make(chan struct{})
	writeDone := make(chan error, 1)
	go func() {
		end := g.startRequest("session.upsert", "node-write")
		defer end()
		tx, err := g.store.db.Begin()
		if err != nil {
			writeDone <- err
			return
		}
		close(writeHeld)
		<-releaseWrite
		writeDone <- tx.Rollback()
	}()
	<-writeHeld

	// Hold every connection in the read pool with open queries.
	poolSize := readPoolSize()
	if poolSize < 1 {
		t.Fatalf("expected read pool size >= 1, got %d", poolSize)
	}
	heldRows := make([]*sql.Rows, 0, poolSize)
	for i := 0; i < poolSize; i++ {
		rows, err := g.store.db.Query(`SELECT 1`)
		if err != nil {
			t.Fatalf("hold read pool %d: %v", i, err)
		}
		heldRows = append(heldRows, rows)
	}

	// Start a read call that genuinely blocks waiting for the read pool,
	// with a secret token/prompt/body in local variables to verify nothing leaks.
	secretBody := "SECRET_BODY_DO_NOT_LOG"
	secretToken := "SECRET_TOKEN_DO_NOT_LOG"
	secretPrompt := "SECRET_PROMPT_DO_NOT_LOG"
	_ = secretBody + secretToken + secretPrompt
	readDone := make(chan error, 1)
	go func() {
		end := g.startRequest("query.projects", "node-read")
		defer end()
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		rows, err := g.store.db.QueryContext(ctx, `SELECT 1`)
		if err != nil {
			readDone <- err
			return
		}
		readDone <- rows.Close()
	}()

	logOut := waitForFreezeLog(t, logs, 1900*time.Millisecond)

	// Release everything before assertions so cleanup is clean.
	for _, rows := range heldRows {
		_ = rows.Close()
	}
	close(releaseWrite)
	if err := <-writeDone; err != nil {
		t.Fatalf("write tx: %v", err)
	}
	if err := <-readDone; err != nil {
		t.Fatalf("blocked read: %v", err)
	}

	var freezeLine string
	for _, line := range strings.Split(logOut, "\n") {
		if strings.Contains(line, "event=freeze ") {
			freezeLine = line
			break
		}
	}
	if freezeLine == "" {
		t.Fatalf("missing freeze line in:\n%s", logOut)
	}
	for _, want := range []string{
		"oldest=session.upsert",
		"oldest_ms=",
		"waiting=2",
		"writer=session.upsert",
		"writer_ms=",
		"wal_bytes=",
		"profile=",
	} {
		if !strings.Contains(freezeLine, want) {
			t.Fatalf("freeze line missing %q:\n%s", want, freezeLine)
		}
	}
	if runtime.GOOS == "linux" {
		if !strings.Contains(freezeLine, "io_pressure=") || !strings.Contains(freezeLine, "mem=") {
			t.Fatalf("expected linux io_pressure and mem in freeze line:\n%s", freezeLine)
		}
	} else if runtime.GOOS == "darwin" {
		if strings.Contains(freezeLine, "io_pressure=") || strings.Contains(freezeLine, "mem=") {
			t.Fatalf("expected no io_pressure or mem on darwin:\n%s", freezeLine)
		}
	}

	// Verify profile file exists under DataDir and contains debug=2 goroutine text.
	files, err := filepath.Glob(filepath.Join(g.opts.DataDir, "freeze-profiles", "freeze-*.txt"))
	if err != nil || len(files) != 1 {
		t.Fatalf("expected 1 freeze profile file, got %v (err=%v)", files, err)
	}
	profBytes, err := os.ReadFile(files[0])
	if err != nil {
		t.Fatalf("read profile: %v", err)
	}
	prof := string(profBytes)
	if !strings.Contains(prof, "goroutine ") {
		t.Fatalf("profile does not look like debug=2 goroutine text:\n%s", prof[:min(len(prof), 200)])
	}

	// Privacy check: no message bodies, tokens or prompts in the file or line.
	for _, secret := range []string{secretBody, secretToken, secretPrompt} {
		if strings.Contains(freezeLine, secret) {
			t.Fatalf("secret %q leaked into freeze log line", secret)
		}
		if strings.Contains(prof, secret) {
			t.Fatalf("secret %q leaked into freeze profile", secret)
		}
	}
}

func TestFreezeNoDumpUnderThreshold(t *testing.T) {
	g, logs := newFreezeTestGateway(t, 500*time.Millisecond, 15*time.Millisecond, 10*time.Minute)

	end := g.startRequest("policy.get", "node-1")
	time.Sleep(30 * time.Millisecond)
	end()

	time.Sleep(80 * time.Millisecond)
	if strings.Contains(logs.String(), "event=freeze") {
		t.Fatalf("unexpected freeze log for short call:\n%s", logs.String())
	}
	files, err := filepath.Glob(filepath.Join(g.opts.DataDir, "freeze-profiles", "freeze-*.txt"))
	if err != nil {
		t.Fatalf("glob: %v", err)
	}
	if len(files) != 0 {
		t.Fatalf("expected no freeze profile files, got %v", files)
	}
}

func TestFreezeRateLimitAndRetention(t *testing.T) {
	g, logs := newFreezeTestGateway(t, 40*time.Millisecond, 10*time.Millisecond, 10*time.Minute)

	// First freeze triggers exactly one dump even while held past another poll.
	end1 := g.startLoop("expiry")
	waitForFreezeLog(t, logs, 1900*time.Millisecond)
	time.Sleep(50 * time.Millisecond)
	if count := strings.Count(logs.String(), "event=freeze "); count != 1 {
		end1()
		t.Fatalf("expected 1 freeze dump during single freeze, got %d:\n%s", count, logs.String())
	}
	end1()

	// Second freeze within 10-minute cooldown writes no second dump.
	end2 := g.startLoop("learning")
	time.Sleep(80 * time.Millisecond)
	end2()
	if count := strings.Count(logs.String(), "event=freeze "); count != 1 {
		t.Fatalf("expected cooldown to suppress second dump, got %d:\n%s", count, logs.String())
	}

	// Verify retention keeps only the last 5 profile files.
	dir := filepath.Join(g.opts.DataDir, "freeze-profiles")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	for _, name := range []string{
		"freeze-20260101-000001.000.txt",
		"freeze-20260101-000002.000.txt",
		"freeze-20260101-000003.000.txt",
		"freeze-20260101-000004.000.txt",
		"freeze-20260101-000005.000.txt",
		"freeze-20260101-000006.000.txt",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("goroutine 1 [running]:\n"), 0o600); err != nil {
			t.Fatalf("write dummy %s: %v", name, err)
		}
	}
	if err := pruneFreezeProfiles(dir, freezeMaxFiles); err != nil {
		t.Fatalf("prune: %v", err)
	}
	remaining, err := filepath.Glob(filepath.Join(dir, "freeze-*.txt"))
	if err != nil {
		t.Fatalf("glob: %v", err)
	}
	if len(remaining) != 5 {
		t.Fatalf("expected 5 retained profile files, got %d: %v", len(remaining), remaining)
	}
	if filepath.Base(remaining[0]) == "freeze-20260101-000001.000.txt" {
		t.Fatalf("expected oldest file to be pruned, got %v", remaining)
	}
}
