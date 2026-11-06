package protocol

import (
	"math"
	"testing"
)

func TestPickerEarlyQuitFailureWeight(t *testing.T) {
	good := workerRotationWeight(WorkerModelCandidate{}, 30, 30, 0, false)
	quit := workerRotationWeight(WorkerModelCandidate{Failed: true}, 30, 30, 0, false)
	if math.Abs(quit-good*.5) > 1e-9 {
		t.Fatalf("weights: %v %v", good, quit)
	}
}

func TestPickerEarlyQuitDifficulty(t *testing.T) {
	scores := []ModelScore{{Match: "a", Score: 51}}
	base := WorkerModelCandidate{Harness: "codex", Model: "a"}
	for _, level := range []string{"hard", "very_complex"} {
		good, err := ChooseWorkerModel([]WorkerModelCandidate{base}, level, scores, nil, nil, nil, &WorkerRotation{})
		if err != nil {
			t.Fatal(err)
		}
		bad := base
		bad.EarlyQuitLevels = map[string]bool{"hard": true}
		quit, err := ChooseWorkerModel([]WorkerModelCandidate{bad}, level, scores, nil, nil, nil, &WorkerRotation{})
		if err != nil {
			t.Fatal(err)
		}
		want := good.Weight
		if level == "hard" {
			want *= .5
		}
		if math.Abs(quit.Weight-want) > 1e-9 {
			t.Fatalf("%s weights %v %v", level, good.Weight, quit.Weight)
		}
	}
}
