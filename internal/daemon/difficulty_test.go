package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// difficultyProfileJSON is a complete evidence profile that ParseProfile
// accepts. Tests change only the confidence.
func difficultyProfileJSON(confidence string) string {
	return `{"reasoning":{"closest_pattern":"internal/daemon/workers.go:700-710","new_logic":"none","evidence":["internal/daemon/workers.go:700-710"]},` +
		`"scope":{"components":["internal/daemon"],"distinct_changes":["one change"],"repeated_changes":[]},` +
		`"coupling":{"contracts":[],"invariants":[],"evidence":["inspected: no shared contract"]},` +
		`"uncertainty":{"open_questions":[],"inspected":["inspected: the worker start path"]},` +
		`"verification":{"tests":["unit"],"needed_checks":[],"gaps":[]},` +
		`"risk":{"failures":[],"reversibility":"easy","evidence":["docs/reference.md:1"]},` +
		`"confidence":"` + confidence + `"}`
}

const difficultyScoresJSON = `{"reasoning":4,"scope":3,"coupling":4,"uncertainty":3,"verification":3,"risk":4,` +
	`"reasons":{"reasoning":"internal/daemon/workers.go:700-710","scope":"one component",` +
	`"coupling":"inspected: no shared contract","uncertainty":"inspected: the start path",` +
	`"verification":"unit tests exist","risk":"docs/reference.md:1"}}`

// fakeDifficultyRun installs the fake codex and the canned replies of one
// assessDifficulty call. Each kind (analyzer, analysis, judge) is a JSON array;
// the fake takes the first entry and writes the rest back.
func fakeDifficultyRun(t *testing.T, replies map[string][]string) string {
	t.Helper()
	t.Setenv(envFakeCodex, "1")
	dir := t.TempDir()
	for kind, list := range replies {
		raw, err := json.Marshal(list)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, kind+".json"), raw, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv(envFakeCodexExec, dir)
	return dir
}

func newDifficultyDaemon(t *testing.T, harness, model string, timeout time.Duration) *Daemon {
	t.Helper()
	d, err := New(Config{
		StateDir:               t.TempDir(),
		HarnessPaths:           map[string]string{harness: os.Args[0]},
		DifficultyHarness:      harness,
		DifficultyModel:        model,
		DifficultyAnalyzer:     true,
		DifficultyJudgeHarness: "codex",
		DifficultyJudgeModel:   model,
		DifficultyTimeout:      timeout,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.st.close() })
	return d
}

type difficultyCall struct {
	Args  []string `json:"args"`
	Cwd   string   `json:"cwd"`
	Stdin string   `json:"stdin"`
	Kind  string   `json:"kind"`
}

func difficultyCalls(t *testing.T, dir string) []difficultyCall {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(dir, "calls.jsonl"))
	if err != nil {
		t.Fatalf("the fake codex wrote no calls: %v", err)
	}
	var out []difficultyCall
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		var c difficultyCall
		if err := json.Unmarshal([]byte(line), &c); err != nil {
			t.Fatalf("call record: %v", err)
		}
		out = append(out, c)
	}
	return out
}

func TestAssessDifficultyHappyPath(t *testing.T) {
	dir := fakeDifficultyRun(t, map[string][]string{
		"analyzer": {difficultyProfileJSON("high")},
		"judge":    {difficultyScoresJSON},
	})
	d := newDifficultyDaemon(t, "codex", "gpt-cheap", 2*time.Second)
	project := t.TempDir()
	a, err := d.assessDifficulty(context.Background(), project, "change the settings tab")
	if err != nil {
		t.Fatal(err)
	}
	if a.Label != "hard" || a.Bottleneck != "" || a.Confidence != "high" {
		t.Fatalf("assessment: %+v", a)
	}
	if a.AnalyzerModel != "codex:gpt-cheap" || a.JudgeModel != "codex:gpt-cheap" {
		t.Fatalf("models: %q %q", a.AnalyzerModel, a.JudgeModel)
	}
	if a.Scores.Reasoning != 4 || a.Scores.Risk != 4 {
		t.Fatalf("scores: %+v", a.Scores)
	}
	if a.Profile.Confidence != "high" {
		t.Fatalf("profile: %+v", a.Profile)
	}
	if a.Elapsed <= 0 {
		t.Fatal("elapsed is not measured")
	}
	calls := difficultyCalls(t, dir)
	if len(calls) != 2 || calls[0].Kind != "analyzer" || calls[1].Kind != "judge" {
		t.Fatalf("calls: %+v", calls)
	}
	if calls[0].Cwd != project {
		t.Errorf("analyzer ran in %q, want the project %q", calls[0].Cwd, project)
	}
	if calls[1].Cwd == project {
		t.Errorf("judge ran in the project folder; it needs no repository")
	}
	if !hasArg(calls[0].Args, "-s", "read-only") {
		t.Errorf("analyzer args lack a read-only sandbox: %v", calls[0].Args)
	}
	for _, want := range []string{"Count distinct changes, not files", `"confidence"`, "Reply with the JSON only"} {
		if !strings.Contains(calls[0].Stdin, want) {
			t.Errorf("analyzer brief lacks %q:\n%s", want, calls[0].Stdin)
		}
	}
	if !strings.Contains(calls[1].Stdin, "Score each dimension 1 to 5") {
		t.Errorf("judge brief lacks the instruction:\n%s", calls[1].Stdin)
	}
}

func TestAssessDifficultyUsesOpenCodeJudgeAndExtractsNoisyJSON(t *testing.T) {
	dir := fakeDifficultyRun(t, map[string][]string{"analyzer": {difficultyProfileJSON("high")}})
	judgeReply := filepath.Join(dir, "opencode-reply")
	if err := os.WriteFile(judgeReply, []byte(difficultyScoresJSON), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv(envFakeOpenCode, "1")
	t.Setenv(envFakeOpenCodeReply, judgeReply)
	d, err := New(Config{
		StateDir:             t.TempDir(),
		HarnessPaths:         map[string]string{"codex": os.Args[0], "opencode": os.Args[0]},
		DifficultyHarness:    "codex",
		DifficultyModel:      "gpt-analyzer",
		DifficultyAnalyzer:   true,
		DifficultyJudgeModel: "opencode-go/deepseek-v4.1-flash",
		DifficultyTimeout:    2 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.st.close() })
	a, err := d.assessDifficulty(context.Background(), t.TempDir(), "change a setting")
	if err != nil {
		t.Fatal(err)
	}
	if a.Label != "hard" || a.AnalyzerModel != "codex:gpt-analyzer" || a.JudgeModel != "opencode:opencode-go/deepseek-v4.1-flash" {
		t.Fatalf("assessment: %+v", a)
	}
	calls := difficultyCalls(t, dir)
	if len(calls) != 1 || calls[0].Kind != "analyzer" {
		t.Fatalf("Codex calls: %+v", calls)
	}
}

func TestAssessDifficultyFallsBackToCodexJudgeWhenOpenCodeMissing(t *testing.T) {
	dir := fakeDifficultyRun(t, map[string][]string{
		"analyzer": {difficultyProfileJSON("high")},
		"judge":    {difficultyScoresJSON},
	})
	sources := DifficultySources{Binary: func(name string) (string, error) {
		if name == "codex" {
			return os.Args[0], nil
		}
		return "", errors.New("opencode is not installed")
	}}
	a, err := NewDifficultyAssessor(Config{
		DifficultyHarness: "codex", DifficultyModel: "gpt-analyzer", DifficultyAnalyzer: true, DifficultyTimeout: 2 * time.Second,
	}, sources).Assess(context.Background(), t.TempDir(), "change a setting")
	if err != nil {
		t.Fatal(err)
	}
	if a.Label != "hard" || a.JudgeModel != "codex:gpt-analyzer" || !strings.Contains(strings.ToLower(a.Notes), "opencode") {
		t.Fatalf("assessment lacks fallback note: %+v", a)
	}
	if calls := difficultyCalls(t, dir); len(calls) != 2 || calls[1].Kind != "judge" {
		t.Fatalf("Codex calls: %+v", calls)
	}
}

func TestParseJudgeReplyFindsScoresInNoisyOutput(t *testing.T) {
	for name, raw := range map[string]string{
		"progress and ANSI":   "> build · model\n\x1b[32mnoise {\"bad\": {\"x\": 1}}\n" + difficultyScoresJSON + "\x1b[0m",
		"code fence":          "```json\n" + difficultyScoresJSON + "\n```",
		"after text":          "Here are the scores:\n" + difficultyScoresJSON,
		"unclosed prose {":    "Note: the brief has a stray { in it.\n```json\n" + difficultyScoresJSON + "\n```",
		"object after scores": difficultyScoresJSON + "\n{\"note\":\"done\"}",
	} {
		t.Run(name, func(t *testing.T) {
			scores, insufficient, err := parseJudgeReply(raw)
			if err != nil || len(insufficient) != 0 || scores.Reasoning != 4 || scores.Risk != 4 {
				t.Fatalf("scores %+v, insufficient %v, err %v", scores, insufficient, err)
			}
		})
	}
	_, insufficient, err := parseJudgeReply("> judge\nINSUFFICIENT_EVIDENCE: risk - inspect the migration")
	if err != nil || len(insufficient) != 1 || insufficient[0] != "risk" {
		t.Fatalf("insufficient %v, err %v", insufficient, err)
	}
}

// The real retry judge failed with "decode scores: unexpected end of JSON
// input", which says nothing about the reply. Each failure names what was
// wrong and the reply size.
func TestParseJudgeReplyFailuresAreClear(t *testing.T) {
	for name, tc := range map[string]struct{ raw, want string }{
		"empty":       {"  \x1b[0m\n", "the reply is empty"},
		"cut off":     {`{"reasoning":2,"scope":1,"coupling":`, "no complete JSON object in the 36-byte reply (it may be cut off)"},
		"no object":   {"I cannot score this brief.", "no JSON object in the 26-byte reply"},
		"wrong shape": {`{"reasoning":2}`, `missing score`},
	} {
		t.Run(name, func(t *testing.T) {
			_, _, err := parseJudgeReply(tc.raw)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want %q", err, tc.want)
			}
			if strings.Contains(err.Error(), "unexpected end of JSON input") {
				t.Fatalf("err = %v, want a clear reason", err)
			}
		})
	}
}

func TestJudgeBriefTrimsLongTask(t *testing.T) {
	short := "Goal: rename a flag.\n\nEvidence:\n- one file"
	if got := judgeTaskText(short); got != short {
		t.Fatalf("short task changed: %q", got)
	}
	var b strings.Builder
	b.WriteString("Goal: make the judge fast.\n\n")
	for b.Len() < 5000 {
		b.WriteString("Background line that the judge does not need to score the task.\n")
	}
	evidence := "Evidence (2026-11-04, research):\n- the flash judge timed out at 39s\n- the retry got 21s\n"
	b.WriteString("\n" + evidence + "\n")
	for b.Len() < 12000 {
		b.WriteString("Rules line that repeats what AGENTS.md says about tests.\n")
	}
	b.WriteString("Report: the cause, the fix, the tests.")
	long := b.String()
	got := judgeTaskText(long)
	if len(got) > judgeTaskLimit+200 {
		t.Fatalf("trimmed task is %d bytes, cap %d", len(got), judgeTaskLimit)
	}
	for _, want := range []string{"Goal: make the judge fast.", strings.TrimSpace(evidence), "Report: the cause, the fix, the tests.", "bytes of the brief cut"} {
		if !strings.Contains(got, want) {
			t.Fatalf("trimmed task lacks %q:\n%s", want, got)
		}
	}
	if brief := judgeBriefForTask(long); !strings.Contains(brief, got) || strings.Contains(brief, long) {
		t.Fatal("the judge brief does not use the trimmed task")
	}
}

func hasArg(args []string, name, value string) bool {
	for i, a := range args {
		if a == name && i+1 < len(args) && args[i+1] == value {
			return true
		}
	}
	return false
}

func TestAssessDifficultyAnalyzerParsesOnRetry(t *testing.T) {
	dir := fakeDifficultyRun(t, map[string][]string{
		"analyzer": {"this is not JSON", difficultyProfileJSON("high")},
		"judge":    {difficultyScoresJSON},
	})
	d := newDifficultyDaemon(t, "codex", "gpt-cheap", 2*time.Second)
	a, err := d.assessDifficulty(context.Background(), t.TempDir(), "rename a flag")
	if err != nil {
		t.Fatal(err)
	}
	if a.Label != "hard" {
		t.Fatalf("assessment: %+v", a)
	}
	calls := difficultyCalls(t, dir)
	if len(calls) != 3 || calls[0].Kind != "analyzer" || calls[1].Kind != "analyzer" || calls[2].Kind != "judge" {
		t.Fatalf("calls: %+v", calls)
	}
	if !strings.Contains(calls[1].Stdin, "previous reply did not parse") {
		t.Fatalf("the retry does not name the parse error:\n%s", calls[1].Stdin)
	}
}

func TestAssessDifficultyInsufficientEvidenceThenReanalysis(t *testing.T) {
	dir := fakeDifficultyRun(t, map[string][]string{
		"analyzer": {difficultyProfileJSON("high")},
		"judge": {
			"INSUFFICIENT_EVIDENCE: risk - inspect the database migrations",
			difficultyScoresJSON,
		},
		"analysis": {`{"risk":{"failures":["a migration"],"reversibility":"hard to reverse","evidence":["db/001.sql:1-9"]}}`},
	})
	d := newDifficultyDaemon(t, "codex", "gpt-cheap", 2*time.Second)
	a, err := d.assessDifficulty(context.Background(), t.TempDir(), "add a migration")
	if err != nil {
		t.Fatal(err)
	}
	if a.Label != "hard" || a.Profile.Risk.Reversibility != "hard to reverse" {
		t.Fatalf("assessment: %+v", a)
	}
	calls := difficultyCalls(t, dir)
	kinds := make([]string, len(calls))
	for i, c := range calls {
		kinds[i] = c.Kind
	}
	if strings.Join(kinds, ",") != "analyzer,judge,analysis,judge" {
		t.Fatalf("calls: %v", kinds)
	}
	if !strings.Contains(calls[2].Stdin, "risk") {
		t.Fatalf("the targeted brief does not name the dimension:\n%s", calls[2].Stdin)
	}
}

func TestAssessDifficultyTimeoutFallsBackToNormal(t *testing.T) {
	dir := fakeDifficultyRun(t, map[string][]string{
		"analyzer": {difficultyProfileJSON("high")},
	})
	t.Setenv(envFakeCodexExecHang, "1")
	// Long enough for the fake to record its call on a busy CI machine, still
	// well under the 2 s bound below.
	d := newDifficultyDaemon(t, "codex", "gpt-cheap", 800*time.Millisecond)
	started := time.Now()
	a, err := d.assessDifficulty(context.Background(), t.TempDir(), "a slow task")
	if err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(started); elapsed > 2*time.Second {
		t.Fatalf("the time limit did not stop the run: %s", elapsed)
	}
	if a.Label != "normal" || !strings.Contains(strings.ToLower(a.Notes), "timed out") {
		t.Fatalf("assessment: %+v", a)
	}
	if calls := difficultyCalls(t, dir); len(calls) != 1 {
		t.Fatalf("calls after a timeout: %+v", calls)
	}
}

func TestAssessDifficultyLowConfidenceFallsBackToNormal(t *testing.T) {
	dir := fakeDifficultyRun(t, map[string][]string{
		"analyzer": {difficultyProfileJSON("low")},
	})
	d := newDifficultyDaemon(t, "codex", "gpt-cheap", 2*time.Second)
	a, err := d.assessDifficulty(context.Background(), t.TempDir(), "an unclear task")
	if err != nil {
		t.Fatal(err)
	}
	if a.Label != "normal" || !strings.Contains(strings.ToLower(a.Notes), "confidence") {
		t.Fatalf("assessment: %+v", a)
	}
	if calls := difficultyCalls(t, dir); len(calls) != 1 || calls[0].Kind != "analyzer" {
		t.Fatalf("a low-confidence profile still reached the judge: %+v", calls)
	}
}

func TestCheapestDifficultyProfileChoosesLowestMaxCodexProfile(t *testing.T) {
	profiles := []protocol.WorkerProfile{
		{Name: "strong", Harness: "codex", Model: "gpt-strong"},
		{Name: "cheap", Harness: "codex", Model: "gpt-cheap"},
		{Name: "claude", Harness: "claude", Model: "claude-cheap"},
	}
	scores := []protocol.ModelScore{
		{Match: "gpt-strong", Score: 60},
		{Match: "gpt-cheap", Score: 10},
		{Match: "claude-cheap", Score: 0},
	}
	harness, model, ok := cheapestDifficultyProfile(profiles, scores, protocol.DifficultyMinimums)
	if !ok || harness != "codex" || model != "gpt-cheap" {
		t.Fatalf("chose %q %q %v", harness, model, ok)
	}
	if _, _, ok := cheapestDifficultyProfile(profiles[2:], scores, protocol.DifficultyMinimums); ok {
		t.Fatal("a harness with no supported one-shot was chosen")
	}
}

func TestParseJudgeReplyTruncatedOuterReasons(t *testing.T) {
	raw := `{"reasoning":3,"scope":2,"coupling":2,"uncertainty":2,"verification":2,"risk":1,"reasons":{"reasoning":"text","scope":"text","coupling":"text","uncertainty":"text","verification":"text","risk":"tex`
	_, _, err := parseJudgeReply(raw)
	if err == nil || !strings.Contains(err.Error(), "cut off") {
		t.Fatalf("got %v", err)
	}
}

func TestParseJudgeReplyKeepsEarlierScoresBeforeTruncation(t *testing.T) {
	_, _, err := parseJudgeReply(difficultyScoresJSON + "\n" + `{"reasoning":`)
	if err != nil {
		t.Fatal(err)
	}
}
func TestParseJudgeReplyLongTruncatedOuter(t *testing.T) {
	raw := `{"padding":"` + strings.Repeat("x", 64<<10) + `","reasons":{"reasoning":"text","scope":"text","coupling":"text","uncertainty":"text","verification":"text","risk":"tex`
	_, _, err := parseJudgeReply(raw)
	if err == nil || !strings.Contains(err.Error(), "cut off") {
		t.Fatalf("got %v", err)
	}
}

func TestParseJudgeReplySkipsMalformedOuterBeforeScores(t *testing.T) {
	raw := `{"reasoning":3,broken,"reasons":{"reasoning":"text"}}` + "\n" + difficultyScoresJSON
	if _, _, err := parseJudgeReply(raw); err != nil {
		t.Fatal(err)
	}
}
