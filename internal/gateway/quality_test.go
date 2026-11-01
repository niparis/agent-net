package gateway

import (
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
	if got.ReworkAverage != 3 || len(got.Profiles) != 1 || got.Profiles[0].Ratings != 1 || got.Profiles[0].Runs != 1 {
		t.Fatalf("analysis: %+v", got)
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
	scores := []protocol.ModelScore{{Match: "a", Score: 50}, {Match: "b", Score: 50}}
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
