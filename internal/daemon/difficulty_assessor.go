package daemon

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"time"

	"github.com/niparis/agent-net/internal/difficulty"
	"github.com/niparis/agent-net/internal/harnesses"
	"github.com/niparis/agent-net/internal/protocol"
	"github.com/niparis/agent-net/internal/redact"
)

// DifficultyAssessor runs the task-difficulty analyzer and judge. It needs no
// running daemon: the dev command builds one from node.json, and the daemon
// builds one for each start. The sources replace the parts that need the
// gateway link or the harness inventory, so a test can supply them.
type DifficultyAssessor struct {
	cfg     Config
	sources DifficultySources
}

// DifficultySources are the reads and lookups the assessor needs beyond its
// config. A nil field uses the standalone default: no worker roster, the
// bundled score table, the bundled cut points, and the harness on this
// machine.
type DifficultySources struct {
	Profiles func(context.Context) ([]protocol.WorkerProfile, error)
	Scores   func(context.Context) ([]protocol.ModelScore, error)
	Minimums func(context.Context) (map[string]float64, error)
	Binary   func(string) (string, error)
}

// NewDifficultyAssessor returns an assessor for cfg. The daemon passes its
// gateway reads; the dev command passes a local roster read.
func NewDifficultyAssessor(cfg Config, sources DifficultySources) *DifficultyAssessor {
	return &DifficultyAssessor{cfg: cfg, sources: sources}
}

// Assess analyzes task and has a judge score the evidence. project is the
// repository folder. It never returns a label other than normal on a failure;
// err is set only when the caller's own context ended.
func (a *DifficultyAssessor) Assess(ctx context.Context, project, task string) (difficulty.Assessment, error) {
	started := time.Now()
	a0 := difficulty.Assessment{Label: "normal"}
	judgeNote := ""
	fallback := func(note string) (difficulty.Assessment, error) {
		a0.Notes = note
		if judgeNote != "" {
			a0.Notes += "; " + judgeNote
		}
		a0.Elapsed = time.Since(started)
		if ctx.Err() != nil {
			return a0, ctx.Err()
		}
		return a0, nil
	}
	dir := strings.TrimSpace(project)
	if dir == "" {
		return fallback("difficulty: the project folder is not known; started at normal")
	}
	if st, err := os.Stat(dir); err != nil || !st.IsDir() {
		return fallback(fmt.Sprintf("difficulty: project folder %s is not available; started at normal", dir))
	}
	if strings.TrimSpace(task) == "" {
		return fallback("difficulty: the task is empty; started at normal")
	}
	harness, model, err := a.difficultyModel(ctx)
	if err != nil {
		return fallback("difficulty: " + err.Error() + "; started at normal")
	}
	a0.AnalyzerModel = harness + ":" + model
	judgeHarness := strings.ToLower(strings.TrimSpace(a.cfg.DifficultyJudgeHarness))
	judgeModel := strings.TrimSpace(a.cfg.DifficultyJudgeModel)
	if judgeHarness == "" {
		judgeHarness = "opencode"
	}
	if judgeModel == "" {
		judgeModel = "opencode-go/deepseek-v4.1-flash"
	}
	if judgeHarness == "opencode" {
		if _, err := a.harnessBinary("opencode"); err != nil {
			judgeHarness, judgeModel = "codex", model
			judgeNote = "OpenCode is not installed; used the Codex judge instead"
		}
	}
	a0.JudgeModel = judgeHarness + ":" + judgeModel
	timeout := a.cfg.DifficultyTimeout
	if timeout <= 0 {
		timeout = defaultDifficultyTimeout
	}

	brief := analyzerBrief(task)
	raw, err := a.runDifficultyOneShot(ctx, harness, model, dir, brief, timeout)
	if err != nil {
		return fallback("difficulty: the analyzer " + err.Error() + "; started at normal")
	}
	profile, err := difficulty.ParseProfile([]byte(raw))
	if err != nil {
		retry := brief + "\nThe previous reply did not parse: " + err.Error() + "\nReply with the JSON only.\n"
		raw, runErr := a.runDifficultyOneShot(ctx, harness, model, dir, retry, timeout)
		if runErr != nil {
			return fallback("difficulty: the analyzer retry " + runErr.Error() + "; started at normal")
		}
		profile, err = difficulty.ParseProfile([]byte(raw))
		if err != nil {
			return fallback("difficulty: the analyzer reply did not parse: " + err.Error() + "; started at normal")
		}
	}
	a0.Profile = profile
	a0.Confidence = profile.Confidence
	if profile.Confidence == "low" {
		return fallback("difficulty: the analyzer confidence is low; started at normal")
	}

	judgeDir, err := os.MkdirTemp("", "agent-net-difficulty-judge-")
	if err != nil {
		return fallback("difficulty: cannot make a judge folder: " + err.Error() + "; started at normal")
	}
	defer os.RemoveAll(judgeDir)

	scores, insufficient, err := a.judgeDifficulty(ctx, judgeHarness, judgeModel, judgeDir, profile, timeout)
	if err != nil {
		return fallback("difficulty: the judge " + err.Error() + "; started at normal")
	}
	if len(insufficient) > 0 {
		patch, err := a.runDifficultyOneShot(ctx, harness, model, dir, analysisBrief(task, insufficient), timeout)
		if err != nil {
			return fallback("difficulty: the analyzer re-run " + err.Error() + "; started at normal")
		}
		merged, err := mergeDifficultyProfile(profile, []byte(patch), insufficient)
		if err != nil {
			return fallback("difficulty: the analyzed evidence could not be merged: " + err.Error() + "; started at normal")
		}
		profile = merged
		a0.Profile = profile
		a0.Confidence = profile.Confidence
		if profile.Confidence == "low" {
			return fallback("difficulty: the analyzer confidence is low; started at normal")
		}
		scores, insufficient, err = a.judgeDifficulty(ctx, judgeHarness, judgeModel, judgeDir, profile, timeout)
		if err != nil {
			return fallback("difficulty: the judge " + err.Error() + "; started at normal")
		}
		if len(insufficient) > 0 {
			return fallback("difficulty: the judge still lacked evidence for " + strings.Join(insufficient, ", ") + "; started at normal")
		}
	}
	a0.Scores = scores
	a0.Label, a0.D, a0.Bottleneck = difficulty.Label(scores)
	a0.Notes = judgeNote
	a0.Elapsed = time.Since(started)
	return a0, nil
}

// difficultyModel returns the harness and model of both runs. An explicit
// config pair wins. Otherwise it takes the lowest-max Codex profile of the
// worker roster: the weakest model the owner has registered is the cheapest
// one, and it is enough to find facts and score them.
func (a *DifficultyAssessor) difficultyModel(ctx context.Context) (string, string, error) {
	harness := strings.ToLower(strings.TrimSpace(a.cfg.DifficultyHarness))
	model := strings.TrimSpace(a.cfg.DifficultyModel)
	if harness != "" || model != "" {
		if harness == "" || model == "" {
			return "", "", errors.New("the difficulty override needs both a harness and a model")
		}
		if !difficultyOneShotHarnesses[harness] {
			return "", "", fmt.Errorf("harness %q has no supported one-shot run", harness)
		}
		return harness, model, nil
	}
	lookupCtx, cancel := context.WithTimeout(ctx, difficultyLookupTimeout)
	defer cancel()
	profiles, err := a.difficultyProfiles(lookupCtx)
	if err != nil {
		return "", "", err
	}
	harness, model, ok := cheapestDifficultyProfile(profiles, a.difficultyScores(lookupCtx), a.difficultyMinimums(lookupCtx))
	if !ok {
		return "", "", errors.New("no Codex profile in the worker roster can run a one-shot")
	}
	return harness, model, nil
}

func (a *DifficultyAssessor) difficultyProfiles(ctx context.Context) ([]protocol.WorkerProfile, error) {
	if a.sources.Profiles == nil {
		return nil, errors.New("the worker roster cannot be read outside a daemon; name a model")
	}
	return a.sources.Profiles(ctx)
}

// difficultyScores prefers the gateway's effective scores and falls back to the
// bundled table when the network is not reachable.
func (a *DifficultyAssessor) difficultyScores(ctx context.Context) []protocol.ModelScore {
	if a.sources.Scores != nil {
		if scores, err := a.sources.Scores(ctx); err == nil && len(scores) > 0 {
			return scores
		}
	}
	return protocol.EmbeddedModelScores()
}

// difficultyMinimums prefers the owner's cut points and falls back to the
// defaults.
func (a *DifficultyAssessor) difficultyMinimums(ctx context.Context) map[string]float64 {
	if a.sources.Minimums != nil {
		if minimums, err := a.sources.Minimums(ctx); err == nil && minimums != nil {
			return minimums
		}
	}
	return protocol.DifficultyMinimums
}

func (a *DifficultyAssessor) judgeDifficulty(ctx context.Context, harness, model, dir string, profile difficulty.Profile, timeout time.Duration) (difficulty.Scores, []string, error) {
	raw, err := a.runDifficultyOneShot(ctx, harness, model, dir, judgeBrief(profile), timeout)
	if err != nil {
		return difficulty.Scores{}, nil, err
	}
	return difficulty.ParseScores([]byte(extractDifficultyJudgeReply(raw)))
}

// runDifficultyOneShot runs one prompt and returns its final text. It never
// runs longer than timeout.
func (a *DifficultyAssessor) runDifficultyOneShot(ctx context.Context, harness, model, dir, prompt string, timeout time.Duration) (string, error) {
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	switch harness {
	case "codex":
		return a.runCodexExec(runCtx, model, dir, prompt, timeout)
	case "opencode":
		return a.runOpenCode(runCtx, model, prompt, timeout)
	default:
		return "", fmt.Errorf("harness %q has no supported one-shot run", harness)
	}
}

var ansiEscape = regexp.MustCompile(`\x1b\[[0-?]*[ -/]*[@-~]`)

// extractDifficultyJudgeReply strips terminal formatting and returns the last
// complete JSON object. OpenCode may print progress before its final answer.
func extractDifficultyJudgeReply(raw string) string {
	clean := ansiEscape.ReplaceAllString(raw, "")
	start, depth := -1, 0
	inString, escaped := false, false
	last := ""
	for i, r := range clean {
		if start < 0 {
			if r == '{' {
				start, depth = i, 1
			}
			continue
		}
		if inString {
			if escaped {
				escaped = false
			} else if r == '\\' {
				escaped = true
			} else if r == '"' {
				inString = false
			}
			continue
		}
		switch r {
		case '"':
			inString = true
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				last = clean[start : i+1]
				start = -1
			}
		}
	}
	if last != "" {
		return last
	}
	if i := strings.Index(strings.ToUpper(clean), "INSUFFICIENT_EVIDENCE"); i >= 0 {
		return strings.TrimSpace(clean[i:])
	}
	return strings.TrimSpace(clean)
}

// runOpenCode starts a one-shot judge in a fresh empty folder. OpenCode accepts
// its prompt as the final positional argument, so the prompt is passed there.
func (a *DifficultyAssessor) runOpenCode(ctx context.Context, model, prompt string, timeout time.Duration) (string, error) {
	bin, err := a.harnessBinary("opencode")
	if err != nil {
		return "", fmt.Errorf("cannot run: %w", err)
	}
	dir, err := os.MkdirTemp("", "agent-net-difficulty-opencode-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(dir)
	args := []string{"run", "-m", model, prompt}
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Dir = dir
	stdout, stderr := &capWriter{limit: 1 << 20}, &capWriter{limit: 4096}
	cmd.Stdout, cmd.Stderr = stdout, stderr
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	cmd.WaitDelay = 2 * time.Second
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			if errors.Is(ctx.Err(), context.DeadlineExceeded) {
				return "", fmt.Errorf("timed out after %s", timeout)
			}
			return "", errors.New("was canceled")
		}
		detail := redact.Cut(strings.Join(strings.Fields(stderr.String()), " "), 300)
		if detail == "" {
			return "", fmt.Errorf("failed: %v", err)
		}
		return "", fmt.Errorf("failed: %v: %s", err, detail)
	}
	text := strings.TrimSpace(stdout.String())
	if text == "" {
		return "", errors.New("returned no message")
	}
	return text, nil
}

// runCodexExec is `codex exec` in a read-only sandbox. The final message goes
// to -o, so progress on stdout and stderr cannot be mistaken for the answer.
func (a *DifficultyAssessor) runCodexExec(ctx context.Context, model, dir, prompt string, timeout time.Duration) (string, error) {
	bin, err := a.harnessBinary("codex")
	if err != nil {
		return "", fmt.Errorf("cannot run: %w", err)
	}
	outDir, err := os.MkdirTemp("", "agent-net-difficulty-out-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(outDir)
	last := filepath.Join(outDir, "last-message")
	args := []string{"exec", "-C", dir, "-s", "read-only", "--skip-git-repo-check", "--ephemeral", "-o", last}
	if model != "" {
		args = append(args, "-m", model)
	}
	if codexConfigDefinesAgentNet() {
		args = append(args, "-c", "mcp_servers.agent-net.enabled=false")
	}
	args = append(args, "-")
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Dir = dir
	cmd.Stdin = strings.NewReader(prompt)
	stderr := &capWriter{limit: 4096}
	cmd.Stderr = stderr
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
	cmd.WaitDelay = 2 * time.Second
	err = cmd.Run()
	out, _ := os.ReadFile(last)
	if ctx.Err() != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return "", fmt.Errorf("timed out after %s", timeout)
		}
		return "", errors.New("was canceled")
	}
	if err != nil {
		detail := redact.Cut(strings.Join(strings.Fields(stderr.String()), " "), 300)
		if detail == "" {
			return "", fmt.Errorf("failed: %v", err)
		}
		return "", fmt.Errorf("failed: %v: %s", err, detail)
	}
	text := strings.TrimSpace(string(out))
	if text == "" {
		return "", errors.New("returned no message")
	}
	return text, nil
}

// harnessBinary returns the path of an installed harness. Tests and the daemon
// supply their own lookup; standalone it checks HarnessPaths and then the usual
// install folders.
func (a *DifficultyAssessor) harnessBinary(name string) (string, error) {
	if a.sources.Binary != nil {
		return a.sources.Binary(name)
	}
	if p := a.cfg.HarnessPaths[name]; p != "" {
		return p, nil
	}
	search := harnesses.Options{}
	if a.cfg.HarnessSearch != nil {
		search = *a.cfg.HarnessSearch
	}
	if p := harnesses.Locate(context.Background(), name, search); p != "" {
		return p, nil
	}
	return "", fmt.Errorf("%s is not installed on this machine", name)
}
