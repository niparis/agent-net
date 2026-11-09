package gateway

import (
	"database/sql"
	"strings"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// A worker record with no start time (written by an older daemon) counts as
// old, but not while the machine still runs that worker.
func TestCleanWorkersKeepsWorkersTheMachineRuns(t *testing.T) {
	s, _ := newTestStore(t)
	node, _, err := s.CreateNode("h1", "linux")
	if err != nil {
		t.Fatal(err)
	}
	add := func(native string, started int64) string {
		id, err := s.UpsertSession(node, protocol.SessionInfo{Harness: "opencode", NativeID: native,
			Worker: &protocol.WorkerInfo{ID: "w_" + native, Owner: "s_o", StartedAt: started}})
		if err != nil {
			t.Fatal(err)
		}
		if err := s.SetSessionOffline(node, id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	now := s.now().Unix()
	oldStyleRunning := add("a", 0)
	oldStyleGone := add("b", 0)
	oldRunning := add("c", now-100*3600)
	oldGone := add("d", now-100*3600)
	recent := add("e", now-60)
	n, err := s.CleanWorkers(node, 4*time.Hour, []string{oldStyleRunning, oldRunning})
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("cleaned %d records, want 2", n)
	}
	for id, want := range map[string]bool{oldStyleRunning: true, oldRunning: true, recent: true, oldStyleGone: false, oldGone: false} {
		if _, ok, _ := s.Agent(id); ok != want {
			t.Errorf("record %s kept=%v, want %v", id, ok, want)
		}
	}
}

// A daemon can ask for the session ID it knew for a new record, so a worker
// whose record was lost comes back under the same ID.
func TestUpsertSessionKeepsTheRequestedIDForANewRecord(t *testing.T) {
	s, _ := newTestStore(t)
	n1, _, _ := s.CreateNode("h1", "linux")
	n2, _, _ := s.CreateNode("h2", "linux")
	id, err := s.UpsertSession(n1, protocol.SessionInfo{SessionID: "s_keepme", Harness: "pi", NativeID: "x"})
	if err != nil || id != "s_keepme" {
		t.Fatalf("id %q err %v", id, err)
	}
	// The ID is in use by another record: a fresh one is made.
	id2, err := s.UpsertSession(n2, protocol.SessionInfo{SessionID: "s_keepme", Harness: "pi", NativeID: "y"})
	if err != nil || id2 == "s_keepme" || id2 == "" {
		t.Fatalf("id2 %q err %v", id2, err)
	}
	// A bad format is ignored.
	id3, _ := s.UpsertSession(n2, protocol.SessionInfo{SessionID: "bad id!", Harness: "pi", NativeID: "z"})
	if !strings.HasPrefix(id3, "s_") || id3 == "bad id!" {
		t.Fatalf("id3 %q", id3)
	}
}

// cleanupOnInsert simulates cleanup deleting a session after its lookup but
// before the upsert inserts it again.
type cleanupOnInsert struct {
	*storeDB
	id string
}

func (q *cleanupOnInsert) Exec(query string, args ...any) (sql.Result, error) {
	if strings.HasPrefix(strings.TrimSpace(query), "INSERT INTO sessions ") {
		if _, err := q.storeDB.Exec("DELETE FROM sessions WHERE id = ?", q.id); err != nil {
			return nil, err
		}
	}
	return q.storeDB.Exec(query, args...)
}

func TestUpsertKeepsKnownIDWhenCleanupRacesInsert(t *testing.T) {
	s, _ := newTestStore(t)
	node, _, err := s.CreateNode("test", "linux")
	if err != nil {
		t.Fatal(err)
	}
	in := protocol.SessionInfo{Harness: "opencode", NativeID: "native", Worker: &protocol.WorkerInfo{ID: "w_test"}}
	id, err := s.UpsertSession(node, in)
	if err != nil {
		t.Fatal(err)
	}
	got, _, err := s.upsertSession(&cleanupOnInsert{storeDB: s.db, id: id}, node, in)
	if err != nil {
		t.Fatal(err)
	}
	if got != id {
		t.Fatalf("session ID = %q, want %q", got, id)
	}
}

func TestCleanWorkersRecentOwnerUseAndPinned(t *testing.T) {
	s, c := newTestStore(t)
	node, _, err := s.CreateNode("box", "linux")
	if err != nil {
		t.Fatal(err)
	}
	ids := map[string]string{}
	for _, name := range []string{"recent", "pinned"} {
		id, err := s.UpsertSession(node, protocol.SessionInfo{Harness: "claude", NativeID: name, Worker: &protocol.WorkerInfo{ID: "w_" + name, StartedAt: c.t.Add(-48 * time.Hour).Unix()}})
		if err != nil {
			t.Fatal(err)
		}
		if err = s.SetWorkerSessionEnded(node, id); err != nil {
			t.Fatal(err)
		}
		if err = s.RecordOwnerUse(id); err != nil {
			t.Fatal(err)
		}
		if name == "pinned" {
			if err = s.SetSessionPin(id, true); err != nil {
				t.Fatal(err)
			}
		}
		ids[name] = id
	}
	n, err := s.CleanWorkers(node, 24*time.Hour, nil)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("cleaned %d workers, want 1", n)
	}
	for name, id := range ids {
		_, ok, err := s.Agent(id)
		if err != nil {
			t.Fatal(err)
		}
		if ok != (name == "pinned") {
			t.Errorf("%s worker retained=%v", name, ok)
		}
	}
}

func TestPruneOnlyRetainsReportedWorkers(t *testing.T) {
	s, _ := newTestStore(t)
	node, _, err := s.CreateNode("h1", "linux")
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, native := range []string{"kept", "gone"} {
		id, err := s.UpsertSession(node, protocol.SessionInfo{Harness: "opencode", NativeID: native, Worker: &protocol.WorkerInfo{ID: "w_" + native, StartedAt: s.now().Unix()}})
		if err != nil {
			t.Fatal(err)
		}
		if err := s.SetSessionOffline(node, id); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	// Report retention before aging the rows, so CleanWorkers does not remove them.
	if _, err := s.CleanWorkers(node, 4*time.Hour, ids[:1]); err != nil {
		t.Fatal(err)
	}
	old := s.now().Add(-100 * time.Hour).UnixNano()
	if _, err := s.db.Exec(`UPDATE sessions SET last_active=?, last_seen=?, owner_used_at=0`, old, old); err != nil {
		t.Fatal(err)
	}
	result, err := s.Prune(Retention{Sessions: 24 * time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	if result.Sessions != 1 {
		t.Fatalf("pruned %d, want 1", result.Sessions)
	}
	for i, id := range ids {
		if _, ok, _ := s.Agent(id); ok != (i == 0) {
			t.Fatalf("session %s kept=%v", id, ok)
		}
	}
	// Replacing the daemon's report releases the last retained session.
	if _, err := s.CleanWorkers(node, 4*time.Hour, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Prune(Retention{Sessions: 24 * time.Hour}); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := s.Agent(ids[0]); ok {
		t.Fatal("released worker remains")
	}
}
