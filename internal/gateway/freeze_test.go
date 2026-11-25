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

	"github.com/niparis/agent-net/internal/sqlitecache"
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
	g, logs := newFreezeTestGateway(t, 100*time.Millisecond, 15*time.Millisecond, 10*time.Minute)

	// Hold every connection in the read pool with open queries before starting
	// the tracked calls so setup time does not race FreezeAfter.
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

	// Start a read call that genuinely blocks waiting for the read pool,
	// with a secret token/prompt/body in local variables to verify nothing leaks.
	secretBody := "SECRET_BODY_DO_NOT_LOG"
	secretToken := "SECRET_TOKEN_DO_NOT_LOG"
	secretPrompt := "SECRET_PROMPT_DO_NOT_LOG"
	_ = secretBody + secretToken + secretPrompt
	readStarted := make(chan struct{})
	readDone := make(chan error, 1)
	go func() {
		end := g.startRequest("query.projects", "node-read")
		defer end()
		close(readStarted)
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		rows, err := g.store.db.QueryContext(ctx, `SELECT 1`)
		if err != nil {
			readDone <- err
			return
		}
		readDone <- rows.Close()
	}()
	<-readStarted

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
		"writer=gateway.TestFreezeDumpWhenBothStuck",
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
		for _, field := range []string{"io_pressure=", "mem="} {
			idx := strings.Index(freezeLine, field)
			rest := freezeLine[idx:]
			for _, sub := range []string{"some ", "full ", "total="} {
				if !strings.Contains(rest, sub) {
					t.Fatalf("expected %q in %s:\n%s", sub, field, freezeLine)
				}
			}
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

func TestFreezeRelayDoesNotTriggerDump(t *testing.T) {
	g, logs := newFreezeTestGateway(t, 40*time.Millisecond, 10*time.Millisecond, 10*time.Minute)

	// A relay held past FreezeAfter must not trigger a dump or consume cooldown.
	endRelay := g.startWait("query.proposal_list", "node-relay")
	defer endRelay()
	time.Sleep(80 * time.Millisecond)
	if strings.Contains(logs.String(), "event=freeze") {
		t.Fatalf("relay should not trigger freeze dump:\n%s", logs.String())
	}

	// An answered frame over the limit while the relay is still running must trigger
	// the dump (proving cooldown was not consumed) and include the relay in waiting.
	endFrame := g.startRequest("session.upsert", "node-frame")
	defer endFrame()
	logOut := waitForFreezeLog(t, logs, 1900*time.Millisecond)
	if !strings.Contains(logOut, "waiting=2") {
		t.Fatalf("expected waiting=2 (relay + frame), got:\n%s", logOut)
	}
	if !strings.Contains(logOut, "oldest=query.proposal_list") {
		t.Fatalf("expected oldest=query.proposal_list, got:\n%s", logOut)
	}
}

func TestFreezeLogBeforeFileWrite(t *testing.T) {
	g, logs := newFreezeTestGateway(t, 40*time.Millisecond, 10*time.Millisecond, 10*time.Minute)

	writeStarted := make(chan struct{})
	releaseWrite := make(chan struct{})
	writeDone := make(chan error, 1)
	var writtenPath string
	g.freeze.writeProfile = func(path string, data []byte) error {
		writtenPath = path
		close(writeStarted)
		<-releaseWrite
		err := writeFreezeProfileFile(path, data)
		writeDone <- err
		return err
	}

	end := g.startRequest("msg.send", "node-1")
	defer end()

	// Log line must appear BEFORE the file write unblocks, and carry the profile path.
	logOut := waitForFreezeLog(t, logs, 1900*time.Millisecond)
	<-writeStarted
	if !strings.Contains(logOut, "wal_bytes=") || !strings.Contains(logOut, "profile="+writtenPath) {
		close(releaseWrite)
		<-writeDone
		t.Fatalf("expected wal_bytes and profile path in log before file write:\n%s", logOut)
	}
	if _, err := os.Stat(writtenPath); !os.IsNotExist(err) {
		close(releaseWrite)
		<-writeDone
		t.Fatalf("expected profile file not to exist while writer is blocked, stat err=%v", err)
	}

	close(releaseWrite)
	if err := <-writeDone; err != nil {
		t.Fatalf("writeProfile: %v", err)
	}
	prof, err := os.ReadFile(writtenPath)
	if err != nil {
		t.Fatalf("read written profile: %v", err)
	}
	if !strings.Contains(string(prof), "goroutine ") {
		t.Fatalf("expected goroutine dump in %s", writtenPath)
	}
}

func TestFreezeNestedCallsKeepOuterTracked(t *testing.T) {
	g, logs := newFreezeTestGateway(t, 50*time.Millisecond, 10*time.Millisecond, 10*time.Minute)

	endOuter := g.startRequest("outer.frame", "node-1")
	endInner := g.startLoop("inner.loop")
	endInner()

	g.freeze.mu.Lock()
	remaining := len(g.freeze.active)
	g.freeze.mu.Unlock()
	if remaining != 1 {
		endOuter()
		t.Fatalf("expected outer call to remain tracked after inner ended, got %d", remaining)
	}

	logOut := waitForFreezeLog(t, logs, 1900*time.Millisecond)
	endOuter()
	if !strings.Contains(logOut, "oldest=outer.frame") || !strings.Contains(logOut, "waiting=1") {
		t.Fatalf("expected outer.frame with waiting=1, got:\n%s", logOut)
	}
}

func TestFreezeWriterHolderConcurrentAndEdgeCases(t *testing.T) {
	g, _ := newFreezeTestGateway(t, 10*time.Second, time.Second, 10*time.Minute)

	var wg sync.WaitGroup
	for i := 0; i < 6; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			ctx := context.Background()

			// Normal Exec.
			if _, err := g.store.db.ExecContext(ctx, `INSERT OR IGNORE INTO gateway_state (key, value) VALUES (?, 'v')`, "k1"); err != nil {
				t.Errorf("exec: %v", err)
			}

			// BeginTx + Commit.
			tx, err := g.store.db.BeginTx(ctx, nil)
			if err != nil {
				t.Errorf("begin commit: %v", err)
				return
			}
			_, _ = tx.ExecContext(ctx, `INSERT OR IGNORE INTO gateway_state (key, value) VALUES (?, 'v')`, "k2")
			if err := tx.Commit(); err != nil {
				t.Errorf("commit: %v", err)
			}

			// BeginTx + Rollback.
			tx2, err := g.store.db.BeginTx(ctx, nil)
			if err != nil {
				t.Errorf("begin rollback: %v", err)
				return
			}
			_, _ = tx2.ExecContext(ctx, `INSERT OR IGNORE INTO gateway_state (key, value) VALUES (?, 'v')`, "k3")
			if err := tx2.Rollback(); err != nil {
				t.Errorf("rollback: %v", err)
			}

			// Text BEGIN and COMMIT on a pinned writer connection.
			conn, err := g.store.db.w.Conn(ctx)
			if err != nil {
				t.Errorf("conn: %v", err)
				return
			}
			if _, err := conn.ExecContext(ctx, `BEGIN`); err != nil {
				t.Errorf("text BEGIN: %v", err)
			}
			_, _ = conn.ExecContext(ctx, `INSERT OR IGNORE INTO gateway_state (key, value) VALUES (?, 'v')`, "k4")
			if _, err := conn.ExecContext(ctx, `COMMIT`); err != nil {
				t.Errorf("text COMMIT: %v", err)
			}
			_ = conn.Close()

			// Rows closed late on writer connection.
			rows, err := g.store.db.w.QueryContext(ctx, `SELECT key FROM gateway_state LIMIT 1`)
			if err != nil {
				t.Errorf("query writer: %v", err)
			} else {
				time.Sleep(2 * time.Millisecond)
				_ = rows.Close()
			}

			// Errors in Exec and Query.
			_, _ = g.store.db.ExecContext(ctx, `INVALID SQL STATEMENT`)
			if r, qerr := g.store.db.w.QueryContext(ctx, `SELECT * FROM nonexistent_table_xyz`); qerr == nil {
				_ = r.Close()
			}
		}(i)
	}
	wg.Wait()

	g.freeze.mu.Lock()
	depth := g.freeze.wDepth
	pcsLen := g.freeze.wPCsLen
	since := g.freeze.wSince
	g.freeze.mu.Unlock()
	if depth != 0 || pcsLen != 0 || !since.IsZero() {
		t.Fatalf("expected empty writer holder after all ops, got depth=%d pcsLen=%d since=%v", depth, pcsLen, since)
	}
}

func TestFreezeWriterNamesCaller(t *testing.T) {
	extractWriter := func(t *testing.T, logOut string) string {
		t.Helper()
		for _, line := range strings.Split(logOut, "\n") {
			if !strings.Contains(line, "event=freeze ") {
				continue
			}
			for _, field := range strings.Fields(line) {
				if strings.HasPrefix(field, "writer=") {
					return strings.TrimPrefix(field, "writer=")
				}
			}
		}
		t.Fatalf("missing writer= in freeze log:\n%s", logOut)
		return ""
	}

	checkWriter := func(t *testing.T, writer string) {
		t.Helper()
		if writer == "unknown" || writer == "none" || writer == "" {
			t.Fatalf("expected resolved writer name, got %q", writer)
		}
		for _, bad := range []string{"storeDB", "sqlitecache", "database/sql", "call.one", "call.two", "loop.three"} {
			if strings.Contains(writer, bad) {
				t.Fatalf("writer %q must not be wrapper or active call (%q)", writer, bad)
			}
		}
		if !strings.Contains(writer, "TestFreezeWriterNamesCaller") {
			t.Fatalf("expected writer %q to name TestFreezeWriterNamesCaller", writer)
		}
	}

	t.Run("Begin", func(t *testing.T) {
		g, logs := newFreezeTestGateway(t, 40*time.Millisecond, 10*time.Millisecond, 10*time.Minute)
		defer g.startRequest("call.one", "n1")()
		defer g.startRequest("call.two", "n2")()
		defer g.startLoop("loop.three")()

		tx, err := g.store.db.Begin()
		if err != nil {
			t.Fatalf("Begin: %v", err)
		}
		defer func() { _ = tx.Rollback() }()

		logOut := waitForFreezeLog(t, logs, 1900*time.Millisecond)
		checkWriter(t, extractWriter(t, logOut))
	})

	t.Run("Exec", func(t *testing.T) {
		g, logs := newFreezeTestGateway(t, 40*time.Millisecond, 10*time.Millisecond, 10*time.Minute)
		defer g.startRequest("call.one", "n1")()
		defer g.startRequest("call.two", "n2")()
		defer g.startLoop("loop.three")()

		started := make(chan struct{})
		release := make(chan struct{})
		g.store.connector.SetObserver(func(_ sqlitecache.Statement) {
			g.store.connector.SetObserver(nil)
			close(started)
			<-release
		})

		execDone := make(chan error, 1)
		go func() {
			_, err := g.store.db.Exec(`INSERT OR IGNORE INTO gateway_state (key, value) VALUES ('k_exec', 'v')`)
			execDone <- err
		}()
		<-started

		logOut := waitForFreezeLog(t, logs, 1900*time.Millisecond)
		close(release)
		if err := <-execDone; err != nil {
			t.Fatalf("Exec: %v", err)
		}
		checkWriter(t, extractWriter(t, logOut))
	})

	t.Run("Query", func(t *testing.T) {
		g, logs := newFreezeTestGateway(t, 40*time.Millisecond, 10*time.Millisecond, 10*time.Minute)
		defer g.startRequest("call.one", "n1")()
		defer g.startRequest("call.two", "n2")()
		defer g.startLoop("loop.three")()

		rows, err := g.store.db.Query(`INSERT INTO gateway_state (key, value) VALUES ('k_query', 'v') ON CONFLICT(key) DO UPDATE SET value=excluded.value RETURNING key`)
		if err != nil {
			t.Fatalf("Query: %v", err)
		}
		defer func() { _ = rows.Close() }()

		logOut := waitForFreezeLog(t, logs, 1900*time.Millisecond)
		checkWriter(t, extractWriter(t, logOut))
	})
}

func BenchmarkFreezeRequest(b *testing.B) {
	g := &Gateway{freeze: newFreezeTracker(Options{})}
	b.Run("tracked", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			end := g.startRequest("policy.get", "node-1")
			end()
		}
	})
	gNil := &Gateway{}
	b.Run("untracked", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			end := gNil.startRequest("policy.get", "node-1")
			end()
		}
	})
}

func BenchmarkFreezeWriterStatement(b *testing.B) {
	run := func(b *testing.B, withHooks bool) {
		dir := b.TempDir()
		g, err := New(Options{DataDir: dir, FreezeAfter: time.Hour, FreezePollInterval: time.Hour})
		if err != nil {
			b.Fatalf("New: %v", err)
		}
		defer g.Close()
		if !withHooks {
			g.store.connector.SetWriterHooks(nil, nil)
		}
		ctx := context.Background()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			if _, err := g.store.db.ExecContext(ctx, `UPDATE gateway_state SET value = 'v' WHERE key = 'nonexistent'`); err != nil {
				b.Fatalf("exec: %v", err)
			}
		}
		b.StopTimer()
	}
	b.Run("with_hooks", func(b *testing.B) { run(b, true) })
	b.Run("without_hooks", func(b *testing.B) { run(b, false) })
}
