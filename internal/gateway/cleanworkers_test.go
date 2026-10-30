package gateway

import (
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
