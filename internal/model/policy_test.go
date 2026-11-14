package model

import "testing"

// PolicyFor is the one table that says which optional steps each caller gets.
// Each case pins one row of the table in decision 0162.
func TestPolicyForPinsEveryRow(t *testing.T) {
	all := Checks{Floor: true, Block90: true, Burn: true, Cooling: true}
	tests := []struct {
		purpose Purpose
		want    Policy
	}{
		{PurposeWorkerAutomatic, Policy{Ranker: RankCost, Checks: all, Pace: true, ProtectedLast: true, Rotation: true}},
		{PurposeWorkerNamed, Policy{Ranker: RankFixed, Checks: all}},
		{PurposeOwnerNamed, Policy{Ranker: RankFixed}},
		{PurposeContinueOrResume, Policy{Ranker: RankSame}},
		{PurposeDifficultyJudge, Policy{Ranker: RankFixed, Checks: all, Pace: true, ProtectedLast: true}},
		{PurposeRole, Policy{Ranker: RankFixed, Checks: Checks{Cooling: true}}},
	}
	for _, tt := range tests {
		if got := PolicyFor(tt.purpose); got != tt.want {
			t.Errorf("%s: got %+v, want %+v", tt.purpose, got, tt.want)
		}
	}
}

// A preview is the row of the caller it previews, with "decide, do not start".
func TestPolicyPreviewIsThePreviewedRow(t *testing.T) {
	want := PolicyFor(PurposeWorkerAutomatic)
	want.DecideOnly = true
	if got := PolicyFor(PurposePreview); got != want {
		t.Fatalf("preview: got %+v, want %+v", got, want)
	}
}

// An unknown purpose must not quietly get the automatic row: a new route has
// to add its row before it can choose a model.
func TestPolicyUnknownPurposeHasNoChecks(t *testing.T) {
	if got := PolicyFor(Purpose("not-a-purpose")); got != (Policy{}) {
		t.Fatalf("unknown purpose: %+v", got)
	}
}
