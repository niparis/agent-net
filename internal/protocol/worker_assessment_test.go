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

func TestWorkerLevel(t *testing.T) {
	for _, tc := range []struct {
		name string
		a    *WorkerAssessment
		want string
	}{
		{"missing", nil, "normal"},
		{"judge label", &WorkerAssessment{Label: "trivial", PickerLabel: "trivial"}, "trivial"},
		{"failed", &WorkerAssessment{Label: "normal", Error: "judge failed"}, "normal"},
		{"low confidence", &WorkerAssessment{Label: "hard", Confidence: "low"}, "normal"},
		{"empty label", &WorkerAssessment{}, "normal"},
		{"unknown label", &WorkerAssessment{Label: "huge"}, "normal"},
	} {
		if got := WorkerLevel(tc.a); got != tc.want {
			t.Errorf("%s: got %s, want %s", tc.name, got, tc.want)
		}
	}
}

func TestWorkerOwnerLine(t *testing.T) {
	ok := &WorkerAssessment{Reasoning: 2, Scope: 1, Coupling: 2, Uncertainty: 2, Verification: 2, Risk: 1, D: 1.75, Label: "simple", Confidence: "high"}
	if got := WorkerOwnerLine(ok, "gpt-6-luna:xhigh"); got != "Tell your user: worker on gpt-6-luna:xhigh, difficulty simple (D 1.75; R2 S1 C2 U2 V2 K1)." {
		t.Errorf("judged: %q", got)
	}
	failed := &WorkerAssessment{Label: "normal", Error: "the judge timed out after 30s"}
	if got := WorkerOwnerLine(failed, "m"); got != "Tell your user: worker on m, difficulty normal (no assessment: the judge timed out after 30s)." {
		t.Errorf("failed: %q", got)
	}
	low := *ok
	low.Confidence = "low"
	if got := WorkerOwnerLine(&low, "m"); got != "Tell your user: worker on m, difficulty normal (the judge said simple, D 1.75, with low confidence)." {
		t.Errorf("low confidence: %q", got)
	}
	if got := WorkerOwnerLine(nil, "m"); got != "" {
		t.Errorf("continued worker: %q", got)
	}
}
