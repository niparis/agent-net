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

// A model with fewer than three tasks in the bucket is not in the qualifying
// set. It takes the exploration share of the picks, counted from the rotation
// state: exactly one pick in ten at the default share.
func TestPickExploresAThinCellAtTheExploreShare(t *testing.T) {
	tasks := append(pickTasks("good", 2.5, repeat(15, 1, .5)...), pickTasks("new", 2.5, 1, 0)...)
	// "new" benchmarks at normal (ceiling D 3.0), so the benchmark allows it.
	scores := pickScores(map[string]float64{"good": 51, "new": 39})
	counts := pickN(t, pickCandidates("good", "new"), "normal", 2.5, tasks, scores, DefaultPickerSettings(), 40)
	if counts["new"] != 4 || counts["good"] != 36 {
		t.Fatalf("picks: %v, want 4 exploration picks in 40", counts)
	}
	set := DefaultPickerSettings()
	set.ExploreShare = .25
	counts = pickN(t, pickCandidates("good", "new"), "normal", 2.5, tasks, scores, set, 40)
	if counts["new"] != 10 {
		t.Fatalf("share .25: picks %v, want 10 of 40", counts)
	}
}

// A model with three tasks or more whose score is under 0.9 x best is neither
// in the qualifying set nor in the exploration set.
func TestPickLeavesOutAModelBelowTheQualifyingScore(t *testing.T) {
	tasks := append(pickTasks("good", 2.5, repeat(15, 1, .5)...), pickTasks("new", 2.5, 1, 0, 1, 0)...)
	scores := pickScores(map[string]float64{"good": 51, "new": 39})
	counts := pickN(t, pickCandidates("good", "new"), "normal", 2.5, tasks, scores, DefaultPickerSettings(), 40)
	if counts["new"] != 0 {
		t.Fatalf("picks: %v", counts)
	}
}

// The score decides, not the better model's lead: two qualifying models with
// the same benchmark and quota share the picks evenly.
func TestPickQualifyingModelsShareEvenlyAtTheSameBenchmark(t *testing.T) {
	tasks := append(pickTasks("a", 2.5, repeat(10, 1, .5, 1, .5)...), pickTasks("b", 2.5, repeat(10, 1, .5, .5, 1)...)...)
	scores := pickScores(map[string]float64{"a": 51, "b": 51})
	counts := pickN(t, pickCandidates("a", "b"), "normal", 2.5, tasks, scores, DefaultPickerSettings(), 60)
	if counts["a"] != 30 || counts["b"] != 30 {
		t.Fatalf("picks: %v", counts)
	}
}

func TestPickUncertainBoostIsOnTheWeight(t *testing.T) {
	tasks := append(pickTasks("known", 2.5, repeat(10, 1, 0)...), pickTasks("fresh", 2.5, 1, 0)...)
	// Both benchmark at normal: the ceiling (D 3.0) is one bucket above D 2.5.
	// "fresh" has a thin cell and no candidate qualifies beside it, so the
	// allowed set rotates with the cell score, as before.
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
	// A qualifying model weighs its quota pace times the benchmark factor: 1.
	near(t, "weight", choice.Weight, 1)
}

// A cell more than one bucket below the model's benchmark ceiling gets no
// boost, with or without tasks: the model is well above this work.
func TestPickNoUncertainBoostMoreThanOneBucketBelowTheCeiling(t *testing.T) {
	tasks := pickTasks("fresh", 2.5, 1, 0)
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

func TestPickOpusWithNoRecordIsAllowedInTheTopColumn(t *testing.T) {
	// D 4.1 is bucket 9 (4.00-4.33). Opus benchmarks 51.7, ceiling D 5.0, which
	// is within one bucket of the work, and a weaker model with a record is
	// also allowed. Opus is allowed by its benchmark, and the rotation reaches it.
	scores := pickScores(map[string]float64{"weak": 39, "opus": 51.7})
	hard := pickTasks("weak", 4.1, repeat(10, 1, .8, 1)...)
	table := BuildPickerTable(hard, DefaultPickerSettings())
	lo, _ := table.BucketRange(9)
	facts := []PickerCandidateFacts{
		{Key: "weak", Cell: table.Cell("weak", 9), Scored: true, Benchmark: 39, Ceiling: 3.75},
		{Key: "opus", Cell: table.Cell("opus", 9), Scored: true, Benchmark: 51.7, Ceiling: 5},
	}
	got, _ := PickerBucketVerdicts(facts, lo, table.BucketMean(9), 9, DefaultPickerSettings())
	if !got[1].Allowed || !got[1].Prior || got[1].Reason != "" {
		t.Fatalf("opus = %+v, want allowed by its benchmark", got[1])
	}
	counts := pickN(t, pickCandidates("weak", "opus"), "very_complex", 4.1, hard, scores, DefaultPickerSettings(), 40)
	if counts["opus"] == 0 {
		t.Fatalf("opus with n=0 was never picked in the top column: %v", counts)
	}
}

func TestPickModelFarAboveTheWorkIsNotCutByItsBenchmark(t *testing.T) {
	// D 1.8, bucket 2. The ceiling of "opus" is D 5.0, three buckets above the
	// work, and "opus" has no record. The old over-qualification cut left it
	// out. Now it is allowed by its benchmark and the verdict says so.
	tasks := pickTasks("cheap", 1.8, repeat(10, 1, .8, 1)...)
	table := BuildPickerTable(tasks, DefaultPickerSettings())
	lo, _ := table.BucketRange(2)
	facts := []PickerCandidateFacts{
		{Key: "cheap", Cell: table.Cell("cheap", 2), Scored: true, Benchmark: 29, Ceiling: 2.25},
		{Key: "opus", Cell: table.Cell("opus", 2), Scored: true, Benchmark: 51.7, Ceiling: 5},
	}
	got, _ := PickerBucketVerdicts(facts, lo, table.BucketMean(2), 2, DefaultPickerSettings())
	if !got[1].Allowed || !got[1].Prior {
		t.Fatalf("opus = %+v, want allowed by its benchmark", got[1])
	}
}

func TestPickModelUnderTheBestIsLeftOutByTheQualifyingRule(t *testing.T) {
	// "strong" has 10 tasks scoring about 0.12 under the best cell. Its
	// interval is wide enough to be allowed, so only the qualifying rule
	// (cell score under 0.9 x best) keeps it out of the rotation. The note
	// says so, and never says "overqualified".
	var tasks []PickerTask
	tasks = append(tasks, pickTasks("cheap", 1.8, repeat(10, 1)...)...)
	tasks = append(tasks, pickTasks("strong", 1.8, repeat(3, 1, .75, .75, .75)...)...)
	scores := pickScores(map[string]float64{"cheap": 29, "strong": 51})
	table := BuildPickerTable(tasks, DefaultPickerSettings())
	cheapScore, strongScore := table.Cell("cheap", 2).Score, table.Cell("strong", 2).Score
	if gap := cheapScore - strongScore; gap < .1 || gap > .14 {
		t.Fatalf("fixture gap %.3f, want about 0.12 (cheap %.3f, strong %.3f)", gap, cheapScore, strongScore)
	}
	counts := pickN(t, pickCandidates("cheap", "strong"), "simple", 1.8, tasks, scores, DefaultPickerSettings(), 20)
	if counts["strong"] != 0 || counts["cheap"] != 20 {
		t.Fatalf("picks: %v", counts)
	}
	choice, err := ChooseWorkerModel(pickCandidates("cheap", "strong"), "simple", scores, nil, nil, nil, &WorkerRotation{}, &PickerInput{D: 1.8, Table: table})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(choice.PickNote, "strong ") || !strings.Contains(choice.PickNote, "left out (score below 0.9×best)") || strings.Contains(choice.PickNote, "overqualified") {
		t.Fatalf("note: %q", choice.PickNote)
	}
}

func TestPickKeepsAModelAboveItsBenchmarkOut(t *testing.T) {
	// A thin model whose benchmark ceiling is below the task stays out. D 4.5
	// is far above "weak" (ceiling D 2.25).
	scores := pickScores(map[string]float64{"weak": 29, "strong": 51})
	tasks := pickTasks("strong", 4.5, repeat(10, 1)...)
	counts := pickN(t, pickCandidates("weak", "strong"), "very_complex", 4.5, tasks, scores, DefaultPickerSettings(), 20)
	if counts["weak"] != 0 || counts["strong"] != 20 {
		t.Fatalf("picks: %v", counts)
	}
}

func TestPickUntriedStrongModelsAreNotExploredOnEasyWork(t *testing.T) {
	// D 1.8 is bucket 2. "cheap" has a record. "strong" benchmarks very_complex
	// (ceiling D 5.0) and has no record, or only two tasks. It is allowed by its
	// benchmark, but the explore share is up only: a simple task is a step down
	// for it, so it gets no pick.
	scores := pickScores(map[string]float64{"cheap": 29, "strong": 51})
	cheap := pickTasks("cheap", 1.8, repeat(10, 1, .8, 1)...)
	for name, tasks := range map[string][]PickerTask{
		"no record": cheap,
		"two tasks": append(append([]PickerTask(nil), cheap...), pickTasks("strong", 1.8, 1, 1)...),
	} {
		counts := pickN(t, pickCandidates("cheap", "strong"), "simple", 1.8, tasks, scores, DefaultPickerSettings(), 20)
		if counts["strong"] != 0 || counts["cheap"] != 20 {
			t.Fatalf("%s: picks %v, want none for the strong model", name, counts)
		}
	}
	// With no weaker model allowed, the strong one is used.
	counts := pickN(t, pickCandidates("strong"), "simple", 1.8, nil, scores, DefaultPickerSettings(), 5)
	if counts["strong"] != 5 {
		t.Fatalf("alone: picks %v", counts)
	}
	// Two untried strong models are equally strong.
	both := pickScores(map[string]float64{"s1": 51, "s2": 51})
	counts = pickN(t, pickCandidates("s1", "s2"), "simple", 1.8, nil, both, DefaultPickerSettings(), 10)
	if counts["s1"] == 0 || counts["s2"] == 0 {
		t.Fatalf("equal strong models: picks %v", counts)
	}
}

// The explore share is up only (decision 0108): it sends a task only to a thin
// cell for which the task is a step up.
func TestPickExploreIsUpOnly(t *testing.T) {
	picks := func(level string, d float64, tasks []PickerTask, scores []ModelScore, names ...string) (map[string]int, string) {
		t.Helper()
		counts := pickN(t, pickCandidates(names...), level, d, tasks, scores, DefaultPickerSettings(), 100)
		choice, err := ChooseWorkerModel(pickCandidates(names...), level, scores, nil, nil, nil, &WorkerRotation{}, &PickerInput{D: d, Table: BuildPickerTable(tasks, DefaultPickerSettings())})
		if err != nil {
			t.Fatal(err)
		}
		return counts, choice.PickNote
	}

	t.Run("opus with no record is not explored on trivial work", func(t *testing.T) {
		scores := pickScores(map[string]float64{"cheap": 20, "opus": 51.7})
		counts, note := picks("trivial", 1.0, pickTasks("cheap", 1.0, repeat(10, 1, .8, 1)...), scores, "cheap", "opus")
		if counts["opus"] != 0 || counts["cheap"] != 100 {
			t.Fatalf("picks: %v", counts)
		}
		if !strings.Contains(note, "opus n0 left out (explore is up only)") {
			t.Fatalf("note: %q", note)
		}
	})

	t.Run("opus with no record is explored at D 4.00", func(t *testing.T) {
		scores := pickScores(map[string]float64{"weak": 39, "opus": 51.7})
		counts, _ := picks("very_complex", 4.0, pickTasks("weak", 4.0, repeat(10, 1, .8, 1)...), scores, "weak", "opus")
		if counts["opus"] != 10 {
			t.Fatalf("picks: %v, want the 10 percent explore share", counts)
		}
	})

	t.Run("a model proven at simple is explored at normal", func(t *testing.T) {
		// Its benchmark is very_complex, so only a record below the task can
		// make this a step up.
		scores := pickScores(map[string]float64{"norm": 39, "stretch": 51})
		tasks := append(pickTasks("norm", 2.5, repeat(10, 1, .8, 1)...), pickTasks("stretch", 1.8, repeat(10, 1)...)...)
		counts, _ := picks("normal", 2.5, tasks, scores, "norm", "stretch")
		if counts["stretch"] != 10 {
			t.Fatalf("picks: %v, want the 10 percent explore share", counts)
		}
	})

	t.Run("a model proven at hard is not explored at simple", func(t *testing.T) {
		scores := pickScores(map[string]float64{"cheap": 29, "hardpro": 51})
		tasks := append(pickTasks("cheap", 1.8, repeat(10, 1, .8, 1)...), pickTasks("hardpro", 3.5, repeat(10, 1)...)...)
		counts, note := picks("simple", 1.8, tasks, scores, "cheap", "hardpro")
		if counts["hardpro"] != 0 {
			t.Fatalf("picks: %v", counts)
		}
		if !strings.Contains(note, "hardpro n0 left out (explore is up only)") {
			t.Fatalf("note: %q", note)
		}
	})

	t.Run("a new model is explored at its own level", func(t *testing.T) {
		scores := pickScores(map[string]float64{"cheap": 29, "fresh": 29})
		counts, _ := picks("simple", 1.8, pickTasks("cheap", 1.8, repeat(10, 1, .8, 1)...), scores, "cheap", "fresh")
		if counts["fresh"] != 10 {
			t.Fatalf("picks: %v, want the 10 percent explore share", counts)
		}
	})
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
	want := "bucket 2.33-2.67: deepseek 0.73 [0.65-0.80] n30, best; qualifying: deepseek 0.73 [0.65-0.80] n30 weight 1.00; flash 0.63 [0.44-0.81] n6 left out (score below 0.9×best); terra:medium 0.19 [0.07-0.31] n10 left out (below 0.9×best)"
	if choice.Name != "deepseek" || choice.PickNote != want {
		t.Fatalf("choice %s\nnote %q\nwant %q", choice.Name, choice.PickNote, want)
	}
}

func TestPickNoteShowsPriorAndChosenNotBest(t *testing.T) {
	tasks := append(pickTasks("cheap", 1.8, repeat(10, 1, .8, 1)...), pickTasks("strong", 1.8, repeat(10, .5, .5, .5)...)...)
	scores := pickScores(map[string]float64{"cheap": 29, "strong": 51, "fresh": 51})
	s := DefaultPickerSettings()
	s.AllowedFraction = .3
	table := BuildPickerTable(tasks, s)
	choice, err := ChooseWorkerModel(pickCandidates("cheap", "strong", "fresh"), "simple", scores, nil, nil, nil, &WorkerRotation{}, &PickerInput{D: 1.8, Table: table})
	if err != nil {
		t.Fatal(err)
	}
	// The lenient fraction lets "strong" qualify. "fresh" has no record and its
	// benchmark (very_complex) is above this simple task, so exploring it would
	// be a step down. Nothing is left out as overqualified.
	for _, want := range []string{"cheap 0.90 [0.86-0.95] n30, best", "strong 0.53 [0.49-0.58] n30 weight", "fresh n0 left out (explore is up only)"} {
		if !strings.Contains(choice.PickNote, want) {
			t.Fatalf("note misses %q: %q", want, choice.PickNote)
		}
	}
	if strings.Contains(choice.PickNote, "overqualified") {
		t.Fatalf("note: %q", choice.PickNote)
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

func TestPickerBucketVerdictsAreTheCellRule(t *testing.T) {
	table := BuildPickerTable(shrinkFixture(), DefaultPickerSettings())
	lo, _ := table.BucketRange(4)
	mean := table.BucketMean(4)
	a := table.Cell("A", 4) // three tasks, the best record
	b := table.Cell("B", 4) // one task, strong benchmark, above the work
	facts := []PickerCandidateFacts{
		{Key: "A", Cell: a, Scored: true, Benchmark: 30, Ceiling: 2.25},
		{Key: "B", Cell: b, Scored: true, Benchmark: 55, Ceiling: 5},
		{Key: "D", Cell: PickerCell{Score: .2, Lo: .1, Hi: .3, N: 5}, Scored: true, Benchmark: 20, Ceiling: 1.5},
		{Key: "E", Cell: PickerCell{Score: mean, Lo: mean, Hi: mean, N: 0}, Scored: true, Benchmark: 15, Ceiling: 2},
		{Key: "C", Cell: PickerCell{Score: mean, Lo: mean, Hi: mean, N: 0}},
	}
	got, best := PickerBucketVerdicts(facts, lo, mean, 4, DefaultPickerSettings())
	if best != 0 || !got[0].Allowed {
		t.Fatalf("best = %d, A = %+v, want A allowed and best", best, got[0])
	}
	if !got[1].Allowed || !got[1].Prior || got[1].Reason != "" {
		t.Errorf("B = %+v, want allowed by its benchmark, however far above the work", got[1])
	}
	if got[2].Allowed || got[2].Reason != "below 0.9×best" {
		t.Errorf("D = %+v, want left out below the allowed fraction", got[2])
	}
	if got[3].Allowed || got[3].Reason != "above its benchmark" {
		t.Errorf("E = %+v, want left out above its benchmark", got[3])
	}
	if got[4].Allowed || got[4].Reason != "no record, no benchmark" {
		t.Errorf("C = %+v, want left out with no record and no benchmark", got[4])
	}

	// A thin model below its ceiling is allowed by the benchmark and flagged
	// as a prior, which the screen shows as "chosen by benchmark (n<3)".
	facts[1].Ceiling = 2.5
	got, _ = PickerBucketVerdicts(facts, lo, mean, 4, DefaultPickerSettings())
	if !got[1].Allowed || !got[1].Prior {
		t.Errorf("B with ceiling 2.5 = %+v, want allowed by benchmark", got[1])
	}
}

func TestPickerBPrimeRotation(t *testing.T) {
	tasks := append(pickTasks("strong", 2.5, repeat(5, 1)...), pickTasks("weak", 2.5, repeat(5, 1)...)...)
	scores := pickScores(map[string]float64{"strong": 51, "weak": 39, "fresh": 39})
	got := pickN(t, pickCandidates("strong", "weak", "fresh"), "normal", 2.5, tasks, scores, DefaultPickerSettings(), 100)
	if got["fresh"] != 10 || got["weak"] <= got["strong"] || got["strong"] == 0 {
		t.Fatalf("rotation: %v", got)
	}
}

func TestPickerBPrimeUsesScoreNotInterval(t *testing.T) {
	tasks := append(pickTasks("good", 2.5, repeat(10, 1)...), pickTasks("weak", 2.5, repeat(10, .7)...)...)
	scores := pickScores(map[string]float64{"good": 39, "weak": 39})
	got := pickN(t, pickCandidates("good", "weak"), "normal", 2.5, tasks, scores, DefaultPickerSettings(), 30)
	if got["weak"] != 0 {
		t.Fatalf("below-threshold model was picked: %v", got)
	}
}

// Weight = quota pace x (best benchmark / benchmark) ^ weaker_bias. With
// benchmarks 50 and 40 the weights are 1 and 1.25, so 90 smooth picks split
// 40 and 50.
func TestPickerBPrimeWeakerBiasSetsTheShares(t *testing.T) {
	tasks := append(pickTasks("strong", 2.5, repeat(5, 1)...), pickTasks("weak", 2.5, repeat(5, 1)...)...)
	scores := pickScores(map[string]float64{"strong": 50, "weak": 40})
	got := pickN(t, pickCandidates("strong", "weak"), "normal", 2.5, tasks, scores, DefaultPickerSettings(), 90)
	if got["strong"] != 40 || got["weak"] != 50 {
		t.Fatalf("bias 1: %v, want 40 and 50", got)
	}
	set := DefaultPickerSettings()
	set.WeakerBias = 2 // weights 1 and 1.5625
	got = pickN(t, pickCandidates("strong", "weak"), "normal", 2.5, tasks, scores, set, 256)
	if got["strong"] != 100 || got["weak"] != 156 {
		t.Fatalf("bias 2: %v, want 100 and 156", got)
	}
}

// A provider behind its quota pace gets more weight, with equal benchmarks.
func TestPickerBPrimeQuotaPaceScalesTheWeight(t *testing.T) {
	tasks := append(pickTasks("a", 2.5, repeat(5, 1)...), pickTasks("b", 2.5, repeat(5, 1)...)...)
	scores := pickScores(map[string]float64{"a": 50, "b": 50})
	c := pickCandidates("a", "b")
	c[0].Provider, c[1].Provider = "pa", "pb"
	// pa is halfway through its week and has used 30 percent: 20 points of room.
	q := &Quotas{GeneratedAt: 1000, Providers: []QuotaProvider{{Provider: "pa", Windows: []QuotaWindow{{Kind: "weekly", PercentUsed: 30, ResetsAt: 1000 + 7*12*3600}}}}}
	in := &PickerInput{D: 2.5, Table: BuildPickerTable(tasks, DefaultPickerSettings())}
	rotation := &WorkerRotation{}
	counts := map[string]int{}
	for i := 0; i < 100; i++ {
		choice, err := ChooseWorkerModel(c, "normal", scores, nil, q, nil, rotation, in)
		if err != nil {
			t.Fatal(err)
		}
		counts[choice.Name]++
	}
	// exp(20/20) = 2.718 against 1: about 73 and 27.
	if counts["a"] < 70 || counts["a"] > 76 {
		t.Fatalf("picks: %v", counts)
	}
}

// The qualifying set is the models with three tasks or more and a cell score
// of 0.9 x best or more. The note names it with the weights, names the thin
// cells open to exploration and gives the reason for each model left out.
func TestPickerBPrimeNoteNamesTheQualifyingSetAndWeights(t *testing.T) {
	tasks := append(pickTasks("a", 2.5, repeat(10, 1)...), pickTasks("b", 2.5, repeat(10, 1)...)...)
	tasks = append(tasks, pickTasks("c", 2.5, repeat(10, 0)...)...)
	tasks = append(tasks, pickTasks("thin", 2.5, 1)...)
	scores := pickScores(map[string]float64{"a": 50, "b": 40, "c": 40, "thin": 35})
	in := &PickerInput{D: 2.5, Table: BuildPickerTable(tasks, DefaultPickerSettings())}
	choice, err := ChooseWorkerModel(pickCandidates("a", "b", "c", "thin"), "normal", scores, nil, nil, nil, &WorkerRotation{}, in)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"qualifying: ", "a 0.89 [0.75-1.00] n10 weight 1.00", "b 0.89 [0.75-1.00] n10 weight 1.25", "thin cells (explore share 0.1): thin ", "(benchmark) weight 1.43", "c 0.23 [0.09-0.37] n10 left out (below 0.9×best)"} {
		if !strings.Contains(choice.PickNote, want) {
			t.Errorf("note lacks %q: %s", want, choice.PickNote)
		}
	}
	if strings.Contains(choice.PickNote, "qualifying: thin") {
		t.Errorf("a thin cell is in the qualifying set: %s", choice.PickNote)
	}
}

// With no model at three tasks, nothing qualifies: the allowed set rotates as
// before, with no exploration state used.
func TestPickerBPrimeWithoutQualifiersRotatesTheAllowedSet(t *testing.T) {
	tasks := append(pickTasks("x", 2.5, 1, 0), pickTasks("y", 2.5, 1, 1)...)
	scores := pickScores(map[string]float64{"x": 39, "y": 39})
	got := pickN(t, pickCandidates("x", "y"), "normal", 2.5, tasks, scores, DefaultPickerSettings(), 40)
	if got["x"] == 0 || got["y"] == 0 {
		t.Fatalf("picks: %v", got)
	}
}

func TestPickerBPrimeExplorationStateSurvivesAClone(t *testing.T) {
	tasks := append(pickTasks("good", 2.5, repeat(15, 1, .5)...), pickTasks("new", 2.5, 1, 0)...)
	scores := pickScores(map[string]float64{"good": 51, "new": 39})
	in := &PickerInput{D: 2.5, Table: BuildPickerTable(tasks, DefaultPickerSettings())}
	rotation := &WorkerRotation{}
	counts := map[string]int{}
	for i := 0; i < 40; i++ {
		clone := rotation.Clone() // the gateway stores and reloads the state between picks
		rotation = &clone
		choice, err := ChooseWorkerModel(pickCandidates("good", "new"), "normal", scores, nil, nil, nil, rotation, in)
		if err != nil {
			t.Fatal(err)
		}
		counts[choice.Name]++
	}
	if counts["new"] != 4 {
		t.Fatalf("picks: %v", counts)
	}
}
