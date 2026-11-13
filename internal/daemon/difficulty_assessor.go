package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
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
// config. A nil picker means no judge is available. A nil analyzer source
// uses the bundled score table and cut points or the local harness lookup.
type DifficultySources struct {
	PickJudge func(context.Context, string) (protocol.DifficultyJudgeChoice, error)
	Profiles  func(context.Context) ([]protocol.WorkerProfile, error)
	Scores    func(context.Context) ([]protocol.ModelScore, error)
	Minimums  func(context.Context) (map[string]float64, error)
	Binary    func(string) (string, error)
	Logf      func(string, ...any)
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
	mode := "judge-only"
	if a.cfg.DifficultyAnalyzer {
		mode = "analyzer + judge"
	}
	a0 := difficulty.Assessment{Label: "normal", Mode: mode}
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
	lookupCtx, lookupCancel := context.WithTimeout(ctx, difficultyLookupTimeout)
	first, err := a.pickJudge(lookupCtx, "")
	lookupCancel()
	if err != nil {
		return fallback("difficulty: could not choose a judge: " + err.Error() + "; started at normal")
	}
	if first.Harness == "" || first.Model == "" {
		machine := first.Machine
		if machine == "" {
			machine = a.cfg.Hostname
		}
		if machine == "" {
			machine = "this machine"
		}
		return fallback("difficulty: no judge: no registered normal-level model available on " + machine + "; started at normal")
	}
	judgeHarness, judgeModel := first.Harness, first.Model
	a0.JudgeModel = judgeHarness + ":" + judgeModel
	timeout := a.cfg.DifficultyTimeout
	if timeout <= 0 {
		timeout = defaultDifficultyTimeout
	}
	if !a.cfg.DifficultyAnalyzer {
		judgeDir, err := os.MkdirTemp("", "agent-net-difficulty-judge-")
		if err != nil {
			return fallback("difficulty: cannot make a judge folder: " + err.Error() + "; started at normal")
		}
		defer os.RemoveAll(judgeDir)
		res := a.judgeWithRetry(ctx, judgeDir, judgeBriefForTask(task), timeout,
			judgeSpec{judgeHarness, judgeModel})
		a0.JudgeModel = res.judge.harness + ":" + res.judge.model
		judgeNote = res.note
		if res.failure != "" {
			return fallback("difficulty: " + res.failure + "; started at normal")
		}
		if len(res.insufficient) > 0 {
			return fallback("difficulty: the judge returned INSUFFICIENT_EVIDENCE in judge-only mode; started at normal")
		}
		scores := res.scores
		a0.Scores = scores
		a0.Confidence = scores.Confidence
		if a0.Confidence == "" {
			a0.Confidence = "unknown"
		}
		a0.Label, a0.D, a0.Bottleneck, a0.RulesVersion = difficulty.Label(scores)
		a0.Notes = judgeNote
		a0.Elapsed = time.Since(started)
		return a0, nil
	}
	harness, model, err := a.difficultyModel(ctx)
	if err != nil {
		return fallback("difficulty: " + err.Error() + "; started at normal")
	}
	a0.AnalyzerModel = harness + ":" + model

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

	res := a.judgeWithRetry(ctx, judgeDir, judgeBrief(profile), timeout, judgeSpec{judgeHarness, judgeModel})
	a0.JudgeModel = res.judge.harness + ":" + res.judge.model
	judgeNote = res.note
	if res.failure != "" {
		return fallback("difficulty: " + res.failure + "; started at normal")
	}
	scores, insufficient := res.scores, res.insufficient
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
		res = a.judgeWithRetry(ctx, judgeDir, judgeBrief(profile), timeout, res.judge)
		a0.JudgeModel = res.judge.harness + ":" + res.judge.model
		judgeNote = res.note
		if res.failure != "" {
			return fallback("difficulty: " + res.failure + "; started at normal")
		}
		scores, insufficient = res.scores, res.insufficient
		if len(insufficient) > 0 {
			return fallback("difficulty: the judge still lacked evidence for " + strings.Join(insufficient, ", ") + "; started at normal")
		}
	}
	a0.Scores = scores
	if scores.Confidence != "" {
		a0.Confidence = scores.Confidence
	}
	a0.Label, a0.D, a0.Bottleneck, a0.RulesVersion = difficulty.Label(scores)
	a0.Notes = judgeNote
	a0.Elapsed = time.Since(started)
	return a0, nil
}

// Start a backup after 30 seconds, leaving it time in the 90 second budget.
func judgeHedge(budget time.Duration) time.Duration {
	return min(30*time.Second, budget/3)
}

func (a *DifficultyAssessor) pickJudge(ctx context.Context, exclude string) (protocol.DifficultyJudgeChoice, error) {
	if a.sources.PickJudge == nil {
		return protocol.DifficultyJudgeChoice{Machine: a.cfg.Hostname}, nil
	}
	return a.sources.PickJudge(ctx, exclude)
}

type judgeSpec struct{ harness, model string }

func (j judgeSpec) name() string { return judgeName(j.harness, j.model) }

type judgeOutcome struct {
	retry        bool
	scores       difficulty.Scores
	insufficient []string
	runErr       error
	parseErr     error
	dur          time.Duration
	raw          string
}

func (o judgeOutcome) ok() bool { return o.runErr == nil && o.parseErr == nil }

// judgeResult is the answer of judgeWithRetry. failure is set when no judge
// answered; note says how an answer was reached when it was not the plain
// first judge.
type judgeResult struct {
	judge        judgeSpec
	scores       difficulty.Scores
	insufficient []string
	note         string
	failure      string
}

// judgeWithRetry picks a backup only when the first fails or reaches the hedge
// point. The first valid answer wins and the other process is stopped.
func (a *DifficultyAssessor) judgeWithRetry(ctx context.Context, dir, prompt string, budget time.Duration, first judgeSpec) judgeResult {
	budgetCtx, cancel := context.WithTimeout(ctx, budget)
	defer cancel()
	deadline, _ := budgetCtx.Deadline()
	hedge := judgeHedge(budget)
	if a.cfg.testJudgeHedgeAfter > 0 {
		hedge = a.cfg.testJudgeHedgeAfter
	}
	started := time.Now()
	results := make(chan judgeOutcome, 2)
	run := func(runCtx context.Context, j judgeSpec, isRetry bool, timeout time.Duration) {
		t0 := time.Now()
		raw, err := a.runDifficultyOneShot(runCtx, j.harness, j.model, dir, prompt, timeout)
		o := judgeOutcome{retry: isRetry, runErr: err, dur: time.Since(t0), raw: raw}
		if err == nil {
			o.scores, o.insufficient, o.parseErr = parseJudgeReply(raw)
		}
		results <- o
	}
	firstCtx, stopFirst := context.WithCancel(budgetCtx)
	defer stopFirst()
	retryCtx, stopRetry := context.WithCancel(budgetCtx)
	defer stopRetry()
	go run(firstCtx, first, false, budget)
	pending := 1
	hedgeTimer := time.NewTimer(hedge)
	defer hedgeTimer.Stop()

	type pickResult struct {
		choice protocol.DifficultyJudgeChoice
		err    error
	}
	picks := make(chan pickResult, 1)
	var retry judgeSpec
	retryTried, retryStarted, picking := false, false, false
	var retryAt, retryTime time.Duration
	startRetry := func() {
		if retryTried {
			return
		}
		retryTried = true
		remaining := time.Until(deadline)
		if remaining <= 0 || budgetCtx.Err() != nil {
			return
		}
		picking = true
		go func() {
			lookupCtx, cancel := context.WithTimeout(retryCtx, difficultyLookupTimeout)
			defer cancel()
			c, err := a.pickJudge(lookupCtx, protocol.ProfileProvider(first.harness, first.model))
			picks <- pickResult{c, err}
		}()
	}
	firstFailure := func(o judgeOutcome) string {
		switch judgeAttemptResult(o.runErr, o.parseErr) {
		case "timeout":
			return formatJudgeTimeoutNote(first.harness, first.model, budget, budget, "", "", 0)
		case "parse error":
			return "the judge reply did not parse: " + o.parseErr.Error()
		}
		return "the judge " + o.runErr.Error()
	}
	retryFailure := func(o judgeOutcome) string {
		switch judgeAttemptResult(o.runErr, o.parseErr) {
		case "timeout":
			return fmt.Sprintf("retry judge %s timed out after the remaining %s", retry.name(), formatSeconds(retryTime))
		case "parse error":
			return "the retry judge reply did not parse: " + o.parseErr.Error()
		}
		return "the retry judge " + o.runErr.Error()
	}
	// Logs are written here, not in the judge goroutines, so a caller's Logf
	// is never called from two goroutines at once.
	logOutcome := func(o judgeOutcome, res string) {
		j := first
		if o.retry {
			j = retry
		}
		a.logAttempt(j.harness, j.model, o.dur, res)
		if o.runErr == nil && o.parseErr != nil {
			a.logReply(j.harness, j.model, o.raw)
		}
	}

	var firstOut, retryOut *judgeOutcome
	done := budgetCtx.Done()
	for pending > 0 || picking {
		select {
		case pick := <-picks:
			picking = false
			if pick.err != nil || pick.choice.Harness == "" || pick.choice.Model == "" || budgetCtx.Err() != nil {
				continue
			}
			retry = judgeSpec{pick.choice.Harness, pick.choice.Model}
			if protocol.ProfileProvider(retry.harness, retry.model) == protocol.ProfileProvider(first.harness, first.model) {
				continue
			}
			remaining := time.Until(deadline)
			if remaining <= 0 {
				continue
			}
			retryStarted, retryAt, retryTime = true, time.Since(started), remaining
			pending++
			go run(retryCtx, retry, true, remaining)
		case <-done:
			picking = false
			done = nil
		case <-hedgeTimer.C:
			startRetry()
		case o := <-results:
			pending--
			if !o.ok() {
				logOutcome(o, judgeAttemptResult(o.runErr, o.parseErr))
				if o.retry {
					retryOut = &o
				} else {
					firstOut = &o
					startRetry()
				}
				continue
			}
			logOutcome(o, "ok")
			if o.retry {
				stopFirst()
			} else {
				stopRetry()
			}
			// Wait for the other judge so no process outlives the assessment.
			for ; pending > 0; pending-- {
				other := <-results
				res := judgeAttemptResult(other.runErr, other.parseErr)
				if res == "other error" && ctx.Err() == nil {
					res = "stopped"
				}
				logOutcome(other, res)
			}
			out := judgeResult{judge: first, scores: o.scores, insufficient: o.insufficient}
			switch {
			case o.retry:
				out.judge = retry
				if firstOut != nil {
					out.note = firstFailure(*firstOut)
				} else {
					out.note = fmt.Sprintf("first judge %s had no answer after %s; retry judge %s answered first", first.name(), formatSeconds(retryAt), retry.name())
				}
			case retryStarted && retryOut != nil:
				out.note = fmt.Sprintf("first judge %s answered after %s; %s", first.name(), formatSeconds(o.dur), retryFailure(*retryOut))
			case retryStarted:
				out.note = fmt.Sprintf("first judge %s answered after %s; retry judge %s was stopped", first.name(), formatSeconds(o.dur), retry.name())
			}
			return out
		}
	}
	out := judgeResult{judge: first}
	if firstOut != nil {
		out.failure = firstFailure(*firstOut)
	}
	if retryOut != nil {
		out.judge = retry
		out.failure += "; " + retryFailure(*retryOut)
	}
	return out
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
	case "claude", "pi", "agy":
		return a.runJudgeCLI(runCtx, harness, model, dir, prompt, timeout)
	default:
		return "", fmt.Errorf("harness %q has no supported one-shot run", harness)
	}
}

var ansiEscape = regexp.MustCompile(`\x1b\[[0-?]*[ -/]*[@-~]`)

// parseJudgeReply finds the scores in a judge's final text. OpenCode may print
// progress before the answer, and a model may wrap the JSON in a code fence or
// write text around it. Score objects outside other objects are candidates,
// the last one first. A stray brace in the text does not hide a
// later object. The error says what was wrong with the reply and its size,
// never only "unexpected end of JSON input".
func parseJudgeReply(raw string) (difficulty.Scores, []string, error) {
	clean := strings.TrimSpace(ansiEscape.ReplaceAllString(raw, ""))
	if clean == "" {
		return difficulty.Scores{}, nil, errors.New("the reply is empty")
	}
	// Scan from the beginning: slicing the tail can discard an outer brace
	// and turn its nested reasons into a candidate.
	text := clean
	var scanErr error
	var objects []string
	for i := 0; i < len(text); {
		j := strings.IndexByte(text[i:], '{')
		if j < 0 {
			break
		}
		i += j
		dec := json.NewDecoder(strings.NewReader(text[i:]))
		var obj json.RawMessage
		decodeErr := dec.Decode(&obj)
		if decodeErr == nil && len(obj) > 0 && obj[0] == '{' {
			objects = append(objects, string(obj))
			i += int(dec.InputOffset())
			continue
		}
		// A JSON-looking outer object owns its nested objects even when it
		// is incomplete. Never reinterpret its reasons as numeric scores.
		if strings.HasPrefix(strings.TrimSpace(text[i+1:]), "\"") {
			end := judgeObjectEnd(text[i:])
			if end < 0 {
				if repaired := closeJudgeObject(text[i:]); repaired != "" {
					objects = append(objects, repaired)
					break
				}
				scanErr = fmt.Errorf("no complete JSON object in the %d-byte reply (it may be cut off): %v", len(clean), decodeErr)
				break
			}
			scanErr = fmt.Errorf("invalid outer JSON object in the %d-byte reply: %v", len(clean), decodeErr)
			i += end
			continue
		}
		i++
	}
	var lastErr error
	for k := len(objects) - 1; k >= 0; k-- {
		var keys map[string]json.RawMessage
		_ = json.Unmarshal([]byte(objects[k]), &keys)
		if _, ok := keys["reasoning"]; !ok {
			continue
		}
		scores, insufficient, err := difficulty.ParseScores([]byte(objects[k]))
		if err == nil {
			return scores, insufficient, nil
		}
		if lastErr == nil {
			lastErr = err
		}
	}
	if scanErr != nil {
		return difficulty.Scores{}, nil, scanErr
	}
	if lastErr != nil {
		return difficulty.Scores{}, nil, lastErr
	}
	if i := strings.Index(strings.ToUpper(clean), "INSUFFICIENT_EVIDENCE"); i >= 0 {
		return difficulty.ParseScores([]byte(strings.TrimSpace(clean[i:])))
	}
	if strings.Contains(clean, "{") {
		return difficulty.Scores{}, nil, fmt.Errorf("no complete JSON object in the %d-byte reply (it may be cut off)", len(clean))
	}
	return difficulty.Scores{}, nil, fmt.Errorf("no JSON object in the %d-byte reply", len(clean))
}

// closeJudgeObject repairs only omitted terminal object braces. Validation
// still rejects missing values, unfinished strings, arrays, and trailing prose.
func closeJudgeObject(text string) string {
	depth := 0
	quoted, escaped := false, false
	for i := 0; i < len(text); i++ {
		c := text[i]
		if quoted {
			if escaped {
				escaped = false
			} else if c == '\\' {
				escaped = true
			} else if c == '"' {
				quoted = false
			}
			continue
		}
		switch c {
		case '"':
			quoted = true
		case '{':
			depth++
		case '}':
			depth--
		}
	}
	if quoted || depth <= 0 {
		return ""
	}
	repaired := text + strings.Repeat("}", depth)
	if !json.Valid([]byte(repaired)) {
		return ""
	}
	return repaired
}

// judgeObjectEnd finds the boundary even when JSON syntax is invalid, so
// nested objects cannot become independent candidates after a decode error.
func judgeObjectEnd(text string) int {
	depth := 0
	quoted, escaped := false, false
	for i := 0; i < len(text); i++ {
		c := text[i]
		if quoted {
			if escaped {
				escaped = false
			} else if c == '\\' {
				escaped = true
			} else if c == '"' {
				quoted = false
			}
			continue
		}
		switch c {
		case '"':
			quoted = true
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return i + 1
			}
		}
	}
	return -1
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
	// OpenCode files the session under $PWD when it is set, not under the
	// process folder; an inherited PWD would put the judge's session in the
	// caller's folder, where the cleanup below does not look.
	env := workerEnv(os.Environ(), map[string]string{
		"AGENT_NET_SOCKET": filepath.Join(dir, "no-agent-net", "daemon.sock"),
		"PWD":              dir,
		IgnoreEnv:          "1",
	})
	// Even a standalone run saves its session in OpenCode's shared store, where
	// it shows as an idle session. Delete it once the judge is done, in the
	// background: a retry judge must not wait for this cleanup, which runs in
	// dir, so dir goes after it.
	defer func() {
		go func() {
			deleteOpenCodeSessions(ctx, bin, dir, env)
			os.RemoveAll(dir)
		}()
	}()
	// A standalone server exits with the judge instead of leaving a session
	// running in OpenCode's shared service. The nonexistent socket also prevents
	// the agent-net plugin from registering that private session or exposing tools.
	selection := protocol.ParseModelSelection("opencode", "", model, "")
	args := append([]string{"run", "--standalone"}, openCodeModelFlags(ctx, bin, selection)...)
	args = append(args, prompt)
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Dir = dir
	cmd.Env = env
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

// runJudgeCLI runs a prompt without tools or a persistent interactive session.
func (a *DifficultyAssessor) runJudgeCLI(ctx context.Context, harness, model, dir, prompt string, timeout time.Duration) (string, error) {
	bin, err := a.harnessBinary(harness)
	if err != nil {
		return "", fmt.Errorf("cannot run: %w", err)
	}
	selection := protocol.ParseModelSelection(harness, "", model, "")
	models := []string{selection.ModelID()}
	modelArgs, err := selection.Arguments(models)
	if err != nil {
		return "", err
	}
	var args []string
	switch harness {
	case "claude":
		args = []string{"--print", "--output-format", "text", "--no-session-persistence", "--tools", "", "--strict-mcp-config", "--mcp-config", `{"mcpServers":{}}`}
		args = append(args, modelArgs...)
	case "pi":
		args = []string{"--print", "--no-session", "--no-tools", "--no-extensions"}
		args = append(args, modelArgs...)
	case "agy":
		args = []string{"--mode", "plan", "--sandbox", "--settings", `{"permissions":{"deny":["*"]}}`}
		args = append(args, modelArgs...)
	}
	if harness == "agy" {
		args = append(args, "-p="+prompt)
	} else {
		args = append(args, prompt)
	}
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Dir = dir
	cmd.Env = workerEnv(os.Environ(), map[string]string{IgnoreEnv: "1", "PWD": dir, "AGENT_NET_SOCKET": filepath.Join(dir, "no-agent-net", "daemon.sock")})
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

// deleteOpenCodeSessions deletes the sessions OpenCode saved for dir. Only
// sessions whose folder is exactly dir are touched, so the owner's own
// sessions are never listed for deletion. Failures are ignored: a leftover
// session is untidy, not harmful.
func deleteOpenCodeSessions(parent context.Context, bin, dir string, env []string) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(parent), 10*time.Second)
	defer cancel()
	list := exec.CommandContext(ctx, bin, "session", "list", "--format", "json")
	list.Dir, list.Env = dir, env
	out, err := list.Output()
	if err != nil {
		return
	}
	var sessions []struct {
		ID        string `json:"id"`
		Directory string `json:"directory"`
	}
	if json.Unmarshal(out, &sessions) != nil {
		return
	}
	// macOS temp folders sit behind a symlink (/var to /private/var).
	want := map[string]bool{filepath.Clean(dir): true}
	if real, err := filepath.EvalSymlinks(dir); err == nil {
		want[real] = true
	}
	for _, s := range sessions {
		if s.ID == "" || !want[filepath.Clean(s.Directory)] {
			continue
		}
		del := exec.CommandContext(ctx, bin, "session", "delete", s.ID)
		del.Dir, del.Env = dir, env
		_ = del.Run()
	}
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
	selection := protocol.ParseModelSelection("codex", "", model, "")
	if selection.Effort != "" {
		args = append(args, "-c", "model_reasoning_effort="+strconv.Quote(selection.Effort))
	}
	if selection.ModelID() != "" {
		args = append(args, "-m", selection.ModelID())
	}
	if codexConfigDefinesAgentNet() {
		args = append(args, "-c", "mcp_servers.agent-net.enabled=false")
	}
	args = append(args, "-")
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Dir = dir
	cmd.Env = workerEnv(os.Environ(), map[string]string{
		IgnoreEnv: "1",
		"PWD":     dir,
	})
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

func judgeName(harness, model string) string {
	harness = strings.TrimSpace(harness)
	model = strings.TrimSpace(model)
	if strings.HasPrefix(model, harness) {
		return model
	}
	if harness != "" && model != "" {
		return harness + " " + model
	}
	if model != "" {
		return model
	}
	return harness
}

func formatSeconds(d time.Duration) string {
	s := int((d + 500*time.Millisecond) / time.Second)
	if s <= 0 && d > 0 {
		s = 1
	}
	return fmt.Sprintf("%ds", s)
}

func formatJudgeTimeoutNote(firstHarness, firstModel string, firstTimeout, budget time.Duration, retryHarness, retryModel string, remaining time.Duration) string {
	firstNote := fmt.Sprintf("first judge %s timed out at %s of the %s budget", judgeName(firstHarness, firstModel), formatSeconds(firstTimeout), formatSeconds(budget))
	if retryHarness != "" || retryModel != "" {
		return fmt.Sprintf("%s; retry judge %s timed out after the remaining %s", firstNote, judgeName(retryHarness, retryModel), formatSeconds(remaining))
	}
	return firstNote
}

func judgeAttemptResult(runErr, parseErr error) string {
	if runErr != nil {
		if strings.HasPrefix(runErr.Error(), "timed out after") || errors.Is(runErr, context.DeadlineExceeded) {
			return "timeout"
		}
		return "other error"
	}
	if parseErr != nil {
		return "parse error"
	}
	return "ok"
}

func (a *DifficultyAssessor) logf(format string, args ...any) {
	if a.sources.Logf != nil {
		a.sources.Logf(format, args...)
	}
}

// judgeReplyLogLimit caps the reply that a parse failure writes to the log.
const judgeReplyLogLimit = 16 << 10

// logReply writes a reply that did not parse, without secrets and capped, so
// the next failure shows what the judge said.
func (a *DifficultyAssessor) logReply(harness, model, raw string) {
	clean := strings.TrimSpace(ansiEscape.ReplaceAllString(raw, ""))
	a.logf("judge.reply harness=%s model=%s bytes=%d reply=%q", harness, model, len(clean), redact.Cut(redact.Secrets(clean), judgeReplyLogLimit))
}

func (a *DifficultyAssessor) logAttempt(harness, model string, dur time.Duration, res string) {
	resultVal := res
	if strings.Contains(res, " ") {
		resultVal = fmt.Sprintf("%q", res)
	}
	a.logf("judge.attempt harness=%s model=%s duration=%s result=%s", harness, model, dur.Round(time.Millisecond), resultVal)
}
