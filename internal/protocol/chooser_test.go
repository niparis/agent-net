package protocol

import (
	"strings"
	"testing"
	"time"
)

func TestChooseWorkerModel(t *testing.T) {
	now := time.Unix(1000000, 0)
	scores := []ModelScore{{Match: "a", Score: 51}, {Match: "b", Score: 51}, {Match: "c", Score: 39}, {Match: "d", Score: 29}}
	candidates := []WorkerModelCandidate{{Harness: "pi", Model: "a"}, {Harness: "pi", Model: "b"}, {Harness: "pi", Model: "c"}, {Harness: "pi", Model: "d"}}
	q := &Quotas{GeneratedAt: now.Unix(), Providers: []QuotaProvider{{Provider: "a", Windows: []QuotaWindow{{Kind: "weekly", PercentUsed: 40, ResetsAt: now.Add(3 * 24 * time.Hour).Unix()}}}, {Provider: "b", Windows: []QuotaWindow{{Kind: "weekly", PercentUsed: 70, ResetsAt: now.Add(3 * 24 * time.Hour).Unix()}}}}}
	candidates[0].Provider = "a"
	candidates[1].Provider = "b"
	tests := []struct {
		name    string
		q       *Quotas
		running map[string]int
		failed  bool
		rotate  int
		want    string
	}{
		{"quota room", q, nil, false, 0, "a"}, {"workers consume room", q, map[string]int{"a": 7}, false, 0, "b"},
		{"round robin first", nil, nil, false, 0, "a"}, {"round robin second", nil, nil, false, 1, "b"}, {"failed last", nil, nil, true, 0, "b"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := append([]WorkerModelCandidate(nil), candidates...)
			c[0].Failed = tt.failed
			rotation := &WorkerRotation{}
			var got WorkerModelChoice
			var err error
			for i := 0; i <= tt.rotate; i++ {
				got, err = ChooseWorkerModel(c, "hard", scores, nil, tt.q, tt.running, rotation)
			}
			if err != nil || got.Model != tt.want {
				t.Fatalf("got %+v: %v", got, err)
			}
		})
	}
	q.Providers[0].Windows[0].PercentUsed = 91
	got, err := ChooseWorkerModel(candidates, "hard", scores, nil, q, nil, &WorkerRotation{})
	if err != nil || got.Model != "b" {
		t.Fatalf("high quota: %+v %v", got, err)
	}
	_, err = ChooseWorkerModel([]WorkerModelCandidate{{Model: "d"}}, "very_complex", scores, nil, nil, nil, &WorkerRotation{})
	if err == nil || !strings.Contains(err.Error(), "denied") {
		t.Fatalf("no eligible: %v", err)
	}
}

func TestChooseWorkerModelUsesHarnessDefaultEffort(t *testing.T) {
	candidates := []WorkerModelCandidate{{Harness: "codex", Model: "gpt-6.1-terra", DefaultEffort: "high"}}
	choice, err := ChooseWorkerModel(candidates, "hard", nil, nil, nil, nil, &WorkerRotation{})
	if err != nil || choice.Score != 42.3 || choice.Fit != "good" {
		t.Fatalf("choice: %+v %v", choice, err)
	}
}

func TestWorkerQuotaRoomMonthly(t *testing.T) {
	now := time.Date(2026, 2, 15, 0, 0, 0, 0, time.UTC)
	q := &Quotas{GeneratedAt: now.Unix(), Providers: []QuotaProvider{{Provider: "p", Windows: []QuotaWindow{{Kind: "monthly", Label: "month", PercentUsed: 30, ResetsAt: time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC).Unix()}}}}}
	room, note, known, blocked := workerQuotaRoom(q, "p")
	if !known || blocked || room < 20 || room > 25 || !strings.Contains(note, "50% elapsed") {
		t.Fatalf("room=%v note=%q known=%v blocked=%v", room, note, known, blocked)
	}
}

func TestChooseWorkerModelPrefersRecentSuccess(t *testing.T) {
	scores := []ModelScore{{Match: "a", Score: 51}, {Match: "b", Score: 51}}
	candidates := []WorkerModelCandidate{{Name: "a", Harness: "pi", Model: "a"}, {Name: "b", Harness: "pi", Model: "b", Succeeded: true}}
	counts := map[string]int{}
	rotation := &WorkerRotation{}
	for i := 0; i < 12; i++ {
		got, err := ChooseWorkerModel(candidates, "hard", scores, nil, nil, nil, rotation)
		if err != nil {
			t.Fatal(err)
		}
		counts[got.Name]++
	}
	if counts["b"] <= counts["a"] || counts["a"] == 0 {
		t.Fatalf("recent success did not raise the share: %v", counts)
	}
	candidates[1].Failed = true
	counts = map[string]int{}
	rotation = &WorkerRotation{}
	for i := 0; i < 12; i++ {
		got, err := ChooseWorkerModel(candidates, "hard", scores, nil, nil, nil, rotation)
		if err != nil {
			t.Fatal(err)
		}
		counts[got.Name]++
	}
	if counts["a"] <= counts["b"] {
		t.Fatalf("a failed model kept its share: %v", counts)
	}
}

func TestChooseWorkerModelAdequateScore(t *testing.T) {
	c := []WorkerModelCandidate{{Harness: "pi", Model: "terra"}, {Harness: "pi", Model: "deepseek"}}
	scores := []ModelScore{{Match: "terra", Score: 42.3}, {Match: "deepseek", Score: 32.5}}
	for _, tt := range []struct{ level, want string }{{"normal", "deepseek"}, {"hard", "terra"}} {
		t.Run(tt.level, func(t *testing.T) {
			got, err := ChooseWorkerModel(c, tt.level, scores, nil, nil, nil, &WorkerRotation{})
			if err != nil || got.Model != tt.want {
				t.Fatalf("got %+v: %v", got, err)
			}
		})
	}
}

// workerShareStartsForTest matches the Quotas heatmap window.
const workerShareStartsForTest = 20

// With today's scores and equal quota pace, over-qualification keeps the
// weakest adequate model in front at trivial difficulty and saves the strong
// models for harder work. Only eligible models rotate.
func TestChooseWorkerModelOverQualificationPenalty(t *testing.T) {
	candidates := []WorkerModelCandidate{
		{Name: "luna", Harness: "codex", Model: "gpt-6-luna"},
		{Name: "deepseek", Harness: "opencode", Model: "opencode-go/deepseek-v4.1-flash"},
		{Name: "sonnet", Harness: "claude", Model: "claude-sonnet-5-5"},
		{Name: "terra", Harness: "codex", Model: "gpt-6.1-terra"},
		{Name: "sol", Harness: "codex", Model: "gpt-6.1-sol"},
	}
	scores := []ModelScore{
		{Match: "gpt-6-luna", Score: 23.3},
		{Match: "deepseek-v4.1-flash", Score: 32.5},
		{Match: "claude-sonnet-5-5", Score: 41.1},
		{Match: "gpt-6.1-terra", Score: 42.3},
		{Match: "gpt-6.1-sol", Score: 46.1},
	}
	shares := func(level string, starts int) map[string]int {
		counts := map[string]int{}
		rotation := &WorkerRotation{}
		for i := 0; i < starts; i++ {
			choice, err := ChooseWorkerModel(candidates, level, scores, nil, nil, nil, rotation)
			if err != nil {
				t.Fatalf("%s start %d: %v", level, i, err)
			}
			counts[choice.Name]++
		}
		return counts
	}

	trivial := shares("trivial", workerShareStartsForTest)
	for name, count := range trivial {
		if count <= 0 {
			t.Errorf("trivial: adequate model %s has no share: %v", name, trivial)
		}
	}
	for name, count := range trivial {
		if name != "luna" && count >= trivial["luna"] {
			t.Errorf("trivial: %s beat the weakest adequate model: %v", name, trivial)
		}
	}
	if trivial["sol"]*100/workerShareStartsForTest >= 10 {
		t.Errorf("trivial: sol got %d of %d starts, want under 10%%: %v", trivial["sol"], workerShareStartsForTest, trivial)
	}

	hard := shares("hard", workerShareStartsForTest)
	for _, name := range []string{"sonnet", "terra"} {
		if hard[name] <= hard["sol"] {
			t.Errorf("hard: %s did not beat sol: %v", name, hard)
		}
	}
	for _, name := range []string{"sonnet", "terra", "sol"} {
		if hard[name] <= 0 {
			t.Errorf("hard: adequate model %s has no share: %v", name, hard)
		}
	}
	for _, name := range []string{"luna", "deepseek"} {
		if hard[name] != 0 {
			t.Errorf("hard: below-minimum model %s got %d starts: %v", name, hard[name], hard)
		}
	}
}

func TestChooseWorkerModelQuotaPaceBeforeScore(t *testing.T) {
	now := time.Unix(1000000, 0)
	c := []WorkerModelCandidate{{Model: "cheap", Provider: "a"}, {Model: "strong", Provider: "b"}}
	scores := []ModelScore{{Match: "cheap", Score: 32.5}, {Match: "strong", Score: 42.3}}
	for _, tt := range []struct {
		name string
		a, b float64
		want string
	}{
		{"behind beats ahead", 70, 20, "strong"},
		{"cheap behind", 20, 70, "cheap"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			q := &Quotas{GeneratedAt: now.Unix(), Providers: []QuotaProvider{{Provider: "a", Windows: []QuotaWindow{{Kind: "weekly", PercentUsed: tt.a, ResetsAt: now.Add(84 * time.Hour).Unix()}}}, {Provider: "b", Windows: []QuotaWindow{{Kind: "weekly", PercentUsed: tt.b, ResetsAt: now.Add(84 * time.Hour).Unix()}}}}}
			got, err := ChooseWorkerModel(c, "normal", scores, nil, q, nil, &WorkerRotation{})
			if err != nil || got.Model != tt.want {
				t.Fatalf("got %+v: %v", got, err)
			}
		})
	}
}

func TestChooseWorkerModelTrackRecord(t *testing.T) {
	scores := []ModelScore{{Match: "cheap", Score: 32.5}, {Match: "strong", Score: 42.3}}
	for _, tt := range []struct {
		name                      string
		complete, partial, failed int
		want                      string
	}{
		{"poor record", 2, 2, 1, "strong"},
		{"too few runs", 1, 2, 1, "cheap"},
		{"half complete", 3, 2, 1, "cheap"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			c := []WorkerModelCandidate{{Model: "cheap", Track: TrackRecord{Complete: tt.complete, Partial: tt.partial, Failed: tt.failed}}, {Model: "strong"}}
			got, err := ChooseWorkerModel(c, "normal", scores, nil, nil, nil, &WorkerRotation{})
			if err != nil || got.Model != tt.want {
				t.Fatalf("got %+v: %v", got, err)
			}
		})
	}
}

// Terra is well behind its weekly pace and DeepSeek has no reading. Over
// twenty normal starts both models must appear, and the lagging one must still
// win more often. The gap is wide enough to overcome the over-qualification
// penalty that favours the weaker adequate model.
func TestChooseWorkerModelWeightedRotationSpreads(t *testing.T) {
	now := time.Unix(1000000, 0)
	candidates := []WorkerModelCandidate{
		{Name: "terra", Harness: "codex", Model: "gpt-6.1-terra", Provider: "codex"},
		{Name: "deepseek", Harness: "opencode", Model: "opencode-go/deepseek", Provider: "opencode-go"},
	}
	scores := []ModelScore{{Match: "terra", Score: 42.3}, {Match: "deepseek", Score: 32.5}}
	// The window resets in 84 hours of a 168 hour week, so 50 percent has
	// elapsed and 20 percent is used: thirty points behind pace.
	q := &Quotas{GeneratedAt: now.Unix(), Providers: []QuotaProvider{{Provider: "codex", Windows: []QuotaWindow{{Kind: "weekly", PercentUsed: 20, ResetsAt: now.Add(84 * time.Hour).Unix()}}}}}
	rotation := &WorkerRotation{}
	counts := map[string]int{}
	var last WorkerModelChoice
	for i := 0; i < 20; i++ {
		choice, err := ChooseWorkerModel(candidates, "normal", scores, nil, q, nil, rotation)
		if err != nil {
			t.Fatal(err)
		}
		counts[choice.Name]++
		last = choice
	}
	if counts["deepseek"] == 0 || counts["terra"] == 0 {
		t.Fatalf("a model was starved: %v", counts)
	}
	if counts["terra"] <= counts["deepseek"] {
		t.Fatalf("the lagging model did not win more: %v", counts)
	}
	if !strings.Contains(last.ShareNote, "terra") || !strings.Contains(last.ShareNote, "deepseek") || !strings.Contains(last.ShareNote, "normal starts") {
		t.Fatalf("share note: %q", last.ShareNote)
	}
	if last.Weight <= 0 {
		t.Fatalf("weight: %v", last.Weight)
	}
}

func TestChooseWorkerModelRotationIsDeterministic(t *testing.T) {
	candidates := []WorkerModelCandidate{
		{Name: "a", Harness: "pi", Model: "a"},
		{Name: "b", Harness: "pi", Model: "b"},
		{Name: "c", Harness: "pi", Model: "c"},
	}
	scores := []ModelScore{{Match: "a", Score: 41}, {Match: "b", Score: 42}, {Match: "c", Score: 43}}
	base := &WorkerRotation{}
	for i := 0; i < 3; i++ {
		if _, err := ChooseWorkerModel(candidates, "hard", scores, nil, nil, nil, base); err != nil {
			t.Fatal(err)
		}
	}
	left, right := base.Clone(), base.Clone()
	for i := 0; i < 7; i++ {
		x, err := ChooseWorkerModel(candidates, "hard", scores, nil, nil, nil, &left)
		if err != nil {
			t.Fatal(err)
		}
		y, err := ChooseWorkerModel(candidates, "hard", scores, nil, nil, nil, &right)
		if err != nil {
			t.Fatal(err)
		}
		if x.Model != y.Model || x.ShareNote != y.ShareNote {
			t.Fatalf("start %d differs: %q %q", i, x.Model, y.Model)
		}
	}
}

func TestChooseWorkerModelExhaustedNeverAppears(t *testing.T) {
	candidates := []WorkerModelCandidate{{Name: "empty", Harness: "pi", Model: "empty", Provider: "a"}, {Name: "open", Harness: "pi", Model: "open", Provider: "b"}}
	scores := []ModelScore{{Match: "empty", Score: 40}, {Match: "open", Score: 40}}
	q := &Quotas{Providers: []QuotaProvider{{Provider: "a", Windows: []QuotaWindow{{Exhausted: true}}}}}
	rotation := &WorkerRotation{}
	for i := 0; i < 10; i++ {
		got, err := ChooseWorkerModel(candidates, "normal", scores, nil, q, nil, rotation)
		if err != nil || got.Model != "open" {
			t.Fatalf("start %d: %+v %v", i, got, err)
		}
	}
}

func TestChooseWorkerModelUsesPoorOnlyWhenAlone(t *testing.T) {
	scores := []ModelScore{{Match: "cheap", Score: 32.5}, {Match: "strong", Score: 42.3}}
	c := []WorkerModelCandidate{{Name: "cheap", Model: "cheap", Track: TrackRecord{Complete: 1, Partial: 4}}, {Name: "strong", Model: "strong"}}
	rotation := &WorkerRotation{}
	for i := 0; i < 10; i++ {
		got, err := ChooseWorkerModel(c, "normal", scores, nil, nil, nil, rotation)
		if err != nil || got.Model != "strong" {
			t.Fatalf("start %d: %+v %v", i, got, err)
		}
	}
	got, err := ChooseWorkerModel(c[:1], "normal", scores, nil, nil, nil, rotation)
	if err != nil || got.Model != "cheap" {
		t.Fatalf("poor alone: %+v %v", got, err)
	}
}

func TestWorkerRotationCloneIsIndependent(t *testing.T) {
	a := WorkerRotation{Levels: map[string]*WorkerRotationLevel{"normal": {Current: map[string]float64{"x": 1}, Recent: []string{"x"}}}}
	b := a.Clone()
	b.Levels["normal"].Current["x"] = 99
	b.Levels["normal"].Recent[0] = "y"
	if a.Levels["normal"].Current["x"] != 1 || a.Levels["normal"].Recent[0] != "x" {
		t.Fatalf("clone shares state: %+v", a)
	}
}

func TestWorkerCandidateMaximumLevelEligibility(t *testing.T) {
	levels := []string{"trivial", "simple", "normal", "hard", "very_complex"}
	for _, tc := range []struct {
		name  string
		score float64
		max   int
	}{
		{"luna", 23.3, 1}, {"deepseek", 32.5, 2}, {"terra", 38.1, 2},
		{"sol", 46.1, 3}, {"terra-high", 42.3, 3}, {"terra-max", 51, 4},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := WorkerModelCandidate{Name: tc.name, Harness: "codex", Model: tc.name}
			scores := []ModelScore{{Match: tc.name, Score: tc.score}}
			for i, level := range levels {
				_, _, eligible, ok := WorkerCandidateFit(c, level, scores, nil)
				want := i == tc.max || i == tc.max-1
				if !ok || eligible != want {
					t.Errorf("%s: eligible=%v, want %v", level, eligible, want)
				}
				rotation := &WorkerRotation{}
				for start := 0; start < 25; start++ {
					_, err := ChooseWorkerModel([]WorkerModelCandidate{c}, level, scores, nil, nil, nil, rotation)
					if (err == nil) != want {
						t.Fatalf("%s start %d: %v", level, start, err)
					}
				}
			}
		})
	}
}

func TestWorkerNoEligibleModelExplainsFix(t *testing.T) {
	c := []WorkerModelCandidate{{Name: "terra max codex", Harness: "codex", Model: "terra-max"}, {Name: "unknown", Harness: "pi", Model: "unscored"}}
	_, err := ChooseWorkerModel(c, "normal", []ModelScore{{Match: "terra-max", Score: 51}}, nil, nil, nil, nil)
	if err == nil {
		t.Fatal("over-qualified model was picked")
	}
	for _, want := range []string{"normal", "terra max codex", "very_complex", "unknown", "unscored", "register a profile", "model explicitly"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q lacks %q", err, want)
		}
	}
}

func TestWorkerRotationExcludesModelsAboveMaximumGap(t *testing.T) {
	candidates := []WorkerModelCandidate{}
	scores := []ModelScore{}
	for i, score := range []float64{23.3, 32.5, 38.1, 46.1, 42.3, 51} {
		name := []string{"luna", "deepseek", "terra", "sol", "terra-high", "terra-max"}[i]
		candidates = append(candidates, WorkerModelCandidate{Harness: "codex", Model: name})
		scores = append(scores, ModelScore{Match: name, Score: score})
	}
	levels := []string{"trivial", "simple", "normal", "hard", "very_complex"}
	for i, level := range levels {
		rotation := &WorkerRotation{Levels: map[string]*WorkerRotationLevel{level: {Current: map[string]float64{WorkerModelKey(candidates[5]): 10000}}}}
		for start := 0; start < 100; start++ {
			choice, err := ChooseWorkerModel(candidates, level, scores, nil, nil, nil, rotation)
			if err != nil {
				t.Fatal(err)
			}
			maxLevel := WorkerMaximumLevel(choice.Score, nil)
			if maxLevel != level && (i+1 >= len(levels) || maxLevel != levels[i+1]) {
				t.Fatalf("%s picked max %s", level, maxLevel)
			}
		}
		if i < 3 && rotation.Levels[level].Current[WorkerModelKey(candidates[5])] != 0 {
			t.Fatalf("excluded model carries rotation weight at %s", level)
		}
	}
}

func TestWorkerMaximumLevelUsesMinimumOverrides(t *testing.T) {
	minimums := map[string]float64{"hard": 35, "very_complex": 60}
	c := WorkerModelCandidate{Model: "terra"}
	scores := []ModelScore{{Match: "terra", Score: 38.1}}
	if max := WorkerMaximumLevel(38.1, minimums); max != "hard" {
		t.Fatalf("max=%s", max)
	}
	for _, level := range []string{"simple", "normal", "hard"} {
		_, _, eligible, _ := WorkerCandidateFit(c, level, scores, minimums)
		if eligible != (level != "simple") {
			t.Errorf("%s: eligible=%v", level, eligible)
		}
	}
}
