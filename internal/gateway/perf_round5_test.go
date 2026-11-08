package gateway

import (
	"encoding/json"
	"math"
	"os"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// ---- the code as it was before round 5, kept here as the reference ----

// refTaskRuns is taskRuns as it was.
type refTaskRuns struct {
	caller, model, harness, difficulty string
	continueCount, fixCount, midrun    int
	endedAt                            int64
	nonStopped, recent, hasRoot        bool
	sessions                           map[string]bool
	touches                            map[string]bool
}

// refReworkTasks is reworkTasks as it was: it scanned worker_runs.
func refReworkTasks(s *Store, cut int64) ([]*refTaskRuns, []string, error) {
	rows, err := s.db.Query(`SELECT session_id,task_id,caller,model,harness,difficulty,touches,outcome,ended_at,continue_count,fix_count,midrun_count FROM worker_runs ORDER BY ended_at,id`)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	tasks := map[string]*refTaskRuns{}
	var order, callers []string
	seenCaller := map[string]bool{}
	for rows.Next() {
		var sid, root, caller, model, harness, difficulty, touches, outcome string
		var ended int64
		var cont, fix, mid int
		if err := rows.Scan(&sid, &root, &caller, &model, &harness, &difficulty, &touches, &outcome, &ended, &cont, &fix, &mid); err != nil {
			return nil, nil, err
		}
		if ended >= cut && caller != "" && !seenCaller[caller] {
			seenCaller[caller] = true
			callers = append(callers, caller)
		}
		if sid == "" {
			continue
		}
		if root == "" {
			root = sid
		}
		t := tasks[root]
		if t == nil {
			t = &refTaskRuns{caller: caller, model: model, harness: harness, difficulty: difficulty, sessions: map[string]bool{}, touches: map[string]bool{}}
			tasks[root] = t
			order = append(order, root)
		}
		if sid == root && !t.hasRoot {
			t.caller, t.model, t.harness, t.difficulty = caller, model, harness, difficulty
			t.hasRoot = true
		}
		for _, area := range strings.Split(touches, ",") {
			if area != "" {
				t.touches[area] = true
			}
		}
		t.sessions[sid] = true
		t.continueCount += cont
		t.fixCount += fix
		t.midrun += mid
		t.nonStopped = t.nonStopped || outcome != protocol.OutcomeStopped && outcome != protocol.OutcomeInfra
		t.recent = t.recent || ended >= cut
		if ended > t.endedAt {
			t.endedAt = ended
		}
	}
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}
	out := make([]*refTaskRuns, 0, len(order))
	for _, root := range order {
		if t := tasks[root]; t.recent && t.nonStopped {
			out = append(out, t)
		}
	}
	return out, callers, nil
}

// refTrackRecords is TrackRecords as it was: it queried worker_runs.
func refTrackRecords(s *Store, nodeID string, window time.Duration) ([]protocol.TrackRecord, error) {
	if window <= 0 {
		window = protocol.TrackRecordDays * 24 * time.Hour
	}
	cut := s.now().Add(-window).Unix()
	query := `SELECT harness, model, outcome, run_seconds, continue_count, fix_count, auto_continue_count, early_quit FROM worker_runs WHERE ended_at >= ?`
	args := []any{cut}
	if nodeID != "" {
		query += ` AND node_id = ?`
		args = append(args, nodeID)
	}
	query += ` ORDER BY harness, model, ended_at`
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	type key struct{ harness, model string }
	order := []key{}
	byKey := map[key]*protocol.TrackRecord{}
	durations := map[key][]int64{}
	for rows.Next() {
		var harness, model, outcome string
		var run int64
		var cont, fix, auto int
		var early bool
		if err := rows.Scan(&harness, &model, &outcome, &run, &cont, &fix, &auto, &early); err != nil {
			return nil, err
		}
		k := key{harness, model}
		tr := byKey[k]
		if tr == nil {
			tr = &protocol.TrackRecord{Harness: harness, Model: model, Resumable: protocol.HarnessCanResume(harness)}
			byKey[k] = tr
			order = append(order, k)
		}
		tr.Runs++
		tr.Continue += cont
		tr.Fix += fix
		tr.AutoContinue += auto
		if early {
			tr.EarlyQuits++
		}
		switch outcome {
		case protocol.OutcomeComplete:
			tr.Complete++
		case protocol.OutcomePartial:
			tr.Partial++
		case protocol.OutcomeFailed:
			tr.Failed++
		case protocol.OutcomeTimedOut:
			tr.TimedOut++
		case protocol.OutcomeStopped:
			tr.Stopped++
		case protocol.OutcomeInfra:
			tr.Infra++
		}
		if run > 0 {
			durations[k] = append(durations[k], run)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	out := make([]protocol.TrackRecord, 0, len(order))
	for _, k := range order {
		tr := byKey[k]
		tr.MedianRunSeconds = refMedian(durations[k])
		out = append(out, *tr)
	}
	return out, nil
}

func refMedian(values []int64) int64 {
	if len(values) == 0 {
		return 0
	}
	v := append([]int64(nil), values...)
	sort.Slice(v, func(i, j int) bool { return v[i] < v[j] })
	m := len(v) / 2
	if len(v)%2 == 1 {
		return v[m]
	}
	return int64(math.Round(float64(v[m-1]+v[m]) / 2))
}

// refCalibration is Calibration as it was, with the old reworkTasks.
func refCalibration(s *Store) (protocol.Calibration, error) {
	cut := s.now().Add(-calibrationWindow).Unix()
	tasks, _, err := refReworkTasks(s, cut)
	if err != nil {
		return protocol.Calibration{}, err
	}
	defects, err := s.DefectCountsBySession(cut)
	if err != nil {
		return protocol.Calibration{}, err
	}
	defectRework, err := s.DefectReworkBySession(cut)
	if err != nil {
		return protocol.Calibration{}, err
	}
	totalDefects := 0
	for _, n := range defects {
		totalDefects += n
	}
	samples := []protocol.CalibrationSample{}
	for _, task := range tasks {
		if task.caller == "" {
			continue
		}
		difficulty := task.difficulty
		if difficulty == "" {
			difficulty = "normal"
		}
		fix := task.fixCount
		for session := range task.sessions {
			fix += defectRework[session]
		}
		samples = append(samples, protocol.CalibrationSample{Caller: task.caller, Difficulty: difficulty,
			Continue: task.continueCount, Fix: fix, EndedAt: task.endedAt})
	}
	scores, err := s.scores()
	if err != nil {
		return protocol.Calibration{}, err
	}
	callers := []string{}
	seen := map[string]bool{}
	for _, sample := range samples {
		if !seen[sample.Caller] {
			seen[sample.Caller] = true
			callers = append(callers, sample.Caller)
		}
	}
	names, models, strong, err := s.callerFacts(scores, callers)
	if err != nil {
		return protocol.Calibration{}, err
	}
	cal := protocol.BuildCalibration(samples, strong)
	ratings, err := s.RatingCounts()
	if err != nil {
		return protocol.Calibration{}, err
	}
	cal.Ratings = ratings
	cal.Defects = totalDefects
	cal.Quality = s.QualityAnalysis()
	for i := range cal.Callers {
		cal.Callers[i].Name = names[cal.Callers[i].Caller]
		cal.Callers[i].Model = models[cal.Callers[i].Caller]
	}
	sort.SliceStable(cal.Callers, func(i, j int) bool {
		a, b := cal.Callers[i], cal.Callers[j]
		if a.Name != b.Name {
			return a.Name < b.Name
		}
		return a.Caller < b.Caller
	})
	return cal, nil
}

// ---- helpers ----

// sameTasks fails unless the tasks are what the old reworkTasks gave.
func sameTasks(t *testing.T, s *Store, cut int64) {
	t.Helper()
	got, gotCallers, err := s.reworkTasks(cut)
	if err != nil {
		t.Fatal(err)
	}
	want, wantCallers, err := refReworkTasks(s, cut)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(gotCallers, wantCallers) {
		t.Fatalf("cut %d callers: got %v want %v", cut, gotCallers, wantCallers)
	}
	if len(got) != len(want) {
		t.Fatalf("cut %d: %d tasks, want %d", cut, len(got), len(want))
	}
	for i := range got {
		g, w := got[i], want[i]
		if g.caller != w.caller || g.model != w.model || g.harness != w.harness || g.difficulty != w.difficulty ||
			g.continueCount != w.continueCount || g.fixCount != w.fixCount || g.midrun != w.midrun ||
			g.endedAt != w.endedAt || g.nonStopped != w.nonStopped || g.hasRoot != w.hasRoot ||
			!reflect.DeepEqual(g.sessions, w.sessions) || !reflect.DeepEqual(g.touches, w.touches) {
			t.Fatalf("cut %d task %d: got %+v want %+v", cut, i, *g, *w)
		}
	}
}

// sameTracks fails unless TrackRecords is what the old query gave.
func sameTracks(t *testing.T, s *Store, nodeID string, window time.Duration) {
	t.Helper()
	got, err := s.TrackRecords(nodeID, window)
	if err != nil {
		t.Fatal(err)
	}
	want, err := refTrackRecords(s, nodeID, window)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("node %q window %v:\n got %+v\nwant %+v", nodeID, window, got, want)
	}
}

// sameCalibration fails unless Calibration is what the old code gave.
func sameCalibration(t *testing.T, s *Store) protocol.Calibration {
	t.Helper()
	got, err := s.Calibration()
	if err != nil {
		t.Fatal(err)
	}
	want, err := refCalibration(s)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("calibration:\n got %+v\nwant %+v", got, want)
	}
	return got
}

type runsFixture struct {
	s            *Store
	c            *clock
	node1, node2 string
	callerA      string
	callerB      string
	workers      []string
}

// runsFix makes runs of two machines, two callers, several harnesses and
// models, a task with continuation runs, a task of stopped runs only, a run
// with no caller, a run with no session and runs outside the window.
func runsFix(t *testing.T) *runsFixture {
	t.Helper()
	s, c := newTestStore(t)
	f := &runsFixture{s: s, c: c}
	f.node1, _, _ = s.CreateNode("mac", "linux")
	f.node2, _, _ = s.CreateNode("box", "linux")
	f.callerA = ratingCaller(t, s, f.node1, "callerA", "claude-opus-5-5")
	f.callerB = ratingCaller(t, s, f.node2, "callerB", "gpt-6.1-terra")
	now := c.now()
	ago := func(d time.Duration) int64 { return now.Add(-d).Unix() }
	add := func(r protocol.WorkerRun) {
		t.Helper()
		if err := s.RecordWorkerRun(r); err != nil {
			t.Fatal(err)
		}
	}
	for i, spec := range []struct {
		node, caller, harness, model, difficulty, outcome string
		age                                               time.Duration
		cont, fix, run                                    int
	}{
		{f.node1, f.callerA, "opencode", "p/m", "hard", protocol.OutcomeComplete, 2 * time.Hour, 1, 0, 120},
		{f.node1, f.callerA, "opencode", "p/m", "hard", protocol.OutcomeComplete, 3 * time.Hour, 0, 2, 300},
		{f.node1, f.callerA, "codex", "gpt-6.1-terra", "normal", protocol.OutcomePartial, 5 * time.Hour, 3, 1, 90},
		{f.node2, f.callerB, "codex", "gpt-6.1-terra", "hard", protocol.OutcomeFailed, 7 * time.Hour, 0, 0, 0},
		{f.node2, f.callerB, "claude", "claude-opus-5-5", "", protocol.OutcomeTimedOut, 9 * time.Hour, 1, 1, 600},
		{f.node2, f.callerB, "claude", "claude-opus-5-5", "easy", protocol.OutcomeComplete, 11 * time.Hour, 0, 0, 45},
		{f.node1, f.callerA, "opencode", "p/m", "hard", protocol.OutcomeStopped, 13 * time.Hour, 0, 0, 10},
		{f.node1, f.callerB, "opencode", "q/n", "hard", protocol.OutcomeInfra, 14 * time.Hour, 0, 0, 0},
		{f.node1, "", "pi", "p/m", "", protocol.OutcomeComplete, 15 * time.Hour, 4, 4, 77},
		// Outside the 30 days window.
		{f.node1, f.callerA, "opencode", "p/m", "hard", protocol.OutcomeComplete, 40 * 24 * time.Hour, 5, 5, 500},
		{f.node2, f.callerB, "claude", "claude-opus-5-5", "hard", protocol.OutcomeComplete, 45 * 24 * time.Hour, 5, 5, 400},
	} {
		sid := "w" + string(rune('a'+i))
		f.workers = append(f.workers, sid)
		add(protocol.WorkerRun{NodeID: spec.node, SessionID: sid, Harness: spec.harness, Model: spec.model, Outcome: spec.outcome,
			StartedAt: ago(spec.age + time.Hour), EndedAt: ago(spec.age), RunSeconds: int64(spec.run),
			Continue: spec.cont, Fix: spec.fix, AutoContinue: i % 3, Caller: spec.caller, Difficulty: spec.difficulty,
			Touches: []string{"api", "ui"}[:1+i%2]})
	}
	// Continuation runs of the first worker (one task) and a task of stopped
	// runs only, with a root run that ended earlier than its children.
	add(protocol.WorkerRun{NodeID: f.node1, SessionID: "wk1", TaskID: f.workers[0], Harness: "opencode", Model: "p/m", Outcome: protocol.OutcomeComplete, EndedAt: ago(time.Hour), Continue: 2, Fix: 1, Midrun: 1, Caller: f.callerA, Difficulty: "hard", Touches: []string{"db"}})
	add(protocol.WorkerRun{NodeID: f.node1, SessionID: "wk2", TaskID: f.workers[0], Harness: "opencode", Model: "p/m", Outcome: protocol.OutcomeStopped, EndedAt: ago(30 * time.Minute), Caller: f.callerA, Difficulty: "hard"})
	// A run with no session id.
	if _, err := s.db.Exec(`INSERT INTO worker_runs (node_id, session_id, harness, model, outcome, ended_at, caller, difficulty, counters_known) VALUES (?, '', 'codex', 'm', ?, ?, ?, 'normal', 1)`, f.node2, protocol.OutcomeComplete, ago(20*time.Minute), f.callerB); err != nil {
		t.Fatal(err)
	}
	return f
}

// ---- the copy of worker_runs ----

func TestReworkTasksSameAsBefore(t *testing.T) {
	f := runsFix(t)
	now := f.c.now()
	for _, cut := range []int64{0, now.Add(-45 * 24 * time.Hour).Unix(), now.Add(-calibrationWindow).Unix(), now.Add(-6 * time.Hour).Unix(), now.Add(-110 * time.Minute).Unix(), now.Add(time.Hour).Unix()} {
		sameTasks(t, f.s, cut)
	}
	tasks, _, err := f.s.reworkTasks(now.Add(-calibrationWindow).Unix())
	if err != nil || len(tasks) < 5 {
		t.Fatalf("the fixture gave %d tasks: %v", len(tasks), err)
	}
}

func TestTrackRecordsSameAsBefore(t *testing.T) {
	f := runsFix(t)
	for _, node := range []string{"", f.node1, f.node2, "no-such-node"} {
		for _, window := range []time.Duration{0, time.Hour, 6 * time.Hour, 24 * time.Hour, protocol.TrackRecordDays * 24 * time.Hour, 60 * 24 * time.Hour} {
			sameTracks(t, f.s, node, window)
		}
	}
	got, err := f.s.TrackRecords("", 0)
	if err != nil || len(got) < 4 {
		t.Fatalf("the fixture gave %d records: %v", len(got), err)
	}
}

func TestCalibrationSameAsBefore(t *testing.T) {
	f := runsFix(t)
	for i := 0; i < 3; i++ {
		cal := sameCalibration(t, f.s)
		if i == 0 && len(cal.Callers) == 0 {
			t.Fatal("the fixture gave no callers")
		}
	}
}

// Time passing moves the window without any write: the copy holds no cut.
func TestRunsCopyFollowsTheClock(t *testing.T) {
	f := runsFix(t)
	before := sameCalibration(t, f.s)
	trackBefore, _ := f.s.TrackRecords("", 0)
	f.c.t = f.c.t.Add(30*24*time.Hour - 12*time.Hour)
	after := sameCalibration(t, f.s)
	sameTracks(t, f.s, "", 0)
	sameTasks(t, f.s, f.c.now().Add(-calibrationWindow).Unix())
	trackAfter, _ := f.s.TrackRecords("", 0)
	if reflect.DeepEqual(before, after) && reflect.DeepEqual(trackBefore, trackAfter) {
		t.Fatal("time passing did not move the window")
	}
}

// Each kind of write to worker_runs, and to the ratings and defects that
// Calibration adds, reaches the next call.
func TestRunsCopyFollowsEveryWrite(t *testing.T) {
	f := runsFix(t)
	s := f.s
	snapshots := func() (protocol.Calibration, []protocol.TrackRecord) {
		t.Helper()
		cal := sameCalibration(t, s)
		sameTracks(t, s, "", 0)
		sameTracks(t, s, f.node1, 0)
		sameTasks(t, s, s.now().Add(-calibrationWindow).Unix())
		tr, err := s.TrackRecords("", 0)
		if err != nil {
			t.Fatal(err)
		}
		return cal, tr
	}
	step := func(name string, change func(), calChanges, trackChanges bool) {
		t.Helper()
		calBefore, trBefore := snapshots()
		change()
		calAfter, trAfter := snapshots()
		if calChanges && reflect.DeepEqual(calBefore, calAfter) {
			t.Fatalf("%s did not change the calibration", name)
		}
		if trackChanges && reflect.DeepEqual(trBefore, trAfter) {
			t.Fatalf("%s did not change the track records", name)
		}
	}
	// A new worker run with a new caller and its own session.
	step("a new run", func() {
		wid := ratingWorker(t, s, f.node1, f.callerA, "p/m:high", "hard")
		if err := s.RecordWorkerRun(protocol.WorkerRun{NodeID: f.node1, SessionID: wid, Harness: "opencode", Model: "p/m",
			Outcome: protocol.OutcomeComplete, EndedAt: s.now().Unix(), Caller: f.callerA, Difficulty: "hard", Fix: 3, Continue: 2, RunSeconds: 61}); err != nil {
			t.Fatal(err)
		}
		f.workers = append(f.workers, wid)
	}, true, true)
	wid := f.workers[len(f.workers)-1]
	// A rating of that worker.
	step("a rating", func() {
		out, err := s.AddRating(protocol.RateFrame{RateRequest: protocol.RateRequest{Worker: wid, Score: 5, Note: "fine"}, Rater: f.callerA})
		if err != nil || !out.Accepted {
			t.Fatalf("rating: %+v %v", out, err)
		}
	}, true, false)
	// A defect found later adds its severity as rework.
	step("a defect", func() {
		out, err := s.AddRating(defectFrame(wid, f.callerA, "broke the merge"))
		if err != nil || !out.Accepted {
			t.Fatalf("defect: %+v %v", out, err)
		}
	}, true, false)
	// A change of an existing run, as a backfill makes.
	step("an update", func() {
		if _, err := s.db.Exec(`UPDATE worker_runs SET fix_count = fix_count + 7, outcome = ?, run_seconds = 4000 WHERE session_id = ?`, protocol.OutcomeFailed, f.workers[0]); err != nil {
			t.Fatal(err)
		}
	}, true, true)
	// A delete, as the retention does.
	step("a delete", func() {
		if _, err := s.db.Exec(`DELETE FROM worker_runs WHERE session_id = ?`, f.workers[1]); err != nil {
			t.Fatal(err)
		}
	}, true, true)
}

// The copy is read once for as many calls as there are with no write between.
func TestRunsCopyIsReloadedOnlyAfterAWrite(t *testing.T) {
	f := runsFix(t)
	s := f.s
	first, err := s.runsSnapshot()
	if err != nil {
		t.Fatal(err)
	}
	second, err := s.runsSnapshot()
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Fatal("the copy was reloaded with no write")
	}
	// A write to another table does not reload it.
	if _, err := s.db.Exec(`INSERT INTO owner_settings (key, value, updated_at) VALUES ('x', '1', 0) ON CONFLICT (key) DO UPDATE SET value=excluded.value`); err != nil {
		t.Fatal(err)
	}
	if third, _ := s.runsSnapshot(); third != first {
		t.Fatal("a write to another table reloaded the copy")
	}
	if err := s.RecordWorkerRun(protocol.WorkerRun{NodeID: f.node1, SessionID: "later", Harness: "codex", Model: "m", Outcome: protocol.OutcomeComplete}); err != nil {
		t.Fatal(err)
	}
	if fourth, _ := s.runsSnapshot(); fourth == first {
		t.Fatal("a new run did not reload the copy")
	}
}

// ---- a copy of the real database ----

// This test runs on a copy of a real gateway database when AGENT_NET_BENCH_DB
// is set, and is skipped otherwise.
func TestReadsSameOnDatabaseCopy(t *testing.T) {
	path := os.Getenv("AGENT_NET_BENCH_DB")
	if path == "" {
		t.Skip("AGENT_NET_BENCH_DB is not set")
	}
	if _, err := os.Stat(path); err != nil {
		t.Skip("no database copy at " + path)
	}
	s, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	nodes, err := s.Nodes()
	if err != nil {
		t.Fatal(err)
	}
	for _, back := range []time.Duration{0, 24 * time.Hour, 7 * 24 * time.Hour, 20 * 24 * time.Hour} {
		cut := s.now().Add(-back).Unix()
		sameTasks(t, s, cut)
	}
	sameTasks(t, s, s.now().Add(-calibrationWindow).Unix())
	for _, n := range append([]protocol.NodeView{{}}, nodes...) {
		for _, window := range []time.Duration{0, 24 * time.Hour, 3 * 24 * time.Hour} {
			sameTracks(t, s, n.NodeID, window)
		}
	}
	sameCalibration(t, s)
	for _, f := range []protocol.QueryAgents{{}, {IncludeOwners: true}, {OnlineOnly: true}, {Harness: "claude"}} {
		want, err := s.agentsBaseWith(agentSelect, f)
		if err != nil {
			t.Fatal(err)
		}
		got, err := s.agentsBase(f)
		if err != nil {
			t.Fatal(err)
		}
		if len(got) == 0 || !reflect.DeepEqual(got, want) {
			t.Fatalf("agents %+v: %d rows, differ from the subqueries", f, len(got))
		}
	}
}

// ---- agentsBase ----

// listFixture has sessions in several Repos, one with no Repo, a Repo with a
// max_agents setting, a worker, a merger, the CTO, an offline session and
// stall items: open, dismissed, closed, two for one session and two as new as
// each other.
func listFixture(t *testing.T) (*Store, map[string]string) {
	t.Helper()
	s := teamStore(t)
	var node string
	if err := s.db.QueryRow(`SELECT node_id FROM sessions WHERE repo_id='demo'`).Scan(&node); err != nil {
		t.Fatal(err)
	}
	ids := map[string]string{}
	add := func(name, harness, project string, worker *protocol.WorkerInfo) {
		t.Helper()
		info := protocol.SessionInfo{Harness: harness, NativeID: "n-" + name, Cwd: "/w/" + name, Name: name, Worker: worker}
		if project != "" {
			info.Repo = protocol.RepoInfo{Root: "/w/" + project, Project: project}
		}
		id, err := s.UpsertSession(node, info)
		if err != nil {
			t.Fatal(err)
		}
		ids[name] = id
	}
	add("demo2", "codex", "demo", nil)
	add("demo3", "pi", "demo", nil)
	add("demoworker", "opencode", "demo", &protocol.WorkerInfo{ID: "w", Owner: "x", Project: "demo"})
	add("demomerger", "claude", "demo", nil)
	add("demooff", "claude", "demo", nil)
	add("other2", "claude", "other", nil)
	add("cto", "claude", "demo", nil)
	add("norepo", "claude", "", nil)
	add("norepo2", "codex", "", nil)
	if _, err := s.db.Exec(`INSERT INTO project_mergers(project,node_id,session_id) VALUES('demo',?,?)`, node, ids["demomerger"]); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`UPDATE sessions SET online=0 WHERE id=?`, ids["demooff"]); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`INSERT INTO owner_settings(key,value,updated_at) VALUES('cto_session',?,0)`, ids["cto"]); err != nil {
		t.Fatal(err)
	}
	if _, err := s.WriteSettings([]SettingsChange{{Key: "max_agents", Level: "repo", Scope: "demo", Value: json.RawMessage(`2`)}}, "owner", "limit"); err != nil {
		t.Fatal(err)
	}
	stall := func(id, session, status string, conversation, created int64) {
		t.Helper()
		if _, err := s.db.Exec(`INSERT INTO inbox_items (id, kind, session_id, node, text, recommendation, message_id, conversation_id, created_at, status) VALUES (?, 'stall', ?, ?, 't', '', ?, ?, ?, ?)`,
			id, session, node, sessionStallMarker, conversation, created, status); err != nil {
			t.Fatal(err)
		}
	}
	stall("st1", ids["demo2"], "open", 11, 100)
	stall("st2", ids["demo2"], "dismissed", 12, 200)
	stall("st3", ids["demo2"], "closed", 13, 300)
	stall("st4", ids["demo3"], "closed", 14, 100)
	stall("st5", ids["other2"], "dismissed", 15, 500)
	stall("st6", ids["other2"], "open", 16, 500)
	stall("st7", ids["norepo"], "open", 17, 50)
	if _, err := s.db.Exec(`INSERT INTO inbox_items (id, kind, session_id, node, text, recommendation, message_id, conversation_id, created_at, status) VALUES ('other-kind', 'stall', ?, ?, 't', '', 'something-else', 99, 900, 'open')`, ids["norepo2"], node); err != nil {
		t.Fatal(err)
	}
	return s, ids
}

func TestAgentsBaseSameAsBefore(t *testing.T) {
	s, ids := listFixture(t)
	counts, stalled, maxed := 0, 0, 0
	for _, f := range []protocol.QueryAgents{
		{},
		{IncludeOwners: true},
		{OnlineOnly: true, IncludeOwners: true},
		{Harness: "claude"},
		{Harness: "codex", IncludeOwners: true},
		{Project: "demo"},
		{Text: "demo"},
	} {
		want, err := s.agentsBaseWith(agentSelect, f)
		if err != nil {
			t.Fatal(err)
		}
		got, err := s.agentsBase(f)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("%+v:\n got %+v\nwant %+v", f, got, want)
		}
		if len(f.Project)+len(f.Text)+len(f.Harness) == 0 && !f.OnlineOnly && !f.IncludeOwners {
			for _, a := range got {
				if a.RepoAgentCount > 0 {
					counts++
				}
				if a.StalledSince != 0 {
					stalled++
				}
				if a.RepoMaxAgents != 0 {
					maxed++
				}
			}
		}
	}
	if counts == 0 || stalled == 0 || maxed == 0 {
		t.Fatalf("the fixture is too thin: %d counts, %d stalled, %d with a max", counts, stalled, maxed)
	}
	// The values themselves, not only the same as the subqueries gave.
	all, err := s.agentsBase(protocol.QueryAgents{IncludeOwners: true})
	if err != nil {
		t.Fatal(err)
	}
	byID := map[string]protocol.AgentView{}
	for _, a := range all {
		byID[a.SessionID] = a
	}
	for name, stall := range map[string]int64{"demo2": 12, "demo3": 0, "other2": 16, "norepo": 17, "norepo2": 0} {
		if got := byID[ids[name]].StalledSince; got != stall {
			t.Errorf("%s stall: %d, want %d", name, got, stall)
		}
	}
	// demo: the merger, the worker, the CTO and the offline session do not
	// count; the first session, demo2 and demo3 do.
	if a := byID[ids["demo2"]]; a.RepoMaxAgents != 2 || a.RepoAgentCount != 3 || !a.RepoOverMax {
		t.Errorf("demo2: max %d count %d over %v", a.RepoMaxAgents, a.RepoAgentCount, a.RepoOverMax)
	}
	if a := byID[ids["other2"]]; a.RepoMaxAgents != 0 || a.RepoAgentCount != 2 {
		t.Errorf("other2: max %d count %d", a.RepoMaxAgents, a.RepoAgentCount)
	}
}

// One session's row (the subqueries) says what its row in the list says
// (the joins).
func TestAgentRowSameAsItsRowInTheList(t *testing.T) {
	s, ids := listFixture(t)
	all, err := s.agentsBase(protocol.QueryAgents{IncludeOwners: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(all) < len(ids) {
		t.Fatalf("%d agents for %d sessions", len(all), len(ids))
	}
	for _, listed := range all {
		one, ok, err := s.agentBase(listed.SessionID)
		if err != nil || !ok {
			t.Fatalf("%s: %v %v", listed.SessionID, ok, err)
		}
		if !reflect.DeepEqual(one, listed) {
			t.Fatalf("%s:\n one %+v\nlist %+v", listed.SessionID, one, listed)
		}
	}
}

// A change to what the three values read reaches the next call.
func TestAgentsBaseFollowsItsInputs(t *testing.T) {
	s, ids := listFixture(t)
	view := func(name string) protocol.AgentView {
		t.Helper()
		all, err := s.agentsBase(protocol.QueryAgents{IncludeOwners: true})
		if err != nil {
			t.Fatal(err)
		}
		ref, err := s.agentsBaseWith(agentSelect, protocol.QueryAgents{IncludeOwners: true})
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(all, ref) {
			t.Fatalf("differs from the subqueries:\n got %+v\nwant %+v", all, ref)
		}
		for _, a := range all {
			if a.SessionID == ids[name] {
				return a
			}
		}
		t.Fatalf("no agent %s", name)
		return protocol.AgentView{}
	}
	before := view("demo2")
	if _, err := s.WriteSettings([]SettingsChange{{Key: "max_agents", Level: "repo", Scope: "demo", Value: json.RawMessage(`5`), Expect: json.RawMessage(`2`)}}, "owner", "raise"); err != nil {
		t.Fatal(err)
	}
	if a := view("demo2"); a.RepoMaxAgents != 5 || a.RepoOverMax {
		t.Fatalf("max after the setting: %d over %v", a.RepoMaxAgents, a.RepoOverMax)
	}
	if _, err := s.db.Exec(`UPDATE sessions SET online=0 WHERE id=?`, ids["demo3"]); err != nil {
		t.Fatal(err)
	}
	if a := view("demo2"); a.RepoAgentCount != before.RepoAgentCount-1 {
		t.Fatalf("count after a session went offline: %d, was %d", a.RepoAgentCount, before.RepoAgentCount)
	}
	if _, err := s.db.Exec(`UPDATE inbox_items SET status='closed' WHERE id='st2'`); err != nil {
		t.Fatal(err)
	}
	if a := view("demo2"); a.StalledSince != 11 {
		t.Fatalf("stall after the newest was closed: %d", a.StalledSince)
	}
}
