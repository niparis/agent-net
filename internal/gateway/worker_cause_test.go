package gateway

import (
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// The recorded cause of failure and rework, per source (decision 0141).

// TestWorkerRunStoresEachCauseSource stores the cause the gateway reads from
// each source it already has: provider text, sandbox text, a harness end, a
// timeout, a failed test or accept step, a brief defect, or nothing known.
func TestWorkerRunStoresEachCauseSource(t *testing.T) {
	cases := []struct {
		name   string
		reason string
		detail string
		direct string
		want   string
	}{
		{name: "provider quota", reason: protocol.StopModelFails, detail: "HTTP 429 rate limit", want: protocol.CauseQuotaAuth},
		{name: "provider auth", reason: protocol.StopModelFails, detail: "401 not logged in", want: protocol.CauseQuotaAuth},
		{name: "sandbox denial", reason: protocol.StopRunFailed, detail: "operation not permitted", want: protocol.CauseSandbox},
		{name: "harness crash", reason: protocol.StopExited, detail: "panic: index out of range", want: protocol.CauseHarness},
		{name: "timeout", reason: protocol.StopIdle, want: protocol.CauseHarness},
		{name: "failed test step", direct: protocol.CauseTestFailure, want: protocol.CauseTestFailure},
		{name: "failed accept step", direct: protocol.CauseAcceptFinding, want: protocol.CauseAcceptFinding},
		{name: "brief defect", direct: protocol.CauseBriefDefect, want: protocol.CauseBriefDefect},
		{name: "nothing known", reason: protocol.StopHandback, want: protocol.CauseUnknown},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s, _ := newTestStore(t)
			cause := tc.direct
			if cause == "" {
				cause = protocol.WorkerRunCause(tc.reason, tc.detail)
			}
			if err := s.RecordWorkerRun(protocol.WorkerRun{NodeID: "n", SessionID: "s_w", Harness: "claude", Model: "m",
				Outcome: protocol.OutcomeFailed, Cause: cause}); err != nil {
				t.Fatal(err)
			}
			var got string
			if err := s.db.QueryRow(`SELECT cause FROM worker_runs WHERE session_id='s_w'`).Scan(&got); err != nil {
				t.Fatal(err)
			}
			if got != tc.want {
				t.Fatalf("stored cause = %q, want %q", got, tc.want)
			}
		})
	}
}

// The cause is set on the real worker.stopped path from the frame's reason
// and failure text.
func TestWorkerStoppedFrameStoresItsCause(t *testing.T) {
	a := newAgyGate(t)
	a.fail(t, "gemini-3.8-flash-low", "provider returned HTTP 429 rate limit")
	var cause string
	if err := a.g.store.db.QueryRow(`SELECT cause FROM worker_runs ORDER BY id DESC LIMIT 1`).Scan(&cause); err != nil {
		t.Fatal(err)
	}
	if cause != protocol.CauseQuotaAuth {
		t.Fatalf("run cause = %q, want %q", cause, protocol.CauseQuotaAuth)
	}
}

// A known cause is kept: a later signal only fills an empty or unknown one.
func TestAStoredCauseIsNotReplaced(t *testing.T) {
	s, _ := newTestStore(t)
	if err := s.RecordWorkerRun(protocol.WorkerRun{NodeID: "n", SessionID: "s_w", Harness: "claude", Model: "m",
		Outcome: protocol.OutcomeFailed, Cause: protocol.CauseQuotaAuth}); err != nil {
		t.Fatal(err)
	}
	if err := s.setWorkerRunCause("s_w", protocol.CauseBriefDefect); err != nil {
		t.Fatal(err)
	}
	var got string
	if err := s.db.QueryRow(`SELECT cause FROM worker_runs WHERE session_id='s_w'`).Scan(&got); err != nil {
		t.Fatal(err)
	}
	if got != protocol.CauseQuotaAuth {
		t.Fatalf("a known cause was replaced: %q", got)
	}
}

// A failed test step records a test failure on the worker's run and one
// defect that was caught before release.
func TestFailedTestStepStoresCauseAndCaughtDefect(t *testing.T) {
	r := newDeliveryRig(t)
	r.finishRun(r.worker, r.a)
	id := r.submit(r.a, r.workerSubmit("agent-net/w_1", commitA)).Change.Change.ID
	r.report1(protocol.StepRef{Step: protocol.StepTest}, id, commitA, protocol.StepResultFail,
		map[string]string{protocol.FieldSuite: protocol.SuiteChanged}, "TestParser fails")

	var cause string
	if err := r.s.db.QueryRow(`SELECT cause FROM worker_runs WHERE session_id=? ORDER BY id DESC LIMIT 1`, r.worker).Scan(&cause); err != nil {
		t.Fatal(err)
	}
	if cause != protocol.CauseTestFailure {
		t.Fatalf("run cause = %q, want %q", cause, protocol.CauseTestFailure)
	}
	var dcause, escape string
	if err := r.s.db.QueryRow(`SELECT cause, escape_phase FROM worker_defects WHERE session_id=?`, r.worker).Scan(&dcause, &escape); err != nil {
		t.Fatal(err)
	}
	if dcause != protocol.CauseTestFailure || escape != protocol.DefectCaught {
		t.Fatalf("defect cause/phase = %q/%q, want %q/%q", dcause, escape, protocol.CauseTestFailure, protocol.DefectCaught)
	}
}

// A failed accept step records a finding by the merger on the run.
func TestFailedAcceptStepStoresItsCause(t *testing.T) {
	r := newDeliveryRig(t)
	r.finishRun(r.worker, r.a)
	id := r.submit(r.a, r.workerSubmit("agent-net/w_1", commitA)).Change.Change.ID
	r.report1(stepAccept, id, commitA, protocol.StepResultFail, nil, "the parser still crashes")

	var cause string
	if err := r.s.db.QueryRow(`SELECT cause FROM worker_runs WHERE session_id=? ORDER BY id DESC LIMIT 1`, r.worker).Scan(&cause); err != nil {
		t.Fatal(err)
	}
	if cause != protocol.CauseAcceptFinding {
		t.Fatalf("run cause = %q, want %q", cause, protocol.CauseAcceptFinding)
	}
}

// An escaped defect is a severity 2 or higher find after release.
func TestEscapedDefectIsRecorded(t *testing.T) {
	r := newDeliveryRig(t)
	r.finishRun(r.worker, r.a)
	id := r.submit(r.a, r.workerSubmit("agent-net/w_1", commitA)).Change.Change.ID
	r.shipToVerify(id)
	r.report1(verifyAfterMerge, id, commitMerged, protocol.StepResultFail, nil, "the wake does nothing")

	var escape string
	if err := r.s.db.QueryRow(`SELECT escape_phase FROM worker_defects WHERE session_id=?`, r.worker).Scan(&escape); err != nil {
		t.Fatal(err)
	}
	if escape != protocol.DefectEscaped {
		t.Fatalf("escape = %q, want %q", escape, protocol.DefectEscaped)
	}
}

// A brief defect by its caller fills the run cause with brief_defect.
func TestBriefDefectStoresItsCause(t *testing.T) {
	s, _ := newTestStore(t)
	node, _, _ := s.CreateNode("mac", "linux")
	caller := ratingCaller(t, s, node, "caller", "claude-opus-5-5")
	wid := ratingRun(t, s, node, caller, "p/m", "hard")
	out, err := s.AddRating(protocol.RateFrame{RateRequest: protocol.RateRequest{
		Worker: wid, Kind: protocol.RateKindBriefDefect, Severity: 2, Note: "the brief named the wrong branch"}, Rater: caller})
	if err != nil {
		t.Fatal(err)
	}
	if !out.Accepted {
		t.Fatalf("brief defect not accepted: %+v", out)
	}
	var cause string
	if err := s.db.QueryRow(`SELECT cause FROM worker_runs WHERE session_id=?`, wid).Scan(&cause); err != nil {
		t.Fatal(err)
	}
	if cause != protocol.CauseBriefDefect {
		t.Fatalf("run cause = %q, want %q", cause, protocol.CauseBriefDefect)
	}
}

// The cause reaches the worker list that agent_workers_find detail full reads.
func TestWorkerListCarriesTheCause(t *testing.T) {
	s, _ := newTestStore(t)
	node, _, _ := s.CreateNode("mac", "linux")
	caller := ratingCaller(t, s, node, "caller", "claude-opus-5-5")
	wid := ratingRun(t, s, node, caller, "p/m", "hard")
	if _, err := s.db.Exec(`UPDATE worker_runs SET cause=? WHERE session_id=?`, protocol.CauseQuotaAuth, wid); err != nil {
		t.Fatal(err)
	}
	agents, err := s.Agents(protocol.QueryAgents{})
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range agents {
		if a.SessionID != wid || a.Worker == nil {
			continue
		}
		if a.Worker.Cause != protocol.CauseQuotaAuth {
			t.Fatalf("worker cause = %q, want %q", a.Worker.Cause, protocol.CauseQuotaAuth)
		}
		return
	}
	t.Fatalf("worker %s is not in the list", wid)
}

// The weekly view counts the causes of rework next to its rework columns.
func TestWeeklyScoresCountCauses(t *testing.T) {
	s, c := newTestStore(t)
	at := time.Date(2026, 11, 12, 12, 0, 0, 0, time.UTC).Unix()
	c.t = time.Unix(at, 0)
	if _, err := s.db.Exec(`INSERT INTO worker_runs (node_id, session_id, harness, model, outcome, ended_at, cause)
		VALUES ('n','s_a','claude','m','failed',?,?)`, at, protocol.CauseQuotaAuth); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`INSERT INTO worker_runs (node_id, session_id, harness, model, outcome, ended_at, cause)
		VALUES ('n','s_b','claude','m','failed',?,?)`, at, protocol.CauseTestFailure); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`INSERT INTO worker_defects (session_id, severity, at, cause, escape_phase)
		VALUES ('s_b', 1, ?, ?, ?)`, at, protocol.CauseTestFailure, protocol.DefectCaught); err != nil {
		t.Fatal(err)
	}
	rows, err := s.ScoresWeeklyAt(time.Unix(at, 0))
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range rows {
		if row.Week != isoWeekKey(at) {
			continue
		}
		want := protocol.CauseCounts{Machine: 1, Work: 1, Caught: 1}
		if row.Causes != want {
			t.Fatalf("causes = %+v, want %+v", row.Causes, want)
		}
		return
	}
	t.Fatalf("week %s is not in the table", isoWeekKey(at))
}
