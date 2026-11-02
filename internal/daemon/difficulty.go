package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/niparis/agent-net/internal/difficulty"
	"github.com/niparis/agent-net/internal/protocol"
	"github.com/niparis/agent-net/internal/redact"
)

// Task difficulty from evidence (docs/design/task-difficulty.md sections 2, 4,
// 5 and 7). assessDifficulty runs two short one-shot models and turns their
// answer into a label. It changes nothing and is not called by a worker start
// yet.
//
// The one-shot run is `codex exec`. Codex is the only worker harness that runs
// one prompt non-interactively and enforces a read-only sandbox in the same
// command: -s read-only, -C <folder>, -m <model>, and -o <file> for the final
// message. `opencode run` needs its own server and has no read-only mode,
// `claude -p` leaves read-only to a tool allowlist, and Pi has no read-only
// mode at all. The model and harness come from the worker roster, so no new
// model client is built here.
//
// The analyzer runs in the project folder and may read it. The judge runs in an
// empty temporary folder and gets only the profile. Each run has its own time
// limit. Any failure, timeout, low confidence or second insufficient answer
// returns "normal" with the reason in Notes: the start must go ahead.

const defaultDifficultyTimeout = 3 * time.Minute

// difficultyLookupTimeout bounds the roster and score reads, so a slow gateway
// cannot hold a worker start past the run limits.
const difficultyLookupTimeout = 10 * time.Second

// difficultyOneShotHarnesses are the harnesses whose one-shot run is supported.
// Codex alone, for the reason above.
var difficultyOneShotHarnesses = map[string]bool{"codex": true}

// difficultySchema is the evidence profile of section 4, as the analyzer sees
// it.
const difficultySchema = `{
  "reasoning":    {"closest_pattern": "...", "new_logic": "...", "evidence": ["path:lines"]},
  "scope":        {"components": [...], "distinct_changes": [...], "repeated_changes": [...]},
  "coupling":     {"contracts": [...], "invariants": [...], "evidence": [...]},
  "uncertainty":  {"open_questions": [...], "inspected": [...]},
  "verification": {"tests": [...], "needed_checks": [...], "gaps": [...]},
  "risk":         {"failures": [...], "reversibility": "...", "evidence": [...]},
  "confidence":   "low | medium | high"
}`

// assessDifficulty analyzes task and has a judge score the evidence. project is
// the repository folder. It never returns a label other than normal on a
// failure; err is set only when the caller's own context ended.
func (d *Daemon) assessDifficulty(ctx context.Context, project, task string) (difficulty.Assessment, error) {
	started := time.Now()
	a := difficulty.Assessment{Label: "normal"}
	fallback := func(note string) (difficulty.Assessment, error) {
		a.Notes = note
		a.Elapsed = time.Since(started)
		if ctx.Err() != nil {
			return a, ctx.Err()
		}
		return a, nil
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
	harness, model, err := d.difficultyModel(ctx)
	if err != nil {
		return fallback("difficulty: " + err.Error() + "; started at normal")
	}
	a.AnalyzerModel = harness + ":" + model
	a.JudgeModel = a.AnalyzerModel
	timeout := d.cfg.DifficultyTimeout
	if timeout <= 0 {
		timeout = defaultDifficultyTimeout
	}

	brief := analyzerBrief(task)
	raw, err := d.runDifficultyOneShot(ctx, harness, model, dir, brief, timeout)
	if err != nil {
		return fallback("difficulty: the analyzer " + err.Error() + "; started at normal")
	}
	profile, err := difficulty.ParseProfile([]byte(raw))
	if err != nil {
		retry := brief + "\nThe previous reply did not parse: " + err.Error() + "\nReply with the JSON only.\n"
		raw, runErr := d.runDifficultyOneShot(ctx, harness, model, dir, retry, timeout)
		if runErr != nil {
			return fallback("difficulty: the analyzer retry " + runErr.Error() + "; started at normal")
		}
		profile, err = difficulty.ParseProfile([]byte(raw))
		if err != nil {
			return fallback("difficulty: the analyzer reply did not parse: " + err.Error() + "; started at normal")
		}
	}
	a.Profile = profile
	a.Confidence = profile.Confidence
	if profile.Confidence == "low" {
		return fallback("difficulty: the analyzer confidence is low; started at normal")
	}

	judgeDir, err := os.MkdirTemp("", "agent-net-difficulty-judge-")
	if err != nil {
		return fallback("difficulty: cannot make a judge folder: " + err.Error() + "; started at normal")
	}
	defer os.RemoveAll(judgeDir)

	scores, insufficient, err := d.judgeDifficulty(ctx, harness, model, judgeDir, profile, timeout)
	if err != nil {
		return fallback("difficulty: the judge " + err.Error() + "; started at normal")
	}
	if len(insufficient) > 0 {
		patch, err := d.runDifficultyOneShot(ctx, harness, model, dir, analysisBrief(task, insufficient), timeout)
		if err != nil {
			return fallback("difficulty: the analyzer re-run " + err.Error() + "; started at normal")
		}
		merged, err := mergeDifficultyProfile(profile, []byte(patch), insufficient)
		if err != nil {
			return fallback("difficulty: the analyzed evidence could not be merged: " + err.Error() + "; started at normal")
		}
		profile = merged
		a.Profile = profile
		a.Confidence = profile.Confidence
		if profile.Confidence == "low" {
			return fallback("difficulty: the analyzer confidence is low; started at normal")
		}
		scores, insufficient, err = d.judgeDifficulty(ctx, harness, model, judgeDir, profile, timeout)
		if err != nil {
			return fallback("difficulty: the judge " + err.Error() + "; started at normal")
		}
		if len(insufficient) > 0 {
			return fallback("difficulty: the judge still lacked evidence for " + strings.Join(insufficient, ", ") + "; started at normal")
		}
	}
	a.Scores = scores
	a.Label, a.D, a.Bottleneck = difficulty.Label(scores)
	a.Elapsed = time.Since(started)
	return a, nil
}

// difficultyModel returns the harness and model of both runs. An explicit
// config pair wins. Otherwise it takes the lowest-max Codex profile of the
// worker roster: the weakest model the owner has registered is the cheapest
// one, and it is enough to find facts and score them.
func (d *Daemon) difficultyModel(ctx context.Context) (string, string, error) {
	harness := strings.ToLower(strings.TrimSpace(d.cfg.DifficultyHarness))
	model := strings.TrimSpace(d.cfg.DifficultyModel)
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
	profiles, err := d.difficultyProfiles(lookupCtx)
	if err != nil {
		return "", "", err
	}
	harness, model, ok := cheapestDifficultyProfile(profiles, d.difficultyScores(lookupCtx), d.difficultyMinimums(lookupCtx))
	if !ok {
		return "", "", errors.New("no Codex profile in the worker roster can run a one-shot")
	}
	return harness, model, nil
}

// difficultyProfiles reads the worker roster through the gateway link. The
// gateway checks the requester, so use a local agent session.
func (d *Daemon) difficultyProfiles(ctx context.Context) ([]protocol.WorkerProfile, error) {
	requester := d.difficultyRequester()
	if requester == "" {
		return nil, errors.New("the worker roster cannot be read without an agent session")
	}
	raw, err := d.call(ctx, "worker.profile.list", protocol.WorkerProfileFrame{Requester: requester})
	if err != nil {
		return nil, fmt.Errorf("the worker roster could not be read: %w", err)
	}
	var profiles []protocol.WorkerProfile
	if err := json.Unmarshal(raw, &profiles); err != nil {
		return nil, fmt.Errorf("the worker roster is not readable: %w", err)
	}
	return profiles, nil
}

func (d *Daemon) difficultyRequester() string {
	if id := d.ownerSessionID(); id != "" {
		return id
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	for _, ls := range d.sessions {
		if ls == nil || ls.sessionID == "" || ls.info.Worker != nil {
			continue
		}
		return ls.sessionID
	}
	return ""
}

// difficultyScores prefers the gateway's effective scores and falls back to the
// bundled table when the network is not reachable.
func (d *Daemon) difficultyScores(ctx context.Context) []protocol.ModelScore {
	if scores, err := d.scores(ctx, nil); err == nil && len(scores) > 0 {
		return scores
	}
	return protocol.EmbeddedModelScores()
}

// difficultyMinimums prefers the owner's cut points and falls back to the
// defaults.
func (d *Daemon) difficultyMinimums(ctx context.Context) map[string]float64 {
	if minimums, err := d.difficulty(ctx, nil); err == nil && minimums != nil {
		return minimums
	}
	return protocol.DifficultyMinimums
}

// cheapestDifficultyProfile picks the profile with the lowest maximum
// difficulty among the harnesses that can run a one-shot. Ties go by model and
// profile name, so the choice is stable.
func cheapestDifficultyProfile(profiles []protocol.WorkerProfile, scores []protocol.ModelScore, minimums map[string]float64) (string, string, bool) {
	levels := []string{"trivial", "simple", "normal", "hard", "very_complex"}
	type candidate struct {
		harness, model, name string
		rank                 int
	}
	var best *candidate
	for _, p := range profiles {
		harness := strings.ToLower(strings.TrimSpace(p.Harness))
		if !difficultyOneShotHarnesses[harness] {
			continue
		}
		model := strings.TrimSpace(p.Model)
		if model == "" {
			continue
		}
		score, ok := protocol.LookupModelScoreWithDefaultEffort(model, p.Effort, scores)
		if !ok {
			continue
		}
		max := protocol.WorkerMaximumLevel(score.Score, minimums)
		rank := -1
		for i, level := range levels {
			if level == max {
				rank = i
				break
			}
		}
		if rank < 0 {
			continue
		}
		c := candidate{harness: harness, model: model, name: p.Name, rank: rank}
		if best == nil || c.rank < best.rank ||
			(c.rank == best.rank && (c.model < best.model || (c.model == best.model && c.name < best.name))) {
			best = &c
		}
	}
	if best == nil {
		return "", "", false
	}
	return best.harness, best.model, true
}

func analyzerBrief(task string) string {
	return "AGENT_NET_DIFFICULTY_KIND=analyzer\n" +
		"[difficulty analyzer] Read the repository in this folder. Report evidence only; do not rate the task.\n\n" +
		"Task:\n" + task + "\n\n" +
		"Rules:\n" + difficulty.AnalyzerRules() + "\n\n" +
		"Reply with a JSON object matching this schema:\n" + difficultySchema + "\n" +
		"Reply with the JSON only.\n"
}

func analysisBrief(task string, dimensions []string) string {
	return "AGENT_NET_DIFFICULTY_KIND=analysis\n" +
		"[difficulty analyzer] The judge needs more evidence. Read the repository in this folder and report evidence only.\n\n" +
		"Task:\n" + task + "\n\n" +
		"Only these dimensions need evidence: " + strings.Join(dimensions, ", ") + ".\n" +
		"Rules:\n" + difficulty.AnalyzerRules() + "\n\n" +
		"Reply with a JSON object with only these keys, each matching its section in this schema:\n" + difficultySchema + "\n" +
		"Reply with the JSON only.\n"
}

func judgeBrief(profile difficulty.Profile) string {
	raw, _ := json.Marshal(profile)
	return "AGENT_NET_DIFFICULTY_KIND=judge\n" +
		"[difficulty judge] Score the evidence profile. Do not inspect a repository.\n\n" +
		"Rubric:\n" + difficulty.RubricText() + "\n\n" +
		"Evidence profile:\n" + string(raw) + "\n\n" +
		"Score each dimension 1 to 5 with one line of reason, as JSON; or INSUFFICIENT_EVIDENCE with the dimensions and what to inspect.\n" +
		`Reply like {"reasoning":3,"scope":3,"coupling":3,"uncertainty":3,"verification":3,"risk":3,"reasons":{"reasoning":"...","scope":"...","coupling":"...","uncertainty":"...","verification":"...","risk":"..."}}.` + "\n"
}

func (d *Daemon) judgeDifficulty(ctx context.Context, harness, model, dir string, profile difficulty.Profile, timeout time.Duration) (difficulty.Scores, []string, error) {
	raw, err := d.runDifficultyOneShot(ctx, harness, model, dir, judgeBrief(profile), timeout)
	if err != nil {
		return difficulty.Scores{}, nil, err
	}
	return difficulty.ParseScores([]byte(raw))
}

// mergeDifficultyProfile replaces the named sections of base with the sections
// the targeted analyzer returned, then validates the whole profile again.
func mergeDifficultyProfile(base difficulty.Profile, patch []byte, dimensions []string) (difficulty.Profile, error) {
	baseRaw, err := json.Marshal(base)
	if err != nil {
		return difficulty.Profile{}, err
	}
	var full map[string]json.RawMessage
	if err := json.Unmarshal(baseRaw, &full); err != nil {
		return difficulty.Profile{}, err
	}
	var sections map[string]json.RawMessage
	if err := json.Unmarshal(patch, &sections); err != nil {
		return difficulty.Profile{}, fmt.Errorf("targeted profile: %w", err)
	}
	for _, dimension := range dimensions {
		value, ok := sections[dimension]
		if !ok {
			return difficulty.Profile{}, fmt.Errorf("targeted profile lacks %q", dimension)
		}
		full[dimension] = value
	}
	merged, err := json.Marshal(full)
	if err != nil {
		return difficulty.Profile{}, err
	}
	return difficulty.ParseProfile(merged)
}

// runDifficultyOneShot runs one prompt and returns its final text. It never
// runs longer than timeout.
func (d *Daemon) runDifficultyOneShot(ctx context.Context, harness, model, dir, prompt string, timeout time.Duration) (string, error) {
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	switch harness {
	case "codex":
		return d.runCodexExec(runCtx, model, dir, prompt, timeout)
	default:
		return "", fmt.Errorf("harness %q has no supported one-shot run", harness)
	}
}

// runCodexExec is `codex exec` in a read-only sandbox. The final message goes
// to -o, so progress on stdout and stderr cannot be mistaken for the answer.
func (d *Daemon) runCodexExec(ctx context.Context, model, dir, prompt string, timeout time.Duration) (string, error) {
	bin, err := d.harnessBinary("codex")
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

// capWriter keeps the first limit bytes and counts the rest.
type capWriter struct {
	b     strings.Builder
	limit int
	n     int
}

func (w *capWriter) Write(p []byte) (int, error) {
	if w.n < w.limit {
		room := w.limit - w.n
		if len(p) < room {
			room = len(p)
		}
		w.b.Write(p[:room])
	}
	w.n += len(p)
	return len(p), nil
}

func (w *capWriter) String() string { return w.b.String() }
