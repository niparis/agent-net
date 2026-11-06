package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

// A worker that ended keeps the ended state when its machine still lists its
// harness session for a moment. The session sync of that race used to make the
// row online and busy again, and the removal that followed kept busy.
func TestEndedWorkerStaysEndedWhenItsSessionSyncsAgain(t *testing.T) {
	s, _ := newTestStore(t)
	node, _, _ := s.CreateNode("mac", "linux")
	in := protocol.SessionInfo{Harness: "opencode", NativeID: "worker", Status: "busy",
		Worker: &protocol.WorkerInfo{ID: "w_test"}}
	id, err := s.UpsertSession(node, in)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.RecordWorkerRun(protocol.WorkerRun{NodeID: node, SessionID: id, Harness: "opencode", Outcome: protocol.OutcomeComplete}); err != nil {
		t.Fatal(err)
	}
	if err := s.SetWorkerSessionEnded(node, id); err != nil {
		t.Fatal(err)
	}
	// The machine's next session sync still has the worker's harness session,
	// with the busy status it had before the hand-back.
	if _, err := s.UpsertSession(node, in); err != nil {
		t.Fatal(err)
	}
	a, ok, err := s.Agent(id)
	if err != nil || !ok {
		t.Fatalf("session: %v %v", ok, err)
	}
	if a.Online || a.Status != "ended" {
		t.Fatalf("after a late session sync: online=%v status=%q", a.Online, a.Status)
	}
	// The machine then removes the session. The end state must survive.
	if err := s.SetSessionOffline(node, id); err != nil {
		t.Fatal(err)
	}
	a, _, _ = s.Agent(id)
	if a.Status != "ended" {
		t.Errorf("after removal: %q", a.Status)
	}
}

// An offline worker whose run was recorded as finished is ended, even when an
// older version of the gateway left its status busy.
func TestOfflineEndsAWorkerWhoseRunIsOver(t *testing.T) {
	s, _ := newTestStore(t)
	node, _, _ := s.CreateNode("mac", "linux")
	in := protocol.SessionInfo{Harness: "codex", NativeID: "worker", Status: "busy",
		Worker: &protocol.WorkerInfo{ID: "w_test"}}
	id, err := s.UpsertSession(node, in)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.RecordWorkerRun(protocol.WorkerRun{NodeID: node, SessionID: id, Harness: "codex", Outcome: protocol.OutcomeStopped}); err != nil {
		t.Fatal(err)
	}
	if err := s.SetSessionOffline(node, id); err != nil {
		t.Fatal(err)
	}
	a, _, _ := s.Agent(id)
	if a.Online || a.Status != "ended" {
		t.Fatalf("finished worker offline: online=%v status=%q", a.Online, a.Status)
	}
}

// A worker that only lost its connection is still busy: its run is not over.
func TestOfflineKeepsAWorkerWithAnActiveRunBusy(t *testing.T) {
	s, _ := newTestStore(t)
	node, _, _ := s.CreateNode("mac", "linux")
	in := protocol.SessionInfo{Harness: "codex", NativeID: "worker", Status: "busy",
		Worker: &protocol.WorkerInfo{ID: "w_test"}}
	id, err := s.UpsertSession(node, in)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetSessionOffline(node, id); err != nil {
		t.Fatal(err)
	}
	a, _, _ := s.Agent(id)
	if a.Status != "busy" {
		t.Fatalf("connected worker offline: status=%q", a.Status)
	}
}

// A session sync that omits a finished worker leaves it ended, like a removal.
func TestSyncEndsAWorkerWhoseRunIsOver(t *testing.T) {
	s, _ := newTestStore(t)
	node, _, _ := s.CreateNode("mac", "linux")
	in := protocol.SessionInfo{Harness: "codex", NativeID: "worker", Status: "busy",
		Worker: &protocol.WorkerInfo{ID: "w_test"}}
	id, err := s.UpsertSession(node, in)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.RecordWorkerRun(protocol.WorkerRun{NodeID: node, SessionID: id, Harness: "codex", Outcome: protocol.OutcomeStopped}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SyncSessions(node, nil); err != nil {
		t.Fatal(err)
	}
	a, _, _ := s.Agent(id)
	if a.Online || a.Status != "ended" {
		t.Fatalf("finished worker after sync: online=%v status=%q", a.Online, a.Status)
	}
}

// The one-time repair ends the worker sessions an older version left offline
// and busy after their run finished.
func TestMigrationEndsFinishedWorkerSessions(t *testing.T) {
	s, _ := newTestStore(t)
	node, _, _ := s.CreateNode("mac", "linux")
	in := protocol.SessionInfo{Harness: "opencode", NativeID: "finished", Status: "busy",
		Worker: &protocol.WorkerInfo{ID: "w_test"}}
	finished, err := s.UpsertSession(node, in)
	if err != nil {
		t.Fatal(err)
	}
	// The pre-fix state: offline and busy, with a run recorded as finished.
	if _, err := s.db.Exec(`UPDATE sessions SET online=0, status='busy', worker_run_active=0 WHERE id=?`, finished); err != nil {
		t.Fatal(err)
	}
	// A worker that only lost its connection is not touched.
	running, err := s.UpsertSession(node, protocol.SessionInfo{Harness: "codex", NativeID: "running", Status: "busy",
		Worker: &protocol.WorkerInfo{ID: "w_running"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`UPDATE sessions SET online=0 WHERE id=?`, running); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`DELETE FROM schema_version WHERE version>=?`, migrationVersion(t, "One-time repair: older versions left")); err != nil {
		t.Fatal(err)
	}
	if err := s.migrate(); err != nil {
		t.Fatal(err)
	}
	a, _, _ := s.Agent(finished)
	if a.Status != "ended" {
		t.Errorf("finished worker after the repair: %q", a.Status)
	}
	a, _, _ = s.Agent(running)
	if a.Status != "busy" {
		t.Errorf("running worker after the repair: %q", a.Status)
	}
}

func TestWorkerRunGenerationEnd(t *testing.T) {
	for _, path := range []string{"gateway", "delivery"} {
		for _, report := range []int{1, 2, 0} {
			t.Run(fmt.Sprintf("%s/report%d", path, report), func(t *testing.T) {
				g, node := scopeGateway(t)
				in := protocol.SessionInfo{Harness: "opencode", NativeID: "worker", Status: "busy", Worker: &protocol.WorkerInfo{ID: "w_test", RunGeneration: 1}}
				id, err := g.store.UpsertSession(node, in)
				if err != nil {
					t.Fatal(err)
				}
				if err := g.store.SetWorkerSessionEnded(node, id, 1); err != nil {
					t.Fatal(err)
				}
				if path == "gateway" {
					if err := g.store.SetWorkerRunActive(node, id, 2); err != nil {
						t.Fatal(err)
					}
				}
				in.Worker.RunGeneration = 2
				if _, err := g.store.UpsertSession(node, in); err != nil {
					t.Fatal(err)
				}
				// The store-level end and run-record writes must also fence old runs.
				if report == 1 {
					if err := g.store.RecordWorkerRun(protocol.WorkerRun{NodeID: node, SessionID: id, Harness: "opencode", Outcome: protocol.OutcomeComplete, RunGeneration: 1}); err != nil {
						t.Fatal(err)
					}
					if err := g.store.SetWorkerSessionEnded(node, id, 1); err != nil {
						t.Fatal(err)
					}
					var active int
					if err := g.store.db.QueryRow(`SELECT worker_run_active FROM sessions WHERE id=?`, id).Scan(&active); err != nil || active != 1 {
						t.Fatalf("stale record cleared active: %d %v", active, err)
					}
				}
				raw, _ := json.Marshal(protocol.WorkerStopped{SessionID: id, WorkerID: "w_test", Harness: "opencode", Reason: protocol.StopHandback, RunGeneration: report})
				if _, err := g.handleWorker(context.Background(), &nodeConn{id: node}, "worker.stopped", raw); err != nil {
					t.Fatal(err)
				}
				a, _, _ := g.store.Agent(id)
				if report == 1 {
					if !a.Online || a.Status != "busy" {
						t.Fatalf("stale end: online=%v status=%q", a.Online, a.Status)
					}
				} else if a.Online || a.Status != "ended" {
					t.Fatalf("matching/legacy end: online=%v status=%q", a.Online, a.Status)
				}
			})
		}
	}
}
