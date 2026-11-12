package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// The struggle detector reads a run's own tool record once at run end. These
// tests use fixture traces in temporary folders, never a real transcript.

const struggleTestCwd = "/work/shop"

func struggleToolUse(id, name, input string) string {
	return fmt.Sprintf(`{"type":"assistant","message":{"content":[{"type":"tool_use","id":%q,"name":%q,"input":%s}]}}`, id, name, input)
}

func struggleToolResult(id string, failed bool, text string) string {
	body, _ := json.Marshal(text)
	return fmt.Sprintf(`{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":%q,"is_error":%t,"content":%s}]}}`, id, failed, body)
}

func struggleTestDaemon(t *testing.T, harness string, ln launcher) *Daemon {
	t.Helper()
	dir := t.TempDir()
	return &Daemon{cfg: Config{StateDir: dir, ClaudeSessionsDir: filepath.Join(dir, "sessions"), CodexHome: filepath.Join(dir, "codex")},
		launchers: map[string]launcher{harness: ln}}
}

func struggleTestWorker(harness, native string) *worker {
	return &worker{id: "w_test", harness: harness, nativeID: native, path: struggleTestCwd, startedAt: time.Now(), sessionID: "s_test", project: "shop"}
}

func TestStruggleClaudeCountsAndNotice(t *testing.T) {
	d := struggleTestDaemon(t, "claude", claudeLauncher{})
	projectsDir := filepath.Join(filepath.Dir(d.cfg.ClaudeSessionsDir), "projects")
	const session = "ses_struggle"
	writeClaudeTranscriptLine(t, projectsDir, session, struggleTestCwd, struggleToolUse("t1", "Bash", `{"command":"go test ./internal/daemon"}`))
	writeClaudeTranscriptLine(t, projectsDir, session, struggleTestCwd, struggleToolResult("t1", true, "FAIL exit status 1"))
	writeClaudeTranscriptLine(t, projectsDir, session, struggleTestCwd, struggleToolUse("t2", "Bash", `{"command":"go test ./internal/daemon"}`))
	writeClaudeTranscriptLine(t, projectsDir, session, struggleTestCwd, struggleToolResult("t2", true, "FAIL exit status 1"))
	writeClaudeTranscriptLine(t, projectsDir, session, struggleTestCwd, struggleToolUse("t3", "Bash", `{"command":"go test ./internal/daemon"}`))
	writeClaudeTranscriptLine(t, projectsDir, session, struggleTestCwd, struggleToolResult("t3", true, "FAIL exit status 1"))
	writeClaudeTranscriptLine(t, projectsDir, session, struggleTestCwd, struggleToolUse("t4", "Bash", `{"command":"git status"}`))
	writeClaudeTranscriptLine(t, projectsDir, session, struggleTestCwd, struggleToolResult("t4", true, "fatal: cannot create .git/packed-refs.lock: File exists"))
	writeClaudeTranscriptLine(t, projectsDir, session, struggleTestCwd, struggleToolUse("t5", "Edit", `{"file_path":"main.go"}`))
	writeClaudeTranscriptLine(t, projectsDir, session, struggleTestCwd, struggleToolResult("t5", false, "ok"))

	w := struggleTestWorker("claude", session)
	st := d.noteWorkerStruggle(context.Background(), w)
	if st.Trace != "transcript" {
		t.Fatalf("trace: %q", st.Trace)
	}
	if st.Failed != 4 {
		t.Fatalf("failed calls: got %d, want 4", st.Failed)
	}
	if st.Repeats != 1 {
		t.Fatalf("repeats: got %d, want 1 (the same command ran 3 times)", st.Repeats)
	}
	if st.Retries != 2 {
		t.Fatalf("retries: got %d, want 2", st.Retries)
	}
	if st.Exploration != 4 {
		t.Fatalf("exploration: got %d, want 4 tool calls before the first edit", st.Exploration)
	}
	if st.EnvErrors != 1 {
		t.Fatalf("environment errors: got %d, want 1", st.EnvErrors)
	}
	if len(st.Top) == 0 || !strings.Contains(st.Top[0], "go test ./internal/daemon") {
		t.Fatalf("top offenders: %+v", st.Top)
	}

	n := d.notice(w, protocol.StatusDone, "", workerStats{})
	text := protocol.FormatWorkerNotice(n, "")
	want := "struggle: 4 failed tool calls, 1 repeats; top:"
	if !strings.Contains(text, want) {
		t.Fatalf("notice lacks the struggle line:\n%s", text)
	}
	if !strings.Contains(text, "go test ./internal/daemon x3 (3 failed)") {
		t.Fatalf("notice top offender is not the repeated command:\n%s", text)
	}
}

func TestStruggleCleanRunHasNoLine(t *testing.T) {
	d := struggleTestDaemon(t, "claude", claudeLauncher{})
	projectsDir := filepath.Join(filepath.Dir(d.cfg.ClaudeSessionsDir), "projects")
	const session = "ses_clean"
	writeClaudeTranscriptLine(t, projectsDir, session, struggleTestCwd, struggleToolUse("c1", "Read", `{"file_path":"main.go"}`))
	writeClaudeTranscriptLine(t, projectsDir, session, struggleTestCwd, struggleToolResult("c1", false, "package main"))
	writeClaudeTranscriptLine(t, projectsDir, session, struggleTestCwd, struggleToolUse("c2", "Edit", `{"file_path":"main.go"}`))
	writeClaudeTranscriptLine(t, projectsDir, session, struggleTestCwd, struggleToolResult("c2", false, "ok"))
	writeClaudeTranscriptLine(t, projectsDir, session, struggleTestCwd, struggleToolUse("c3", "Bash", `{"command":"go test ./..."}`))
	writeClaudeTranscriptLine(t, projectsDir, session, struggleTestCwd, struggleToolResult("c3", false, "ok"))

	w := struggleTestWorker("claude", session)
	st := d.noteWorkerStruggle(context.Background(), w)
	if st.Failed != 0 || st.Repeats != 0 || st.EnvErrors != 0 {
		t.Fatalf("clean run counted: %+v", st)
	}
	n := d.notice(w, protocol.StatusDone, "", workerStats{})
	if text := protocol.FormatWorkerNotice(n, ""); strings.Contains(text, "struggle:") {
		t.Fatalf("clean run notice has a struggle line:\n%s", text)
	}
}

// TestStruggleEnvErrorTable matches each text the owner listed.
func TestStruggleEnvErrorTable(t *testing.T) {
	for _, text := range []string{
		"fatal: cannot lock ref: .git/packed-refs.lock",
		"bash: /work/shop/run.sh: Permission denied",
		"bwrap: Creating new namespace failed: Operation not permitted",
		"sh: 1: npm: command not found",
		"listen tcp 127.0.0.1:8080: bind: address already in use",
		"context deadline exceeded",
	} {
		if !struggleEnvError(text) {
			t.Errorf("not matched: %q", text)
		}
	}
	if struggleEnvError("all tests passed") || struggleEnvError("") {
		t.Fatal("a clean text matched an environment error")
	}
}

// TestStruggleNoTraceHarness: a harness without a tool-call record says so in
// full detail, and its notice carries nothing.
func TestStruggleNoTraceHarness(t *testing.T) {
	d := struggleTestDaemon(t, "pi", piLauncher{})
	w := struggleTestWorker("pi", "ses_pi")
	st := d.noteWorkerStruggle(context.Background(), w)
	if st.Trace != protocol.StruggleNoTrace {
		t.Fatalf("trace: got %q, want %q", st.Trace, protocol.StruggleNoTrace)
	}
	if got := protocol.StruggleDetail(&st); got != "no trace" {
		t.Fatalf("detail: %q", got)
	}
	if info := d.workerInfo(w); info.Struggle == nil || info.Struggle.Trace != protocol.StruggleNoTrace {
		t.Fatalf("worker info: %+v", info.Struggle)
	}
	n := d.notice(w, protocol.StatusDone, "", workerStats{})
	if text := protocol.FormatWorkerNotice(n, ""); strings.Contains(text, "struggle:") {
		t.Fatalf("a no-trace harness put a struggle line in the notice:\n%s", text)
	}
}

// TestStruggleTraceTailCap reads only the end of a long trace, drops the
// partial first line and says that it was capped.
func TestStruggleTraceTailCap(t *testing.T) {
	path := filepath.Join(t.TempDir(), "trace.jsonl")
	body := "first line that is long\nsecond line\nthird line\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	var seen []string
	capped, err := scanTraceTail(path, int64(len(body)-27), func(line []byte) {
		seen = append(seen, string(line))
	})
	if err != nil {
		t.Fatal(err)
	}
	if !capped {
		t.Fatal("a short read was not reported as capped")
	}
	if len(seen) == 0 || seen[len(seen)-1] != "third line\n" {
		t.Fatalf("tail read lost the end: %q", seen)
	}
	for _, line := range seen {
		if strings.Contains(line, "first line") {
			t.Fatalf("tail read kept the start: %q", seen)
		}
	}
}

// TestWorkerRowKeepsStruggle round-trips the run's counts through the local
// worker record, so a lost worker is reported with them after a restart.
func TestWorkerRowKeepsStruggle(t *testing.T) {
	s, err := openState(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.close()
	now := time.Now()
	row := workerRow{ID: "w_test", Harness: "claude", Project: "shop", Path: t.TempDir(), Owner: "s_owner", SessionID: "s_test",
		StartToken: "tok", StartedAt: now, LastActive: now,
		Struggle: &protocol.Struggle{Trace: "transcript", Failed: 4, Repeats: 1, Retries: 2, Exploration: 7, EnvErrors: 1, Top: []string{"go test ./x x3 (3 failed)"}}}
	if err := s.saveWorker(row); err != nil {
		t.Fatal(err)
	}
	rows, err := s.workers()
	if err != nil || len(rows) != 1 {
		t.Fatalf("rows: %+v, %v", rows, err)
	}
	got := rows[0].Struggle
	if got == nil || got.Failed != 4 || got.Repeats != 1 || got.Retries != 2 || got.Exploration != 7 || got.EnvErrors != 1 || got.Trace != "transcript" || len(got.Top) != 1 {
		t.Fatalf("round trip: %+v", got)
	}
}

// Claude reports its live model without the start effort, so the worker record
// that the gateway lists carries the effort the worker was started with.
func TestWorkerInfoCarriesStartEffort(t *testing.T) {
	d := struggleTestDaemon(t, "claude", claudeLauncher{})
	w := struggleTestWorker("claude", "ses_effort")
	w.model = "claude-opus-5-5:high"
	if got := d.workerInfo(w).Effort; got != "high" {
		t.Fatalf("effort %q, want high", got)
	}
	w.model = "claude-opus-5-5"
	if got := d.workerInfo(w).Effort; got != "" {
		t.Fatalf("effort %q, want none", got)
	}
}
