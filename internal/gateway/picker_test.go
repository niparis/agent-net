package gateway

import (
	"encoding/json"
	"fmt"
	"math"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

// pickerRun records one finished run of a task.
func pickerRun(t testing.TB, s *Store, node, session, harness, model, outcome string, fix int, ended int64) {
	t.Helper()
	if err := s.RecordWorkerRun(protocol.WorkerRun{NodeID: node, SessionID: session, Harness: harness, Model: model, Outcome: outcome, StartedAt: ended - 60, EndedAt: ended, Fix: fix}); err != nil {
		t.Fatal(err)
	}
}

var assessmentSeq int

// pickerAssessment records a judge result for a session.
func pickerAssessment(t testing.TB, s *Store, session string, d float64) {
	t.Helper()
	assessmentSeq++
	if err := s.RecordWorkerAssessment(protocol.WorkerAssessment{RunID: fmt.Sprintf("run-%d", assessmentSeq), SessionID: session, StartedAt: int64(assessmentSeq) * 1e9, AssessedAt: int64(assessmentSeq), D: d, Label: "normal"}); err != nil {
		t.Fatal(err)
	}
}

func pickerTasksByKey(t testing.TB, s *Store) map[string][]protocol.PickerTask {
	t.Helper()
	tasks, err := s.pickerTasks()
	if err != nil {
		t.Fatal(err)
	}
	out := map[string][]protocol.PickerTask{}
	for _, task := range tasks {
		out[task.Key] = append(out[task.Key], task)
	}
	return out
}

func TestPickerTaskScoresFromEveryDataSource(t *testing.T) {
	r := newDeliveryRig(t)
	s := r.s
	rev := func() uint64 { return s.pickerTable().Revision }
	// The worker's run: complete after one fix round, judged D 2.5.
	before := rev()
	pickerRun(t, s, r.a, r.worker, "claude", "claude-opus-5-5:high", protocol.OutcomeComplete, 1, 1000)
	if rev() <= before {
		t.Fatal("a recorded run did not refresh the picker table")
	}
	// No judge result yet: the task has no D, so it is not in the table.
	if got := pickerTasksByKey(t, s); len(got) != 0 {
		t.Fatalf("task without D: %+v", got)
	}
	before = rev()
	pickerAssessment(t, s, r.worker, 2.5)
	if rev() <= before {
		t.Fatal("a judge result did not refresh the picker table")
	}
	got := pickerTasksByKey(t, s)["claude-opus-5-5:high"]
	if len(got) != 1 || got[0].D != 2.5 || math.Abs(got[0].Score-.75) > 1e-9 {
		t.Fatalf("after the fix round: %+v", got)
	}
	// A fix round alone is not dirty: its cost is already in the run's tokens.
	if got[0].Dirty {
		t.Fatalf("a task with only a fix round is dirty: %+v", got)
	}
	// A failed accept step on the change this worker opened costs a quarter.
	id := r.submit(r.a, r.workerSubmit("agent-net/w_1", commitA)).Change.Change.ID
	before = rev()
	r.mustReport(r.a, protocol.StepReportRequest{SessionID: r.caller, ChangeID: id, StepRef: stepAccept, Result: protocol.StepResultFail, Commit: commitA, Note: "missing test"})
	if rev() <= before {
		t.Fatal("a failed accept step did not refresh the picker table")
	}
	if got := pickerTasksByKey(t, s)["claude-opus-5-5:high"]; len(got) != 1 || math.Abs(got[0].Score-.5) > 1e-9 || !got[0].Dirty {
		t.Fatalf("after the failed step: %+v", got)
	}
	// A late defect of severity 2 costs a quarter for the defect and a half for
	// its severity.
	before = rev()
	res, err := s.AddRating(protocol.RateFrame{RateRequest: protocol.RateRequest{Worker: r.worker, Kind: protocol.RateKindDefectLater, Severity: 2}, Rater: r.caller})
	if err != nil || !res.Accepted {
		t.Fatalf("defect: %+v %v", res, err)
	}
	if rev() <= before {
		t.Fatal("a defect did not refresh the picker table")
	}
	if got := pickerTasksByKey(t, s)["claude-opus-5-5:high"]; len(got) != 1 || got[0].Score != 0 {
		t.Fatalf("after the defect the task floors at 0: %+v", got)
	}
}

func TestPickerPassedStepsAndOtherStepsDoNotCount(t *testing.T) {
	for _, c := range []struct {
		step, result string
		want         bool
	}{
		{protocol.StepAccept, protocol.StepResultFail, true}, {protocol.StepReview, protocol.StepResultFail, true}, {protocol.StepTest, protocol.StepResultFail, true},
		{protocol.StepAccept, protocol.StepResultPass, false}, {protocol.StepMerge, protocol.StepResultFail, false}, {protocol.StepVerify, protocol.StepResultFail, false},
	} {
		if got := pickerStepCounts(c.step, c.result); got != c.want {
			t.Errorf("%s %s = %v", c.step, c.result, got)
		}
	}
}

func TestPickerTaskKeysOutcomesAndInvalidD(t *testing.T) {
	s, c := newTestStore(t)
	node, _, _ := s.CreateNode("h", "linux")
	at := c.now().Unix()
	for i, w := range []struct {
		harness, model, outcome string
		d                       float64
	}{
		{"codex", "gpt-6.1-sol:low", protocol.OutcomeComplete, 2.0},
		{"codex", "gpt-6.1-sol:medium", protocol.OutcomeComplete, 2.0},
		{"codex", "gpt-6.1-terra", protocol.OutcomePartial, 2.0},
		{"opencode", "opencode-go/deepseek-v4.1-flash", protocol.OutcomeComplete, 3.0},
		{"agy", "gemini-3.8-flash-medium", protocol.OutcomeTimedOut, 3.0},
		{"codex", "gpt-6.1-luna:xhigh", protocol.OutcomeComplete, 0},
	} {
		session := fmt.Sprintf("s_%d", i)
		pickerRun(t, s, node, session, w.harness, w.model, w.outcome, 0, at+int64(i))
		if w.d > 0 {
			pickerAssessment(t, s, session, w.d)
		} else {
			// A failed judge stores D 0. Such a task is skipped.
			assessmentSeq++
			if err := s.RecordWorkerAssessment(protocol.WorkerAssessment{RunID: fmt.Sprintf("run-%d", assessmentSeq), SessionID: session, StartedAt: 1, Error: "timed out"}); err != nil {
				t.Fatal(err)
			}
		}
	}
	got := pickerTasksByKey(t, s)
	want := map[string]float64{
		"gpt-6.1-sol:low": 1, "gpt-6.1-sol:medium": 1, "gpt-6.1-terra:default": 0,
		"opencode-go/deepseek-v4.1-flash": 1, "gemini-3.8-flash-medium": 0,
	}
	if len(got) != len(want) {
		t.Fatalf("keys: %+v", got)
	}
	for key, score := range want {
		if len(got[key]) != 1 || got[key][0].Score != score {
			t.Errorf("%s: %+v, want score %v", key, got[key], score)
		}
	}
}

// A session that ran twice (a continue to the same session) is one task. Its D
// is the first valid judge result, its fix rounds add up and the session's
// defects and failed steps count once.
func TestPickerSessionRunsAreOneTaskAndPenaltiesCountOnce(t *testing.T) {
	s, c := newTestStore(t)
	node, _, _ := s.CreateNode("h", "linux")
	at := c.now().Unix()
	pickerRun(t, s, node, "s_a", "codex", "gpt-6.1-sol:low", protocol.OutcomeComplete, 0, at)
	pickerRun(t, s, node, "s_a", "codex", "gpt-6.1-sol:low", protocol.OutcomeComplete, 1, at+10)
	pickerAssessment(t, s, "s_a", 2.4)
	pickerAssessment(t, s, "s_a", 3.1)
	// A judge that failed later (D 0) does not replace the valid result.
	assessmentSeq++
	if err := s.RecordWorkerAssessment(protocol.WorkerAssessment{RunID: fmt.Sprintf("run-%d", assessmentSeq), SessionID: "s_a", StartedAt: int64(assessmentSeq) * 1e9, Error: "timed out"}); err != nil {
		t.Fatal(err)
	}
	// One severity 1 defect costs a quarter for the defect and a quarter for its
	// severity.
	if _, err := s.db.Exec(`INSERT INTO worker_defects(session_id, severity, at) VALUES('s_a', 1, ?)`, at); err != nil {
		t.Fatal(err)
	}
	if err := s.refreshPicker(); err != nil {
		t.Fatal(err)
	}
	got := pickerTasksByKey(t, s)["gpt-6.1-sol:low"]
	if len(got) != 1 {
		t.Fatalf("tasks: %+v", got)
	}
	if got[0].D != 2.4 || math.Abs(got[0].Score-.25) > 1e-9 {
		t.Fatalf("first D 2.4; one fix round and one defect (1 + severity 1) cost .75: %+v", got)
	}
}

// Stopped and infra runs say nothing about the model: they are not tasks, so
// they add nothing to n and nothing to the score.
func TestPickerStoppedAndInfraRunsAreLeftOut(t *testing.T) {
	s, c := newTestStore(t)
	node, _, _ := s.CreateNode("h", "linux")
	at := c.now().Unix()
	pickerRun(t, s, node, "s_x", "codex", "gpt-6.1-sol:low", protocol.OutcomeComplete, 0, at)
	pickerRun(t, s, node, "s_x", "codex", "gpt-6.1-sol:low", protocol.OutcomeStopped, 0, at+10)
	pickerRun(t, s, node, "s_y", "codex", "gpt-6.1-sol:low", protocol.OutcomeInfra, 0, at+20)
	pickerRun(t, s, node, "s_y", "codex", "gpt-6.1-sol:low", protocol.OutcomeStopped, 0, at+30)
	pickerRun(t, s, node, "s_z", "codex", "gpt-6.1-sol:low", protocol.OutcomeTimedOut, 0, at+40)
	for _, session := range []string{"s_x", "s_y", "s_z"} {
		pickerAssessment(t, s, session, 2.5)
	}
	if _, err := s.db.Exec(`INSERT INTO worker_defects(session_id, severity, at) VALUES('s_x', 1, ?)`, at); err != nil {
		t.Fatal(err)
	}
	if err := s.refreshPicker(); err != nil {
		t.Fatal(err)
	}
	got := pickerTasksByKey(t, s)["gpt-6.1-sol:low"]
	// s_x gives one task, with its defect; s_y gives none; a timed out run is a
	// failed task and scores 0.
	if len(got) != 2 || math.Abs(got[0].Score-.5) > 1e-9 || got[1].Score != 0 {
		t.Fatalf("tasks: %+v", got)
	}
	if n := s.pickerTable().Cell("gpt-6.1-sol:low", s.pickerTable().Bucket(2.5)).N; n != 2 {
		t.Fatalf("n = %d", n)
	}
}

func TestPickerNoDatabaseReadPerPick(t *testing.T) {
	s, c := newTestStore(t)
	node, _, _ := s.CreateNode("h", "linux")
	for i := 0; i < 5; i++ {
		session := fmt.Sprintf("s_%d", i)
		pickerRun(t, s, node, session, "codex", "gpt-6.1-sol:low", protocol.OutcomeComplete, 0, c.now().Unix()+int64(i))
		pickerAssessment(t, s, session, 2.5)
	}
	g := &Gateway{store: s}
	table := s.pickerTable()
	if table.Tasks() != 5 {
		t.Fatalf("tasks: %d", table.Tasks())
	}
	// With the database closed any query fails, so a pick that succeeds read
	// the cache only.
	if err := s.db.Close(); err != nil {
		t.Fatal(err)
	}
	if s.pickerTable() != table {
		t.Fatal("the table changed without a write")
	}
	health := func(string, string) (ModelHealth, error) { return ModelHealth{}, nil }
	rotation := func() (protocol.WorkerRotation, error) { return protocol.WorkerRotation{}, nil }
	candidates := []protocol.WorkerModelCandidate{{Name: "sol low", Harness: "codex", Model: "gpt-6.1-sol:low"}, {Name: "sol medium", Harness: "codex", Model: "gpt-6.1-sol:medium"}}
	pick := &protocol.PickerInput{D: 2.5, Table: s.pickerTable()}
	choice, _, err := g.chooseAutomaticWorkerCore(health, rotation, pick, candidates, "normal", []protocol.ModelScore{{Match: "gpt-6.1-sol", Score: 45}}, nil, nil, nil, protocol.ProtectedProviders{}, false)
	if err != nil || choice.PickNote == "" {
		t.Fatalf("pick with the database closed: %+v %v", choice, err)
	}
}

func BenchmarkPickTwentyProfilesGateway(b *testing.B) {
	s, c := newTestStore(b)
	node, _, _ := s.CreateNode("h", "linux")
	var candidates []protocol.WorkerModelCandidate
	var scores []protocol.ModelScore
	for i := 0; i < 20; i++ {
		model := fmt.Sprintf("model-%02d:high", i)
		candidates = append(candidates, protocol.WorkerModelCandidate{Name: fmt.Sprintf("m%02d", i), Harness: "codex", Model: model})
		scores = append(scores, protocol.ModelScore{Match: model, Score: float64(25 + i*2)})
		for j := 0; j < 12; j++ {
			session := fmt.Sprintf("s_%d_%d", i, j)
			outcome := protocol.OutcomeComplete
			if j%(i%4+2) == 0 {
				outcome = protocol.OutcomePartial
			}
			pickerRun(b, s, node, session, "codex", model, outcome, j%2, c.now().Unix()+int64(i*100+j))
			pickerAssessment(b, s, session, 1.5+float64(j)*.3)
		}
	}
	g := &Gateway{store: s}
	health := func(string, string) (ModelHealth, error) { return ModelHealth{}, nil }
	var rotation protocol.WorkerRotation
	readRotation := func() (protocol.WorkerRotation, error) { return rotation, nil }
	pick := &protocol.PickerInput{D: 2.65, Table: s.pickerTable()}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, _, err := g.chooseAutomaticWorkerCore(health, readRotation, pick, candidates, "normal", scores, nil, nil, nil, protocol.ProtectedProviders{}, false); err != nil {
			b.Fatal(err)
		}
	}
}

func TestPickerSettingsComeFromTheRecipeAndRebuildTheTable(t *testing.T) {
	s, _ := newTestStore(t)
	if got := s.pickerTable().Settings(); got != protocol.DefaultPickerSettings().Normalized() {
		t.Fatalf("defaults: %+v", got)
	}
	before := s.pickerTable().Revision
	if _, err := s.WriteSettings([]SettingsChange{
		{Key: "picker.k", Level: "network", Scope: "network", Value: json.RawMessage(`2.5`)},
		{Key: "picker.uncertain_n", Level: "network", Scope: "network", Value: json.RawMessage(`8`)},
	}, "cto", "test"); err != nil {
		t.Fatal(err)
	}
	got := s.pickerTable()
	if got.Settings().K != 2.5 || got.Settings().UncertainN != 8 || got.Revision <= before {
		t.Fatalf("settings: %+v revision %d -> %d", got.Settings(), before, got.Revision)
	}
	// A setting that is not a picker setting leaves the table alone.
	rev := got.Revision
	if _, err := s.WriteSettings([]SettingsChange{{Key: "max_agents", Level: "network", Scope: "network", Value: json.RawMessage(`3`)}}, "owner", "test"); err != nil {
		t.Logf("max_agents is repo level only: %v", err)
	}
	if s.pickerTable().Revision != rev {
		t.Fatal("an unrelated setting rebuilt the picker table")
	}
}

func TestPickerAnalysisIsTheReadAPI(t *testing.T) {
	s, c := newTestStore(t)
	node, _, _ := s.CreateNode("h", "linux")
	for i := 0; i < 4; i++ {
		session := fmt.Sprintf("s_%d", i)
		pickerRun(t, s, node, session, "codex", "gpt-6.1-sol:low", protocol.OutcomeComplete, i%2, c.now().Unix()+int64(i))
		pickerAssessment(t, s, session, 2.5)
	}
	for _, p := range []protocol.WorkerProfile{
		{Name: "sol low", Harness: "codex", Model: "gpt-6.1-sol", Effort: "low"},
		{Name: "fresh", Harness: "codex", Model: "gpt-6.1-terra", Effort: "high"},
	} {
		if err := s.AddWorkerProfile(p); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.db.Exec(`INSERT INTO owner_settings (key, value, updated_at) VALUES (?, ?, 0)`, scoresKey,
		`[{"match":"gpt-6.1-sol:low","score":45},{"match":"gpt-6.1-terra:high","score":33}]`); err != nil {
		t.Fatal(err)
	}
	got, err := s.PickerAnalysis()
	if err != nil {
		t.Fatal(err)
	}
	if got.Tasks != 4 || got.Settings != protocol.DefaultPickerSettings().Normalized() || got.Revision == 0 {
		t.Fatalf("header: %+v", got)
	}
	// Buckets run from D 1.0 to 5.0 and each model row has one cell per bucket.
	if n := len(got.Buckets); n != 12 || got.Buckets[0].Lo != 1 || got.Buckets[n-1].Hi < 4.99 {
		t.Fatalf("buckets: %+v", got.Buckets)
	}
	var sol, fresh *protocol.PickerModelRow
	for i := range got.Models {
		switch got.Models[i].Key {
		case "gpt-6.1-sol:low":
			sol = &got.Models[i]
		case "gpt-6.1-terra:high":
			fresh = &got.Models[i]
		}
	}
	if sol == nil || fresh == nil {
		t.Fatalf("models: %+v", got.Models)
	}
	for _, row := range []*protocol.PickerModelRow{sol, fresh} {
		if len(row.Cells) != len(got.Buckets) {
			t.Fatalf("%s: %d cells for %d buckets", row.Key, len(row.Cells), len(got.Buckets))
		}
	}
	// D 2.5 is bucket 4. The sol cell holds four tasks, the fresh one none and
	// takes the bucket mean.
	if sol.Tasks != 4 || sol.Cells[4].N != 4 || fresh.Tasks != 0 || fresh.Cells[4].N != 0 || fresh.Cells[4].Score != got.Buckets[4].Mean {
		t.Fatalf("cells: sol %+v fresh %+v mean %v", sol.Cells[4], fresh.Cells[4], got.Buckets[4].Mean)
	}
	if sol.Cells[4].Lo > sol.Cells[4].Score || sol.Cells[4].Hi < sol.Cells[4].Score {
		t.Fatalf("interval: %+v", sol.Cells[4])
	}
	// Each profile carries its benchmark ceiling.
	ceilings := map[string]protocol.PickerProfileRow{}
	for _, p := range got.Profiles {
		ceilings[p.Name] = p
	}
	if p := ceilings["sol low"]; !p.Scored || p.Level != "hard" || p.Ceiling != 3.75 || p.Key != "gpt-6.1-sol:low" {
		t.Fatalf("sol low: %+v", p)
	}
	if p := ceilings["fresh"]; !p.Scored || p.Level != "normal" || p.Ceiling != 3 {
		t.Fatalf("fresh: %+v", p)
	}
}

func TestWorkerChoiceNoteForACellPick(t *testing.T) {
	choice := protocol.WorkerModelChoice{
		PickNote: "bucket 2.33-2.67: deepseek 0.76 [0.67-0.84] n42, best", QuotaNote: "claude on pace (5h, 30% used at 30% elapsed)",
		Weight: 1.25, ShareNote: "deepseek: 3 of the last 3 normal starts",
	}
	got := workerChoiceNote("normal", choice, nil, nil, nil)
	want := "bucket 2.33-2.67: deepseek 0.76 [0.67-0.84] n42, best; claude on pace (5h, 30% used at 30% elapsed); rotation weight 1.25; deepseek: 3 of the last 3 normal starts"
	if got != want {
		t.Fatalf("note %q\nwant %q", got, want)
	}
	// A fallback says so first and then gives the level rule's own note.
	choice = protocol.WorkerModelChoice{PickNote: "bucket 4.33-4.67: fallback to the level rule (x)", Fallback: true, Score: 39.5, Weight: 1}
	if got := workerChoiceNote("normal", choice, nil, nil, nil); got != "bucket 4.33-4.67: fallback to the level rule (x); 39.5, good enough; no quota pace reading; rotation weight 1.00" {
		t.Fatalf("fallback note: %q", got)
	}
}

func TestPickerContinuationChain(t *testing.T) {
	s, c := newTestStore(t)
	node, _, _ := s.CreateNode("h", "linux")
	at := c.now().Unix()
	for i, run := range []struct{ session, task, outcome string }{
		{"s_root", "", protocol.OutcomePartial},
		{"s_next", "s_root", protocol.OutcomeComplete},
	} {
		if err := s.RecordWorkerRun(protocol.WorkerRun{NodeID: node, SessionID: run.session, TaskID: run.task, Continue: i, Harness: "codex", Model: "gpt-6.1-terra:high", Outcome: run.outcome, StartedAt: at + int64(i)*100, EndedAt: at + int64(i)*100 + 60}); err != nil {
			t.Fatal(err)
		}
	}
	pickerAssessment(t, s, "s_root", 2.5)
	pickerAssessment(t, s, "s_next", 3.5)
	got := pickerTasksByKey(t, s)["gpt-6.1-terra:high"]
	if len(got) != 1 || got[0].D != 2.5 || math.Abs(got[0].Score-1) > 1e-9 {
		t.Fatalf("chain = %+v, want one task at D 2.5 and score 1", got)
	}
	if _, err := s.db.Exec(`INSERT INTO worker_defects(session_id, severity, at) VALUES('s_next', 1, ?)`, at); err != nil {
		t.Fatal(err)
	}
	got = pickerTasksByKey(t, s)["gpt-6.1-terra:high"]
	if len(got) != 1 || got[0].Score != .5 {
		t.Fatalf("chain with one defect = %+v, want one task scored .5", got)
	}
}

// The live data shape: a continue to the same worker session records another
// worker_runs row with the same session id and no task_id. Those rows are one
// task.
func TestPickerChainSameSessionRows(t *testing.T) {
	s, c := newTestStore(t)
	node, _, _ := s.CreateNode("h", "linux")
	at := c.now().Unix()
	for i, r := range []struct {
		outcome string
		fix     int
	}{{protocol.OutcomeComplete, 1}, {protocol.OutcomeComplete, 1}, {protocol.OutcomeComplete, 0}} {
		pickerRun(t, s, node, "s_worker", "codex", "gpt-6.1-terra:high", r.outcome, r.fix, at+int64(i)*100)
	}
	pickerAssessment(t, s, "s_worker", 3.0)
	got := pickerTasksByKey(t, s)["gpt-6.1-terra:high"]
	// One task, two fix rounds in total, nothing for the third run.
	if len(got) != 1 || got[0].D != 3.0 || math.Abs(got[0].Score-.5) > 1e-9 {
		t.Fatalf("same-session rows = %+v, want one task scored .5", got)
	}
}

// A continue costs nothing, and a partial run that was never continued is 0.
func TestPickerChainContinueIsFree(t *testing.T) {
	s, c := newTestStore(t)
	node, _, _ := s.CreateNode("h", "linux")
	at := c.now().Unix()
	for i, outcome := range []string{protocol.OutcomePartial, protocol.OutcomePartial, protocol.OutcomeComplete} {
		if err := s.RecordWorkerRun(protocol.WorkerRun{NodeID: node, SessionID: "s_done", Continue: 2, Harness: "codex", Model: "gpt-6.1-terra:high", Outcome: outcome, StartedAt: at, EndedAt: at + int64(i)*100}); err != nil {
			t.Fatal(err)
		}
	}
	pickerRun(t, s, node, "s_never", "codex", "gpt-6.1-terra:high", protocol.OutcomePartial, 0, at)
	pickerAssessment(t, s, "s_done", 3.0)
	pickerAssessment(t, s, "s_never", 3.0)
	got := pickerTasksByKey(t, s)["gpt-6.1-terra:high"]
	if len(got) != 2 || got[0].Score != 1 || got[1].Score != 0 {
		t.Fatalf("tasks = %+v, want a continued task at 1 and a partial one at 0", got)
	}
}

// Defects and failed steps count once per chain, whichever session holds them.
func TestPickerChainEvidenceCountsOnce(t *testing.T) {
	s, c := newTestStore(t)
	node, _, _ := s.CreateNode("h", "linux")
	at := c.now().Unix()
	pickerRun(t, s, node, "s_root", "codex", "gpt-6.1-terra:high", protocol.OutcomePartial, 0, at)
	pickerRun(t, s, node, "s_root", "codex", "gpt-6.1-terra:high", protocol.OutcomePartial, 0, at+50)
	if err := s.RecordWorkerRun(protocol.WorkerRun{NodeID: node, SessionID: "s_next", TaskID: "s_root", Harness: "codex", Model: "gpt-6.1-terra:high", Outcome: protocol.OutcomeComplete, StartedAt: at + 100, EndedAt: at + 200}); err != nil {
		t.Fatal(err)
	}
	pickerAssessment(t, s, "s_root", 2.5)
	if _, err := s.db.Exec(`INSERT INTO worker_defects(session_id, severity, at) VALUES('s_root', 1, ?)`, at); err != nil {
		t.Fatal(err)
	}
	got := pickerTasksByKey(t, s)["gpt-6.1-terra:high"]
	if len(got) != 1 || got[0].D != 2.5 || got[0].Score != .5 {
		t.Fatalf("chain = %+v, want one task at D 2.5 scored .5 (one defect)", got)
	}
}

func TestPickerSameSessionAutoContinueChain(t *testing.T) {
	s, c := newTestStore(t)
	node, _, _ := s.CreateNode("h", "linux")
	at := c.now().Unix()
	pickerRun(t, s, node, "s_worker", "codex", "gpt-6.1-terra:high", protocol.OutcomePartial, 0, at)
	pickerRun(t, s, node, "s_worker", "codex", "gpt-6.1-terra:high", protocol.OutcomeComplete, 0, at+100)
	pickerAssessment(t, s, "s_worker", 3.0)
	pickerAssessment(t, s, "s_worker", 4.0)
	got := pickerTasksByKey(t, s)["gpt-6.1-terra:high"]
	if len(got) != 1 || got[0].Score != 1 || got[0].D != 3.0 {
		t.Fatalf("auto-continuation = %+v", got)
	}
}

// taskAfterStep runs one worker task through a delivery step and gives the
// picker's view of it.
func taskAfterStep(t *testing.T, step func(r *deliveryRig, id string)) protocol.PickerTask {
	t.Helper()
	r := newDeliveryRig(t)
	pickerRun(t, r.s, r.a, r.worker, "claude", "claude-opus-5-5:high", protocol.OutcomeComplete, 0, 1000)
	pickerAssessment(t, r.s, r.worker, 2.5)
	id := r.submit(r.a, r.workerSubmit("agent-net/w_1", commitA)).Change.Change.ID
	step(r, id)
	got := pickerTasksByKey(t, r.s)["claude-opus-5-5:high"]
	if len(got) != 1 {
		t.Fatalf("tasks: %+v", got)
	}
	return got[0]
}

// A failed merger test step costs the task once. Agent-net also records a
// severity 1 defect for it (decision 0103), which would add a quarter for the
// defect and a quarter for its severity on top of the quarter for the failed
// step: 0.75 for one failure. The score takes the failed step and skips the
// defect, so a failed test step scores like any other failed step (decision
// 0161).
func TestPickerCountsAFailedTestStepOnce(t *testing.T) {
	failedTest := taskAfterStep(t, func(r *deliveryRig, id string) {
		r.report1(stepAccept, id, commitA, protocol.StepResultPass, nil, "")
		r.report1(protocol.StepRef{Step: protocol.StepTest}, id, commitA, protocol.StepResultFail, map[string]string{protocol.FieldSuite: protocol.SuiteChanged}, "TestFoo fails")
		if rows := autoDefectRows(t, r.s); len(rows) != 1 {
			t.Fatalf("the failed test step should record its automatic defect: %+v", rows)
		}
	})
	failedAccept := taskAfterStep(t, func(r *deliveryRig, id string) {
		r.report1(stepAccept, id, commitA, protocol.StepResultFail, nil, "needs work")
	})
	if math.Abs(failedTest.Score-.75) > 1e-9 || failedTest.Score != failedAccept.Score {
		t.Fatalf("failed test step scores %v, failed accept step %v, want both 0.75", failedTest.Score, failedAccept.Score)
	}
	if !failedTest.Dirty || !failedAccept.Dirty {
		t.Fatalf("a failed step makes a task dirty: test %+v accept %+v", failedTest, failedAccept)
	}
}
