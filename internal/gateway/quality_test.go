package gateway

import (
	"fmt"
	"math"
	"reflect"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

func TestQualityFitRecoversEffects(t *testing.T) {
	var samples []qualitySample
	for i := 0; i < 200; i++ {
		for _, m := range []string{"a", "b"} {
			for _, h := range []string{"codex", "pi"} {
				y := 3.0
				if m == "a" {
					y++
				}
				samples = append(samples, qualitySample{Model: m, Harness: h, Effort: "medium", Difficulty: "normal", Value: y, Weight: 1})
			}
		}
	}
	f := fitQuality(samples)
	if math.Abs(f.effect("model", "a")-f.effect("model", "b")-1) > .03 {
		t.Fatalf("model effects: %+v", f)
	}
	if math.Abs(f.effect("harness", "codex")) > .01 || math.Abs(f.effect("harness", "pi")) > .01 {
		t.Fatalf("irrelevant harness: %+v", f)
	}
}

func TestQualityFitShrinkage(t *testing.T) {
	var samples []qualitySample
	for i := 0; i < 100; i++ {
		samples = append(samples, qualitySample{Model: "common", Harness: "codex", Effort: "medium", Difficulty: "normal", Value: 3, Weight: 1})
	}
	samples = append(samples, qualitySample{Model: "rare", Harness: "pi", Effort: "high", Difficulty: "hard", Value: 5, Weight: 1})
	f := fitQuality(samples)
	if got := f.predict("rare", "pi", "high", "hard"); math.Abs(got-f.Average) > .6 {
		t.Fatalf("one task prediction %g too far from mean %g", got, f.Average)
	}
}

func TestQualityCacheAndChanges(t *testing.T) {
	s, _ := newTestStore(t)
	if got := s.QualityAnalysis(); got.Status != "not enough ratings" {
		t.Fatalf("empty: %+v", got)
	}
	node, _, _ := s.CreateNode("test", "linux")
	caller := ratingCaller(t, s, node, "caller", "gpt-6.1-sol")
	worker := ratingWorker(t, s, node, caller, "model:high", "normal")
	if _, err := s.AddRating(protocol.RateFrame{RateRequest: protocol.RateRequest{Worker: worker, Score: 4}, Rater: caller}); err != nil {
		t.Fatal(err)
	}
	before := s.QualityAnalysis()
	if before.Ratings != 1 {
		t.Fatalf("not refreshed: %+v", before)
	}
	// Destroy the source behind the cache. Requests must still work and must not fit.
	if _, err := s.db.Exec(`DROP TABLE worker_ratings`); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		if got := s.QualityAnalysis(); !reflect.DeepEqual(before, got) {
			t.Fatalf("cache changed: %+v", got)
		}
	}
}

func TestQualityChooserOffExactlyUnchanged(t *testing.T) {
	s, _ := newTestStore(t)
	s.quality = protocol.QualityAnalysis{Ratings: 20}
	s.qualityFit = qualityFit{Average: 3, Effects: map[string]float64{"model\x00gpt-6.1-sol": 2, "model\x00gpt-6.1-terra": -2}}
	s.qualityStrong = map[string]int{"gpt-6.1-sol": 10, "gpt-6.1-terra": 10}
	c := []protocol.WorkerModelCandidate{{Model: "gpt-6.1-sol", Harness: "codex"}, {Model: "gpt-6.1-terra", Harness: "codex"}}
	before := append([]protocol.WorkerModelCandidate(nil), c...)
	if err := s.applyQualityWeights(c, "normal"); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, c) {
		t.Fatalf("off changed candidates: %+v", c)
	}
	a, b := &protocol.WorkerRotation{}, &protocol.WorkerRotation{}
	for i := 0; i < 100; i++ {
		x, e := protocol.ChooseWorkerModel(before, "normal", nil, nil, nil, nil, a)
		if e != nil {
			t.Fatal(e)
		}
		y, e := protocol.ChooseWorkerModel(c, "normal", nil, nil, nil, nil, b)
		if e != nil {
			t.Fatal(e)
		}
		if !reflect.DeepEqual(x, y) || !reflect.DeepEqual(a, b) {
			t.Fatal("off changed chooser")
		}
	}
}

func TestQualityReworkProfilesAndWeakGuard(t *testing.T) {
	s, _ := newTestStore(t)
	node, _, _ := s.CreateNode("test", "linux")
	caller := ratingCaller(t, s, node, "caller", "unknown")
	if err := s.AddWorkerProfile(protocol.WorkerProfile{Name: "rare", Harness: "opencode", Model: "m", Effort: "high"}); err != nil {
		t.Fatal(err)
	}
	wid := ratingWorker(t, s, node, caller, "m:high", "normal")
	if err := s.RecordWorkerRun(protocol.WorkerRun{NodeID: node, SessionID: wid, Harness: "opencode", Model: "m:high", Outcome: protocol.OutcomeComplete, Fix: 2, Continue: 2}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddRating(protocol.RateFrame{RateRequest: protocol.RateRequest{Worker: wid, Score: 5}, Rater: caller}); err != nil {
		t.Fatal(err)
	}
	got := s.QualityAnalysis()
	if want := protocol.ReworkWeight(2, 2); got.ReworkAverage != want || len(got.Profiles) != 1 || got.Profiles[0].Ratings != 1 || got.Profiles[0].Runs != 1 {
		t.Fatalf("analysis: %+v (want rework average %v)", got, want)
	}
	f := qualityFit{Average: 3, Effects: map[string]float64{"model\x00m": 1}}
	if v := qualityPrediction(f, nil, "m", "opencode", "high", "normal"); v != 3 {
		t.Fatalf("weak-only raised quality: %g", v)
	}
	if v := qualityPrediction(f, map[string]int{"m": 1}, "m", "opencode", "high", "normal"); v != 4 {
		t.Fatalf("strong prediction: %g", v)
	}
}

func TestQualityFitEffortAndDifficulty(t *testing.T) {
	var data []qualitySample
	for i := 0; i < 100; i++ {
		for _, model := range []string{"a", "b"} {
			for _, effort := range []string{"low", "high"} {
				for _, difficulty := range []string{"normal", "hard"} {
					value := 3.0
					if effort == "high" {
						value += .5
					} else {
						value -= .5
					}
					if difficulty == "hard" {
						value -= .5
					}
					data = append(data, qualitySample{Model: model, Harness: "codex", Effort: effort, Difficulty: difficulty, Value: value, Weight: 1})
				}
			}
		}
	}
	f := fitQuality(data)
	if math.Abs(f.effect("effort", "a high")-f.effect("effort", "a low")-1) > .05 {
		t.Fatalf("effort: %+v", f)
	}
	if math.Abs(f.effect("difficulty", "normal")-f.effect("difficulty", "hard")-.5) > .03 {
		t.Fatalf("difficulty: %+v", f)
	}
}

func TestQualityChooserEnabledAndBounded(t *testing.T) {
	s, _ := newTestStore(t)
	s.quality = protocol.QualityAnalysis{Ratings: 20}
	s.qualityFit = qualityFit{Average: 3, Effects: map[string]float64{"model\x00a": 2, "model\x00b": -2}}
	s.qualityStrong = map[string]int{"a": 10, "b": 10}
	if _, err := s.db.Exec(`INSERT INTO owner_settings(key,value,updated_at) VALUES('quality_weighting','{"enabled":true}',0)`); err != nil {
		t.Fatal(err)
	}
	candidates := []protocol.WorkerModelCandidate{{Model: "a", Harness: "codex"}, {Model: "b", Harness: "codex"}}
	if err := s.applyQualityWeights(candidates, "normal"); err != nil {
		t.Fatal(err)
	}
	if candidates[0].QualityWeight != 1.5 || candidates[1].QualityWeight != .5 {
		t.Fatalf("weights: %+v", candidates)
	}
	scores := []protocol.ModelScore{{Match: "a", Score: 40}, {Match: "b", Score: 40}}
	rotation := &protocol.WorkerRotation{}
	counts := map[string]int{}
	for i := 0; i < 40; i++ {
		c, err := protocol.ChooseWorkerModel(candidates, "normal", scores, nil, nil, nil, rotation)
		if err != nil {
			t.Fatal(err)
		}
		counts[c.Model]++
	}
	if counts["a"] != 30 || counts["b"] != 10 {
		t.Fatalf("weighted rotation: %+v", counts)
	}
}

func TestQualityModelFamily(t *testing.T) {
	for model, want := range map[string]string{
		"openai/gpt-6.1-sol:high": "sol",
		"gpt-6.1-terra":           "terra",
		"gpt-6.1-luna:medium":     "luna",
		"claude-opus-5-5:high":    "opus",
		"claude-sonnet-4-5":       "sonnet",
		"deepseek-v4.1-flash":     "deepseek",
	} {
		if got := qualityModelFamily(model); got != want {
			t.Errorf("family of %q = %q, want %q", model, got, want)
		}
	}
}

func qualityLevel(t *testing.T, levels []protocol.QualityLevel, name string) protocol.QualityLevel {
	t.Helper()
	for _, l := range levels {
		if l.Difficulty == name {
			return l
		}
	}
	t.Fatalf("no level %q in %+v", name, levels)
	return protocol.QualityLevel{}
}

func qualityFamily(t *testing.T, families []protocol.QualityFamily, name string) protocol.QualityFamily {
	t.Helper()
	for _, f := range families {
		if f.Family == name {
			return f
		}
	}
	t.Fatalf("no family %q in %+v", name, families)
	return protocol.QualityFamily{}
}

func TestQualityLevelsAggregatePerSessionCallerAndDefect(t *testing.T) {
	s, _ := newTestStore(t)
	node, _, _ := s.CreateNode("test", "linux")
	callerName := func(id, name, model string) string {
		t.Helper()
		session, err := s.UpsertSession(node, protocol.SessionInfo{Harness: "claude", NativeID: id, Name: name, Model: model, Status: "idle"})
		if err != nil {
			t.Fatal(err)
		}
		return session
	}
	alpha := callerName("alpha", "agent-net", "claude-opus-5-5")
	beta := callerName("beta", "homelab-agent", "gpt-6.1-terra")
	now := s.now().Unix()
	add := func(session, caller, model, difficulty string, fix, cont int, outcome string) {
		t.Helper()
		if err := s.RecordWorkerRun(protocol.WorkerRun{NodeID: node, SessionID: session, Harness: "opencode", Model: model,
			Outcome: outcome, EndedAt: now, Caller: caller, Difficulty: difficulty, Fix: fix, Continue: cont}); err != nil {
			t.Fatal(err)
		}
	}
	// One task is one session: the two runs of t1 add up to 2 fixes and 1 continue.
	add("t1", alpha, "gpt-6.1-terra:high", "normal", 1, 1, protocol.OutcomeComplete)
	add("t1", alpha, "gpt-6.1-terra:high", "normal", 1, 0, protocol.OutcomeComplete)
	add("t2", alpha, "gpt-6.1-terra:high", "normal", 0, 0, protocol.OutcomeComplete)
	// A stopped task says nothing about the model and stays out.
	add("t3", alpha, "gpt-6.1-terra:high", "normal", 5, 5, protocol.OutcomeStopped)
	add("d1", alpha, "deepseek-v4.1-flash", "normal", 1, 0, protocol.OutcomeComplete)
	add("s1", alpha, "gpt-6.1-sol:high", "hard", 0, 3, protocol.OutcomeComplete)
	add("x1", alpha, "deepseek-v4.1-flash", "other", 2, 0, protocol.OutcomeComplete)
	add("l1", beta, "gpt-6.1-luna", "simple", 0, 0, protocol.OutcomeComplete)
	add("o1", beta, "claude-opus-5-5", "normal", 0, 0, protocol.OutcomeComplete)
	// A task with an unknown run still includes its known rework.
	if _, err := s.db.Exec(`INSERT INTO worker_runs (node_id, session_id, harness, model, outcome, started_at, ended_at, caller, difficulty)
		VALUES (?,?,?,?,?,?,?,?,?)`, node, "u1", "opencode", "gpt-6.1-luna", protocol.OutcomeComplete, now, now, alpha, "normal"); err != nil {
		t.Fatal(err)
	}
	add("u1", alpha, "gpt-6.1-luna", "normal", 2, 0, protocol.OutcomeComplete)
	// Later defects belong to the worker's task, not to a new task. The real
	// worker_defects table comes from the rating work in the base migration.
	for _, d := range []struct {
		session  string
		severity int
	}{{"t1", 3}, {"x1", 4}, {"x1", 2}} {
		if _, err := s.db.Exec(`INSERT INTO worker_defects (session_id, severity, at) VALUES (?,?,?)`, d.session, d.severity, now); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.refreshQuality(); err != nil {
		t.Fatal(err)
	}

	q := s.QualityAnalysis()
	// Only levels with tasks appear, in hardest-to-easiest protocol order.
	wantOrder := []string{"simple", "normal", "hard", "unknown"}
	if len(q.Levels) != len(wantOrder) {
		t.Fatalf("levels: %+v", q.Levels)
	}
	for i, name := range wantOrder {
		if q.Levels[i].Difficulty != name {
			t.Fatalf("level %d = %q, want %q (%+v)", i, q.Levels[i].Difficulty, name, q.Levels)
		}
	}
	normal := qualityLevel(t, q.Levels, "normal")
	if normal.Tasks != 5 {
		t.Fatalf("normal tasks = %d, want 5 (%+v)", normal.Tasks, normal)
	}
	terra := qualityFamily(t, normal.Families, "terra")
	if terra.Tasks != 2 || terra.Fixes != 2 || terra.Continues != 1 {
		t.Fatalf("terra counts: %+v", terra)
	}
	if want := (2 + protocol.CalibrationContinueWeight) / 2; terra.FollowUps != want {
		t.Fatalf("terra follow-ups: %+v, want %.4f", terra, want)
	}
	if terra.LateDefects != 1 || terra.LateDefectSeverity != 3 {
		t.Fatalf("terra defects: %+v", terra)
	}
	if want := (float64(protocol.QualityContinueMinutes) + 2*float64(protocol.QualityFixMinutes) + float64(protocol.QualityDefectMinutesSeverity3)) / 2; terra.Headache != want {
		t.Fatalf("terra headache: %+v, want %.4f", terra, want)
	}
	deepseek := qualityFamily(t, normal.Families, "deepseek")
	if deepseek.Tasks != 1 || deepseek.FollowUps != 1 || deepseek.LateDefects != 0 || deepseek.LateDefectSeverity != 0 {
		t.Fatalf("deepseek normal: %+v", deepseek)
	}
	if got := normal.Families[0].Family; got != "terra" {
		t.Fatalf("families are not sorted by tasks: %+v", normal.Families)
	}
	if f := qualityFamily(t, normal.Families, "luna"); f.Tasks != 1 || f.Fixes != 2 {
		t.Fatalf("older task's known rework: %+v", f)
	}
	hard := qualityLevel(t, q.Levels, "hard")
	if f := qualityFamily(t, hard.Families, "sol"); f.Tasks != 1 || f.Continues != 3 {
		t.Fatalf("hard sol: %+v", f)
	}
	if f := qualityFamily(t, qualityLevel(t, q.Levels, "simple").Families, "luna"); f.Tasks != 1 || f.FollowUps != 0 {
		t.Fatalf("simple luna: %+v", f)
	}
	unknown := qualityLevel(t, q.Levels, "unknown")
	if f := qualityFamily(t, unknown.Families, "deepseek"); f.Tasks != 1 || f.LateDefects != 2 ||
		f.LateDefectSeverity != 3 || f.Headache != 230 {
		t.Fatalf("unknown deepseek: %+v", f)
	}
	if len(q.Agents) != 2 || q.Agents[0].Name != "agent-net" || q.Agents[1].Name != "homelab-agent" {
		t.Fatalf("agents: %+v", q.Agents)
	}
	if f := qualityFamily(t, qualityLevel(t, q.Agents[0].Levels, "normal").Families, "terra"); f.Tasks != 2 || f.Fixes != 2 {
		t.Fatalf("agent-net terra: %+v", f)
	}
	if f := qualityFamily(t, qualityLevel(t, q.Agents[1].Levels, "simple").Families, "luna"); f.Tasks != 1 {
		t.Fatalf("homelab-agent simple: %+v", f)
	}
	if f := qualityFamily(t, qualityLevel(t, q.Agents[1].Levels, "normal").Families, "opus"); f.Tasks != 1 {
		t.Fatalf("homelab-agent normal: %+v", f)
	}
}

func TestQualityReworkSumsEveryRunOfASession(t *testing.T) {
	s, _ := newTestStore(t)
	node, _, _ := s.CreateNode("test", "linux")
	caller := ratingCaller(t, s, node, "caller", "unknown")
	now := s.now().Unix()
	// One session, three runs: the continue is on the first row and the fix on
	// the second. The task's rework is the sum of all rows, not the last one.
	for i, run := range []struct{ cont, fix int }{{1, 0}, {0, 1}, {0, 0}} {
		if err := s.RecordWorkerRun(protocol.WorkerRun{NodeID: node, SessionID: "s_multi", Harness: "opencode",
			Model: "m:high", Outcome: protocol.OutcomeComplete, EndedAt: now + int64(i), Caller: caller,
			Difficulty: "normal", Continue: run.cont, Fix: run.fix}); err != nil {
			t.Fatal(err)
		}
	}
	want := protocol.ReworkWeight(1, 1)
	if got := s.QualityAnalysis(); got.ReworkAverage != want {
		t.Fatalf("session rework = %v, want %v (continue 1, fix 1)", got.ReworkAverage, want)
	}
	// A defect found later adds its severity points to the task's fix count.
	if _, err := s.db.Exec(`INSERT INTO worker_defects (session_id, severity, at) VALUES (?,?,?)`, "s_multi", 2, now); err != nil {
		t.Fatal(err)
	}
	if err := s.refreshQuality(); err != nil {
		t.Fatal(err)
	}
	want = protocol.ReworkWeight(3, 1)
	if got := s.QualityAnalysis(); got.ReworkAverage != want {
		t.Fatalf("session rework with a severity 2 defect = %v, want %v", got.ReworkAverage, want)
	}
}

func TestQualityReworkIncludesUnknownCountersAndSkipsStoppedTasks(t *testing.T) {
	s, _ := newTestStore(t)
	node, _, _ := s.CreateNode("test", "linux")
	caller := ratingCaller(t, s, node, "caller", "unknown")
	now := s.now().Unix()
	add := func(session string, outcome string, cont, fix int) {
		t.Helper()
		if err := s.RecordWorkerRun(protocol.WorkerRun{NodeID: node, SessionID: session, Harness: "opencode",
			Model: "m:high", Outcome: outcome, EndedAt: now, Caller: caller, Difficulty: "normal",
			Continue: cont, Fix: fix}); err != nil {
			t.Fatal(err)
		}
	}
	add("s_good", protocol.OutcomeComplete, 0, 1)
	add("s_stopped", protocol.OutcomeStopped, 0, 9)
	// A task with an older run still contributes the counters that are known.
	if _, err := s.db.Exec(`INSERT INTO worker_runs (node_id, session_id, harness, model, outcome, ended_at, caller, difficulty)
		VALUES (?,?,?,?,?,?,?,?)`, node, "s_unknown", "opencode", "m:high", protocol.OutcomeComplete, now, caller, "normal"); err != nil {
		t.Fatal(err)
	}
	add("s_unknown", protocol.OutcomeComplete, 0, 9)
	if got, want := s.QualityAnalysis().ReworkAverage, 5.0; got != want {
		t.Fatalf("non-stopped tasks should count: got %v, want %v", got, want)
	}
}

func TestQualitySummarySortsEffectSizes(t *testing.T) {
	f := qualityFit{
		Effects: map[string]float64{
			"model\x00a":           .8,
			"model\x00b":           -.2,
			"effort\x00a high":     .3,
			"harness\x00codex":     .1,
			"difficulty\x00normal": .9,
		},
	}
	got := qualitySummary(f)
	want := []protocol.QualityFactor{{Kind: "model", Size: .8}, {Kind: "effort", Size: .3}, {Kind: "harness", Size: .1}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("summary = %+v, want %+v", got, want)
	}
}

func TestQualityUnratedProfileHasNoInventedScore(t *testing.T) {
	s, _ := newTestStore(t)
	if err := s.AddWorkerProfile(protocol.WorkerProfile{Name: "new", Harness: "codex", Model: "m"}); err != nil {
		t.Fatal(err)
	}
	got := s.QualityAnalysis()
	if got.Status != "not enough ratings" || len(got.Profiles) != 1 || got.Profiles[0].Quality != 0 {
		t.Fatalf("empty prediction: %+v", got)
	}
}

// Equal star ratings leave the picker to distinguish the tasks by rework.
func TestQualityChooserReworkAndPreview(t *testing.T) {
	for _, ratings := range []int{0, 2, 6} {
		t.Run(fmt.Sprintf("ratings_%d", ratings), func(t *testing.T) {
			s, _ := newTestStore(t)
			node, _, err := s.CreateNode("test", "linux")
			if err != nil {
				t.Fatal(err)
			}
			caller := ratingCaller(t, s, node, "caller", "gpt-6.1-sol")
			for i, model := range []string{"clean", "rework"} {
				if err := s.AddWorkerProfile(protocol.WorkerProfile{Name: model, Harness: "opencode", Model: model, Effort: "high"}); err != nil {
					t.Fatal(err)
				}
				for j := 0; j < 3; j++ {
					wid := ratingWorker(t, s, node, caller, model+":high", "normal")
					if err := s.RecordWorkerRun(protocol.WorkerRun{NodeID: node, SessionID: wid, Harness: "opencode", Model: model + ":high", Outcome: protocol.OutcomeComplete, EndedAt: s.now().Unix(), Caller: caller, Difficulty: "normal", Fix: i, Continue: i}); err != nil {
						t.Fatal(err)
					}
					if j < ratings/2 {
						if _, err := s.AddRating(protocol.RateFrame{RateRequest: protocol.RateRequest{Worker: wid, Score: 4}, Rater: caller}); err != nil {
							t.Fatal(err)
						}
					}
					if i == 1 {
						if _, err := s.AddRating(protocol.RateFrame{RateRequest: protocol.RateRequest{Worker: wid, Kind: protocol.RateKindDefectLater, Severity: 2}, Rater: caller}); err != nil {
							t.Fatal(err)
						}
					}
				}
			}
			candidates := []protocol.WorkerModelCandidate{{Model: "clean:high", Harness: "opencode"}, {Model: "rework", DefaultEffort: "high", Harness: "opencode"}}
			before := append([]protocol.WorkerModelCandidate(nil), candidates...)
			if err := s.applyQualityWeights(candidates, "normal"); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(before, candidates) {
				t.Fatalf("disabled weighting changed candidates: %+v", candidates)
			}
			if _, err := s.db.Exec(`INSERT INTO owner_settings(key,value,updated_at) VALUES('quality_weighting','{"enabled":true}',0)`); err != nil {
				t.Fatal(err)
			}
			if err := s.refreshQuality(); err != nil {
				t.Fatal(err)
			}
			analysis := s.QualityAnalysis()
			if analysis.Ratings != ratings {
				t.Fatalf("ratings = %d, want %d", analysis.Ratings, ratings)
			}
			if want := protocol.ReworkWeight(3, 1) / 2; analysis.ReworkAverage != want {
				t.Fatalf("rework average = %g, want %g including late defects", analysis.ReworkAverage, want)
			}
			for _, level := range protocol.DifficultyLevels {
				if err := s.applyQualityWeights(candidates, level); err != nil {
					t.Fatal(err)
				}
				if candidates[0].QualityWeight <= candidates[1].QualityWeight {
					t.Errorf("%s: more rework should lower weight: %+v", level, candidates)
				}
				for _, row := range analysis.Profiles {
					i := 0
					if row.Name == "rework" {
						i = 1
					}
					if got, want := row.Weights[level], candidates[i].QualityWeight; got != want {
						t.Errorf("%s/%s: preview = %g, picker = %g", row.Name, level, got, want)
					}
				}
			}
		})
	}
}

func TestQualityReworkRotationWeight(t *testing.T) {
	for _, tt := range []struct {
		name                string
		prediction, average float64
		want                float64
	}{
		{"inverse", 4, 3, .75},
		{"upper bound", 1, 3, 1.5},
		{"lower bound", 10, 3, .5},
		{"zero prediction", 0, 3, 1},
		{"negative prediction", -1, 3, 1},
		{"no rework", 0, 0, 1},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := reworkRotationWeight(tt.prediction, tt.average); got != tt.want {
				t.Fatalf("weight = %g, want %g", got, tt.want)
			}
		})
	}
}
