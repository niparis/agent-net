package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/daemon"
	"github.com/niparis/agent-net/internal/difficulty"
)

func devAssessmentFixture() difficulty.Assessment {
	return difficulty.Assessment{
		Scores:        difficulty.Scores{Reasoning: 4, Scope: 3, Coupling: 4, Uncertainty: 3, Verification: 3, Risk: 4},
		Label:         "hard",
		D:             3.6,
		Bottleneck:    "risk",
		Confidence:    "high",
		Notes:         "fine",
		AnalyzerModel: "codex:gpt-cheap",
		JudgeModel:    "codex:gpt-cheap",
		Elapsed:       1500 * time.Millisecond,
	}
}

// stubDev replaces the model run and the config read, and returns what the
// command passed down.
func stubDev(t *testing.T, a difficulty.Assessment) (*string, *string, *daemon.Config) {
	t.Helper()
	var gotProject, gotTask string
	var gotCfg daemon.Config
	oldAssess, oldConfig := assessmentForDev, loadDevConfig
	t.Cleanup(func() { assessmentForDev, loadDevConfig = oldAssess, oldConfig })
	loadDevConfig = func() (daemon.Config, error) { return daemon.Config{}, nil }
	assessmentForDev = func(_ context.Context, cfg daemon.Config, project, task string) (difficulty.Assessment, error) {
		gotCfg, gotProject, gotTask = cfg, project, task
		return a, nil
	}
	return &gotProject, &gotTask, &gotCfg
}

func devTaskFile(t *testing.T, text string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "task.txt")
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestRunDevAssessPrintsTheAssessment(t *testing.T) {
	project, task, cfg := stubDev(t, devAssessmentFixture())
	file := devTaskFile(t, "  change the settings tab\n")
	var out bytes.Buffer
	err := runDevAssess(context.Background(), &out, []string{"--project", "/repo/shop", "--task-file", file})
	if err != nil {
		t.Fatal(err)
	}
	if *project != "/repo/shop" || *task != "change the settings tab" {
		t.Fatalf("project %q task %q", *project, *task)
	}
	_ = cfg
	for _, want := range []string{
		"mode: judge-only",
		"label: hard  (D 3.60)",
		"scores: reasoning 4, scope 3, coupling 4, uncertainty 3, verification 3, risk 4",
		"bottleneck: risk",
		"confidence: high",
		"judge: codex:gpt-cheap",
		"elapsed: 1.5s",
		"notes: fine",
	} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("output lacks %q:\n%s", want, out.String())
		}
	}
}

func TestRunDevAssessJSON(t *testing.T) {
	stubDev(t, devAssessmentFixture())
	file := devTaskFile(t, "rename a flag")
	var out bytes.Buffer
	if err := runDevAssess(context.Background(), &out, []string{"--project", "/repo/shop", "--task-file", file, "--json"}); err != nil {
		t.Fatal(err)
	}
	var a difficulty.Assessment
	if err := json.Unmarshal(out.Bytes(), &a); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, out.String())
	}
	if a.Label != "hard" || a.Scores.Risk != 4 || a.Bottleneck != "risk" {
		t.Fatalf("assessment: %+v", a)
	}
}

func TestRunDevAssessNeedsProjectAndTaskFile(t *testing.T) {
	for _, args := range [][]string{{}, {"--project", "/repo/shop"}, {"--task-file", "task.txt"}} {
		if err := runDevAssess(context.Background(), &bytes.Buffer{}, args); err == nil ||
			!strings.Contains(err.Error(), "usage: agent-net dev assess") {
			t.Fatalf("%v: %v", args, err)
		}
	}
}

func TestRunDevAssessRejectsAnEmptyTask(t *testing.T) {
	stubDev(t, devAssessmentFixture())
	file := devTaskFile(t, " \n")
	err := runDevAssess(context.Background(), &bytes.Buffer{}, []string{"--project", "/repo/shop", "--task-file", file})
	if err == nil || !strings.Contains(err.Error(), "empty") {
		t.Fatalf("error: %v", err)
	}
}

func TestDevIsNotInTheNormalUsage(t *testing.T) {
	if strings.Contains(usage, "dev assess") || strings.Contains(usage, "\n  dev ") {
		t.Fatalf("the hidden dev command leaked into the usage text:\n%s", usage)
	}
}

func TestCmdDevRejectsAnUnknownSubcommand(t *testing.T) {
	err := cmdDev(context.Background(), []string{"assess-everything"})
	if err == nil || !strings.Contains(err.Error(), "unknown dev command") {
		t.Fatalf("error: %v", err)
	}
}
