package protocol

import "testing"

// The cost ranker returns the whole order, not one winner: the first choice is
// the one ChooseWorkerModel makes, and a provider with no room stays in the
// list, where the gate refuses it (decision 0162).
func TestWorkerPickOrderAgreesWithTheChooserAndKeepsBlockedInTheList(t *testing.T) {
	candidates := []WorkerModelCandidate{
		{Name: "terra", Harness: "codex", Model: "gpt-6.1-terra"},
		{Name: "deepseek", Harness: "opencode", Model: "deepseek/deepseek-v4.1-flash"},
		{Name: "go", Harness: "opencode", Model: "opencode-go/deepseek-v4.1-flash"},
	}
	scores := []ModelScore{{Match: "gpt-6.1-terra", Score: 51}, {Match: "deepseek/deepseek-v4.1-flash", Score: 45}, {Match: "opencode-go/deepseek-v4.1-flash", Score: 45}}
	quotas := &Quotas{Providers: []QuotaProvider{{Provider: "opencode-go", Windows: []QuotaWindow{{Kind: "monthly", Exhausted: true}}}}}
	want, err := ChooseWorkerModel(candidates, "hard", scores, nil, quotas, nil, &WorkerRotation{})
	if err != nil {
		t.Fatal(err)
	}
	got, _, err := WorkerPickOrder(candidates, "hard", scores, nil, quotas, nil, &WorkerRotation{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != len(candidates) {
		t.Fatalf("order has %d of %d candidates: %+v", len(got), len(candidates), got)
	}
	if got[0].Name != want.Name {
		t.Fatalf("first = %q, want the chooser's %q", got[0].Name, want.Name)
	}
	found := false
	for _, c := range got {
		if c.Name == "go" {
			found = true
		}
	}
	if !found {
		t.Fatalf("the ranker dropped the blocked candidate: %+v", got)
	}
}

// The cell rule orders every candidate it allows, first one first. The order
// starts with the same pick the single-winner chooser makes.
func TestWorkerPickOrderListsEveryCellCandidate(t *testing.T) {
	candidates := []WorkerModelCandidate{
		{Name: "A", Harness: "pi", Model: "A"},
		{Name: "B", Harness: "pi", Model: "B"},
	}
	scores := []ModelScore{{Match: "A", Score: 51}, {Match: "B", Score: 39}}
	in := &PickerInput{D: 2.5, Table: BuildPickerTable(shrinkFixture(), DefaultPickerSettings())}
	want, err := ChooseWorkerModel(candidates, "normal", scores, nil, nil, nil, &WorkerRotation{}, in)
	if err != nil {
		t.Fatal(err)
	}
	got, _, err := WorkerPickOrder(candidates, "normal", scores, nil, nil, nil, &WorkerRotation{}, in)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("order: %+v", got)
	}
	if got[0].Name != want.Name {
		t.Fatalf("first = %q, want the chooser's %q", got[0].Name, want.Name)
	}
	if got[0].PickNote == "" || got[0].Why == "" {
		t.Fatalf("the first choice lost its note: %+v", got[0])
	}
}
