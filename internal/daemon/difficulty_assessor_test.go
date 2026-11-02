package daemon

import (
	"context"
	"os"
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
	a, err := NewDifficultyAssessor(Config{}, DifficultySources{}).
		Assess(context.Background(), t.TempDir(), "rename a flag")
	if err != nil {
		t.Fatal(err)
	}
	if a.Label != "normal" || !strings.Contains(a.Notes, "name a model") {
		t.Fatalf("assessment: %+v", a)
	}
}
