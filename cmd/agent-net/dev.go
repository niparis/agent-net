package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/niparis/agent-net/internal/daemon"
	"github.com/niparis/agent-net/internal/difficulty"
	"github.com/niparis/agent-net/internal/protocol"
)

// The hidden developer commands. They are not in the normal usage text: they
// exist for tuning agent-net itself, not for the people who use it. `dev
// assess` judges the task brief directly by default, or runs the analyzer and
// judge when requested, without a running daemon. Scripts can compare the
// label with how the worker actually went.

// assessmentForDev is replaced in tests, so they never start a model.
var assessmentForDev = func(ctx context.Context, cfg daemon.Config, project, task string) (difficulty.Assessment, error) {
	return daemon.NewDifficultyAssessor(cfg, daemon.DifficultySources{Profiles: localWorkerProfiles}).Assess(ctx, project, task)
}

// loadDevConfig is replaced in tests, so they never read the real node.json.
var loadDevConfig = func() (daemon.Config, error) {
	nc, ca, err := daemon.LoadNode(daemon.ConfigDir())
	if err != nil {
		return daemon.Config{}, fmt.Errorf("cannot load the daemon config: %w", err)
	}
	return daemon.Config{GatewayURL: nc.GatewayURL, NodeID: nc.NodeID, Token: nc.Token, CAPEM: ca}, nil
}

func cmdDev(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return errors.New("usage: agent-net dev assess --project DIR --task-file FILE [--model M] [--judge-model M] [--judge-harness H] [--analyzer] [--json]")
	}
	switch args[0] {
	case "assess":
		return runDevAssess(ctx, os.Stdout, args[1:])
	default:
		return fmt.Errorf("unknown dev command %q", args[0])
	}
}

func runDevAssess(ctx context.Context, out io.Writer, args []string) error {
	fs := flag.NewFlagSet("dev assess", flag.ContinueOnError)
	project := fs.String("project", "", "repository folder the analyzer may read")
	taskFile := fs.String("task-file", "", "file with the task text (- for stdin)")
	model := fs.String("model", "", "Codex model for the analyzer (default: the cheapest Codex profile of the worker roster)")
	judgeModel := fs.String("judge-model", "", "model for the independent judge (default: opencode-go/deepseek-v4.1-flash)")
	judgeHarness := fs.String("judge-harness", "", "harness for the independent judge (default: opencode)")
	asJSON := fs.Bool("json", false, "print the whole assessment as JSON")
	analyzer := fs.Bool("analyzer", false, "run the repository analyzer before judging")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *project == "" || *taskFile == "" {
		return errors.New("usage: agent-net dev assess --project DIR --task-file FILE [--model M] [--judge-model M] [--judge-harness H] [--analyzer] [--json]")
	}
	task, err := readDevTaskFile(*taskFile)
	if err != nil {
		return err
	}
	cfg, err := loadDevConfig()
	if err != nil {
		return err
	}
	cfg.DifficultyAnalyzer = *analyzer
	if strings.TrimSpace(*model) != "" {
		cfg.DifficultyHarness = "codex"
		cfg.DifficultyModel = strings.TrimSpace(*model)
	}
	if strings.TrimSpace(*judgeModel) != "" {
		cfg.DifficultyJudgeModel = strings.TrimSpace(*judgeModel)
	}
	if strings.TrimSpace(*judgeHarness) != "" {
		cfg.DifficultyJudgeHarness = strings.TrimSpace(*judgeHarness)
	}
	a, err := assessmentForDev(ctx, cfg, *project, task)
	if err != nil {
		return err
	}
	a.Mode = "judge-only"
	if cfg.DifficultyAnalyzer {
		a.Mode = "analyzer + judge"
	}
	if *asJSON {
		b, _ := json.MarshalIndent(a, "", "  ")
		fmt.Fprintln(out, string(b))
		return nil
	}
	printDevAssessment(out, a)
	return nil
}

func readDevTaskFile(path string) (string, error) {
	var raw []byte
	var err error
	if path == "-" {
		raw, err = io.ReadAll(os.Stdin)
	} else {
		raw, err = os.ReadFile(path)
	}
	if err != nil {
		return "", fmt.Errorf("cannot read the task file: %w", err)
	}
	task := strings.TrimSpace(string(raw))
	if task == "" {
		return "", errors.New("the task file is empty")
	}
	return task, nil
}

func printDevAssessment(out io.Writer, a difficulty.Assessment) {
	mode := a.Mode
	if mode == "" {
		mode = "judge-only"
	}
	bottleneck := a.Bottleneck
	if bottleneck == "" {
		bottleneck = "none"
	}
	confidence := a.Confidence
	if confidence == "" {
		confidence = "unknown"
	}
	notes := strings.TrimSpace(a.Notes)
	if notes == "" {
		notes = "none"
	}
	analyzer := a.AnalyzerModel
	if analyzer == "" {
		analyzer = "none"
	}
	judge := a.JudgeModel
	if judge == "" {
		judge = "none"
	}
	fmt.Fprintf(out, "label: %s  (D %.2f)\n", a.Label, a.D)
	fmt.Fprintf(out, "mode: %s\n", mode)
	fmt.Fprintf(out, "scores: reasoning %d, scope %d, coupling %d, uncertainty %d, verification %d, risk %d\n",
		a.Scores.Reasoning, a.Scores.Scope, a.Scores.Coupling, a.Scores.Uncertainty, a.Scores.Verification, a.Scores.Risk)
	fmt.Fprintf(out, "bottleneck: %s\n", bottleneck)
	fmt.Fprintf(out, "confidence: %s\n", confidence)
	fmt.Fprintf(out, "analyzer: %s\n", analyzer)
	fmt.Fprintf(out, "judge: %s\n", judge)
	fmt.Fprintf(out, "elapsed: %s\n", a.Elapsed.Round(time.Millisecond))
	fmt.Fprintf(out, "notes: %s\n", notes)
}

// localWorkerProfiles reads the worker roster through the local daemon. The
// daemon holds the gateway link; a dev command does not.
func localWorkerProfiles(ctx context.Context) ([]protocol.WorkerProfile, error) {
	cl, err := dialCLI(ctx)
	if err != nil {
		return nil, fmt.Errorf("the worker roster needs a running daemon; name a model with --model")
	}
	defer cl.Close()
	raw, err := cl.Call(ctx, protocol.OpWorkerProfiles, nil)
	if err != nil {
		return nil, fmt.Errorf("the worker roster could not be read: %w", err)
	}
	var profiles []protocol.WorkerProfile
	if err := json.Unmarshal(raw, &profiles); err != nil {
		return nil, fmt.Errorf("the worker roster is not readable: %w", err)
	}
	return profiles, nil
}
