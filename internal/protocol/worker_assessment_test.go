package protocol

import (
	"strings"
	"testing"
)

func TestWorkerAssessmentNotice(t *testing.T) {
	a := WorkerAssessment{Reasoning: 2, Scope: 1, Coupling: 1, Uncertainty: 1, Verification: 1, Risk: 1, D: 1.2, Label: "trivial", PickerLabel: "normal", Confidence: "high", Bottleneck: "risk"}
	got := WorkerAssessmentNotice("s_test", a)
	for _, s := range []string{"status: difficulty", "difficulty: R2 S1 C1 U1 V1 K1 · D 1.20 · trivial (picker used: normal)", "bottleneck: risk", "confidence: high"} {
		if !strings.Contains(got, s) {
			t.Errorf("missing %q in %s", s, got)
		}
	}
	a.Error = "judge failed"
	if got := WorkerAssessmentNotice("s_test", a); !strings.Contains(got, "difficulty: no assessment (judge failed)") {
		t.Fatal(got)
	}
}
