package gateway

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

// The struggle counts travel with the worker run and show where the caller
// looks: the worker_runs row and the caller's accept step task.

func TestWorkerRunStoresStruggleCounts(t *testing.T) {
	s, _ := newTestStore(t)
	st := &protocol.Struggle{Trace: "transcript", Failed: 4, Repeats: 1, Retries: 2, Exploration: 7, EnvErrors: 1,
		Top: []string{"go test ./x x3 (3 failed)", "git status (1 failed)"}}
	err := s.RecordWorkerRun(protocol.WorkerRun{NodeID: "n", SessionID: "s_w", Harness: "claude", Model: "m",
		Outcome: protocol.OutcomeComplete, Struggle: st})
	if err != nil {
		t.Fatal(err)
	}
	var failed, repeats, retries, exploration, env, capped int
	var top, trace string
	err = s.db.QueryRow(`SELECT struggle_failed, struggle_repeats, struggle_retries, struggle_exploration,
		struggle_env, struggle_top, struggle_trace, struggle_capped FROM worker_runs WHERE session_id='s_w'`).
		Scan(&failed, &repeats, &retries, &exploration, &env, &top, &trace, &capped)
	if err != nil {
		t.Fatal(err)
	}
	if failed != 4 || repeats != 1 || retries != 2 || exploration != 7 || env != 1 || trace != "transcript" || capped != 0 {
		t.Fatalf("stored counts: %d %d %d %d %d %q %d", failed, repeats, retries, exploration, env, trace, capped)
	}
	if !strings.Contains(top, "go test ./x") {
		t.Fatalf("stored top offenders: %q", top)
	}
}

func TestWorkerRunStruggleColumnsExist(t *testing.T) {
	s, _ := newTestStore(t)
	for _, col := range []string{"struggle_failed", "struggle_repeats", "struggle_retries", "struggle_exploration",
		"struggle_env", "struggle_top", "struggle_trace", "struggle_capped"} {
		var has int
		if err := s.db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('worker_runs') WHERE name=?`, col).Scan(&has); err != nil {
			t.Fatal(err)
		}
		if has == 0 {
			t.Fatalf("worker_runs has no %s column", col)
		}
	}
}

// TestAcceptTaskCarriesStruggleLine puts the line in the caller's accept task.
func TestAcceptTaskCarriesStruggleLine(t *testing.T) {
	r := newDeliveryRig(t)
	if err := r.s.AddArchitect("merger", "owner"); err != nil {
		t.Fatal(err)
	}
	if err := r.s.SetEdge(protocol.Edge{From: "merger", To: "demo", Kind: "accepts"}, "owner"); err != nil {
		t.Fatal(err)
	}
	q := r.workerSubmit("feature/struggle", commitA)
	q.Origin.Struggle = &protocol.Struggle{Trace: "transcript", Failed: 4, Repeats: 1,
		Top: []string{"go test ./x x3 (3 failed)", "git status (1 failed)"}}
	id := r.submit(r.a, q).Change.Change.ID
	sid, err := r.s.UpsertSession(r.b, protocol.SessionInfo{Harness: "pi", NativeID: "merger", Repo: protocol.RepoInfo{Project: "merger"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.s.storeStepTaskMessage(nodeTaskTarget{id: id, revision: 1, ref: stepAccept, owner: "merger", session: sid}); err != nil {
		t.Fatal(err)
	}
	msgs, err := r.s.Messages(r.b, protocol.QueryMessages{SessionID: sid})
	if err != nil || len(msgs) != 1 {
		t.Fatalf("messages: %+v, %v", msgs, err)
	}
	if !strings.Contains(msgs[0].Body, "struggle: 4 failed tool calls, 1 repeats; top: go test ./x x3 (3 failed); git status (1 failed)") {
		t.Fatalf("accept task lacks the struggle line:\n%s", msgs[0].Body)
	}
}

// TestAcceptTaskStruggleFallsBackToTheRunRow covers a change recorded before
// the origin carried the counts.
func TestAcceptTaskStruggleFallsBackToTheRunRow(t *testing.T) {
	r := newDeliveryRig(t)
	if err := r.s.AddArchitect("merger", "owner"); err != nil {
		t.Fatal(err)
	}
	if err := r.s.SetEdge(protocol.Edge{From: "merger", To: "demo", Kind: "accepts"}, "owner"); err != nil {
		t.Fatal(err)
	}
	id := r.submit(r.a, r.workerSubmit("feature/fallback", commitA)).Change.Change.ID
	if err := r.s.RecordWorkerRun(protocol.WorkerRun{NodeID: r.a, SessionID: r.worker, Harness: "claude", Outcome: protocol.OutcomeComplete,
		Struggle: &protocol.Struggle{Trace: "transcript", Failed: 3, Repeats: 0, Top: []string{"make (3 failed)"}}}); err != nil {
		t.Fatal(err)
	}
	sid, err := r.s.UpsertSession(r.b, protocol.SessionInfo{Harness: "pi", NativeID: "merger", Repo: protocol.RepoInfo{Project: "merger"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.s.storeStepTaskMessage(nodeTaskTarget{id: id, revision: 1, ref: stepAccept, owner: "merger", session: sid}); err != nil {
		t.Fatal(err)
	}
	msgs, err := r.s.Messages(r.b, protocol.QueryMessages{SessionID: sid})
	if err != nil || len(msgs) != 1 {
		t.Fatalf("messages: %+v, %v", msgs, err)
	}
	if !strings.Contains(msgs[0].Body, "struggle: 3 failed tool calls, 0 repeats") {
		t.Fatalf("accept task lacks the fallback struggle line:\n%s", msgs[0].Body)
	}
}

// TestWorkerStoppedCarriesStruggleToTheRun covers the wire bridge: the
// daemon's worker.stopped frame puts the counts in the run row.
func TestWorkerStoppedCarriesStruggleToTheRun(t *testing.T) {
	g, node := scopeGateway(t)
	id, err := g.store.UpsertSession(node, protocol.SessionInfo{Harness: "claude", NativeID: "worker", Status: "busy", Worker: &protocol.WorkerInfo{ID: "w_test"}})
	if err != nil {
		t.Fatal(err)
	}
	st := &protocol.Struggle{Trace: "transcript", Failed: 4, Repeats: 1, Top: []string{"go test ./x x3 (3 failed)"}}
	raw, _ := json.Marshal(protocol.WorkerStopped{SessionID: id, WorkerID: "w_test", Harness: "claude", Reason: protocol.StopHandback, Struggle: st})
	if _, err := g.handleWorker(context.Background(), &nodeConn{id: node}, "worker.stopped", raw); err != nil {
		t.Fatal(err)
	}
	var failed, repeats int
	var trace string
	if err := g.store.db.QueryRow(`SELECT struggle_failed, struggle_repeats, struggle_trace FROM worker_runs WHERE session_id=?`, id).Scan(&failed, &repeats, &trace); err != nil {
		t.Fatal(err)
	}
	if failed != 4 || repeats != 1 || trace != "transcript" {
		t.Fatalf("run rows: %d %d %q", failed, repeats, trace)
	}
}
