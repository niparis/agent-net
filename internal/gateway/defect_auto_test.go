package gateway

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// Defects that agent-net records by itself from the delivery steps: a failed
// verify after merge, a failed test before merge, and a fix of a shipped
// change. Each is recorded once, and never on top of a manual report.

type autoDefectRow struct {
	session  string
	severity int
	note     string
}

func autoDefectRows(t *testing.T, s *Store) []autoDefectRow {
	t.Helper()
	rows, err := s.db.Query(`SELECT session_id, severity, note FROM worker_defects ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []autoDefectRow
	for rows.Next() {
		var x autoDefectRow
		if err := rows.Scan(&x.session, &x.severity, &x.note); err != nil {
			t.Fatal(err)
		}
		out = append(out, x)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

func autoDefectLog(t *testing.T, s *Store) []string {
	t.Helper()
	entries, err := s.DeskLog(100)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, e := range entries {
		if strings.HasPrefix(e.Text, "defect recorded:") {
			out = append(out, e.Text)
		}
	}
	return out
}

// finishRun gives a worker session the finished run its hand-back leaves.
func (r *deliveryRig) finishRun(session, node string) {
	r.t.Helper()
	if err := r.s.RecordWorkerRun(protocol.WorkerRun{NodeID: node, SessionID: session, Harness: "claude", Model: "claude-opus-5-5",
		Outcome: protocol.OutcomeComplete, EndedAt: r.s.now().Unix(), Caller: r.caller, Difficulty: "normal"}); err != nil {
		r.t.Fatal(err)
	}
}

// reportBy is the session that reports steps in these tests, when it is not the caller.
var reportBy = map[*deliveryRig]string{}

func (r *deliveryRig) report1(ref protocol.StepRef, id, commit, result string, fields map[string]string, note string) {
	r.t.Helper()
	by := r.caller
	if other := reportBy[r]; other != "" {
		by = other
	}
	r.mustReport(r.a, protocol.StepReportRequest{SessionID: by, ChangeID: id, StepRef: ref, Result: result, Commit: commit, Fields: fields, Note: note})
}

// shipToVerify takes a change through merge and release, up to its verify.
func (r *deliveryRig) shipToVerify(id string) {
	r.t.Helper()
	r.report1(stepAccept, id, commitA, protocol.StepResultPass, nil, "")
	r.report1(protocol.StepRef{Step: protocol.StepTest}, id, commitA, protocol.StepResultPass, map[string]string{protocol.FieldSuite: protocol.SuiteChanged}, "")
	r.report1(protocol.StepRef{Step: protocol.StepMerge}, id, commitA, protocol.StepResultPass, map[string]string{protocol.FieldMergedCommit: commitMerged}, "")
	r.report1(protocol.StepRef{Step: protocol.StepTest, AfterMerge: true}, id, commitMerged, protocol.StepResultPass, map[string]string{protocol.FieldSuite: protocol.SuiteFull}, "")
	r.report1(protocol.StepRef{Step: protocol.StepRelease, AfterMerge: true}, id, commitMerged, protocol.StepResultPass, map[string]string{protocol.FieldTag: "v1.2.3"}, "")
}

var verifyAfterMerge = protocol.StepRef{Step: protocol.StepVerify, AfterMerge: true}

func TestVerifyFailAfterMergeRecordsSeverityThree(t *testing.T) {
	r := newDeliveryRig(t)
	r.finishRun(r.worker, r.a)
	id := r.submit(r.a, r.workerSubmit("agent-net/w_1", commitA)).Change.Change.ID
	r.shipToVerify(id)

	r.report1(verifyAfterMerge, id, commitMerged, protocol.StepResultFail, nil, "the wake on message does nothing")
	rows := autoDefectRows(t, r.s)
	if len(rows) != 1 || rows[0].session != r.worker || rows[0].severity != 3 || !strings.Contains(rows[0].note, "the wake on message does nothing") {
		t.Fatalf("defects after a failed verify: %+v", rows)
	}
	log := autoDefectLog(t, r.s)
	want := "defect recorded: " + r.worker + " sev 3 on " + id + " (verify failed)"
	if len(log) != 1 || log[0] != want {
		t.Fatalf("desk log %q, want %q", log, want)
	}

	// The same step failing again on the same revision is the same defect.
	r.report1(verifyAfterMerge, id, commitMerged, protocol.StepResultFail, nil, "still broken")
	if rows := autoDefectRows(t, r.s); len(rows) != 1 {
		t.Fatalf("a second failed verify on one revision made %d defects", len(rows))
	}
}

func TestVerifyFailTakesTheSeverityOfTheReport(t *testing.T) {
	r := newDeliveryRig(t)
	id := r.submit(r.a, r.workerSubmit("agent-net/w_1", commitA)).Change.Change.ID
	r.shipToVerify(id)
	r.report1(verifyAfterMerge, id, commitMerged, protocol.StepResultFail, map[string]string{protocol.FieldSeverity: "2"}, "graph lead label is wrong")
	rows := autoDefectRows(t, r.s)
	if len(rows) != 1 || rows[0].severity != 2 {
		t.Fatalf("defects: %+v", rows)
	}
}

func TestVerifyFailOfAChangeWithoutAWorkerRecordsNothing(t *testing.T) {
	r := newDeliveryRig(t)
	q := r.workerSubmit("feature", commitA)
	q.SessionID = r.sonnet
	q.Origin = protocol.ChangeOrigin{Kind: protocol.OriginSession, SessionID: r.sonnet}
	id := r.submit(r.a, q).Change.Change.ID
	reportBy[r] = r.sonnet
	t.Cleanup(func() { delete(reportBy, r) })
	r.shipToVerify(id)
	r.report1(verifyAfterMerge, id, commitMerged, protocol.StepResultFail, nil, "broken")
	if rows := autoDefectRows(t, r.s); len(rows) != 0 {
		t.Fatalf("a session's change made defects: %+v", rows)
	}
}

func TestTestFailBeforeMergeRecordsSeverityOnePerRevisionAgainstItsWorker(t *testing.T) {
	r := newDeliveryRig(t)
	second, err := r.s.UpsertSession(r.a, protocol.SessionInfo{Harness: "claude", NativeID: "worker2", Model: "claude-opus-5-5",
		Worker: &protocol.WorkerInfo{ID: "w_3", Owner: r.caller}})
	if err != nil {
		t.Fatal(err)
	}
	id := r.submit(r.a, r.workerSubmit("agent-net/w_1", commitA)).Change.Change.ID
	test := protocol.StepRef{Step: protocol.StepTest}
	suite := map[string]string{protocol.FieldSuite: protocol.SuiteChanged}

	// Accept and review failures are normal iteration.
	r.report1(stepAccept, id, commitA, protocol.StepResultFail, nil, "needs work")
	if rows := autoDefectRows(t, r.s); len(rows) != 0 {
		t.Fatalf("a failed accept made defects: %+v", rows)
	}

	r.report1(test, id, commitA, protocol.StepResultFail, suite, "TestFoo fails")
	r.report1(test, id, commitA, protocol.StepResultFail, suite, "TestFoo still fails")
	rows := autoDefectRows(t, r.s)
	if len(rows) != 1 || rows[0].session != r.worker || rows[0].severity != 1 {
		t.Fatalf("defects after a failed test: %+v", rows)
	}
	if log := autoDefectLog(t, r.s); len(log) != 1 || log[0] != "defect recorded: "+r.worker+" sev 1 on "+id+" (test failed)" {
		t.Fatalf("desk log %q", log)
	}

	// A new revision by another worker: its failure is its own.
	q := r.workerSubmit("agent-net/w_1", commitB)
	q.Origin = protocol.ChangeOrigin{Kind: protocol.OriginWorker, WorkerID: "w_3", SessionID: second}
	if got := r.submit(r.a, q).Change.Change; got.ID != id || got.Revision != 2 {
		t.Fatalf("second revision: %+v", got)
	}
	r.report1(test, id, commitB, protocol.StepResultFail, suite, "TestBar fails")
	rows = autoDefectRows(t, r.s)
	if len(rows) != 2 || rows[1].session != second || rows[1].severity != 1 {
		t.Fatalf("defects after the second revision failed: %+v", rows)
	}
}

func TestTestFailMarkedAsFlakeRecordsNothing(t *testing.T) {
	r := newDeliveryRig(t)
	id := r.submit(r.a, r.workerSubmit("agent-net/w_1", commitA)).Change.Change.ID
	r.report1(protocol.StepRef{Step: protocol.StepTest}, id, commitA, protocol.StepResultFail,
		map[string]string{protocol.FieldSuite: protocol.SuiteChanged, protocol.FieldFlake: protocol.FieldYes}, "known flake")
	if rows := autoDefectRows(t, r.s); len(rows) != 0 {
		t.Fatalf("a flake made defects: %+v", rows)
	}
	if log := autoDefectLog(t, r.s); len(log) != 0 {
		t.Fatalf("a flake wrote the log: %q", log)
	}
}

func TestFixesRecordsADefectOnceAgainstTheFixedWorker(t *testing.T) {
	r := newDeliveryRig(t)
	r.finishRun(r.worker, r.a)
	shipped := r.submit(r.a, r.workerSubmit("agent-net/w_1", commitA)).Change.Change.ID
	r.shipToVerify(shipped)

	fix := r.workerSubmit("agent-net/w_fix", commitB)
	fix.SessionID = r.sonnet
	fix.Origin = protocol.ChangeOrigin{Kind: protocol.OriginSession, SessionID: r.sonnet}
	fix.Fixes = shipped
	r.submit(r.a, fix)
	rows := autoDefectRows(t, r.s)
	if len(rows) != 1 || rows[0].session != r.worker || rows[0].severity != 2 {
		t.Fatalf("defects after a fix: %+v", rows)
	}
	if log := autoDefectLog(t, r.s); len(log) != 1 || log[0] != "defect recorded: "+r.worker+" sev 2 on "+shipped+" (fixed by a later change)" {
		t.Fatalf("desk log %q", log)
	}

	// Another submit of the fix, and another fix of the same change, add nothing.
	r.submit(r.a, fix)
	other := r.workerSubmit("agent-net/w_fix2", commitC)
	other.SessionID, other.Origin = fix.SessionID, fix.Origin
	other.Fixes, other.FixesSeverity = shipped, 4
	r.submit(r.a, other)
	if rows := autoDefectRows(t, r.s); len(rows) != 1 {
		t.Fatalf("a repeated fix made %d defects", len(rows))
	}
}

func TestFixesSeverityComesFromTheBrief(t *testing.T) {
	r := newDeliveryRig(t)
	shipped := r.submit(r.a, r.workerSubmit("agent-net/w_1", commitA)).Change.Change.ID
	if got, err := r.s.RecordFixDefect(shipped, 4); err != nil || got {
		t.Fatalf("a fix of a change that is not merged recorded a defect: %v %v", got, err)
	}
	r.shipToVerify(shipped)
	got, err := r.s.RecordFixDefect(shipped, 4)
	if err != nil || !got {
		t.Fatalf("RecordFixDefect: %v %v", got, err)
	}
	rows := autoDefectRows(t, r.s)
	if len(rows) != 1 || rows[0].severity != 4 {
		t.Fatalf("defects: %+v", rows)
	}
	if again, err := r.s.RecordFixDefect(shipped, 4); err != nil || again {
		t.Fatalf("a second RecordFixDefect recorded again: %v %v", again, err)
	}
	if _, err := r.s.RecordFixDefect("ch_unknown", 2); err == nil {
		t.Fatal("a fix of an unknown change was accepted")
	}
}

// A fix of a change whose failed verify was already recorded is the same defect.
func TestFixesAfterARecordedVerifyFailAddsNothing(t *testing.T) {
	r := newDeliveryRig(t)
	id := r.submit(r.a, r.workerSubmit("agent-net/w_1", commitA)).Change.Change.ID
	r.shipToVerify(id)
	r.report1(verifyAfterMerge, id, commitMerged, protocol.StepResultFail, nil, "broken")
	if again, err := r.s.RecordFixDefect(id, 2); err != nil || again {
		t.Fatalf("RecordFixDefect after a failed verify: %v %v", again, err)
	}
	if rows := autoDefectRows(t, r.s); len(rows) != 1 {
		t.Fatalf("defects: %+v", rows)
	}
}

// A defect the caller filed by hand in the last hour is not doubled.
func TestAutoDefectDoesNotDoubleAManualOne(t *testing.T) {
	r := newDeliveryRig(t)
	r.finishRun(r.worker, r.a)
	id := r.submit(r.a, r.workerSubmit("agent-net/w_1", commitA)).Change.Change.ID
	r.shipToVerify(id)

	out, err := r.s.AddRating(defectFrame(r.worker, r.caller, "filed by hand"))
	if err != nil || !out.Accepted {
		t.Fatalf("manual defect: %+v %v", out, err)
	}
	r.clk.t = r.clk.t.Add(30 * time.Minute)
	r.report1(verifyAfterMerge, id, commitMerged, protocol.StepResultFail, nil, "broken")
	rows := autoDefectRows(t, r.s)
	if len(rows) != 1 || rows[0].note != "filed by hand" {
		t.Fatalf("defects: %+v", rows)
	}
	if log := autoDefectLog(t, r.s); len(log) != 0 {
		t.Fatalf("the skipped defect was logged: %q", log)
	}
}

// A manual defect older than an hour does not hide a new failure.
func TestAutoDefectRecordsWhenTheManualOneIsOld(t *testing.T) {
	r := newDeliveryRig(t)
	r.finishRun(r.worker, r.a)
	id := r.submit(r.a, r.workerSubmit("agent-net/w_1", commitA)).Change.Change.ID
	r.shipToVerify(id)
	if out, err := r.s.AddRating(defectFrame(r.worker, r.caller, "older")); err != nil || !out.Accepted {
		t.Fatalf("manual defect: %+v %v", out, err)
	}
	r.clk.t = r.clk.t.Add(2 * time.Hour)
	r.report1(verifyAfterMerge, id, commitMerged, protocol.StepResultFail, nil, "broken")
	if rows := autoDefectRows(t, r.s); len(rows) != 2 {
		t.Fatalf("defects: %+v", rows)
	}
}

// A manual report after an automatic one still counts: manual calls keep working.
func TestManualDefectStillWorksAfterAnAutomaticOne(t *testing.T) {
	r := newDeliveryRig(t)
	r.finishRun(r.worker, r.a)
	id := r.submit(r.a, r.workerSubmit("agent-net/w_1", commitA)).Change.Change.ID
	r.shipToVerify(id)
	r.report1(verifyAfterMerge, id, commitMerged, protocol.StepResultFail, nil, "broken")
	if out, err := r.s.AddRating(defectFrame(r.worker, r.caller, "another bug")); err != nil || !out.Accepted {
		t.Fatalf("manual defect: %+v %v", out, err)
	}
	if rows := autoDefectRows(t, r.s); len(rows) != 2 {
		t.Fatalf("defects: %+v", rows)
	}
}

// A worker start that names something that is not a change is refused before
// anything starts.
func TestWorkerStartRefusesABadFixes(t *testing.T) {
	r := newDeliveryRig(t)
	for _, q := range []protocol.WorkerStartFrame{
		{WorkerStartRequest: protocol.WorkerStartRequest{Project: "demo", Task: "fix it", Fixes: "not-a-change"}, Owner: r.caller},
		{WorkerStartRequest: protocol.WorkerStartRequest{Project: "demo", Task: "fix it", Fixes: "ch_x", FixesSeverity: 9}, Owner: r.caller},
		{WorkerStartRequest: protocol.WorkerStartRequest{Project: "demo", Task: "fix it", FixesSeverity: 3}, Owner: r.caller},
	} {
		raw, err := json.Marshal(q)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := r.g.handleWorker(context.Background(), &nodeConn{id: r.a}, "worker.start", raw); err == nil || !strings.Contains(err.Error(), "fixes") {
			t.Errorf("start %+v: %v", q.WorkerStartRequest, err)
		}
	}
}
