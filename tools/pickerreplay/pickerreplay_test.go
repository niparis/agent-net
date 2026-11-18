package main

import (
	"crypto/sha256"
	"database/sql"
	"math"
	"os"
	"path/filepath"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
	_ "modernc.org/sqlite"
)

func near(t *testing.T, what string, got, want float64) {
	t.Helper()
	if math.Abs(got-want) > 1e-6 {
		t.Fatalf("%s: got %.6f, want %.6f", what, got, want)
	}
}

func TestOptionAScoreCountsEachFailureOnce(t *testing.T) {
	cases := []struct {
		name string
		task Task
		want float64
	}{
		{"incomplete work is 0", Task{Complete: false}, 0},
		{"incomplete but reusable keeps its score", Task{Complete: false, Reusable: true}, 1},
		{"a fix round is free", Task{Complete: true, FixRounds: 3}, 1},
		{"a failed test costs .2", Task{Complete: true, FailedTest: 1}, .8},
		{"a failed review costs .3", Task{Complete: true, FailedReview: 1}, .7},
		{"a severity 1 defect costs .25, not .5", Task{Complete: true, Defects: 1, DefectSeverity: 1}, .75},
		{"a severity 3 defect costs .75", Task{Complete: true, Defects: 1, DefectSeverity: 3}, .25},
		{"failures add and never go below 0", Task{Complete: true, FailedReview: 1, Defects: 2, DefectSeverity: 8}, 0},
	}
	for _, c := range cases {
		if got := OptionAScore(c.task); math.Abs(got-c.want) > 1e-9 {
			t.Errorf("%s: got %.2f, want %.2f", c.name, got, c.want)
		}
	}
}

func ev(name string, quality float64, adjusted, cost, pass, expected float64) *candEval {
	return &candEval{
		row:       CandRow{Name: name, BucketLo: 2.0},
		cell:      protocol.PickerCell{Score: quality, N: 10},
		adjusted:  adjusted,
		cost:      cost,
		pass:      pass,
		expected:  expected,
		qualified: true,
	}
}

func TestPickTodayTakesTheLowestExpectedCost(t *testing.T) {
	cheap := ev("cheap", .61, .65, .47, .41, .65)
	strong := ev("strong", .77, 1.70, 1.04, .73, 1.06)
	if got := pickToday([]*candEval{strong, cheap}); got != cheap {
		t.Fatalf("today's rule picked %s, want cheap", got.row.Name)
	}
}

func TestPickOptionAWeighsQualityAgainstCost(t *testing.T) {
	cheap := ev("cheap", .61, .65, .47, .41, .65)
	strong := ev("strong", .77, 1.70, 1.04, .73, 1.06)
	es := []*candEval{strong, cheap}
	if got := pickOptionA(es, 0); got != cheap {
		t.Fatalf("alpha 0 picked %s, want the cheapest", got.row.Name)
	}
	// The gap is .16, so a large alpha must lift the stronger model over a
	// cost difference of 1.70/.65 = 2.6.
	if got := pickOptionA(es, 20); got != strong {
		t.Fatalf("alpha 20 picked %s, want the stronger model", got.row.Name)
	}
}

// fixture writes a small but complete store copy and returns its path.
func fixture(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "snapshot.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	schema := `
CREATE TABLE worker_runs (id INTEGER PRIMARY KEY, session_id TEXT, task_id TEXT, model TEXT, harness TEXT,
  outcome TEXT, ended_at INTEGER, fix_count INTEGER);
CREATE TABLE worker_assessments (session_id TEXT, run_id TEXT, started_at INTEGER, assessed_at INTEGER, d REAL,
  label TEXT, confidence TEXT, reasoning REAL, scope REAL, coupling REAL, uncertainty REAL, verification REAL, risk REAL);
CREATE TABLE worker_defects (id INTEGER PRIMARY KEY, session_id TEXT, severity INTEGER, at INTEGER, moved_at INTEGER);
CREATE TABLE worker_defect_auto (defect_id INTEGER PRIMARY KEY, kind TEXT);
CREATE TABLE delivery_changes (id TEXT, origin TEXT, state TEXT, project TEXT, updated_at INTEGER);
CREATE TABLE delivery_evidence (change_id TEXT, step TEXT, result TEXT, at INTEGER);
CREATE TABLE worker_usage (session_id TEXT PRIMARY KEY, usage TEXT, updated_at INTEGER);
CREATE TABLE worker_starts (session_id TEXT, project TEXT);
CREATE TABLE owner_settings (key TEXT PRIMARY KEY, value TEXT, updated_at INTEGER);
CREATE TABLE model_prices (id TEXT PRIMARY KEY, prompt REAL, completion REAL, cache_read REAL, cache_write REAL, updated_at INTEGER);
CREATE TABLE pick_candidates (run_id TEXT, pos INTEGER, name TEXT, harness TEXT, provider TEXT, model TEXT, effort TEXT,
  rule TEXT, bucket_lo REAL, bucket_hi REAL, cell_score REAL, cell_low REAL, cell_high REAL, cell_n INTEGER,
  is_allowed INTEGER, is_qualified INTEGER, is_prior INTEGER, is_best INTEGER, is_chosen INTEGER, is_winner INTEGER,
  left_out_reason TEXT NOT NULL DEFAULT '', cost_per_run REAL, p_clean REAL, rescue_cost REAL, expected_cost REAL, pace_room REAL,
  pace_factor REAL, adjusted_cost REAL);
`
	if _, err := db.Exec(schema); err != nil {
		t.Fatal(err)
	}
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := db.Exec(q, args...); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	// Six finished tasks: three by the cheap model, two of them clean, and
	// three clean ones by the dear model.
	tasks := []struct {
		session, model, outcome string
		ended                   int64
	}{
		{"s_d1", "opencode-go/deepseek-v4.1-flash", "complete", 1793000000},
		{"s_d2", "opencode-go/deepseek-v4.1-flash", "complete", 1793000001},
		{"s_d3", "opencode-go/deepseek-v4.1-flash", "failed", 1793000002},
		{"s_b1", "big-model", "complete", 1793000003},
		{"s_b2", "big-model", "complete", 1793000004},
		{"s_b3", "big-model", "complete", 1793000005},
	}
	for _, task := range tasks {
		exec(`INSERT INTO worker_runs(session_id,task_id,model,harness,outcome,ended_at,fix_count) VALUES(?,?,?,?,?,?,0)`,
			task.session, "", task.model, "opencode", task.outcome, task.ended)
		exec(`INSERT INTO worker_assessments(session_id,run_id,started_at,assessed_at,d,label,confidence,reasoning,scope,coupling,uncertainty,verification,risk)
			VALUES(?,?,?,?,2.2,'normal','high',3,3,3,3,3,3)`, task.session, "k_"+task.session, task.ended*1e9, task.ended)
		exec(`INSERT INTO worker_usage(session_id,usage,updated_at) VALUES(?, '{"in":1000000}', 0)`, task.session)
	}
	exec(`INSERT INTO model_prices(id,prompt,completion,cache_read,cache_write,updated_at) VALUES('test/deepseek-v4.1-flash',0.000001,0,0,0,0)`)
	exec(`INSERT INTO model_prices(id,prompt,completion,cache_read,cache_write,updated_at) VALUES('test/big-model',0.000005,0,0,0,0)`)

	pick := struct {
		at int64
	}{1794000000}
	exec(`INSERT INTO worker_assessments(session_id,run_id,started_at,assessed_at,d,label,confidence,reasoning,scope,coupling,uncertainty,verification,risk)
		VALUES('s_pick','k_pick',?,?,2.2,'normal','high',3,3,3,3,3,3)`, pick.at*1e9, pick.at)
	two := []struct {
		name, model string
		chosen      int
	}{
		{"deepseek", "opencode-go/deepseek-v4.1-flash", 1},
		{"big", "big-model", 0},
	}
	for pos, c := range two {
		exec(`INSERT INTO pick_candidates(run_id,pos,name,harness,provider,model,effort,rule,bucket_lo,bucket_hi,
			is_allowed,is_qualified,is_prior,is_best,is_chosen,is_winner,pace_factor)
			VALUES('k_pick',?,?,'opencode','',?,'','cell',2.0,2.3333,1,1,0,0,?,0,1)`, pos, c.name, c.model, c.chosen)
	}
	return path
}

func TestReplayReadsSnapshotAndMatchesTheRecordedPick(t *testing.T) {
	path := fixture(t)
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	s, err := OpenSnapshot(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Tasks) != 6 {
		t.Fatalf("tasks: got %d, want 6", len(s.Tasks))
	}
	if len(s.Picks) != 1 {
		t.Fatalf("picks: got %d, want 1", len(s.Picks))
	}
	r := RunReplay(s, []float64{0, 3}, floorSets())
	if r.Check.Picks != 1 || r.Check.Match != 1 {
		t.Fatalf("replay check: %d/%d, want 1/1", r.Check.Match, r.Check.Picks)
	}
	if r.Today.Models["deepseek"] != 1 {
		t.Fatalf("today's replay picked %v, want deepseek", r.Today.Models)
	}
	if r.V2.Models["deepseek"] != 1 || r.OptionsA[0].Models["deepseek"] != 1 {
		t.Fatalf("option A at alpha 0 picked %v, want deepseek", r.OptionsA[0].Models)
	}
	selected, err := parseFloorSets("B")
	if err != nil {
		t.Fatal(err)
	}
	filtered := RunReplay(s, []float64{0}, selected)
	if len(filtered.Floors) != 1 || filtered.Floors[0].Name != "floor B" {
		t.Fatalf("selected floors: %+v", filtered.Floors)
	}

	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if sha256.Sum256(before) != sha256.Sum256(after) {
		t.Fatal("the snapshot changed; the replay must open it read only")
	}
}

func TestTasksAtLeavesOutWhatDidNotExistYet(t *testing.T) {
	path := fixture(t)
	s, err := OpenSnapshot(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := len(s.TasksAt(1793000000)); got != 1 {
		t.Fatalf("at the first end: got %d tasks, want 1", got)
	}
	if got := len(s.TasksAt(1794000000)); got != 6 {
		t.Fatalf("at the pick: got %d tasks, want 6", got)
	}
}

func TestQualityV2FloorsAndFallback(t *testing.T) {
	cheap := ev("cheap", .6, .5, .5, .4, .5)
	strong := ev("strong", .8, 1, 1, .8, 1)
	cheap.rank, strong.rank = .5, 1
	if got := pickV2([]*candEval{cheap, strong}, .7); got != strong {
		t.Fatalf("floor picked %+v", got)
	}
	if got := pickV2([]*candEval{cheap, strong}, .9); got != nil {
		t.Fatalf("expected fallback, got %+v", got)
	}
	if got := pickV2([]*candEval{cheap, strong}, 0); got != cheap {
		t.Fatalf("no floor picked %+v", got)
	}
}

func TestDifficultyFloorSets(t *testing.T) {
	if levelForD(1.49) != "trivial" || levelForD(1.5) != "simple" || levelForD(2.25) != "normal" || levelForD(3) != "hard" || levelForD(3.75) != "very_complex" {
		t.Fatal("wrong difficulty boundaries")
	}
	if floorSets()[0].floor("hard") != .7 || floorSets()[1].floor("simple") != .55 || floorSets()[2].floor("normal") != .5 {
		t.Fatal("wrong floor set")
	}
}

func TestFloorFallbackUsesToday(t *testing.T) {
	today := ev("today", .5, 2, 2, .2, 2)
	v2 := ev("v2", .8, 1, 1, .6, 1)
	v2.rank = 1
	got, fallback := pickWithFloor([]*candEval{v2}, today, .7)
	if got != today || !fallback {
		t.Fatalf("got %v, fallback %t", got, fallback)
	}
	got, fallback = pickWithFloor([]*candEval{v2}, today, .5)
	if got != v2 || fallback {
		t.Fatalf("got %v, fallback %t", got, fallback)
	}
}

func TestFloorFlagSelectsSets(t *testing.T) {
	selected, err := parseFloorSets("C,A")
	if err != nil || len(selected) != 2 || selected[0].name != "floor C" || selected[1].name != "floor A" {
		t.Fatalf("sets: %+v, %v", selected, err)
	}
	if _, err := parseFloorSets("D"); err == nil {
		t.Fatal("unknown floor set accepted")
	}
	if sets, err := parseFloorSets(""); err != nil || len(sets) != 0 {
		t.Fatalf("empty sets: %+v, %v", sets, err)
	}
}

func TestYardstickIsRunCostOverCleanShare(t *testing.T) {
	e := ev("sol", .77, 1.70, 1.04, .73, 1.06)
	got := yardstick(e, 2)
	near(t, "run cost", got.Run, 1.04)
	near(t, "clean p", got.Clean, .73)
	near(t, "cost per accepted result", got.PerAccepted, 1.04/.73)
	near(t, "pace-adjusted", got.Adjusted, 1.04/.73/2)
}

func TestEveryRowPricesThePickTheSameWay(t *testing.T) {
	s, err := OpenSnapshot(fixture(t))
	if err != nil {
		t.Fatal(err)
	}
	r := RunReplay(s, []float64{0}, nil)
	today, v2 := r.Today.averages(), r.V2.averages()
	near(t, "run cost, today against V2", today.Run, v2.Run)
	near(t, "clean p, today against V2", today.P, v2.P)
	near(t, "cost per accepted, today against V2", today.PerAcc, v2.PerAcc)
	near(t, "cost per accepted is run cost over clean p", today.PerAcc, today.Run/today.P)
}

// An exhausted provider does not compete in any method, though the replay
// check still reproduces the pick the gateway recorded.
func TestReplayLeavesOutAnExhaustedProvider(t *testing.T) {
	s, err := OpenSnapshot(fixture(t))
	if err != nil {
		t.Fatal(err)
	}
	s.Exhausted = map[string]bool{"opencode-go": true}
	r := RunReplay(s, nil, nil)
	if r.Check.Match != 1 {
		t.Fatalf("the check must read the recorded roster: %d/%d", r.Check.Match, r.Check.Picks)
	}
	for _, m := range []*summary{r.Today, r.V2, r.V2Ceiling} {
		if m.Models["deepseek"] != 0 || m.Models["big"] != 1 {
			t.Errorf("%s picked %v, want big only", m.Name, m.Models)
		}
	}
	gated, left := s.gate(s.Picks[0])
	if len(gated.Rows) != 1 || len(left) != 1 || left[0] != "deepseek" {
		t.Fatalf("gate kept %d rows, left out %v", len(gated.Rows), left)
	}
}

// With no proven candidate allowed, a pick is counted by level and uses
// today's pick for its cost.
func TestReplayCountsPicksWithNoCandidateLeft(t *testing.T) {
	s, err := OpenSnapshot(fixture(t))
	if err != nil {
		t.Fatal(err)
	}
	s.Exhausted = map[string]bool{"opencode-go": true}
	s.Picks[0].Rows = s.Picks[0].Rows[:1] // only the exhausted candidate is left in the roster
	r := RunReplay(s, nil, nil)
	if r.V2.PicksByLevel["simple"] != 1 || r.V2.NoQualified["simple"] != 1 || r.V2.NoneAllowed["simple"] != 1 {
		t.Fatalf("V2 counts: %+v %+v %+v", r.V2.PicksByLevel, r.V2.NoQualified, r.V2.NoneAllowed)
	}
}

// The live picker never starts on a protected provider, so the replay drops
// its candidates from every method, matching them as the gateway does.
func TestReplayLeavesOutAProtectedProvider(t *testing.T) {
	s, err := OpenSnapshot(fixture(t))
	if err != nil {
		t.Fatal(err)
	}
	s.Protected = map[string]bool{"opencode-go": true}
	gated, left := s.gate(s.Picks[0])
	if len(gated.Rows) != 1 || len(left) != 1 || left[0] != "deepseek" {
		t.Fatalf("gate kept %d rows, left out %v", len(gated.Rows), left)
	}
	r := RunReplay(s, nil, nil)
	if r.V2.Models["deepseek"] != 0 || r.V2.Models["big"] != 1 {
		t.Fatalf("V2 picked %v, want big only", r.V2.Models)
	}
}
