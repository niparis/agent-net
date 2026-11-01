package protocol

import "testing"

// The owner set the headache costs on 2026-11-01.
func TestQualityHeadacheMinutes(t *testing.T) {
	if QualityContinueMinutes != 2 || QualityFixMinutes != 10 {
		t.Fatalf("task minutes: continue %d, fix %d", QualityContinueMinutes, QualityFixMinutes)
	}
	want := map[int]float64{1: 10, 2: 30, 3: 60, 4: 180}
	for severity, minutes := range want {
		if got := DefectMinutes(severity); got != minutes {
			t.Errorf("severity %d = %v minutes, want %v", severity, got, minutes)
		}
	}
	if got := DefectMinutes(0); got != 0 {
		t.Errorf("severity 0 = %v minutes, want 0", got)
	}
	if got := DefectMinutes(5); got != 0 {
		t.Errorf("severity 5 = %v minutes, want 0", got)
	}
}
