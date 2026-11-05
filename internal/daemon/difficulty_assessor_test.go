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

// The dev command runs the same assessment without a running daemon. These
// tests pin the standalone path with the fake codex of difficulty_test.go.

func TestAssessDifficultyOutsideDaemon(t *testing.T) {
	dir := fakeDifficultyRun(t, map[string][]string{
		"analyzer": {difficultyProfileJSON("high")},
		"judge":    {difficultyScoresJSON},
	})
	project := t.TempDir()
	a, err := NewDifficultyAssessor(Config{
		HarnessPaths:           map[string]string{"codex": os.Args[0]},
		DifficultyHarness:      "codex",
		DifficultyModel:        "gpt-cheap",
		DifficultyAnalyzer:     true,
		DifficultyJudgeHarness: "codex",
		DifficultyJudgeModel:   "gpt-cheap",
		DifficultyTimeout:      2 * time.Second,
	}, DifficultySources{}).Assess(context.Background(), project, "change the settings tab")
	if err != nil {
		t.Fatal(err)
	}
	if a.Label != "hard" || a.Confidence != "high" || a.D < 3.59 || a.D > 3.61 {
		t.Fatalf("assessment: %+v", a)
	}
	if a.AnalyzerModel != "codex:gpt-cheap" || a.JudgeModel != "codex:gpt-cheap" {
		t.Fatalf("models: %q %q", a.AnalyzerModel, a.JudgeModel)
	}
	calls := difficultyCalls(t, dir)
	if len(calls) != 2 || calls[0].Cwd != project || calls[1].Cwd == project {
		t.Fatalf("calls: %+v", calls)
	}
}

func TestAssessDifficultyOutsideDaemonUsesRosterSources(t *testing.T) {
	fakeDifficultyRun(t, map[string][]string{
		"analyzer": {difficultyProfileJSON("high")},
		"judge":    {difficultyScoresJSON},
	})
	sources := DifficultySources{
		Profiles: func(context.Context) ([]protocol.WorkerProfile, error) {
			return []protocol.WorkerProfile{{Name: "cheap", Harness: "codex", Model: "gpt-cheap"}}, nil
		},
		Scores: func(context.Context) ([]protocol.ModelScore, error) {
			return []protocol.ModelScore{{Match: "gpt-cheap", Score: 10}}, nil
		},
	}
	a, err := NewDifficultyAssessor(Config{
		HarnessPaths:           map[string]string{"codex": os.Args[0]},
		DifficultyAnalyzer:     true,
		DifficultyJudgeHarness: "codex",
		DifficultyJudgeModel:   "gpt-cheap",
		DifficultyTimeout:      2 * time.Second,
	}, sources).Assess(context.Background(), t.TempDir(), "rename a flag")
	if err != nil {
		t.Fatal(err)
	}
	if a.AnalyzerModel != "codex:gpt-cheap" || a.Label != "hard" {
		t.Fatalf("assessment: %+v", a)
	}
}

func TestAssessDifficultyOutsideDaemonWithoutRosterNamesTheModel(t *testing.T) {
	sources := DifficultySources{Binary: func(string) (string, error) { return "", os.ErrNotExist }}
	a, err := NewDifficultyAssessor(Config{}, sources).
		Assess(context.Background(), t.TempDir(), "rename a flag")
	if err != nil {
		t.Fatal(err)
	}
	if a.Label != "normal" || !strings.Contains(a.Notes, "OpenCode is not installed") {
		t.Fatalf("assessment: %+v", a)
	}
}

func TestJudgeOnlyDefaultUsesBriefAndDoesNotRunCodex(t *testing.T) {
	dir := t.TempDir()
	reply := filepath.Join(dir, "reply.json")
	if err := os.WriteFile(reply, []byte(`{"reasoning":1,"scope":1,"coupling":1,"uncertainty":1,"verification":1,"risk":1,"confidence":"low"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv(envFakeOpenCode, "1")
	t.Setenv(envFakeOpenCodeReply, reply)
	promptFile := filepath.Join(dir, "prompt.txt")
	t.Setenv("FAKE_OPENCODE_PROMPT_FILE", promptFile)
	a, err := NewDifficultyAssessor(Config{
		HarnessPaths: map[string]string{"opencode": os.Args[0]}, DifficultyTimeout: 2 * time.Second,
	}, DifficultySources{}).Assess(context.Background(), t.TempDir(), "BRIEF: add a small setting")
	if err != nil {
		t.Fatal(err)
	}
	if a.Mode != "judge-only" || a.Confidence != "low" || a.Label != "trivial" || a.Profile.Confidence != "" {
		t.Fatalf("assessment: %+v", a)
	}
	prompt, err := os.ReadFile(promptFile)
	if err != nil {
		t.Fatalf("judge prompt missing: %v", err)
	}
	if !strings.Contains(string(prompt), "BRIEF: add a small setting") || !strings.Contains(string(prompt), "Start every dimension at 1") {
		t.Fatalf("judge prompt missing brief or start-at-1 instruction: %s", prompt)
	}
}

func TestJudgeOpenCodeUsesPrivateServerAndDisabledSocket(t *testing.T) {
	dir := t.TempDir()
	record := filepath.Join(dir, "record.json")
	reply := filepath.Join(dir, "reply.json")
	if err := os.WriteFile(reply, []byte(`{"reasoning":1,"scope":1,"coupling":1,"uncertainty":1,"verification":1,"risk":1}`), 0600); err != nil {
		t.Fatal(err)
	}
	parentSocket := filepath.Join(dir, "real.sock")
	if err := os.WriteFile(parentSocket, nil, 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AGENT_NET_SOCKET", parentSocket)
	t.Setenv(envFakeOpenCode, "1")
	t.Setenv(envFakeOpenCodeReply, reply)
	t.Setenv("FAKE_OPENCODE_RUN_RECORD", record)
	t.Setenv("PWD", t.TempDir())
	a, err := NewDifficultyAssessor(Config{HarnessPaths: map[string]string{"opencode": os.Args[0]}}, DifficultySources{}).Assess(context.Background(), t.TempDir(), "a task")
	if err != nil || a.Label != "trivial" {
		t.Fatalf("judge: %+v %v", a, err)
	}
	raw, err := os.ReadFile(record)
	if err != nil {
		t.Fatal(err)
	}
	var rec struct {
		Args         []string `json:"args"`
		Socket       string   `json:"socket"`
		SocketExists bool     `json:"socket_exists"`
		PWD          string   `json:"pwd"`
		Dir          string   `json:"dir"`
	}
	if err := json.Unmarshal(raw, &rec); err != nil {
		t.Fatal(err)
	}
	if len(rec.Args) < 4 || rec.Args[0] != "run" || rec.Args[1] != "--standalone" || rec.Args[2] != "-m" {
		t.Errorf("judge arguments: %v", rec.Args)
	}
	if rec.Socket == "" || rec.Socket == parentSocket || rec.SocketExists || !filepath.IsAbs(rec.Socket) {
		t.Errorf("judge socket: %q exists=%v", rec.Socket, rec.SocketExists)
	}
	// OpenCode files the session under $PWD, so it must be the judge's folder.
	if rec.PWD == "" || rec.PWD != rec.Dir {
		t.Errorf("judge PWD %q, folder %q", rec.PWD, rec.Dir)
	}
}

func TestJudgeOpenCodeDeletesOnlyItsOwnSessions(t *testing.T) {
	dir := t.TempDir()
	reply := filepath.Join(dir, "reply.json")
	if err := os.WriteFile(reply, []byte(`{"reasoning":1,"scope":1,"coupling":1,"uncertainty":1,"verification":1,"risk":1}`), 0600); err != nil {
		t.Fatal(err)
	}
	sessions := filepath.Join(dir, "sessions.json")
	if err := os.WriteFile(sessions, []byte(`[{"id":"ses_judge","directory":"{dir}"},{"id":"ses_owner","directory":"/home/someone/project"}]`), 0600); err != nil {
		t.Fatal(err)
	}
	deleted := filepath.Join(dir, "deleted")
	t.Setenv(envFakeOpenCode, "1")
	t.Setenv(envFakeOpenCodeReply, reply)
	t.Setenv("FAKE_OPENCODE_SESSIONS", sessions)
	t.Setenv("FAKE_OPENCODE_DELETED", deleted)
	if _, err := NewDifficultyAssessor(Config{HarnessPaths: map[string]string{"opencode": os.Args[0]}}, DifficultySources{}).Assess(context.Background(), t.TempDir(), "a task"); err != nil {
		t.Fatal(err)
	}
	var raw []byte
	eventually(t, "the judge's sessions deleted", func() bool {
		var err error
		raw, err = os.ReadFile(deleted)
		return err == nil && len(raw) > 0
	})
	if got := strings.TrimSpace(string(raw)); got != "ses_judge" {
		t.Fatalf("deleted sessions: %q", got)
	}
}

func TestJudgeRetryAfterParseFailure(t *testing.T) {
	dir := fakeDifficultyRun(t, map[string][]string{"judge": {difficultyScoresJSON}})
	reply := filepath.Join(t.TempDir(), "reply")
	if err := os.WriteFile(reply, []byte("not json"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv(envFakeOpenCode, "1")
	t.Setenv(envFakeOpenCodeReply, reply)
	a, err := NewDifficultyAssessor(Config{HarnessPaths: map[string]string{"opencode": os.Args[0], "codex": os.Args[0]}, DifficultyTimeout: 60 * time.Second}, DifficultySources{}).Assess(context.Background(), t.TempDir(), "brief")
	if err != nil || a.Label != "hard" || a.JudgeModel != "codex:gpt-6.1-sol" || !strings.Contains(a.Notes, "parse") {
		t.Fatalf("assessment: %+v, %v", a, err)
	}
	calls := difficultyCalls(t, dir)
	if len(calls) != 1 || !strings.Contains(strings.Join(calls[0].Args, " "), `model_reasoning_effort="low"`) {
		t.Fatalf("calls: %+v", calls)
	}
}

// The first judge is slow, so the retry judge starts at the hedge point and
// its answer is used without waiting for the first one or the budget.
func TestJudgeRetryStartsAtHedgeAndWinsWhileFirstRuns(t *testing.T) {
	dir := fakeDifficultyRun(t, map[string][]string{"judge": {difficultyScoresJSON}})
	t.Setenv(envFakeOpenCode, "1")
	t.Setenv("FAKE_DIFFICULTY_GATE", filepath.Join(t.TempDir(), "never"))
	started := time.Now()
	a, err := NewDifficultyAssessor(Config{HarnessPaths: map[string]string{"opencode": os.Args[0], "codex": os.Args[0]}, DifficultyTimeout: 60 * time.Second, testJudgeHedgeAfter: 100 * time.Millisecond}, DifficultySources{}).Assess(context.Background(), t.TempDir(), "brief")
	if err != nil || a.Label != "hard" || a.JudgeModel != "codex:gpt-6.1-sol" {
		t.Fatalf("assessment: %+v, %v", a, err)
	}
	if elapsed := time.Since(started); elapsed > 10*time.Second {
		t.Fatalf("waited %s for a slow first judge", elapsed)
	}
	want := "first judge opencode-go/deepseek-v4.1-flash had no answer after 1s; retry judge codex gpt-6.1-sol answered first"
	if a.Notes != want {
		t.Fatalf("note: %q, want %q", a.Notes, want)
	}
	if calls := difficultyCalls(t, dir); len(calls) != 1 {
		t.Fatalf("calls: %+v", calls)
	}
}

// The first judge keeps its time after the retry starts. When it answers
// first, its answer is used and the retry is stopped.
func TestJudgeFirstAnswerAfterHedgeStopsRetry(t *testing.T) {
	dir := fakeDifficultyRun(t, map[string][]string{"judge": {"HANG"}})
	reply := filepath.Join(t.TempDir(), "reply")
	if err := os.WriteFile(reply, []byte(difficultyScoresJSON), 0o600); err != nil {
		t.Fatal(err)
	}
	gate := filepath.Join(t.TempDir(), "gate")
	t.Setenv(envFakeOpenCode, "1")
	t.Setenv(envFakeOpenCodeReply, reply)
	t.Setenv("FAKE_DIFFICULTY_GATE", gate)
	go func() {
		// Open the gate once the retry judge is running.
		deadline := time.Now().Add(20 * time.Second)
		for time.Now().Before(deadline) {
			if _, err := os.Stat(filepath.Join(dir, "calls.jsonl")); err == nil {
				_ = os.WriteFile(gate, []byte("go"), 0o600)
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
	}()
	started := time.Now()
	a, err := NewDifficultyAssessor(Config{HarnessPaths: map[string]string{"opencode": os.Args[0], "codex": os.Args[0]}, DifficultyTimeout: 60 * time.Second, testJudgeHedgeAfter: 100 * time.Millisecond}, DifficultySources{}).Assess(context.Background(), t.TempDir(), "brief")
	if err != nil || a.Label != "hard" || a.JudgeModel != "opencode:opencode-go/deepseek-v4.1-flash" {
		t.Fatalf("assessment: %+v, %v", a, err)
	}
	if elapsed := time.Since(started); elapsed > 8*time.Second {
		t.Fatalf("waited %s for the stopped retry judge", elapsed)
	}
	if !strings.Contains(a.Notes, "retry judge codex gpt-6.1-sol was stopped") {
		t.Fatalf("note: %q", a.Notes)
	}
}

// A first judge that fails early starts the retry at once, not at the hedge
// point.
func TestJudgeFirstFailureStartsRetryAtOnce(t *testing.T) {
	fakeDifficultyRun(t, map[string][]string{"judge": {difficultyScoresJSON}})
	reply := filepath.Join(t.TempDir(), "reply")
	if err := os.WriteFile(reply, []byte("not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv(envFakeOpenCode, "1")
	t.Setenv(envFakeOpenCodeReply, reply)
	started := time.Now()
	a, err := NewDifficultyAssessor(Config{HarnessPaths: map[string]string{"opencode": os.Args[0], "codex": os.Args[0]}, DifficultyTimeout: 60 * time.Second, testJudgeHedgeAfter: 50 * time.Second}, DifficultySources{}).Assess(context.Background(), t.TempDir(), "brief")
	if err != nil || a.Label != "hard" || a.JudgeModel != "codex:gpt-6.1-sol" {
		t.Fatalf("assessment: %+v, %v", a, err)
	}
	if elapsed := time.Since(started); elapsed > 10*time.Second {
		t.Fatalf("the retry waited for the hedge point: %s", elapsed)
	}
}

// A reply that does not parse is logged, capped, so the next failure shows
// what the judge said.
func TestJudgeParseFailureLogsReply(t *testing.T) {
	fakeDifficultyRun(t, map[string][]string{"judge": {`{"reasoning":2,"scope":` + strings.Repeat("x", 20000)}})
	reply := filepath.Join(t.TempDir(), "reply")
	if err := os.WriteFile(reply, []byte("I will not score this."), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv(envFakeOpenCode, "1")
	t.Setenv(envFakeOpenCodeReply, reply)
	var logs []string
	a, err := NewDifficultyAssessor(Config{HarnessPaths: map[string]string{"opencode": os.Args[0], "codex": os.Args[0]}, DifficultyTimeout: 60 * time.Second}, DifficultySources{Logf: func(format string, args ...any) {
		logs = append(logs, fmt.Sprintf(format, args...))
	}}).Assess(context.Background(), t.TempDir(), "brief")
	if err != nil || a.Label != "normal" {
		t.Fatalf("assessment: %+v, %v", a, err)
	}
	if !strings.Contains(a.Notes, "the judge reply did not parse: no JSON object in the 53-byte reply") ||
		!strings.Contains(a.Notes, "the retry judge reply did not parse: no complete JSON object in the 20023-byte reply (it may be cut off)") {
		t.Fatalf("note: %q", a.Notes)
	}
	var replies []string
	for _, l := range logs {
		if strings.HasPrefix(l, "judge.reply ") {
			replies = append(replies, l)
		}
	}
	if len(replies) != 2 || !strings.Contains(replies[0], "I will not score this.") || !strings.Contains(replies[1], `{\"reasoning\":2`) {
		t.Fatalf("reply logs: %q", replies)
	}
	if len(replies[1]) > judgeReplyLogLimit+200 {
		t.Fatalf("reply log is not capped: %d bytes", len(replies[1]))
	}
}

func TestJudgeRetrySkippedWhenUnavailableOrBudgetShort(t *testing.T) {
	reply := filepath.Join(t.TempDir(), "reply")
	if err := os.WriteFile(reply, []byte("not json"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv(envFakeOpenCode, "1")
	t.Setenv(envFakeOpenCodeReply, reply)
	for _, tc := range []struct {
		name      string
		timeout   time.Duration
		available bool
	}{{"missing", 60 * time.Second, false}, {"short budget", 2 * time.Second, true}} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			source := DifficultySources{Binary: func(name string) (string, error) {
				if name == "opencode" {
					return os.Args[0], nil
				}
				calls++
				if tc.available {
					return os.Args[0], nil
				}
				return "", os.ErrNotExist
			}}
			a, err := NewDifficultyAssessor(Config{DifficultyTimeout: tc.timeout}, source).Assess(context.Background(), t.TempDir(), "brief")
			if err != nil || a.Label != "normal" || !strings.Contains(a.Notes, "parse") || a.JudgeModel != "opencode:opencode-go/deepseek-v4.1-flash" {
				t.Fatalf("assessment: %+v, %v", a, err)
			}
			if tc.available && calls != 0 {
				t.Fatalf("looked up retry with too little time")
			}
		})
	}
}

func TestJudgeOpenCodeCleanupRunsAfterTimeout(t *testing.T) {
	dir := t.TempDir()
	sessions := filepath.Join(dir, "sessions.json")
	if err := os.WriteFile(sessions, []byte(`[{"id":"ses_judge_timeout","directory":"{dir}"}]`), 0600); err != nil {
		t.Fatal(err)
	}
	deleted := filepath.Join(dir, "deleted")
	t.Setenv(envFakeOpenCode, "1")
	t.Setenv("FAKE_OPENCODE_SESSIONS", sessions)
	t.Setenv("FAKE_OPENCODE_DELETED", deleted)
	t.Setenv("FAKE_DIFFICULTY_GATE", filepath.Join(t.TempDir(), "never"))
	_, _ = NewDifficultyAssessor(Config{
		HarnessPaths:        map[string]string{"opencode": os.Args[0]},
		DifficultyTimeout:   100 * time.Millisecond,
		testJudgeHedgeAfter: 100 * time.Millisecond,
	}, DifficultySources{}).Assess(context.Background(), t.TempDir(), "brief")

	// The cleanup runs in the background so a retry judge does not wait.
	var raw []byte
	eventually(t, "sessions deleted after timeout", func() bool {
		var err error
		raw, err = os.ReadFile(deleted)
		return err == nil && len(raw) > 0
	})
	if got := strings.TrimSpace(string(raw)); got != "ses_judge_timeout" {
		t.Fatalf("deleted sessions: %q, want ses_judge_timeout", got)
	}
}

func TestJudgeTimeoutNoteFirstTimeoutRetryTimeout(t *testing.T) {
	fakeDifficultyRun(t, map[string][]string{"judge": {difficultyScoresJSON}})
	t.Setenv(envFakeOpenCode, "1")
	t.Setenv("FAKE_DIFFICULTY_GATE", filepath.Join(t.TempDir(), "never"))
	t.Setenv(envFakeCodexExecHang, "1")
	a, err := NewDifficultyAssessor(Config{
		HarnessPaths:        map[string]string{"opencode": os.Args[0], "codex": os.Args[0]},
		DifficultyTimeout:   300 * time.Millisecond,
		testJudgeHedgeAfter: 100 * time.Millisecond,
	}, DifficultySources{}).Assess(context.Background(), t.TempDir(), "brief")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(a.Notes, "first judge opencode-go/deepseek-v4.1-flash timed out at 1s of the 1s budget") {
		t.Fatalf("note missing first judge: %q", a.Notes)
	}
	if !strings.Contains(a.Notes, "retry judge codex gpt-6.1-sol timed out after the remaining 1s") {
		t.Fatalf("note missing retry judge: %q", a.Notes)
	}
}

func TestJudgeTimeoutNoteFormatSample(t *testing.T) {
	note := formatJudgeTimeoutNote("opencode", "opencode-go/deepseek-v4.1-flash", 39*time.Second, 60*time.Second, "codex", "gpt-6.1-sol", 20994038720*time.Nanosecond)
	want := "first judge opencode-go/deepseek-v4.1-flash timed out at 39s of the 60s budget; retry judge codex gpt-6.1-sol timed out after the remaining 21s"
	if note != want {
		t.Fatalf("note:\n  got:  %q\n  want: %q", note, want)
	}
}

func TestJudgeAttemptLoggedPerAttempt(t *testing.T) {
	fakeDifficultyRun(t, map[string][]string{"judge": {difficultyScoresJSON}})
	t.Setenv(envFakeOpenCode, "1")
	t.Setenv("FAKE_DIFFICULTY_GATE", filepath.Join(t.TempDir(), "never"))
	var logs []string
	sources := DifficultySources{
		Logf: func(format string, args ...any) {
			logs = append(logs, fmt.Sprintf(format, args...))
		},
	}
	_, err := NewDifficultyAssessor(Config{
		HarnessPaths:        map[string]string{"opencode": os.Args[0], "codex": os.Args[0]},
		DifficultyTimeout:   60 * time.Second,
		testJudgeHedgeAfter: 100 * time.Millisecond,
	}, sources).Assess(context.Background(), t.TempDir(), "brief")
	if err != nil {
		t.Fatal(err)
	}
	// The retry answers first; the slow first judge is then stopped.
	if len(logs) != 2 {
		t.Fatalf("expected 2 log lines, got %d: %v", len(logs), logs)
	}
	if !strings.HasPrefix(logs[0], "judge.attempt harness=codex model=gpt-6.1-sol ") || !strings.Contains(logs[0], "result=ok") {
		t.Fatalf("unexpected log line 1: %q", logs[0])
	}
	if !strings.HasPrefix(logs[1], "judge.attempt harness=opencode model=opencode-go/deepseek-v4.1-flash ") || !strings.Contains(logs[1], "result=stopped") {
		t.Fatalf("unexpected log line 2: %q", logs[1])
	}
}

// On the 60 s start budget the retry judge starts at 25 s, so it has 35 s,
// not the 21 s it timed out with.
func TestJudgeHedgePoint(t *testing.T) {
	for budget, want := range map[time.Duration]time.Duration{
		60 * time.Second: 25 * time.Second,
		3 * time.Minute:  145 * time.Second,
		45 * time.Second: 15 * time.Second,
		3 * time.Second:  time.Second,
	} {
		if got := judgeHedge(budget); got != want {
			t.Errorf("judgeHedge(%s) = %s, want %s", budget, got, want)
		}
	}
}

func TestJudgeWorkerBudget(t *testing.T) {
	if workerJudgeTimeout != 90*time.Second {
		t.Fatalf("worker judge budget = %s, want 90s", workerJudgeTimeout)
	}
}
