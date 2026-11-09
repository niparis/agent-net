package gateway

import (
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

// Real units: worker_runs.started_at is Unix seconds, while a worker
// assessment's started_at is Unix nanoseconds, taken when the judge was asked
// a few seconds before the worker started.
const (
	testRunStartFirst  = int64(1793730799) // Unix seconds
	testRunStartSecond = int64(1793731833) // Unix seconds
)

func TestWorkerLogNewestRunningAndAssessment(t *testing.T) {
	s, _ := newTestStore(t)
	node, _, _ := s.CreateNode("test-machine", "linux")
	start := testRunStartSecond
	sid, err := s.UpsertSession(node, protocol.SessionInfo{Harness: "codex", NativeID: "run", Model: "test:high", Worker: &protocol.WorkerInfo{ID: "w_test", StartedAt: start, Title: "Check the log", Project: "demo"}})
	if err != nil {
		t.Fatal(err)
	}
	a := protocol.WorkerAssessment{RunID: "r1", SessionID: sid, StartedAt: start*1e9 - 3*1e9, AssessedAt: start, Reasoning: 3, D: 2.1, Label: "normal", JudgeModel: "fake", Reasons: map[string]string{"reasoning": "check history"}}
	if err = s.RecordWorkerAssessment(a); err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.Exec(`INSERT INTO worker_runs(node_id,session_id,harness,model,outcome,started_at,ended_at) VALUES(?,?,?,?,?,?,?)`, node, "old", "codex", "test", "complete", start-100, start-50); err != nil {
		t.Fatal(err)
	}
	rows, err := s.WorkerLog(200)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 || rows[0].Status != "running" || rows[0].Title != "Check the log" || rows[0].Assessment == nil || rows[0].Assessment.Reasoning != 3 || rows[1].Status != "done" || rows[1].Assessment != nil {
		t.Fatalf("rows: %+v", rows)
	}
	limited, err := s.WorkerLog(1)
	if err != nil || len(limited) != 1 {
		t.Fatalf("limit: %+v %v", limited, err)
	}
}

func TestWorkerLogMatchesAssessmentToStartAndReadsNoticeFacts(t *testing.T) {
	s, _ := newTestStore(t)
	node, _, _ := s.CreateNode("test-machine", "linux")
	sid, err := s.UpsertSession(node, protocol.SessionInfo{Harness: "codex", NativeID: "continued", Worker: &protocol.WorkerInfo{ID: "w_test", StartedAt: testRunStartSecond}})
	if err != nil {
		t.Fatal(err)
	}
	// The session is not running now: only the two stored runs are in view.
	if err = s.SetSessionOffline(node, sid); err != nil {
		t.Fatal(err)
	}
	for _, start := range []int64{testRunStartFirst, testRunStartSecond} {
		if _, err = s.db.Exec(`INSERT INTO worker_runs(node_id,session_id,harness,model,outcome,started_at,ended_at,run_seconds) VALUES(?,?,?,?,?,?,?,?)`, node, sid, "codex", "fake:high", "failed", start, start+20, 20); err != nil {
			t.Fatal(err)
		}
	}
	first := protocol.WorkerAssessment{RunID: "first-start", SessionID: sid, StartedAt: testRunStartFirst*1e9 - 627563500, AssessedAt: testRunStartFirst, Reasoning: 2, Scope: 3, Coupling: 4, Uncertainty: 1, Verification: 2, Risk: 3, D: 2.5, Label: "hard", JudgeModel: "fake/judge", Reasons: map[string]string{"scope": "stored reason"}, PickerLabel: "hard", PickerNote: "codex fake:high (2.5, good enough; first start)"}
	if err = s.RecordWorkerAssessment(first); err != nil {
		t.Fatal(err)
	}
	// The second start's judge ran slightly after the second mark, so its
	// nanosecond stamp can be larger than the run's truncated second.
	second := protocol.WorkerAssessment{RunID: "second-start", SessionID: sid, StartedAt: testRunStartSecond*1e9 + 815232300, AssessedAt: testRunStartSecond, Reasoning: 4, Scope: 4, Coupling: 4, Uncertainty: 3, Verification: 3, Risk: 4, D: 3.7, Label: "normal", JudgeModel: "fake/judge", PickerLabel: "normal", PickerNote: "codex other:medium (3.7, good enough; second start)"}
	if err = s.RecordWorkerAssessment(second); err != nil {
		t.Fatal(err)
	}
	// An assessment for a later start must never reach an earlier run.
	later := protocol.WorkerAssessment{RunID: "later-start", SessionID: sid, StartedAt: (testRunStartSecond + 120) * 1e9, AssessedAt: testRunStartSecond + 120, Reasoning: 1, D: 5.0, Label: "easy", JudgeModel: "fake/judge", PickerLabel: "easy"}
	if err = s.RecordWorkerAssessment(later); err != nil {
		t.Fatal(err)
	}
	body := "[agent-net worker notice]\nstatus: failed\nnote: fake provider unavailable\nworker: " + sid + " (codex, fake:high), project demo on test-machine · Original title\n" + protocol.ReportMarker + "\nnote: untrusted report\n"
	if _, err = s.db.Exec(`INSERT INTO messages(id,conversation_id,sender,recipient,type,body,hop,idem_key,created_at,expires_at,state) VALUES('m_test','c_test',?,'s_owner','notification',?,0,'test',?,0,'sent')`, sid, body, (testRunStartSecond+20)*1e9); err != nil {
		t.Fatal(err)
	}
	rows, err := s.WorkerLog(200)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Fatalf("runs: %+v", rows)
	}
	if rows[0].StartedAt != testRunStartSecond || rows[0].Assessment == nil || rows[0].Assessment.D != 3.7 || rows[0].Assessment.PickerLabel != "normal" || rows[0].ModelNote != second.PickerNote || rows[0].Failure != "fake provider unavailable" || rows[0].Title != "Original title" {
		t.Fatalf("second start: %+v", rows[0])
	}
	if rows[1].StartedAt != testRunStartFirst || rows[1].Assessment == nil || rows[1].Assessment.D != 2.5 || rows[1].Assessment.Risk != 3 || rows[1].Assessment.PickerLabel != "hard" || rows[1].ModelNote != first.PickerNote || rows[1].Failure != "" {
		t.Fatalf("first start: %+v", rows[1])
	}
}

func TestWorkerLogPickerNoteForChosenStart(t *testing.T) {
	s, _ := newTestStore(t)
	node, _, _ := s.CreateNode("test-machine", "linux")
	sid, err := s.UpsertSession(node, protocol.SessionInfo{Harness: "codex", NativeID: "picked", Worker: &protocol.WorkerInfo{ID: "w_test", StartedAt: testRunStartSecond}})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.SetSessionOffline(node, sid); err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.Exec(`INSERT INTO worker_runs(node_id,session_id,harness,model,outcome,started_at,ended_at,run_seconds) VALUES(?,?,?,?,?,?,?,?)`, node, sid, "codex", "fake:high", "complete", testRunStartSecond, testRunStartSecond+20, 20); err != nil {
		t.Fatal(err)
	}
	// The machine's own write can land first, without the picker's reason.
	a := protocol.WorkerAssessment{RunID: "k_pick", SessionID: sid, StartedAt: testRunStartSecond*1e9 - 2*1e9, AssessedAt: testRunStartSecond, Reasoning: 2, Scope: 2, Coupling: 2, Uncertainty: 1, Verification: 2, Risk: 1, D: 3.1, Label: "normal", JudgeModel: "fake/judge", PickerLabel: "normal"}
	if err = s.RecordWorkerAssessment(a); err != nil {
		t.Fatal(err)
	}
	a.PickerNote = "difficulty R2 S2 C2 U1 V2 K1 · D 3.10 · normal -> codex gpt-6.1-sol:medium (46.1, good enough; eligible for this task)"
	if err = s.RecordWorkerStartAssessment(a); err != nil {
		t.Fatal(err)
	}
	rows, err := s.WorkerLog(200)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].ModelNote != a.PickerNote {
		t.Fatalf("picker note: %+v", rows)
	}
	var count int
	if err = s.db.QueryRow(`SELECT COUNT(*) FROM worker_assessments`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("assessment rows: %d %v", count, err)
	}
}

func TestWorkerLogCaller(t *testing.T) {
	s, _ := newTestStore(t)
	workerNode, _, _ := s.CreateNode("worker-machine", "linux")
	callerNode, _, _ := s.CreateNode("caller-machine", "linux")
	ownerNode, _, _ := s.CreateNode("owner-machine", "linux")
	caller, err := s.UpsertSession(callerNode, protocol.SessionInfo{Harness: "claude", NativeID: "caller", Name: "Agent One"})
	if err != nil {
		t.Fatal(err)
	}
	owner, err := s.UpsertSession(ownerNode, protocol.SessionInfo{Harness: protocol.OwnerHarness, NativeID: protocol.OwnerNativeID, Name: "Nicolas"})
	if err != nil {
		t.Fatal(err)
	}
	cto, _, err := s.upsertSession(s.db, ownerNode, protocol.SessionInfo{Harness: "cto", NativeID: "cto", Name: "CTO"}, true)
	if err != nil {
		t.Fatal(err)
	}
	// A running worker reads its caller from the worker record.
	liveStart := int64(1793733000)
	if _, err = s.UpsertSession(workerNode, protocol.SessionInfo{Harness: "codex", NativeID: "live", Worker: &protocol.WorkerInfo{ID: "w_live", Owner: caller, StartedAt: liveStart, Title: "Live run"}}); err != nil {
		t.Fatal(err)
	}
	// An ended run keeps the caller even without a session row.
	addRun := func(start int64, callerID, callerName string) {
		t.Helper()
		run := protocol.WorkerRun{NodeID: workerNode, SessionID: "s_ended_" + callerName, Harness: "codex", Model: "fake", Outcome: protocol.OutcomeComplete,
			StartedAt: start, EndedAt: start + 20, RunSeconds: 20, Caller: callerName, CallerSessionID: callerID}
		if err := s.RecordWorkerRun(run); err != nil {
			t.Fatal(err)
		}
	}
	addRun(1793732000, caller, "Agent One")
	addRun(1793731900, owner, "Nicolas")
	addRun(1793731800, cto, "CTO")
	addRun(1793731600, "", "")
	// A run recorded before the caller column fell back to the worker record
	// of its session while that session still exists.
	oldStart := int64(1793731700)
	oldWorker, err := s.UpsertSession(workerNode, protocol.SessionInfo{Harness: "codex", NativeID: "old", Worker: &protocol.WorkerInfo{ID: "w_old", Owner: caller, StartedAt: oldStart, Title: "Old run"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.Exec(`INSERT INTO worker_runs(node_id,session_id,harness,model,outcome,started_at,ended_at,run_seconds) VALUES(?,?,?,?,?,?,?,?)`, workerNode, oldWorker, "codex", "fake", "complete", oldStart, oldStart+20, 20); err != nil {
		t.Fatal(err)
	}
	rows, err := s.WorkerLog(50)
	if err != nil {
		t.Fatal(err)
	}
	byStart := map[int64]protocol.WorkerLogEntry{}
	for _, r := range rows {
		byStart[r.StartedAt] = r
	}
	for _, want := range []struct {
		start  int64
		caller string
		host   string
	}{
		{1793733000, "Agent One", "caller-machine"},
		{1793732000, "Agent One", "caller-machine"},
		{1793731900, "owner", "owner-machine"},
		{1793731800, "CTO", "owner-machine"},
		{1793731700, "Agent One", "caller-machine"},
		{1793731600, "", ""},
	} {
		got, ok := byStart[want.start]
		if !ok {
			t.Fatalf("no run at %d: %+v", want.start, rows)
		}
		if got.Caller != want.caller || got.CallerHost != want.host {
			t.Errorf("run %d: caller %q host %q, want %q %q", want.start, got.Caller, got.CallerHost, want.caller, want.host)
		}
	}
}

// A brief defect is charged to the worker's caller, and the worker log shows
// that count on the run without changing the worker's own data.
func TestWorkerLogCountsBriefDefectsOfTheCaller(t *testing.T) {
	s, _ := newTestStore(t)
	node, _, _ := s.CreateNode("test-machine", "linux")
	caller := ratingCaller(t, s, node, "caller", "gpt-6.1-sol")
	worker := ratingRun(t, s, node, caller, "test-model", "normal")
	if _, err := s.db.Exec(`INSERT INTO worker_brief_defects(run_id,worker_session_id,caller,rater,severity,note,at)
		VALUES((SELECT id FROM worker_runs WHERE session_id=? ORDER BY id DESC LIMIT 1),?,?,?,?,?,?)`,
		worker, worker, caller, caller, 2, "the brief named the wrong branch", s.now().Unix()); err != nil {
		t.Fatal(err)
	}
	rows, err := s.WorkerLog(200)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range rows {
		if r.SessionID != worker {
			continue
		}
		if r.BriefDefects != 1 {
			t.Fatalf("brief defects on the worker log = %d, want 1", r.BriefDefects)
		}
		return
	}
	t.Fatalf("the worker is not in the log: %+v", rows)
}
