package gateway

import (
	"database/sql"
	"math"
	"testing"

	"github.com/niparis/agent-net/internal/model"
	"github.com/niparis/agent-net/internal/protocol"
)

// pickRowsForTest is a pick of two candidates: a cell row that won, and a
// level-rule row with no cell terms.
func pickRowsForTest() []protocol.PickCandidate {
	room, margin := 10.0, 1.5
	return []protocol.PickCandidate{
		{Name: "fast", Harness: "pi", Provider: "pa", Model: "fast", Effort: "low", Rule: "cell", BucketLo: 2.33, BucketHi: 2.67,
			Cell: protocol.PickerCell{Score: .8, Lo: .7, Hi: .9, N: 12}, Allowed: true, Qualified: true, Best: true, Chosen: true, Winner: true,
			LeftOutReason: "", CostKnown: true, CostRecorded: true, CostPerRun: 1, PClean: 1, RescueCost: 1, ExpectedCost: 1,
			PaceRoom: &room, PaceFactor: 1.6, AdjustedCost: .6, Weight: 0, Probability: 1, Margin: &margin},
		{Name: "slow", Harness: "pi", Model: "slow", Rule: "level", BucketLo: 2.33, BucketHi: 2.67, Fallback: "fallback to the level rule (none allowed)",
			Allowed: true, Weight: 2, Probability: 0, PaceFactor: 1},
	}
}

// Each judge result stores its pick once. A second write of the same run is a
// no-op, and a result that has no pick adds no rows.
func TestPickCandidatesAreStoredWithTheJudgeResultOnce(t *testing.T) {
	s, _ := newTestStore(t)
	a := protocol.WorkerAssessment{RunID: "run-pick", SessionID: "session-pick", StartedAt: 1e9, AssessedAt: 2, D: 2.5, Label: "normal",
		PickerNote: "bucket 2.33-2.67: fast 0.80 [0.70-0.90] n12, best", PickRows: pickRowsForTest()}
	for i := 0; i < 2; i++ {
		if err := s.RecordWorkerAssessment(a); err != nil {
			t.Fatal(err)
		}
	}
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM pick_candidates WHERE run_id=?`, a.RunID).Scan(&n); err != nil || n != 2 {
		t.Fatalf("rows for the run: %d, %v; want 2", n, err)
	}

	var name, rule string
	var cellScore, expected, paceRoom, margin, adjusted sql.NullFloat64
	var winner, probability, chosen float64
	err := s.db.QueryRow(`SELECT name, rule, cell_score, expected_cost, pace_room, adjusted_cost, is_winner, is_chosen, probability, margin
		FROM pick_candidates WHERE run_id=? AND pos=0`, a.RunID).Scan(&name, &rule, &cellScore, &expected, &paceRoom, &adjusted, &winner, &chosen, &probability, &margin)
	if err != nil {
		t.Fatal(err)
	}
	if name != "fast" || rule != "cell" || cellScore.Float64 != .8 || expected.Float64 != 1 || paceRoom.Float64 != 10 || adjusted.Float64 != .6 ||
		winner != 1 || chosen != 1 || probability != 1 || margin.Float64 != 1.5 {
		t.Fatalf("winner row: name %q rule %q cell %v expected %v room %v adjusted %v winner %v chosen %v probability %v margin %v",
			name, rule, cellScore, expected, paceRoom, adjusted, winner, chosen, probability, margin)
	}

	// The level rule has no cell and no cost, and the pace room is unknown.
	var levelExpected, levelCell, levelRoom, levelMargin sql.NullFloat64
	var levelWeight float64
	err = s.db.QueryRow(`SELECT expected_cost, cell_score, pace_room, margin, weight FROM pick_candidates WHERE run_id=? AND pos=1`, a.RunID).
		Scan(&levelExpected, &levelCell, &levelRoom, &levelMargin, &levelWeight)
	if err != nil {
		t.Fatal(err)
	}
	if levelExpected.Valid || levelCell.Valid || levelRoom.Valid || levelMargin.Valid || levelWeight != 2 {
		t.Fatalf("level row: expected %v, cell %v, room %v, margin %v, weight %v; want NULL, NULL, NULL, NULL, 2",
			levelExpected, levelCell, levelRoom, levelMargin, levelWeight)
	}

	// A judge result with no pick is stored without rows.
	if err := s.RecordWorkerAssessment(protocol.WorkerAssessment{RunID: "run-plain", SessionID: "session-plain", StartedAt: 2e9, AssessedAt: 3, D: 1}); err != nil {
		t.Fatal(err)
	}
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM pick_candidates WHERE run_id='run-plain'`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("rows for a result with no pick: %d, %v", n, err)
	}
}

// A candidate the gate skipped carries the gate's reason in its row. Other rows
// keep an empty reason.
func TestPickCandidatesCarryTheGateSkipReason(t *testing.T) {
	rows := pickRowsForTest()
	skips := []model.Skip{{Candidate: protocol.Candidate{Harness: "pi", Model: "slow"}, Reason: "quota floor"}}
	out := markPickSkips(rows, skips)
	if out[1].SkippedReason != "quota floor" {
		t.Fatalf("skipped row: %+v", out[1])
	}
	if out[0].SkippedReason != "" {
		t.Fatalf("a row the gate did not skip has a reason: %+v", out[0])
	}
	if rows[1].SkippedReason != "" {
		t.Fatal("markPickSkips changed its input")
	}
}

// Choose hands the rows of its pick to the caller. The candidate whose provider
// is out of quota is refused by the gate, and its row says why. The row of the
// candidate the pick chose is marked, and the note is written from the rows.
func TestChooseHandsTheRowsOfThePick(t *testing.T) {
	s, _ := newTestStore(t)
	g := &Gateway{store: s}
	node := &protocol.NodeView{NodeID: "n_test", Hostname: "h", Harnesses: []protocol.HarnessInfo{
		{Name: "codex", SignedIn: "yes"}, {Name: "opencode", SignedIn: "yes"}}}
	candidates := []protocol.WorkerModelCandidate{
		{Name: "sol low", Harness: "codex", Model: "gpt-6.1-sol:low"},
		{Name: "blocked", Harness: "opencode", Model: "opencode-go/deepseek-v4.1-flash", Provider: "opencode-go"},
	}
	scores := []protocol.ModelScore{{Match: "gpt-6.1-sol", Score: 45}, {Match: "opencode-go/deepseek-v4.1-flash", Score: 45}}
	quotas := &protocol.Quotas{Providers: []protocol.QuotaProvider{{Provider: "opencode-go", Windows: []protocol.QuotaWindow{{Kind: "monthly", Exhausted: true}}}}}
	facts, err := g.chooseFacts(node, candidates, quotas, protocol.WorkerFloors{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	rotation := protocol.WorkerRotation{}
	got, err := g.Choose(ChooseRequest{Purpose: model.PurposeWorkerAutomatic, Node: node, Candidates: candidates, Difficulty: "normal", D: 2.5,
		Scores: scores, Quotas: quotas, Facts: &facts, Rotation: &rotation})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Pick) != len(candidates) {
		t.Fatalf("rows: %+v, want one per candidate", got.Pick)
	}
	chosen := 0
	for _, r := range got.Pick {
		if r.Chosen {
			chosen++
		}
		if r.Name == "blocked" && r.SkippedReason == "" {
			t.Fatalf("the gate refused the blocked candidate, its row has no reason: %+v", r)
		}
		if r.Name != "blocked" && r.SkippedReason != "" {
			t.Fatalf("row %s has a skip reason it did not get: %q", r.Name, r.SkippedReason)
		}
	}
	if chosen != 1 {
		t.Fatalf("%d rows are chosen, want 1", chosen)
	}
	if want := protocol.PickNote(got.Pick, protocol.DefaultPickerSettings().ExploreShare); got.Choice.PickNote != want {
		t.Fatalf("note %q, the rows give %q", got.Choice.PickNote, want)
	}
}

func TestPickCandidatesStoreUnboundedCost(t *testing.T) {
	s, _ := newTestStore(t)
	row := pickRowsForTest()[0]
	row.CostUnbounded = true
	row.ExpectedCost = 0
	row.AdjustedCost = 0
	a := protocol.WorkerAssessment{RunID: "unbounded", SessionID: "s", StartedAt: 1, AssessedAt: 2, D: 2.5, PickRows: []protocol.PickCandidate{row}}
	if err := s.RecordWorkerAssessment(a); err != nil {
		t.Fatal(err)
	}
	var expected, adjusted float64
	if err := s.db.QueryRow(`SELECT expected_cost, adjusted_cost FROM pick_candidates WHERE run_id=?`, a.RunID).Scan(&expected, &adjusted); err != nil {
		t.Fatal(err)
	}
	if !math.IsInf(expected, 1) || !math.IsInf(adjusted, 1) {
		t.Fatalf("cost %v adjusted %v", expected, adjusted)
	}
}
