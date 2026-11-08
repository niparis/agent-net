package protocol

import (
	"math"
	"strings"
	"testing"
)

func near(t *testing.T, what string, got, want float64) {
	t.Helper()
	if math.Abs(got-want) > 1e-4 {
		t.Fatalf("%s = %.5f, want %.5f", what, got, want)
	}
}

func TestPickerTaskScore(t *testing.T) {
	cases := []struct {
		name  string
		facts PickerTaskFacts
		want  float64
	}{
		{"clean complete task", PickerTaskFacts{Complete: true}, 1},
		{"not complete is zero even when clean", PickerTaskFacts{Complete: false}, 0},
		{"one fix round", PickerTaskFacts{Complete: true, FixRounds: 1}, .75},
		{"two failed steps", PickerTaskFacts{Complete: true, FailedSteps: 2}, .5},
		{"severity 1 defect costs count plus severity", PickerTaskFacts{Complete: true, Defects: 1, DefectSeverity: 1}, .5},
		{"severity 4 defect costs 1.25 and floors at zero", PickerTaskFacts{Complete: true, Defects: 1, DefectSeverity: 4}, 0},
		{"all three together", PickerTaskFacts{Complete: true, FixRounds: 1, FailedSteps: 1}, .5},
		{"floor at zero", PickerTaskFacts{Complete: true, FixRounds: 9}, 0},
		{"incomplete with rework stays zero", PickerTaskFacts{Complete: false, FixRounds: 1, Defects: 1, DefectSeverity: 2}, 0},
	}
	for _, c := range cases {
		near(t, c.name, PickerTaskScore(c.facts), c.want)
	}
}

func TestPickerModelKeyKeepsEffortApart(t *testing.T) {
	cases := []struct{ harness, model, key, display string }{
		{"codex", "gpt-6.1-sol:low", "gpt-6.1-sol:low", "gpt-6.1-sol:low"},
		{"codex", "gpt-6.1-sol:medium", "gpt-6.1-sol:medium", "gpt-6.1-sol:medium"},
		{"codex", "gpt-6.1-terra", "gpt-6.1-terra:default", "gpt-6.1-terra:default"},
		{"opencode", "opencode-go/deepseek-v4.1-flash", "opencode-go/deepseek-v4.1-flash", "deepseek-v4.1-flash"},
		{"agy", "gemini-3.8-flash-medium", "gemini-3.8-flash-medium", "gemini-3.8-flash-medium"},
		{"claude", "claude-opus-5-5:high", "claude-opus-5-5:high", "claude-opus-5-5:high"},
		{"pi", "a", "a", "a"},
	}
	for _, c := range cases {
		key := PickerModelKey(c.harness, c.model)
		if key != c.key {
			t.Errorf("PickerModelKey(%s, %s) = %q, want %q", c.harness, c.model, key, c.key)
		}
		if got := PickerModelDisplay(key); got != c.display {
			t.Errorf("PickerModelDisplay(%q) = %q, want %q", key, got, c.display)
		}
	}
	if PickerModelKey("codex", "gpt-6.1-sol:low") == PickerModelKey("codex", "gpt-6.1-sol:medium") {
		t.Fatal("two efforts share a key")
	}
}

func TestPickerBuckets(t *testing.T) {
	table := BuildPickerTable(nil, DefaultPickerSettings())
	for _, c := range []struct {
		d      float64
		bucket int
		lo, hi float64
	}{
		{1.0, 0, 1, 4.0 / 3}, {1.2, 0, 1, 4.0 / 3}, {1.34, 1, 4.0 / 3, 5.0 / 3}, {2.0, 3, 2, 7.0 / 3},
		{2.65, 4, 7.0 / 3, 8.0 / 3}, {2.95, 5, 8.0 / 3, 3}, {3.0, 6, 3, 10.0 / 3}, {4.7, 11, 4.0 + 2.0/3, 5},
	} {
		if got := table.Bucket(c.d); got != c.bucket {
			t.Errorf("Bucket(%v) = %d, want %d", c.d, got, c.bucket)
		}
		lo, hi := table.BucketRange(c.bucket)
		near(t, "lo", lo, c.lo)
		near(t, "hi", hi, c.hi)
	}
	if got := BuildPickerTable(nil, PickerSettings{BucketWidth: .5}).Bucket(2.65); got != 3 {
		t.Fatalf("width 0.5: bucket %d, want 3", got)
	}
}

// shrinkFixture has two buckets. Bucket 4 (D 2.33-2.67) holds model A's three
// tasks and model B's one. Bucket 6 (D 3.00-3.33) holds one task each.
func shrinkFixture() []PickerTask {
	return []PickerTask{
		{"A", 2.4, 1}, {"A", 2.5, 1}, {"A", 2.6, .5}, {"B", 2.5, .5},
		{"A", 3.1, 0}, {"B", 3.1, 1},
	}
}

func TestPickerCellShrinksTowardTheBucketMean(t *testing.T) {
	table := BuildPickerTable(shrinkFixture(), DefaultPickerSettings())
	near(t, "bucket 4 mean", table.BucketMean(4), .75)
	// score = (2.5 + 5 x .75) / 8. The cell variance (3 x .055556 + 5 x
	// .0625) / 8 = .059896 is under the floor, half of the variance of all
	// six tasks (.138889 x .5 = .069444), so the floor sets the interval.
	a := table.Cell("A", 4)
	near(t, "A score", a.Score, .78125)
	half := 1.645 * math.Sqrt(.069444444/8)
	near(t, "A lo", a.Lo, .78125-half)
	near(t, "A hi", a.Hi, .78125+half)
	if a.N != 3 {
		t.Fatalf("A n = %d", a.N)
	}
	// B has one task: score (.5 + 3.75) / 6.
	b := table.Cell("B", 4)
	near(t, "B score", b.Score, 4.25/6)
	if b.N != 1 {
		t.Fatalf("B n = %d", b.N)
	}
}

func TestPickerEmptyCellTakesTheBucketMeanWithAFlooredVariance(t *testing.T) {
	table := BuildPickerTable(shrinkFixture(), DefaultPickerSettings())
	c := table.Cell("nobody", 4)
	if c.N != 0 {
		t.Fatalf("n = %d", c.N)
	}
	near(t, "score", c.Score, .75)
	half := 1.645 * math.Sqrt(.069444444/5)
	near(t, "lo", c.Lo, .75-half)
	near(t, "hi", c.Hi, .75+half)
	// A bucket with no task at all takes the mean of every task.
	empty := table.Cell("A", 9)
	near(t, "empty bucket score", empty.Score, 4.0/6)
}

func TestPickerIntervalIsClippedToZeroAndOne(t *testing.T) {
	var tasks []PickerTask
	for i := 0; i < 3; i++ {
		tasks = append(tasks, PickerTask{"top", 1.1, 1}, PickerTask{"low", 1.1, 0})
	}
	table := BuildPickerTable(tasks, DefaultPickerSettings())
	top, low := table.Cell("top", 0), table.Cell("low", 0)
	if top.Hi > 1 || low.Lo < 0 {
		t.Fatalf("not clipped: %+v %+v", top, low)
	}
}

func TestPickerVarianceFloorStopsThinCellsLookingCertain(t *testing.T) {
	// Bucket 4 holds only 0.5 scores, so its variance and the cell's own are
	// 0. Without the floor the interval of "x" would have no width. The
	// variance of all ten tasks is .1, so the floor is .05.
	tasks := []PickerTask{{"x", 2.5, .5}, {"x", 2.5, .5}, {"z", 2.5, .5}, {"z", 2.5, .5}, {"z", 2.5, .5}, {"z", 2.5, .5},
		{"y", 3.5, 1}, {"y", 3.5, 0}, {"y", 3.5, 1}, {"y", 3.5, 0}}
	table := BuildPickerTable(tasks, DefaultPickerSettings())
	x := table.Cell("x", 4)
	half := 1.645 * math.Sqrt(.05/7)
	near(t, "half width", (x.Hi-x.Lo)/2, half)
	near(t, "score", x.Score, .5)
}

func TestPickerSettingsChangeTheCells(t *testing.T) {
	s := DefaultPickerSettings()
	s.K = 1
	table := BuildPickerTable(shrinkFixture(), s)
	near(t, "A score with k=1", table.Cell("A", 4).Score, (2.5+.75)/4)
}

// pickTasks builds n tasks for one model at one D.
func pickTasks(key string, d float64, scores ...float64) []PickerTask {
	out := make([]PickerTask, len(scores))
	for i, s := range scores {
		out[i] = PickerTask{Key: key, D: d, Score: s}
	}
	return out
}

func repeat(n int, values ...float64) []float64 {
	var out []float64
	for i := 0; i < n; i++ {
		out = append(out, values...)
	}
	return out
}

func pickCandidates(names ...string) []WorkerModelCandidate {
	out := make([]WorkerModelCandidate, len(names))
	for i, n := range names {
		out[i] = WorkerModelCandidate{Name: n, Harness: "pi", Model: n}
	}
	return out
}

func pickScores(models map[string]float64) []ModelScore {
	var out []ModelScore
	for m, s := range models {
		out = append(out, ModelScore{Match: m, Score: s})
	}
	return out
}

func pickN(t *testing.T, c []WorkerModelCandidate, level string, d float64, tasks []PickerTask, scores []ModelScore, s PickerSettings, n int) map[string]int {
	t.Helper()
	in := &PickerInput{D: d, Table: BuildPickerTable(tasks, s)}
	rotation := &WorkerRotation{}
	counts := map[string]int{}
	for i := 0; i < n; i++ {
		choice, err := ChooseWorkerModel(c, level, scores, nil, nil, nil, rotation, in)
		if err != nil {
			t.Fatalf("pick %d: %v", i, err)
		}
		counts[choice.Name]++
	}
	return counts
}

func TestPickLeavesOutAConfidentlyWorseModel(t *testing.T) {
	tasks := append(pickTasks("good", 2.5, repeat(15, 1, .5)...), pickTasks("bad", 2.5, repeat(10, .5, 0, 0)...)...)
	scores := pickScores(map[string]float64{"good": 51, "bad": 51})
	counts := pickN(t, pickCandidates("good", "bad"), "normal", 2.5, tasks, scores, DefaultPickerSettings(), 20)
	if counts["bad"] != 0 || counts["good"] != 20 {
		t.Fatalf("picks: %v", counts)
	}
}

func TestPickKeepsAnUncertainModelInTheRotation(t *testing.T) {
	tasks := append(pickTasks("good", 2.5, repeat(15, 1, .5)...), pickTasks("new", 2.5, 1, 0, 1, 0)...)
	// "new" benchmarks at normal (ceiling D 3.0), one bucket above this task, so
	// it keeps the boost. Its interval is wide because it has four tasks.
	scores := pickScores(map[string]float64{"good": 51, "new": 39})
	counts := pickN(t, pickCandidates("good", "new"), "normal", 2.5, tasks, scores, DefaultPickerSettings(), 40)
	if counts["new"] < 10 || counts["good"] < 10 {
		t.Fatalf("picks: %v", counts)
	}
}

func TestPickBetterScoreGetsMorePicksWhenBothAreKnown(t *testing.T) {
	tasks := append(pickTasks("a", 2.5, repeat(10, 1, .5, 1, .5)...), pickTasks("b", 2.5, repeat(10, 1, .5, .5, .5)...)...)
	scores := pickScores(map[string]float64{"a": 51, "b": 51})
	counts := pickN(t, pickCandidates("a", "b"), "normal", 2.5, tasks, scores, DefaultPickerSettings(), 60)
	if counts["a"] <= counts["b"] || counts["b"] == 0 {
		t.Fatalf("picks: %v", counts)
	}
}

func TestPickUncertainBoostIsOnTheWeight(t *testing.T) {
	tasks := append(pickTasks("known", 2.5, repeat(10, 1, 0)...), pickTasks("fresh", 2.5, 1, 0, 1, 0)...)
	// Both benchmark at normal: the ceiling (D 3.0) is one bucket above D 2.5.
	scores := pickScores(map[string]float64{"known": 39, "fresh": 39})
	table := BuildPickerTable(tasks, DefaultPickerSettings())
	choice, err := ChooseWorkerModel(pickCandidates("fresh"), "normal", scores, nil, nil, nil, &WorkerRotation{}, &PickerInput{D: 2.5, Table: table})
	if err != nil {
		t.Fatal(err)
	}
	near(t, "weight", choice.Weight, table.Cell("fresh", 4).Score*1.5)
	choice, err = ChooseWorkerModel(pickCandidates("known"), "normal", scores, nil, nil, nil, &WorkerRotation{}, &PickerInput{D: 2.5, Table: table})
	if err != nil {
		t.Fatal(err)
	}
	near(t, "weight", choice.Weight, table.Cell("known", 4).Score)
}

// A cell more than one bucket below the model's benchmark ceiling gets no
// boost, with or without tasks: the model is well above this work.
func TestPickNoUncertainBoostMoreThanOneBucketBelowTheCeiling(t *testing.T) {
	tasks := pickTasks("fresh", 2.5, 1, 0, 1, 0)
	table := BuildPickerTable(tasks, DefaultPickerSettings())
	in := &PickerInput{D: 2.5, Table: table}
	for _, c := range []struct {
		name  string
		score float64
		boost float64
	}{
		{"near the ceiling", 39, 1.5},    // ceiling 3.0, one bucket above D 2.5
		{"far below the ceiling", 51, 1}, // ceiling 5.0
		{"hard model", 41, 1},            // ceiling 3.75
	} {
		choice, err := ChooseWorkerModel(pickCandidates("fresh"), "normal", pickScores(map[string]float64{"fresh": c.score}), nil, nil, nil, &WorkerRotation{}, in)
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		near(t, c.name, choice.Weight, table.Cell("fresh", 4).Score*c.boost)
	}
}

func TestPickLeavesOutAnOverqualifiedModel(t *testing.T) {
	// An easy task, D 1.8. "strong" is far above the task by its benchmark
	// and 0.4 under the best cell. A lenient interval test lets it through, so
	// only the over-qualification rule can remove it.
	tasks := append(pickTasks("cheap", 1.8, repeat(10, 1, .8, 1)...), pickTasks("strong", 1.8, repeat(10, .5, .5, .5)...)...)
	scores := pickScores(map[string]float64{"cheap": 29, "strong": 51})
	s := DefaultPickerSettings()
	s.AllowedFraction = .3
	counts := pickN(t, pickCandidates("cheap", "strong"), "simple", 1.8, tasks, scores, s, 20)
	if counts["strong"] != 0 {
		t.Fatalf("picks: %v", counts)
	}
	// The same record on a task within one bucket of the strong model's
	// ceiling keeps it in the rotation.
	hard := append(pickTasks("cheap", 4.5, repeat(10, 1, .8, 1)...), pickTasks("strong", 4.5, repeat(10, .5, .5, .5)...)...)
	counts = pickN(t, pickCandidates("cheap", "strong"), "very_complex", 4.5, hard, scores, s, 20)
	if counts["strong"] == 0 || counts["cheap"] == 0 {
		t.Fatalf("picks at the ceiling: %v", counts)
	}
}

func TestPickOverqualifiedRuleNeedsAWeakerAllowedModel(t *testing.T) {
	// Only strong models are on the roster, so none can be dropped.
	tasks := append(pickTasks("s1", 1.8, repeat(10, 1, .8, 1)...), pickTasks("s2", 1.8, repeat(10, .5, .5, .5)...)...)
	scores := pickScores(map[string]float64{"s1": 51, "s2": 51})
	s := DefaultPickerSettings()
	s.AllowedFraction = .3
	counts := pickN(t, pickCandidates("s1", "s2"), "simple", 1.8, tasks, scores, s, 20)
	if counts["s1"] == 0 || counts["s2"] == 0 {
		t.Fatalf("picks: %v", counts)
	}
}

func TestPickKeepsUntriedStrongModelsOffEasyWork(t *testing.T) {
	// D 1.8 is bucket 2. "cheap" has a record. "strong" benchmarks very_complex
	// (ceiling D 5.0), far above the task, and has no record, or only two tasks.
	scores := pickScores(map[string]float64{"cheap": 29, "strong": 51})
	cheap := pickTasks("cheap", 1.8, repeat(10, 1, .8, 1)...)
	for name, tasks := range map[string][]PickerTask{
		"no record": cheap,
		"two tasks": append(append([]PickerTask(nil), cheap...), pickTasks("strong", 1.8, 1, 1)...),
	} {
		counts := pickN(t, pickCandidates("cheap", "strong"), "simple", 1.8, tasks, scores, DefaultPickerSettings(), 20)
		if counts["strong"] != 0 || counts["cheap"] != 20 {
			t.Fatalf("%s: picks %v", name, counts)
		}
	}
	// Near its ceiling the same model with no record still gets tries.
	hard := pickTasks("cheap", 4.5, repeat(10, 1, .8, 1)...)
	counts := pickN(t, pickCandidates("cheap", "strong"), "very_complex", 4.5, hard, scores, DefaultPickerSettings(), 20)
	if counts["strong"] == 0 {
		t.Fatalf("near the ceiling: picks %v", counts)
	}
	// With no weaker model allowed, the strong one is not left out.
	counts = pickN(t, pickCandidates("strong"), "simple", 1.8, nil, scores, DefaultPickerSettings(), 5)
	if counts["strong"] != 5 {
		t.Fatalf("alone: picks %v", counts)
	}
	// Two untried strong models are equally strong: neither is weaker.
	both := pickScores(map[string]float64{"s1": 51, "s2": 51})
	counts = pickN(t, pickCandidates("s1", "s2"), "simple", 1.8, nil, both, DefaultPickerSettings(), 10)
	if counts["s1"] == 0 || counts["s2"] == 0 {
		t.Fatalf("equal strong models: picks %v", counts)
	}
}

func TestPickBenchmarkCeilingDecidesWhereACellHasFewerThanThreeTasks(t *testing.T) {
	// Level scores: 29 is simple (ceiling D 2.25), 39 is normal (3.0),
	// 51 is very_complex.
	scores := pickScores(map[string]float64{"simple": 29, "normal": 39, "big": 51})
	table := BuildPickerTable(pickTasks("big", 2.5, repeat(10, 1, .5)...), DefaultPickerSettings())
	rotation := &WorkerRotation{}
	counts := map[string]int{}
	for i := 0; i < 30; i++ {
		choice, err := ChooseWorkerModel(pickCandidates("simple", "normal", "big"), "normal", scores, nil, nil, nil, rotation, &PickerInput{D: 2.5, Table: table})
		if err != nil {
			t.Fatal(err)
		}
		counts[choice.Name]++
	}
	// "simple" tops out under this bucket (2.33-2.67); "normal" does not.
	if counts["simple"] != 0 || counts["normal"] == 0 || counts["big"] == 0 {
		t.Fatalf("picks: %v", counts)
	}
}

func TestPickKeepsEffortsOfOneModelApart(t *testing.T) {
	low := WorkerModelCandidate{Name: "sol low", Harness: "codex", Model: "gpt-6.1-sol:low"}
	medium := WorkerModelCandidate{Name: "sol medium", Harness: "codex", Model: "gpt-6.1-sol:medium"}
	scores := []ModelScore{{Match: "gpt-6.1-sol:low", Score: 51}, {Match: "gpt-6.1-sol:medium", Score: 51}}
	tasks := append(pickTasks("gpt-6.1-sol:low", 2.5, repeat(10, .5, 0, 0)...), pickTasks("gpt-6.1-sol:medium", 2.5, repeat(15, 1, .5)...)...)
	counts := pickN(t, []WorkerModelCandidate{low, medium}, "normal", 2.5, tasks, scores, DefaultPickerSettings(), 10)
	if counts["sol low"] != 0 || counts["sol medium"] != 10 {
		t.Fatalf("picks: %v", counts)
	}
	// Swap the records and the pick follows the effort, not the model name.
	swapped := append(pickTasks("gpt-6.1-sol:medium", 2.5, repeat(10, .5, 0, 0)...), pickTasks("gpt-6.1-sol:low", 2.5, repeat(15, 1, .5)...)...)
	counts = pickN(t, []WorkerModelCandidate{low, medium}, "normal", 2.5, swapped, scores, DefaultPickerSettings(), 10)
	if counts["sol low"] != 10 || counts["sol medium"] != 0 {
		t.Fatalf("swapped picks: %v", counts)
	}
}

func TestPickFallsBackToTheLevelRule(t *testing.T) {
	// Neither candidate has a record at D 4.5 and both benchmark under it, so
	// the cell rule allows none. The level rule still takes them for "normal".
	scores := pickScores(map[string]float64{"a": 39, "b": 39})
	table := BuildPickerTable(nil, DefaultPickerSettings())
	choice, err := ChooseWorkerModel(pickCandidates("a", "b"), "normal", scores, nil, nil, nil, &WorkerRotation{}, &PickerInput{D: 4.5, Table: table})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(choice.PickNote, "fallback to the level rule") {
		t.Fatalf("note: %q", choice.PickNote)
	}
	if choice.Model != "a" && choice.Model != "b" {
		t.Fatalf("choice: %+v", choice)
	}
}

func TestPickWithoutAPickerInputUsesTheLevelRule(t *testing.T) {
	scores := pickScores(map[string]float64{"a": 39})
	choice, err := ChooseWorkerModel(pickCandidates("a"), "normal", scores, nil, nil, nil, &WorkerRotation{})
	if err != nil || choice.Model != "a" || choice.PickNote != "" {
		t.Fatalf("choice: %+v %v", choice, err)
	}
}

func TestPickKeepsQuotaBlockedModelsOut(t *testing.T) {
	tasks := append(pickTasks("a", 2.5, repeat(15, 1, .5)...), pickTasks("b", 2.5, repeat(15, 1, .5)...)...)
	scores := pickScores(map[string]float64{"a": 51, "b": 51})
	c := pickCandidates("a", "b")
	c[0].Provider, c[1].Provider = "pa", "pb"
	q := &Quotas{GeneratedAt: 1000, Providers: []QuotaProvider{{Provider: "pa", Windows: []QuotaWindow{{Kind: "weekly", PercentUsed: 95, ResetsAt: 1000 + 3*24*3600}}}}}
	in := &PickerInput{D: 2.5, Table: BuildPickerTable(tasks, DefaultPickerSettings())}
	rotation := &WorkerRotation{}
	for i := 0; i < 5; i++ {
		choice, err := ChooseWorkerModel(c, "normal", scores, nil, q, nil, rotation, in)
		if err != nil || choice.Name != "b" {
			t.Fatalf("pick %d: %+v %v", i, choice, err)
		}
	}
}

func TestPickNoteNamesTheCellsAndWhatWasLeftOut(t *testing.T) {
	var tasks []PickerTask
	tasks = append(tasks, pickTasks("deepseek", 2.5, repeat(15, 1, .5)...)...)
	tasks = append(tasks, pickTasks("flash", 2.5, 1, 1, .5, .5, 0, 1)...)
	tasks = append(tasks, pickTasks("terra:medium", 2.5, repeat(10, 0)...)...)
	tasks = append(tasks, pickTasks("deepseek", 1.2, 1, 1, 1, 1)...)
	scores := pickScores(map[string]float64{"deepseek": 51, "flash": 51, "terra:medium": 51})
	c := []WorkerModelCandidate{
		{Name: "deepseek", Harness: "pi", Model: "deepseek"}, {Name: "flash", Harness: "pi", Model: "flash"},
		{Name: "terra:medium", Harness: "pi", Model: "terra:medium"},
	}
	// SplitModelLevel reads ":medium" as an effort, so the key is the model.
	table := BuildPickerTable(tasks, DefaultPickerSettings())
	choice, err := ChooseWorkerModel(c, "normal", scores, nil, nil, nil, &WorkerRotation{}, &PickerInput{D: 2.5, Table: table})
	if err != nil {
		t.Fatal(err)
	}
	want := "bucket 2.33-2.67: deepseek 0.73 [0.65-0.80] n30, best; flash 0.63 [0.44-0.81] n6 allowed; terra:medium 0.19 [0.07-0.31] n10 left out (below 0.9×best)"
	if choice.Name != "deepseek" || choice.PickNote != want {
		t.Fatalf("choice %s\nnote %q\nwant %q", choice.Name, choice.PickNote, want)
	}
}

func TestPickNoteShowsOverqualifiedAndPriorAndChosenNotBest(t *testing.T) {
	tasks := append(pickTasks("cheap", 1.8, repeat(10, 1, .8, 1)...), pickTasks("strong", 1.8, repeat(10, .5, .5, .5)...)...)
	scores := pickScores(map[string]float64{"cheap": 29, "strong": 51, "fresh": 51})
	s := DefaultPickerSettings()
	s.AllowedFraction = .3
	table := BuildPickerTable(tasks, s)
	choice, err := ChooseWorkerModel(pickCandidates("cheap", "strong", "fresh"), "simple", scores, nil, nil, nil, &WorkerRotation{}, &PickerInput{D: 1.8, Table: table})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(choice.PickNote, "strong ") || !strings.Contains(choice.PickNote, "left out (overqualified)") {
		t.Fatalf("note: %q", choice.PickNote)
	}
	// "fresh" has no record and benchmarks far above this easy task, and "cheap"
	// is a weaker model that is allowed, so fresh is left out by its benchmark.
	if !strings.Contains(choice.PickNote, "fresh n0 left out (overqualified)") || !strings.Contains(choice.PickNote, "cheap 0.90 [0.86-0.95] n30") {
		t.Fatalf("note should name the model with no record and the best cell: %q", choice.PickNote)
	}
}

func TestPickNoteForUnscoredModelWithoutRecord(t *testing.T) {
	tasks := pickTasks("known", 2.5, repeat(10, 1, .5)...)
	scores := pickScores(map[string]float64{"known": 51})
	table := BuildPickerTable(tasks, DefaultPickerSettings())
	choice, err := ChooseWorkerModel(pickCandidates("known", "mystery"), "normal", scores, nil, nil, nil, &WorkerRotation{}, &PickerInput{D: 2.5, Table: table})
	if err != nil {
		t.Fatal(err)
	}
	if choice.Name != "known" || !strings.Contains(choice.PickNote, "mystery n0 left out (no record, no benchmark)") {
		t.Fatalf("choice %s note %q", choice.Name, choice.PickNote)
	}
}

func TestLevelDRanges(t *testing.T) {
	// The ranges are the judge's label cut points (internal/difficulty).
	for level, want := range map[string][2]float64{
		"trivial": {1, 1.5}, "simple": {1.5, 2.25}, "normal": {2.25, 3}, "hard": {3, 3.75}, "very_complex": {3.75, 5},
	} {
		lo, hi, ok := LevelDRange(level)
		if !ok || lo != want[0] || hi != want[1] {
			t.Errorf("%s: %v-%v %v", level, lo, hi, ok)
		}
	}
	if _, _, ok := LevelDRange("nope"); ok {
		t.Fatal("unknown level has a range")
	}
}

func TestPickerDClampsToTheLabelAndIgnoresUnreliableJudges(t *testing.T) {
	a := &WorkerAssessment{D: 2.65, Label: "normal", Confidence: "high"}
	near(t, "plain", PickerD(a, "normal"), 2.65)
	// A bottleneck lifts the label to hard while D stays low.
	lifted := &WorkerAssessment{D: 2.0, Label: "hard", Confidence: "high"}
	near(t, "lifted to the label floor", PickerD(lifted, "hard"), 3.0)
	// Low confidence runs the level as normal; D is not trusted.
	low := &WorkerAssessment{D: 3.9, Label: "very_complex", Confidence: "low"}
	near(t, "low confidence", PickerD(low, "normal"), 2.625)
	failed := &WorkerAssessment{Error: "timed out"}
	near(t, "failed judge", PickerD(failed, "normal"), 2.625)
	near(t, "no assessment", PickerD(nil, "hard"), 3.375)
}

func BenchmarkPickTwentyProfiles(b *testing.B) {
	var tasks []PickerTask
	var candidates []WorkerModelCandidate
	scores := map[string]float64{}
	for i := 0; i < 20; i++ {
		name := string(rune('a'+i)) + "-model"
		candidates = append(candidates, WorkerModelCandidate{Name: name, Harness: "pi", Model: name})
		scores[name] = float64(20 + i*2)
		for d := 1.1; d < 4.5; d += .25 {
			tasks = append(tasks, pickTasks(name, d, repeat(4, float64(i%5)/4, .75)...)...)
		}
	}
	table := BuildPickerTable(tasks, DefaultPickerSettings())
	pick := &PickerInput{D: 2.65, Table: table}
	modelScores := pickScores(scores)
	rotation := &WorkerRotation{}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := ChooseWorkerModel(candidates, "normal", modelScores, nil, nil, nil, rotation, pick); err != nil {
			b.Fatal(err)
		}
	}
}
