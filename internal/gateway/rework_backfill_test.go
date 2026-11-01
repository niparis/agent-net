package gateway

import (
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

func legacyReworkRun(t *testing.T, s *Store, session, caller string) {
	t.Helper()
	if _, err := s.db.Exec(`INSERT INTO worker_runs (node_id,session_id,harness,model,outcome,ended_at,caller,difficulty) VALUES ('node',?,'codex','model','complete',?,?,'normal')`, session, s.now().Unix(), caller); err != nil {
		t.Fatal(err)
	}
}

func reworkMessage(t *testing.T, s *Store, from, to, typ, body string, at int) {
	t.Helper()
	id := fmt.Sprintf("m%d", at)
	if _, err := s.db.Exec(`INSERT INTO messages (id,conversation_id,sender,recipient,type,body,hop,idem_key,created_at,expires_at,state) VALUES (?,'conv',?,?,?,?,0,?,?,0,'delivered')`, id, from, to, typ, body, id, at); err != nil {
		t.Fatal(err)
	}
}

func TestReworkBackfill(t *testing.T) {
	for _, tc := range []struct {
		name, status, report string
		cont, fix            int
	}{
		{"done", "done", "complete: yes", 0, 2},
		{"idle", "idle without hand-back", "", 2, 0},
		{"partial", "partial", "", 2, 0},
		{"partial report", "done", "complete: no", 2, 0},
		{"failed", "failed", "", 0, 2},
		{"timeout", "timed out", "", 0, 2},
		{"started", "started", "", 0, 0},
		{"queued", "queued", "", 0, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, _ := newTestStore(t)
			if _, err := s.db.Exec(`DELETE FROM owner_settings WHERE key='rework_backfill_done'`); err != nil {
				t.Fatal(err)
			}
			legacyReworkRun(t, s, "worker", "caller")
			legacyReworkRun(t, s, "worker", "") // One task with two turns.
			legacyReworkRun(t, s, "unknown", "caller")
			reworkMessage(t, s, "caller", "worker", protocol.TypeRequest, "task", 1)
			reworkMessage(t, s, "caller", "worker", protocol.TypeRequest, "before hand-back", 2)
			notice := protocol.FormatWorkerNotice(protocol.WorkerNotice{Status: tc.status, Session: "worker"}, tc.report)
			reworkMessage(t, s, "worker", "caller", protocol.TypeReply, notice, 3)
			reworkMessage(t, s, "stranger", "worker", protocol.TypeRequest, "fix this", 4)
			reworkMessage(t, s, "caller", "worker", protocol.TypeNotification, "fix this", 5)
			reworkMessage(t, s, "caller", "worker", protocol.TypeRequest, "fix the failing test", 6)
			reworkMessage(t, s, "caller", "worker", protocol.TypeRequest, "carry on", 7)
			if err := s.backfillRework(); err != nil {
				t.Fatal(err)
			}
			var cont, fix, known int
			if err := s.db.QueryRow(`SELECT SUM(continue_count),SUM(fix_count),SUM(counters_known) FROM worker_runs WHERE session_id='worker'`).Scan(&cont, &fix, &known); err != nil {
				t.Fatal(err)
			}
			if cont != tc.cont || fix != tc.fix || known != 2 {
				t.Fatalf("got continue=%d fix=%d known=%d", cont, fix, known)
			}
			if err := s.db.QueryRow(`SELECT counters_known FROM worker_runs WHERE session_id='unknown'`).Scan(&known); err != nil || known != 0 {
				t.Fatalf("unknown = %d, %v", known, err)
			}
			cal, err := s.Calibration()
			if err != nil {
				t.Fatal(err)
			}
			if len(cal.Callers) != 1 || len(cal.Callers[0].Levels) != 1 {
				t.Fatalf("calibration: %+v", cal)
			}
			level := cal.Callers[0].Levels[0]
			if level.Tasks != 1 || level.Median != float64(tc.fix)+0.5*float64(tc.cont) {
				t.Fatalf("level: %+v", level)
			}
			// Even a newly inserted unknown session is not visited after the marker.
			legacyReworkRun(t, s, "later", "caller")
			reworkMessage(t, s, "caller", "later", protocol.TypeRequest, "task", 8)
			if err := s.backfillRework(); err != nil {
				t.Fatal(err)
			}
			if err := s.db.QueryRow(`SELECT counters_known FROM worker_runs WHERE session_id='later'`).Scan(&known); err != nil || known != 0 {
				t.Fatalf("second run visited new session = %d, %v", known, err)
			}
			if err := s.db.QueryRow(`SELECT SUM(continue_count),SUM(fix_count) FROM worker_runs WHERE session_id='worker'`).Scan(&cont, &fix); err != nil || cont != tc.cont || fix != tc.fix {
				t.Fatalf("second run: %d %d %v", cont, fix, err)
			}
		})
	}
}

func TestReworkBackfillAtOpenAndInferredCaller(t *testing.T) {
	path := filepath.Join(t.TempDir(), "gateway.db")
	s, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	legacyReworkRun(t, s, "worker", "")
	reworkMessage(t, s, "caller", "worker", protocol.TypeRequest, "task", 1)
	reworkMessage(t, s, "worker", "caller", protocol.TypeReply, protocol.FormatWorkerNotice(protocol.WorkerNotice{Status: "done", Session: "worker"}, ""), 2)
	reworkMessage(t, s, "caller", "worker", protocol.TypeRequest, "fix the failing test", 3)
	if _, err := s.db.Exec(`DELETE FROM owner_settings WHERE key='rework_backfill_done'`); err != nil {
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
	var caller string
	var fix, known int
	if err := s.db.QueryRow(`SELECT caller,fix_count,counters_known FROM worker_runs`).Scan(&caller, &fix, &known); err != nil {
		t.Fatal(err)
	}
	if caller != "caller" || fix != 1 || known != 1 {
		t.Fatalf("caller=%q fix=%d known=%d", caller, fix, known)
	}
}

func TestReworkBackfillTenThousandMessages(t *testing.T) {
	s, _ := newTestStore(t)
	if _, err := s.db.Exec(`DELETE FROM owner_settings WHERE key='rework_backfill_done'`); err != nil {
		t.Fatal(err)
	}
	tx, err := s.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2500; i++ {
		worker := fmt.Sprintf("worker%d", i)
		if _, err := tx.Exec(`INSERT INTO worker_runs (node_id,session_id,harness,outcome,ended_at) VALUES ('node',?,'codex','complete',1)`, worker); err != nil {
			t.Fatal(err)
		}
		for j := 0; j < 4; j++ {
			from, to, typ, body := "caller", worker, protocol.TypeRequest, "task"
			if j == 1 {
				from, to, typ, body = worker, "caller", protocol.TypeReply, protocol.FormatWorkerNotice(protocol.WorkerNotice{Status: "done", Session: worker}, "")
			}
			if _, err := tx.Exec(`INSERT INTO messages (id,conversation_id,sender,recipient,type,body,hop,idem_key,created_at,expires_at,state) VALUES (?,'conv',?,?,?,?,0,?,?,0,'delivered')`, fmt.Sprintf("m%d", i*4+j), from, to, typ, body, fmt.Sprintf("k%d", i*4+j), i*4+j); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	if err := s.backfillRework(); err != nil {
		t.Fatal(err)
	}
	elapsed := time.Since(start)
	t.Logf("backfilled 10,000 messages in %s", elapsed)
	if elapsed > 5*time.Second {
		t.Fatalf("backfill took %s", elapsed)
	}
	var known, fix int
	if err := s.db.QueryRow(`SELECT SUM(counters_known),SUM(fix_count) FROM worker_runs`).Scan(&known, &fix); err != nil || known != 2500 || fix != 5000 {
		t.Fatalf("known=%d fix=%d err=%v", known, fix, err)
	}
}

// Retention may have removed the original task while leaving the notice and
// follow-up. Establish the caller before replaying those remaining messages.
func TestReworkBackfillInfersCallerBeforeReplay(t *testing.T) {
	s, _ := newTestStore(t)
	if _, err := s.db.Exec(`DELETE FROM owner_settings WHERE key='rework_backfill_done'`); err != nil {
		t.Fatal(err)
	}
	legacyReworkRun(t, s, "worker", "")
	reworkMessage(t, s, "worker", "caller", protocol.TypeReply, protocol.FormatWorkerNotice(protocol.WorkerNotice{Status: "done", Session: "worker"}, ""), 1)
	reworkMessage(t, s, "caller", "worker", protocol.TypeRequest, "fix the failing test", 2)
	if err := s.backfillRework(); err != nil {
		t.Fatal(err)
	}
	var fix int
	if err := s.db.QueryRow(`SELECT fix_count FROM worker_runs`).Scan(&fix); err != nil || fix != 1 {
		t.Fatalf("fix=%d err=%v", fix, err)
	}
}

func TestReworkBackfillPreservesKnownSessionsAndStoredCaller(t *testing.T) {
	s, _ := newTestStore(t)
	if _, err := s.db.Exec(`DELETE FROM owner_settings WHERE key='rework_backfill_done'`); err != nil {
		t.Fatal(err)
	}
	legacyReworkRun(t, s, "worker", "caller")
	legacyReworkRun(t, s, "known", "caller")
	if _, err := s.db.Exec(`UPDATE worker_runs SET fix_count=7,counters_known=1 WHERE session_id='known'`); err != nil {
		t.Fatal(err)
	}
	reworkMessage(t, s, "stranger", "worker", protocol.TypeRequest, "task", 1)
	reworkMessage(t, s, "worker", "caller", protocol.TypeReply, protocol.FormatWorkerNotice(protocol.WorkerNotice{Status: "done", Session: "worker"}, ""), 2)
	if err := s.backfillRework(); err != nil {
		t.Fatal(err)
	}
	var known, fix int
	if err := s.db.QueryRow(`SELECT counters_known FROM worker_runs WHERE session_id='worker'`).Scan(&known); err != nil || known != 0 {
		t.Fatalf("no caller request: known=%d err=%v", known, err)
	}
	if err := s.db.QueryRow(`SELECT counters_known,fix_count FROM worker_runs WHERE session_id='known'`).Scan(&known, &fix); err != nil || known != 1 || fix != 7 {
		t.Fatalf("known session: known=%d fix=%d err=%v", known, fix, err)
	}
}

func TestReworkBackfillStoredCallerMessageMakesCountersKnown(t *testing.T) {
	s, _ := newTestStore(t)
	if _, err := s.db.Exec(`DELETE FROM owner_settings WHERE key='rework_backfill_done'`); err != nil {
		t.Fatal(err)
	}
	legacyReworkRun(t, s, "worker", "caller")
	reworkMessage(t, s, "caller", "worker", protocol.TypeReply, "task clarification", 1)
	if err := s.backfillRework(); err != nil {
		t.Fatal(err)
	}
	var known, cont, fix int
	if err := s.db.QueryRow(`SELECT counters_known,continue_count,fix_count FROM worker_runs`).Scan(&known, &cont, &fix); err != nil || known != 1 || cont != 0 || fix != 0 {
		t.Fatalf("known=%d continue=%d fix=%d err=%v", known, cont, fix, err)
	}
}

func TestReworkBackfillReplacesMixedLiveCounters(t *testing.T) {
	s, _ := newTestStore(t)
	if _, err := s.db.Exec(`DELETE FROM owner_settings WHERE key='rework_backfill_done'`); err != nil {
		t.Fatal(err)
	}
	legacyReworkRun(t, s, "worker", "caller")
	legacyReworkRun(t, s, "worker", "")
	if _, err := s.db.Exec(`UPDATE worker_runs SET counters_known=1,fix_count=1 WHERE id=(SELECT MAX(id) FROM worker_runs)`); err != nil {
		t.Fatal(err)
	}
	reworkMessage(t, s, "caller", "worker", protocol.TypeRequest, "task", 1)
	reworkMessage(t, s, "worker", "caller", protocol.TypeReply, protocol.FormatWorkerNotice(protocol.WorkerNotice{Status: "idle without hand-back", Session: "worker"}, ""), 2)
	reworkMessage(t, s, "caller", "worker", protocol.TypeRequest, "carry on", 3)
	reworkMessage(t, s, "worker", "caller", protocol.TypeNotification, protocol.FormatWorkerNotice(protocol.WorkerNotice{Status: "done", Session: "worker"}, ""), 4)
	reworkMessage(t, s, "worker", "caller", protocol.TypeNotification, protocol.FormatWorkerNotice(protocol.WorkerNotice{Status: "started", Session: "worker"}, ""), 5)
	reworkMessage(t, s, "caller", "worker", protocol.TypeRequest, "fix the failing test", 6)
	// Tied timestamps use insertion order, not random message IDs.
	if _, err := s.db.Exec(`UPDATE messages SET created_at=1`); err != nil {
		t.Fatal(err)
	}
	if err := s.backfillRework(); err != nil {
		t.Fatal(err)
	}
	var known, cont, fix int
	if err := s.db.QueryRow(`SELECT SUM(counters_known),SUM(continue_count),SUM(fix_count) FROM worker_runs`).Scan(&known, &cont, &fix); err != nil || known != 2 || cont != 1 || fix != 1 {
		t.Fatalf("known=%d continue=%d fix=%d err=%v", known, cont, fix, err)
	}
}
