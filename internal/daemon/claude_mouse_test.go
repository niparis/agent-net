package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/claudesettings"
	"github.com/niparis/agent-net/internal/protocol"
)

// mouseTestDaemon is a daemon that records CTO intake lines instead of calling
// the gateway.
func mouseTestDaemon(t *testing.T, lines *[]string) *Daemon {
	t.Helper()
	d := &Daemon{log: log.New(io.Discard, "", 0)}
	d.cfg.Hostname = "research"
	d.cfg.testGatewayCall = func(_ context.Context, typ string, params any) (json.RawMessage, bool, error) {
		if typ != "intake.report" {
			return nil, false, nil
		}
		*lines = append(*lines, params.(protocol.IntakeReport).Text)
		return json.RawMessage(`{"ok":true}`), true, nil
	}
	return d
}

func fakeProcessEnv(t *testing.T, env ...string) {
	t.Helper()
	old := processEnviron
	processEnviron = func(int) []string { return env }
	t.Cleanup(func() { processEnviron = old })
}

func fakeMouseStart(t *testing.T, at time.Time) {
	t.Helper()
	old := mouseProcView
	mouseProcView = func(pid int) (procView, error) { return procView{PID: pid, Started: at}, nil }
	t.Cleanup(func() { mouseProcView = old })
}

func TestClaudeMouseSettingMergesAndReapplies(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")
	if err := os.WriteFile(path, []byte(`{"theme":"dark","env":{"OTHER":"kept","CLAUDE_CODE_DISABLE_MOUSE":"1"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	d := &Daemon{log: log.New(io.Discard, "", 0)}
	d.cfg.claudeSettingsPath = path
	d.ensureClaudeMouseSetting(context.Background())
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	env, _ := doc["env"].(map[string]any)
	if doc["theme"] != "dark" || env["OTHER"] != "kept" || env[claudesettings.MouseKey] != claudesettings.MouseOn {
		t.Fatalf("merged file: %s", raw)
	}
	// Drift is repaired on the next pass.
	drifted := strings.Replace(string(raw), `"0"`, `"1"`, 1)
	if err := os.WriteFile(path, []byte(drifted), 0o600); err != nil {
		t.Fatal(err)
	}
	d.ensureClaudeMouseSetting(context.Background())
	value, present, err := claudesettings.MouseValue(path)
	if err != nil || !present || value != claudesettings.MouseOn {
		t.Fatalf("after drift value=%q present=%v err=%v", value, present, err)
	}
}

func TestClaudeMouseSettingLeavesInvalidJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")
	bad := []byte("{ not json ")
	if err := os.WriteFile(path, bad, 0o600); err != nil {
		t.Fatal(err)
	}
	var lines []string
	d := mouseTestDaemon(t, &lines)
	d.cfg.claudeSettingsPath = path
	d.ensureClaudeMouseSetting(context.Background())
	got, err := os.ReadFile(path)
	if err != nil || string(got) != string(bad) {
		t.Fatalf("file changed: %q %v", got, err)
	}
	if len(lines) != 1 || !strings.Contains(lines[0], "is not valid JSON") {
		t.Fatalf("report %v", lines)
	}
}

func TestClaudeMouseDriftReportsLiveSessionsOnceADay(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	path := claudesettings.File()
	if _, err := claudesettings.EnsureMouseOn(path); err != nil {
		t.Fatal(err)
	}
	// The session started before the file was written, so Claude did not read
	// the user setting; its inherited environment still decides.
	fakeMouseStart(t, time.Now().Add(-time.Hour))
	fakeProcessEnv(t, "CLAUDE_CODE_DISABLE_MOUSE=1")

	var lines []string
	d := mouseTestDaemon(t, &lines)
	d.sessions = map[string]*localSession{
		key("claude", "abc"): {info: protocol.SessionInfo{Harness: "claude", NativeID: "abc", Name: "garden", PID: os.Getpid()}},
		key("claude", "def"): {info: protocol.SessionInfo{Harness: "claude", NativeID: "def", Name: "shop", PID: os.Getpid()}},
		// The CTO keeps its own launch settings, so it is never reported.
		key("claude", "cto"): {info: protocol.SessionInfo{Harness: "claude", NativeID: "cto", Name: "agent-net-cto", PID: os.Getpid()}},
	}
	d.claudeCTO.session = "cto"
	d.checkClaudeMouseSessions(context.Background(), time.Now())
	if len(lines) != 2 {
		t.Fatalf("reports = %v", lines)
	}
	want := "garden on research: wheel off (mouse tracking disabled); it scrolls after its next restart"
	if lines[0] != want && lines[1] != want {
		t.Fatalf("reports = %v, want %q", lines, want)
	}
	d.checkClaudeMouseSessions(context.Background(), time.Now())
	if len(lines) != 2 {
		t.Fatalf("repeated a report the same day: %v", lines)
	}
}

func TestClaudeMouseDriftIgnoresSessionsThatReadTheSetting(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	path := claudesettings.File()
	if _, err := claudesettings.EnsureMouseOn(path); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	// The session started after the file was written, so Claude applied "0"
	// even though the inherited environment still has "1".
	fakeMouseStart(t, info.ModTime().Add(time.Minute))
	fakeProcessEnv(t, "CLAUDE_CODE_DISABLE_MOUSE=1")

	var lines []string
	d := mouseTestDaemon(t, &lines)
	d.sessions = map[string]*localSession{
		key("claude", "abc"): {info: protocol.SessionInfo{Harness: "claude", NativeID: "abc", Name: "garden", PID: os.Getpid()}},
	}
	d.checkClaudeMouseSessions(context.Background(), time.Now())
	if len(lines) != 0 {
		t.Fatalf("reported a session that is fine: %v", lines)
	}
}

func TestClaudeMouseServiceRestartDecision(t *testing.T) {
	cases := []struct {
		name string
		env  string
		live int
		want claudeMouseAction
	}{
		{"no live sessions", "1", 0, claudeMouseRestartService},
		{"one live session", "1", 1, claudeMouseWaitService},
		{"several live sessions", "1", 3, claudeMouseWaitService},
		{"value on", "0", 0, claudeMouseNothing},
		{"unset", "", 0, claudeMouseNothing},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := claudeMouseServiceAction(c.env, c.live); got != c.want {
				t.Fatalf("value %q with %d live sessions: got %v, want %v", c.env, c.live, got, c.want)
			}
		})
	}
}

func TestClaudeMouseServiceWaitsWithLiveSessions(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", dir)
	rosterDir := filepath.Join(dir, "daemon")
	if err := os.MkdirAll(rosterDir, 0o700); err != nil {
		t.Fatal(err)
	}
	// A fresh legacy roster: the daemon trusts it by its write time. A
	// supervisor pid would name a real process that the daemon verifies, and
	// this test only fakes the service in the process list.
	writeRoster := func(workers string) {
		t.Helper()
		raw := `{"updatedAt":` + fmt.Sprint(time.Now().UnixMilli()) + `,"workers":` + workers + `}`
		if err := os.WriteFile(filepath.Join(rosterDir, "roster.json"), []byte(raw), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	oldList, oldArgs := processList, processArgs
	processList = func() []int { return []int{1234} }
	processArgs = func(pid int) []string {
		if pid == 1234 {
			return []string{"/home/someone/.local/bin/claude", "daemon", "run"}
		}
		return nil
	}
	oldStop := claudeDaemonStop
	stops := 0
	claudeDaemonStop = func(context.Context, string) error {
		stops++
		return nil
	}
	t.Cleanup(func() {
		processList, processArgs = oldList, oldArgs
		claudeDaemonStop = oldStop
	})
	fakeProcessEnv(t, "CLAUDE_CODE_DISABLE_MOUSE=1")

	// One live session: the service is left alone and the owner is told.
	writeRoster(`{"abc":{"pid":` + fmt.Sprint(os.Getpid()) + `}}`)
	var lines []string
	d := mouseTestDaemon(t, &lines)
	d.cfg.HarnessPaths = map[string]string{"claude": "/bin/true"}
	d.checkClaudeMouseService(context.Background(), time.Now())
	if stops != 0 {
		t.Fatalf("stopped a service with a live session")
	}
	if len(lines) != 1 || !strings.Contains(lines[0], "will be restarted when it has no live sessions") {
		t.Fatalf("reports = %v", lines)
	}

	// The same day, the last session ends: the service is stopped now, and the
	// report it already made does not hide the stop.
	writeRoster(`{}`)
	d.checkClaudeMouseService(context.Background(), time.Now())
	if stops != 1 {
		t.Fatalf("stops = %d, want 1", stops)
	}
	if len(lines) != 2 || !strings.Contains(lines[1], "it was stopped") {
		t.Fatalf("reports = %v", lines)
	}
	// A repeated pass does not stop the same service twice a day.
	d.checkClaudeMouseService(context.Background(), time.Now())
	if stops != 1 {
		t.Fatalf("stops = %d after a repeated pass, want 1", stops)
	}
}

func TestClaudeMouseServiceDoesNotStopWithoutAFreshRoster(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", dir)
	rosterDir := filepath.Join(dir, "daemon")
	if err := os.MkdirAll(rosterDir, 0o700); err != nil {
		t.Fatal(err)
	}
	// An old legacy roster that lists no session proves nothing: a session may
	// have started since. The service is reported and left alone.
	raw := `{"updatedAt":` + fmt.Sprint(time.Now().Add(-time.Hour).UnixMilli()) + `,"workers":{}}`
	if err := os.WriteFile(filepath.Join(rosterDir, "roster.json"), []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	oldList, oldArgs := processList, processArgs
	processList = func() []int { return []int{1234} }
	processArgs = func(pid int) []string {
		if pid == 1234 {
			return []string{"/home/someone/.local/bin/claude", "daemon", "run"}
		}
		return nil
	}
	oldStop := claudeDaemonStop
	stops := 0
	claudeDaemonStop = func(context.Context, string) error {
		stops++
		return nil
	}
	t.Cleanup(func() {
		processList, processArgs = oldList, oldArgs
		claudeDaemonStop = oldStop
	})
	fakeProcessEnv(t, "CLAUDE_CODE_DISABLE_MOUSE=1")

	var lines []string
	d := mouseTestDaemon(t, &lines)
	d.cfg.HarnessPaths = map[string]string{"claude": "/bin/true"}
	d.checkClaudeMouseService(context.Background(), time.Now())
	if stops != 0 {
		t.Fatalf("stopped a service without a fresh roster")
	}
	if len(lines) != 1 || !strings.Contains(lines[0], "will be restarted when it has no live sessions") {
		t.Fatalf("reports = %v", lines)
	}
}

func TestClaudeMouseFindsTheBackgroundService(t *testing.T) {
	oldList, oldArgs := processList, processArgs
	processList = func() []int { return []int{10, 11, 12} }
	processArgs = func(pid int) []string {
		switch pid {
		case 10:
			return []string{"/home/someone/.local/bin/claude", "daemon", "run", "--origin", "transient"}
		case 11:
			return []string{"/home/someone/.local/bin/claude", "daemon", "stop", "--any"}
		default:
			return []string{"claude", "--bg"}
		}
	}
	t.Cleanup(func() { processList, processArgs = oldList, oldArgs })
	got := claudeDaemonServicePIDs()
	if len(got) != 1 || got[0] != 10 {
		t.Fatalf("service pids = %v", got)
	}
}

func TestClaudeMouseDriftPendingSentWhenGatewayLinkUp(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	path := claudesettings.File()
	if _, err := claudesettings.EnsureMouseOn(path); err != nil {
		t.Fatal(err)
	}
	fakeMouseStart(t, time.Now().Add(-time.Hour))
	fakeProcessEnv(t, "CLAUDE_CODE_DISABLE_MOUSE=1")

	var (
		mu     sync.Mutex
		lines  []string
		linkUp bool
	)
	d := &Daemon{log: log.New(io.Discard, "", 0)}
	d.cfg.Hostname = "research"
	d.cfg.testGatewayCall = func(_ context.Context, typ string, params any) (json.RawMessage, bool, error) {
		if typ != "intake.report" {
			return nil, false, nil
		}
		mu.Lock()
		defer mu.Unlock()
		if !linkUp {
			return nil, true, errNotConnected
		}
		lines = append(lines, params.(protocol.IntakeReport).Text)
		return json.RawMessage(`{"ok":true}`), true, nil
	}
	d.sessions = map[string]*localSession{
		key("claude", "abc"): {info: protocol.SessionInfo{Harness: "claude", NativeID: "abc", Name: "garden", PID: os.Getpid()}},
	}

	// First drift check with the gateway link down: the Desk note cannot be sent yet.
	d.checkClaudeMouseSessions(context.Background(), time.Now())
	mu.Lock()
	if len(lines) != 0 {
		mu.Unlock()
		t.Fatalf("reports before gateway link up: %v", lines)
	}
	mu.Unlock()

	// Link is up afterwards: the pending Desk note must be sent.
	mu.Lock()
	linkUp = true
	mu.Unlock()

	eventually(t, "intake report sent once gateway link is up", func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(lines) == 1
	})

	want := "garden on research: wheel off (mouse tracking disabled); it scrolls after its next restart"
	mu.Lock()
	got := lines[0]
	mu.Unlock()
	if got != want {
		t.Fatalf("got report %q, want %q", got, want)
	}

	// Repeated drift checks do not send it again.
	d.checkClaudeMouseSessions(context.Background(), time.Now())
	time.Sleep(50 * time.Millisecond)
	mu.Lock()
	count := len(lines)
	mu.Unlock()
	if count != 1 {
		t.Fatalf("report sent %d times, want 1", count)
	}
}

func TestClaudeMouseDriftRetryBacksOffWhileGatewayOffline(t *testing.T) {
	var calls atomic.Int32
	d := &Daemon{log: log.New(io.Discard, "", 0)}
	d.cfg.testGatewayCall = func(_ context.Context, typ string, _ any) (json.RawMessage, bool, error) {
		if typ == "intake.report" {
			calls.Add(1)
			return nil, true, errNotConnected
		}
		return nil, false, nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	d.showMouseNotice(ctx, "mouse-key", "offline notice")
	// A repeated report while its first sender is pending must not start another.
	d.showMouseNotice(ctx, "mouse-key", "offline notice")
	time.Sleep(1100 * time.Millisecond)
	if got := calls.Load(); got > 2 {
		t.Fatalf("intake.report calls while offline = %d, want at most 2 during 1.1 seconds", got)
	}
}

const claudeMouseEnvChild = "TEST_CLAUDE_MOUSE_ENV_CHILD"

// TestClaudeMouseTestEnvCannotChangeTests proves TestMain scrubs the launch
// variables a Claude Code shell exports for its own sessions. A worker test
// that read the shell's CLAUDE_CODE_DISABLE_MOUSE failed only in such a shell,
// so the package decides these values, not the caller.
func TestClaudeMouseTestEnvCannotChangeTests(t *testing.T) {
	if os.Getenv(claudeMouseEnvChild) == "1" {
		for _, key := range []string{claudesettings.MouseKey, "CLAUDE_CODE_SESSION_ID", "AGENT_NET_WORKER", "AGENT_NET_TRAILER_WORKER"} {
			if v, ok := os.LookupEnv(key); ok {
				t.Fatalf("the tests inherited %s=%s from the shell", key, v)
			}
		}
		if os.Getenv("PATH") == "" {
			t.Fatal("the scrub removed PATH")
		}
		if os.Getenv("AGENT_NET_TEST_SCALE") != "1" {
			t.Fatal("the scrub removed a test switch")
		}
		return
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestClaudeMouseTestEnvCannotChangeTests$", "-test.v")
	cmd.Env = append(os.Environ(),
		claudeMouseEnvChild+"=1",
		claudesettings.MouseKey+"=0",
		"CLAUDE_CODE_SESSION_ID=n_from_the_shell",
		"AGENT_NET_WORKER=w_from_the_shell",
		"AGENT_NET_TRAILER_WORKER=from-the-shell",
		"AGENT_NET_TEST_SCALE=1",
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("a Claude Code shell changed the tests: %v\n%s", err, out)
	}
}
