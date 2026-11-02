package daemon

import (
	"context"
	"encoding/json"
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
	raw, err := os.ReadFile(deleted)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(string(raw)); got != "ses_judge" {
		t.Fatalf("deleted sessions: %q", got)
	}
}
