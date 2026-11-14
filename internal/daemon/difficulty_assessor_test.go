package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// The dev command runs the same assessment without a running daemon. These
// tests pin the standalone path with the fake codex of difficulty_test.go.

// The fixture names the first judge and the next one in its list.
func testDifficultyAssessor(cfg Config, sources DifficultySources) *DifficultyAssessor {
	if sources.PickJudge == nil {
		sources.PickJudge = func(ctx context.Context, exclude string) (protocol.DifficultyJudgeChoice, error) {
			if exclude == "" {
				return protocol.DifficultyJudgeChoice{Harness: "opencode", Model: "opencode-go/deepseek-v4.1-flash", Provider: "opencode-go"}, nil
			}
			if sources.Binary != nil {
				if _, err := sources.Binary("codex"); err != nil {
					return protocol.DifficultyJudgeChoice{}, nil
				}
			} else if cfg.HarnessPaths["codex"] == "" {
				return protocol.DifficultyJudgeChoice{}, nil
			}
			return protocol.DifficultyJudgeChoice{Harness: "codex", Model: "gpt-6.1-sol", Provider: "openai"}, nil
		}
	}
	return NewDifficultyAssessor(cfg, sources)
}

func TestAssessDifficultyOutsideDaemonWithoutRosterNamesTheModel(t *testing.T) {
	sources := DifficultySources{Binary: func(string) (string, error) { return "", os.ErrNotExist }}
	a, err := NewDifficultyAssessor(Config{Hostname: "test-machine"}, sources).
		Assess(context.Background(), t.TempDir(), "rename a flag")
	if err != nil {
		t.Fatal(err)
	}
	if a.Label != "normal" || !strings.Contains(a.Notes, "no judge: no registered normal-level model available on test-machine") {
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
	a, err := testDifficultyAssessor(Config{
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
	a, err := testDifficultyAssessor(Config{HarnessPaths: map[string]string{"opencode": os.Args[0]}}, DifficultySources{}).Assess(context.Background(), t.TempDir(), "a task")
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
	if len(rec.Args) < 4 || rec.Args[0] != "run" || rec.Args[1] != "--standalone" || rec.Args[2] != "--model" {
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
	if _, err := testDifficultyAssessor(Config{HarnessPaths: map[string]string{"opencode": os.Args[0]}}, DifficultySources{}).Assess(context.Background(), t.TempDir(), "a task"); err != nil {
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
	a, err := testDifficultyAssessor(Config{HarnessPaths: map[string]string{"opencode": os.Args[0], "codex": os.Args[0]}, DifficultyTimeout: 60 * time.Second}, DifficultySources{}).Assess(context.Background(), t.TempDir(), "brief")
	if err != nil || a.Label != "hard" || a.JudgeModel != "codex:gpt-6.1-sol" || !strings.Contains(a.Notes, "parse") {
		t.Fatalf("assessment: %+v, %v", a, err)
	}
	calls := difficultyCalls(t, dir)
	if len(calls) != 1 || !hasArg(calls[0].Args, "-m", "gpt-6.1-sol") {
		t.Fatalf("calls: %+v", calls)
	}
}

// The first judge is slow, so the retry judge starts at the hedge point and
// its answer is used without waiting for the first one or the budget.
func TestJudgeFirstFailureStartsRetryAtOnce(t *testing.T) {
	fakeDifficultyRun(t, map[string][]string{"judge": {difficultyScoresJSON}})
	reply := filepath.Join(t.TempDir(), "reply")
	if err := os.WriteFile(reply, []byte("not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv(envFakeOpenCode, "1")
	t.Setenv(envFakeOpenCodeReply, reply)
	started := time.Now()
	a, err := testDifficultyAssessor(Config{HarnessPaths: map[string]string{"opencode": os.Args[0], "codex": os.Args[0]}, DifficultyTimeout: 60 * time.Second}, DifficultySources{}).Assess(context.Background(), t.TempDir(), "brief")
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
	a, err := testDifficultyAssessor(Config{HarnessPaths: map[string]string{"opencode": os.Args[0], "codex": os.Args[0]}, DifficultyTimeout: 60 * time.Second}, DifficultySources{Logf: func(format string, args ...any) {
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
	}{{"missing", 60 * time.Second, false}} {
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
			a, err := testDifficultyAssessor(Config{DifficultyTimeout: tc.timeout}, source).Assess(context.Background(), t.TempDir(), "brief")
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
	_, _ = testDifficultyAssessor(Config{
		HarnessPaths:      map[string]string{"opencode": os.Args[0]},
		DifficultyTimeout: 100 * time.Millisecond,
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

func TestJudgeTimeoutNoteFormatSample(t *testing.T) {
	note := formatJudgeTimeoutNote("opencode", "opencode-go/deepseek-v4.1-flash", 39*time.Second, 60*time.Second, "codex", "gpt-6.1-sol", 20994038720*time.Nanosecond)
	want := "first judge opencode-go/deepseek-v4.1-flash timed out at 39s of the 60s budget; retry judge codex gpt-6.1-sol timed out after the remaining 21s"
	if note != want {
		t.Fatalf("note:\n  got:  %q\n  want: %q", note, want)
	}
}

func TestJudgeWorkerBudget(t *testing.T) {
	if workerJudgeTimeout != 90*time.Second {
		t.Fatalf("worker judge budget = %s, want 90s", workerJudgeTimeout)
	}
}

func TestJudgeMissingClosingBraces(t *testing.T) {
	base := `{"reasoning":3,"scope":2,"coupling":3,"uncertainty":2,"verification":2,"risk":3,"reasons":{"reasoning":"r","scope":"s","coupling":"c","uncertainty":"u","verification":"v","risk":"The evidence identifies delivery routing as the risk: losing a batched task would stall a change.","confidence":"high"}`
	for _, raw := range []string{base, base + " \n", base + "} trailing text"} {
		scores, _, err := parseJudgeReply(raw)
		if err != nil || scores.Risk != 3 {
			t.Fatalf("parseJudgeReply: scores=%+v err=%v", scores, err)
		}
	}
	if _, _, err := parseJudgeReply(base[:len(base)-3]); err == nil {
		t.Fatal("accepted a reply cut inside a string")
	}
}

func TestJudgeConfigurationHasNoPins(t *testing.T) {
	typ := reflect.TypeOf(Config{})
	for _, name := range []string{"DifficultyJudgeModel", "DifficultyJudgeHarness", "DifficultyRetryJudgeModel", "DifficultyRetryJudgeHarness"} {
		if _, ok := typ.FieldByName(name); ok {
			t.Errorf("judge pin %s still exists", name)
		}
	}
}

func TestJudgeCLIUsesSelectedModelAndDisablesTools(t *testing.T) {
	for _, tc := range []struct {
		harness, model string
		want           []string
	}{
		{"claude", "claude-test:high", []string{"--print", "--no-session-persistence", "--tools", "--strict-mcp-config", "--model", "claude-test", "--effort", "high"}},
		{"pi", "google/gemini-test:high", []string{"--print", "--no-session", "--no-tools", "--no-extensions", "--model", "google/gemini-test:high"}},
		{"agy", "gemini-test-high", []string{"--mode", "plan", "--sandbox", "--model", "gemini-test-high"}},
	} {
		t.Run(tc.harness, func(t *testing.T) {
			dir := t.TempDir()
			script := filepath.Join(dir, "fake")
			args := filepath.Join(dir, "args")
			if err := os.WriteFile(script, []byte("#!/bin/sh\nprintf '%s\\n' \"$@\" > \"$FAKE_JUDGE_ARGS\"\nprintf '%s\\n' '"+difficultyScoresJSON+"'\n"), 0700); err != nil {
				t.Fatal(err)
			}
			t.Setenv("FAKE_JUDGE_ARGS", args)
			src := DifficultySources{PickJudge: func(context.Context, string) (protocol.DifficultyJudgeChoice, error) {
				return protocol.DifficultyJudgeChoice{Harness: tc.harness, Model: tc.model}, nil
			}}
			a, err := NewDifficultyAssessor(Config{HarnessPaths: map[string]string{tc.harness: script}, DifficultyTimeout: 2 * time.Second}, src).Assess(context.Background(), dir, "brief")
			if err != nil || a.Label != "hard" || a.JudgeModel != tc.harness+":"+tc.model {
				t.Fatalf("assessment %+v, %v", a, err)
			}
			b, err := os.ReadFile(args)
			if err != nil {
				t.Fatal(err)
			}
			lines := strings.Split(string(b), "\n")
			for _, want := range tc.want {
				found := false
				for _, line := range lines {
					if line == want {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("arguments missing %q: %q", want, b)
				}
			}
		})
	}
}
