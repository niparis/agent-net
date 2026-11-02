package difficulty

import (
	"reflect"
	"testing"
)

func TestShadowRulesVersionAndRelabel(t *testing.T) {
	stored := Assessment{Scores: Scores{Reasoning: 2, Scope: 1, Coupling: 1, Uncertainty: 1, Verification: 1, Risk: 1}, Label: "hard", D: 4.0, RulesVersion: "old"}
	updated := Relabel(stored)
	if updated.Label != "trivial" || updated.D < 1.199 || updated.D > 1.201 || updated.RulesVersion != "2026-11-02.1" {
		t.Fatalf("relabel: %+v", updated)
	}
	if stored.Label != "hard" || stored.RulesVersion != "old" {
		t.Fatal("changed stored row")
	}
}

func TestShadowRulesVersionPinsParameters(t *testing.T) {
	if RulesVersion != "2026-11-02.1" {
		t.Fatal("update this pinned test with the new rules version")
	}
	if !reflect.DeepEqual(scoreWeights, []float64{.20, .10, .20, .15, .15, .20}) || !reflect.DeepEqual(labelCutPoints, []float64{1.5, 2.25, 3, 3.75}) || !reflect.DeepEqual(labelNames, []string{"trivial", "simple", "normal", "hard", "very_complex"}) {
		t.Fatal("weights or cut points changed: update RulesVersion and this test")
	}
}
