package protocol

import (
	"math"
	"strings"
	"testing"
)

// quotaWeekly is a weekly window halfway through its week, used by percent.
// A provider that has used more than half its week is ahead of pace, and one
// that has used less is behind it.
func quotaWeekly(provider string, percent float64) QuotaProvider {
	return QuotaProvider{Provider: provider, Windows: []QuotaWindow{{Kind: "weekly", PercentUsed: percent, ResetsAt: 1000 + 7*12*3600}}}
}

// checkPaceTerms holds one row to the picker's own terms: the pace factor is
// the factor the picker used for the room, and the adjusted cost is the
// expected cost over that factor.
func checkPaceTerms(t *testing.T, r PickCandidate) {
	t.Helper()
	if r.PaceRoom == nil {
		t.Fatalf("%s: no pace room, but the provider has a reading", r.Name)
	}
	near(t, r.Name+" pace factor", r.PaceFactor, quotaPaceFactor(*r.PaceRoom, true))
	near(t, r.Name+" adjusted cost", r.AdjustedCost, r.ExpectedCost/r.PaceFactor)
}

// A provider ahead of its quota pace costs more than its run cost says, and one
// behind it costs less. Here the pace bump changes the winner: model a is the
// cheaper run, but its provider is ahead of pace, so b wins on adjusted cost.
func TestPickRowsRecordThePaceBumpThatChangedTheWinner(t *testing.T) {
	tasks := append(costTasks("a", 2.5, 1, repeat(10, 1)...), costTasks("b", 2.5, 1.2, repeat(10, 1)...)...)
	c := pickCandidates("a", "b")
	c[0].Provider, c[1].Provider = "pa", "pb"
	q := &Quotas{GeneratedAt: 1000, Providers: []QuotaProvider{quotaWeekly("pa", 70), quotaWeekly("pb", 30)}}
	in := &PickerInput{D: 2.5, Table: BuildPickerTable(tasks, DefaultPickerSettings())}
	choice, err := chooseFirst(c, "normal", pickScores(map[string]float64{"a": 50, "b": 50}), nil, q, nil, &WorkerRotation{}, in)
	if err != nil {
		t.Fatal(err)
	}
	if len(choice.PickRows) != len(c) {
		t.Fatalf("rows: %d, want one per candidate (%d)", len(choice.PickRows), len(c))
	}
	a, b := choice.PickRows[0], choice.PickRows[1]
	if a.Name != "a" || b.Name != "b" {
		t.Fatalf("rows are not in roster order: %q, %q", a.Name, b.Name)
	}
	if !b.Winner || !b.Chosen || b.Probability != 1 || b.Weight != 0 {
		t.Fatalf("winner row: %+v", b)
	}
	if a.Winner || a.Chosen || a.Probability != 0 {
		t.Fatalf("runner-up row: %+v", a)
	}
	// The raw expected cost picks a; the adjusted cost picks b.
	if a.ExpectedCost >= b.ExpectedCost || a.AdjustedCost <= b.AdjustedCost {
		t.Fatalf("the pace bump should change the winner: expected %.3f vs %.3f, adjusted %.3f vs %.3f", a.ExpectedCost, b.ExpectedCost, a.AdjustedCost, b.AdjustedCost)
	}
	for _, r := range choice.PickRows {
		checkPaceTerms(t, r)
		// The expected cost is the run cost plus the rescue of the failed share.
		near(t, r.Name+" expected cost", r.ExpectedCost, r.CostPerRun+(1-r.PClean)*r.RescueCost)
	}
	if a.PaceRoom == nil || *a.PaceRoom != -20 || b.PaceRoom == nil || *b.PaceRoom != 20 {
		t.Fatalf("pace rooms: a %v, b %v, want -20 and 20", a.PaceRoom, b.PaceRoom)
	}
	if b.Margin == nil {
		t.Fatal("the winner has no margin")
	}
	near(t, "margin", *b.Margin, a.AdjustedCost/b.AdjustedCost)
	if a.Margin != nil {
		t.Fatalf("a runner-up has a margin: %v", *a.Margin)
	}
}

// An exploration pick is made from the thin cells. Its rows say so, record the
// credit before and after the pick, and give each thin cell its rotation
// weight and its share of the rotation. The shares of the thin cells sum to 1.
func TestPickRowsRecordAnExplorationPickAndItsShares(t *testing.T) {
	tasks := append(pickTasks("good", 2.5, repeat(15, 1, .5)...), pickTasks("t1", 2.5, 1)...)
	tasks = append(tasks, pickTasks("t2", 2.5, 1)...)
	c := pickCandidates("good", "t1", "t2")
	c[1].Provider = "pa"
	q := &Quotas{GeneratedAt: 1000, Providers: []QuotaProvider{quotaWeekly("pa", 30)}}
	scores := pickScores(map[string]float64{"good": 51, "t1": 39, "t2": 39})
	in := &PickerInput{D: 2.5, Table: BuildPickerTable(tasks, DefaultPickerSettings())}
	rotation := &WorkerRotation{}
	var rows []PickCandidate
	for i := 0; i < 20 && rows == nil; i++ {
		choice, err := chooseFirst(c, "normal", scores, nil, q, nil, rotation, in)
		if err != nil {
			t.Fatal(err)
		}
		if len(choice.PickRows) > 0 && choice.PickRows[0].Explore {
			rows = choice.PickRows
		}
	}
	if rows == nil {
		t.Fatal("no exploration pick in 20 picks at the default share")
	}
	var thinShare float64
	chosen := 0
	for _, r := range rows {
		if !r.Explore {
			t.Fatalf("%s: the pick is an exploration pick, but its row says otherwise", r.Name)
		}
		if r.ExploreCreditBefore == nil || r.ExploreCreditAfter == nil {
			t.Fatalf("%s: no explore credit", r.Name)
		}
		if r.Chosen {
			chosen++
		}
		if r.Name == "good" {
			if !r.Winner || r.Probability != 0 || r.Weight != 0 {
				t.Fatalf("the cost winner, not in the exploration set: %+v", r)
			}
			continue
		}
		if r.Chosen && r.Probability == 0 {
			t.Fatalf("%s was chosen with no share of the rotation", r.Name)
		}
		thinShare += r.Probability
	}
	if chosen != 1 {
		t.Fatalf("%d rows are chosen, want 1", chosen)
	}
	near(t, "credit before the pick", *rows[0].ExploreCreditBefore, 0.9)
	near(t, "credit after the pick", *rows[0].ExploreCreditAfter, 0)
	near(t, "thin shares", thinShare, 1)
	// Each thin cell has the same benchmark, so the pace factor alone sets their
	// weights: t1 is behind pace by 20 points, t2 has no pace reading.
	near(t, "t1 share", rows[1].Probability, math.E/(math.E+1))
	near(t, "t1 weight over t2 weight", rows[1].Weight/rows[2].Weight, math.E)
}

// The level rule keeps a weight and a share for each candidate it may choose.
// Its rows name the fallback and carry no cell terms.
func TestPickRowsRecordTheLevelRuleFallback(t *testing.T) {
	scores := pickScores(map[string]float64{"a": 39, "b": 39})
	table := BuildPickerTable(nil, DefaultPickerSettings())
	choice, err := chooseFirst(pickCandidates("a", "b"), "normal", scores, nil, nil, nil, &WorkerRotation{}, &PickerInput{D: 4.5, Table: table})
	if err != nil {
		t.Fatal(err)
	}
	if len(choice.PickRows) != 2 {
		t.Fatalf("rows: %+v", choice.PickRows)
	}
	var share float64
	chosen := 0
	for _, r := range choice.PickRows {
		if r.Rule != "level" || !strings.Contains(r.Fallback, "fallback to the level rule") {
			t.Fatalf("row %s: rule %q, fallback %q", r.Name, r.Rule, r.Fallback)
		}
		if r.Weight <= 0 {
			t.Fatalf("row %s has no rotation weight", r.Name)
		}
		if r.Chosen {
			chosen++
			if r.Probability == 0 {
				t.Fatalf("the chosen row %s has no share", r.Name)
			}
		}
		share += r.Probability
	}
	if chosen != 1 {
		t.Fatalf("%d rows are chosen, want 1", chosen)
	}
	near(t, "shares", share, 1)
}

// The note is written from the rows: for every pick the fixtures make, the rows
// give back the note the start stores. A pick with no rows has no note.
func TestPickNoteIsWrittenFromThePickRows(t *testing.T) {
	for _, fx := range pickFixtures() {
		rotation := &WorkerRotation{}
		for i := 1; i <= fx.picks; i++ {
			order, _, err := WorkerPickOrder(fx.candidates, fx.level, fx.scores, nil, fx.quotas, nil, rotation, fx.in)
			if err != nil {
				t.Fatalf("%s pick %d: %v", fx.name, i, err)
			}
			first := order[0]
			if len(first.PickRows) == 0 {
				if first.PickNote != "" {
					t.Fatalf("%s pick %d: a note with no rows: %q", fx.name, i, first.PickNote)
				}
				continue
			}
			if got := PickNote(first.PickRows, DefaultPickerSettings().ExploreShare); got != first.PickNote {
				t.Fatalf("%s pick %d: the rows give %q, the pick says %q", fx.name, i, got, first.PickNote)
			}
		}
	}
}
