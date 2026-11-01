package gateway

import (
	"fmt"
	"path/filepath"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

// Backfill is exercised by reopening, just as it is on gateway startup.
func TestTaskReworkBackfillLinksChainAndCountsMidrun(t *testing.T) {
	path := filepath.Join(t.TempDir(), "gateway.db")
	s, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	now := s.now().Unix()
	for _, r := range []struct{ sid, outcome string }{{"root", "stopped"}, {"second", "partial"}, {"third", "complete"}, {"alone", "failed"}} {
		if err := s.RecordWorkerRun(protocol.WorkerRun{NodeID: "node", SessionID: r.sid, Harness: "codex", Model: "gpt-terra", Outcome: r.outcome, Caller: "caller", Difficulty: "normal", EndedAt: now}); err != nil {
			t.Fatal(err)
		}
	}
	// One continuation has several runs: each row needs the same task ID.
	if err := s.RecordWorkerRun(protocol.WorkerRun{NodeID: "node", SessionID: "third", Harness: "codex", Model: "gpt-sol", Outcome: "complete", Caller: "caller", Difficulty: "hard", EndedAt: now}); err != nil {
		t.Fatal(err)
	}
	brief := func(task string) string { return "[agent-net worker brief]\nsetup\nTask:\n" + task }
	intro := "You continue the work of an earlier worker that ended (stopped). Finish it.\n\n"
	reworkMessage(t, s, "caller", "root", protocol.TypeRequest, brief("build a useful feature"), 1)
	reworkMessage(t, s, "caller", "root", protocol.TypeRequest, "one clarification", 2)
	reworkMessage(t, s, "root", "caller", protocol.TypeReply, protocol.FormatWorkerNotice(protocol.WorkerNotice{Status: "done"}, "complete: no"), 3)
	reworkMessage(t, s, "caller", "root", protocol.TypeRequest, "after notice", 4)
	reworkMessage(t, s, "caller", "root", protocol.TypeRequest, "another clarification", 5)
	reworkMessage(t, s, "caller", "second", protocol.TypeRequest, brief(intro+"build a useful feature\n\nAlso: tests"), 6)
	reworkMessage(t, s, "caller", "third", protocol.TypeRequest, brief(intro+"build a useful feature\n\nAlso: more tests"), 7)
	reworkMessage(t, s, "caller", "alone", protocol.TypeRequest, brief("another task"), 8)
	reworkMessage(t, s, "stranger", "alone", protocol.TypeRequest, "not the caller", 9)
	reworkMessage(t, s, "caller", "alone", protocol.TypeNotification, "not a request", 10)
	if _, err := s.db.Exec(`DELETE FROM owner_settings WHERE key='task_rework_backfill_done'`); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	check := func() {
		t.Helper()
		for _, want := range []struct {
			sid, root            string
			cont, fix, mid, rows int
		}{{"root", "", 0, 0, 2, 1}, {"second", "root", 0, 1, 0, 1}, {"third", "root", 1, 0, 0, 2}, {"alone", "", 0, 0, 0, 1}} {
			var root string
			var cont, fix, mid, n int
			err := s.db.QueryRow(`SELECT MIN(task_id),SUM(continue_count),SUM(fix_count),SUM(midrun_count),COUNT(*) FROM worker_runs WHERE session_id=?`, want.sid).Scan(&root, &cont, &fix, &mid, &n)
			if err != nil {
				t.Fatal(err)
			}
			if root != want.root || cont != want.cont || fix != want.fix || mid != want.mid || n != want.rows {
				t.Errorf("%s root=%q cont=%d fix=%d mid=%d rows=%d want %+v", want.sid, root, cont, fix, mid, n, want)
			}
			var mismatches int
			if err := s.db.QueryRow(`SELECT COUNT(*) FROM worker_runs WHERE session_id=? AND task_id<>?`, want.sid, want.root).Scan(&mismatches); err != nil || mismatches != 0 {
				t.Fatalf("inconsistent task id: %d %v", mismatches, err)
			}
		}
	}
	check()
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	check()
}

func TestTaskQualityAndCalibrationGroupStoppedContinuation(t *testing.T) {
	s, _ := newTestStore(t)
	// Add the columns to an old schema too, so this test exposes grouping before
	// the migration exists rather than failing on a missing column.
	for _, c := range []struct{ name, spec string }{{"task_id", "TEXT NOT NULL DEFAULT ''"}, {"midrun_count", "INTEGER NOT NULL DEFAULT 0"}} {
		var n int
		if err := s.db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('worker_runs') WHERE name=?`, c.name).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n == 0 {
			if _, err := s.db.Exec(fmt.Sprintf("ALTER TABLE worker_runs ADD COLUMN %s %s", c.name, c.spec)); err != nil {
				t.Fatal(err)
			}
		}
	}
	now := s.now().Unix()
	for _, r := range []struct {
		sid, root, model, harness, difficulty, outcome string
		fix, cont, mid                                 int
	}{
		{"root", "", "gpt-terra:high", "codex", "hard", "stopped", 1, 1, 1},
		{"second", "root", "gpt-sol:low", "opencode", "normal", "complete", 1, 0, 2},
		{"third", "root", "other", "pi", "simple", "complete", 0, 1, 3},
		{"stopped", "", "gpt-luna", "codex", "normal", "stopped", 9, 9, 9},
	} {
		_, err := s.db.Exec(`INSERT INTO worker_runs(node_id,session_id,task_id,model,harness,difficulty,outcome,ended_at,caller,counters_known,fix_count,continue_count,midrun_count) VALUES ('node',?,?,?,?,?,?,?,'caller',1,?,?,?)`, r.sid, r.root, r.model, r.harness, r.difficulty, r.outcome, now, r.fix, r.cont, r.mid)
		if err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.db.Exec(`INSERT INTO worker_defects(session_id,severity,at) VALUES('third',2,?)`, now); err != nil {
		t.Fatal(err)
	}
	if err := s.refreshQuality(); err != nil {
		t.Fatal(err)
	}
	q := s.QualityAnalysis()
	if len(q.Levels) != 1 || q.Levels[0].Difficulty != "hard" || len(q.Levels[0].Families) != 1 {
		t.Fatalf("task must use root difficulty and family: %+v", q.Levels)
	}
	f := q.Levels[0].Families[0]
	if f.Family != "terra" || f.Tasks != 1 || f.Fixes != 2 || f.Continues != 2 || f.Midrun != 6 || f.LateDefects != 1 || f.Headache != 54 {
		t.Fatalf("task totals: %+v", f)
	}
	if q.ReworkAverage != protocol.ReworkWeight(4, 2) {
		t.Errorf("fit rework %v", q.ReworkAverage)
	}
	cal, err := s.Calibration()
	if err != nil {
		t.Fatal(err)
	}
	if len(cal.Callers) != 1 || len(cal.Callers[0].Levels) != 1 || cal.Callers[0].Levels[0].Tasks != 1 || cal.Callers[0].Levels[0].Median != protocol.ReworkWeight(4, 2) {
		t.Fatalf("calibration: %+v", cal.Callers)
	}
}

func TestTaskReworkBackfillContinuationOutcomes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "gateway.db")
	s, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	for i, outcome := range []string{protocol.OutcomePartial, protocol.OutcomeFailed, protocol.OutcomeStopped, protocol.OutcomeTimedOut, protocol.OutcomeComplete} {
		root, next, caller := fmt.Sprintf("root%d", i), fmt.Sprintf("next%d", i), fmt.Sprintf("caller%d", i)
		for _, r := range []struct{ sid, outcome string }{{root, outcome}, {next, protocol.OutcomeComplete}} {
			if err := s.RecordWorkerRun(protocol.WorkerRun{NodeID: "node", SessionID: r.sid, Harness: "codex", Model: "model", Caller: caller, Outcome: r.outcome}); err != nil {
				t.Fatal(err)
			}
		}
		task := fmt.Sprintf("unique task %d", i)
		reworkMessage(t, s, caller, root, protocol.TypeRequest, "[agent-net worker brief]\nTask:\n"+task, 10*i+1)
		reworkMessage(t, s, caller, next, protocol.TypeRequest, "[agent-net worker brief]\nTask:\nYou continue the work of an earlier worker that ended (done).\n\n"+task, 10*i+2)
	}
	if _, err := s.db.Exec(`DELETE FROM owner_settings WHERE key='task_rework_backfill_done'`); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	for i, outcome := range []string{protocol.OutcomePartial, protocol.OutcomeFailed, protocol.OutcomeStopped, protocol.OutcomeTimedOut, protocol.OutcomeComplete} {
		var taskID string
		var cont, fix int
		if err := s.db.QueryRow(`SELECT task_id,continue_count,fix_count FROM worker_runs WHERE session_id=?`, fmt.Sprintf("next%d", i)).Scan(&taskID, &cont, &fix); err != nil {
			t.Fatal(err)
		}
		wantCont, wantFix := 0, 1
		if outcome == protocol.OutcomePartial {
			wantCont, wantFix = 1, 0
		}
		if taskID != fmt.Sprintf("root%d", i) || cont != wantCont || fix != wantFix {
			t.Errorf("%s: task=%q continue=%d fix=%d", outcome, taskID, cont, fix)
		}
	}
}

func TestTaskReworkBackfillUsesOutcomeAtContinuation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "gateway.db")
	s, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range []struct {
		sid, outcome string
		ended        int64
	}{{"root", "partial", 1}, {"root", "complete", 3}, {"next", "complete", 4}} {
		if err := s.RecordWorkerRun(protocol.WorkerRun{NodeID: "node", SessionID: r.sid, Harness: "codex", Model: "model", Caller: "caller", Outcome: r.outcome, EndedAt: r.ended}); err != nil {
			t.Fatal(err)
		}
	}
	reworkMessage(t, s, "caller", "root", protocol.TypeRequest, "[agent-net worker brief]\nTask:\nwork", 500000000)
	reworkMessage(t, s, "caller", "next", protocol.TypeRequest, "[agent-net worker brief]\nTask:\nYou continue the work of an earlier worker that ended (handback).\n\nwork", 2000000000)
	if _, err := s.db.Exec(`DELETE FROM owner_settings WHERE key='task_rework_backfill_done'`); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	var cont, fix int
	if err := s.db.QueryRow(`SELECT continue_count,fix_count FROM worker_runs WHERE session_id='next'`).Scan(&cont, &fix); err != nil {
		t.Fatal(err)
	}
	if cont != 1 || fix != 0 {
		t.Fatalf("continued the earlier partial run: continue=%d fix=%d", cont, fix)
	}
}

func TestTaskQualityUsesFirstRunByEndTime(t *testing.T) {
	s, _ := newTestStore(t)
	now := s.now().Unix()
	for _, r := range []struct {
		model, difficulty string
		ended             int64
	}{{"gpt-sol", "hard", now + 10}, {"gpt-terra", "normal", now}} {
		if err := s.RecordWorkerRun(protocol.WorkerRun{NodeID: "node", SessionID: "root", Harness: "codex", Model: r.model, Difficulty: r.difficulty, Caller: "caller", Outcome: protocol.OutcomeComplete, EndedAt: r.ended}); err != nil {
			t.Fatal(err)
		}
	}
	q := s.QualityAnalysis()
	if len(q.Levels) != 1 || q.Levels[0].Difficulty != "normal" || len(q.Levels[0].Families) != 1 || q.Levels[0].Families[0].Family != "terra" {
		t.Fatalf("first root run was not used: %+v", q.Levels)
	}
}

func TestTaskQualityKeepsNonStoppedUnknownCounters(t *testing.T) {
	s, _ := newTestStore(t)
	now := s.now().Unix()
	if _, err := s.db.Exec(`INSERT INTO worker_runs(node_id,session_id,caller,model,harness,difficulty,outcome,ended_at,counters_known) VALUES('node','root','caller','gpt-terra','codex','normal','complete',?,0)`, now); err != nil {
		t.Fatal(err)
	}
	if err := s.refreshQuality(); err != nil {
		t.Fatal(err)
	}
	q := s.QualityAnalysis()
	if len(q.Levels) != 1 || q.Levels[0].Tasks != 1 {
		t.Fatalf("non-stopped task was excluded: %+v", q.Levels)
	}
	cal, err := s.Calibration()
	if err != nil {
		t.Fatal(err)
	}
	if len(cal.Callers) != 1 || cal.Callers[0].Levels[0].Tasks != 1 {
		t.Fatalf("non-stopped task missing from calibration: %+v", cal.Callers)
	}
}
