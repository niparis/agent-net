package gateway

import (
	"encoding/json"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// Asking Agents for the workers of one owner returns that owner's workers and
// the owner's own session, in the same order and with the same fields as the
// full list. Nothing else is read: this is what a Claude session's workers
// panel polls, so the answer has to stay small.
func TestAgentsWorkersOwnedByIsASubsetOfAll(t *testing.T) {
	s, _ := newTestStore(t)
	node, _, _ := s.CreateNode("host", "linux")
	ownerA, err := s.UpsertSession(node, protocol.SessionInfo{Harness: "claude", NativeID: "owner-a", Cwd: "/repo/a", Name: "a"})
	if err != nil {
		t.Fatal(err)
	}
	ownerB, err := s.UpsertSession(node, protocol.SessionInfo{Harness: "claude", NativeID: "owner-b", Cwd: "/repo/b", Name: "b"})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 20; i++ {
		if _, err := s.UpsertSession(node, protocol.SessionInfo{Harness: "codex", NativeID: fmt.Sprintf("plain%d", i), Cwd: "/repo/c", Name: "plain"}); err != nil {
			t.Fatal(err)
		}
	}
	for i, owner := range []string{ownerA, ownerB, ownerA, ownerB, ownerA} {
		if _, err := s.UpsertSession(node, protocol.SessionInfo{Harness: "opencode", NativeID: fmt.Sprintf("w%d", i), Cwd: "/work",
			Worker: &protocol.WorkerInfo{ID: fmt.Sprintf("w%d", i), Owner: owner, Project: "shop"}}); err != nil {
			t.Fatal(err)
		}
	}
	all, err := s.Agents(protocol.QueryAgents{IncludeOwners: true})
	if err != nil || len(all) != 27 {
		t.Fatalf("all: %d %v", len(all), err)
	}
	for _, owner := range []string{ownerA, ownerB} {
		var want []protocol.AgentView
		for _, a := range all {
			if a.SessionID == owner || (a.Worker != nil && a.Worker.Owner == owner) {
				want = append(want, a)
			}
		}
		got, err := s.Agents(protocol.QueryAgents{IncludeOwners: true, WorkersOwnedBy: owner})
		if err != nil {
			t.Fatal(err)
		}
		if len(want) < 3 || !reflect.DeepEqual(got, want) {
			t.Fatalf("owner %s:\n got %d %+v\nwant %d %+v", owner, len(got), got, len(want), want)
		}
	}
	none, err := s.Agents(protocol.QueryAgents{IncludeOwners: true, WorkersOwnedBy: "no-such-session"})
	if err != nil || len(none) != 0 {
		t.Fatalf("an unknown owner: %d %v", len(none), err)
	}
}

// A panel query for one owner keeps the workers that are still running and the
// workers that ended in the last two minutes, and leaves out the ones that
// ended long ago. The daemon only shows those rows (workersView), so reading
// the old ones costs 62 KB a poll for nothing.
func TestAgentsWorkersOwnedBySkipsWorkersThatEndedLongAgo(t *testing.T) {
	s, c := newTestStore(t)
	node, _, err := s.CreateNode("host", "linux")
	if err != nil {
		t.Fatal(err)
	}
	now := c.t
	owner, err := s.UpsertSession(node, protocol.SessionInfo{Harness: "claude", NativeID: "owner", Cwd: "/repo", Name: "owner"})
	if err != nil {
		t.Fatal(err)
	}
	// Twenty workers ended an hour and a half before this poll. One ended
	// thirty seconds ago.
	c.t = now.Add(-time.Hour)
	var oldIDs []string
	for i := 0; i < 20; i++ {
		id, err := s.UpsertSession(node, protocol.SessionInfo{Harness: "opencode", NativeID: fmt.Sprintf("old%d", i), Cwd: "/old",
			Worker: &protocol.WorkerInfo{ID: fmt.Sprintf("old%d", i), Owner: owner, Project: "shop"}})
		if err != nil {
			t.Fatal(err)
		}
		if err := s.SetWorkerSessionEnded(node, id); err != nil {
			t.Fatal(err)
		}
		oldIDs = append(oldIDs, id)
	}
	c.t = now
	running, err := s.UpsertSession(node, protocol.SessionInfo{Harness: "opencode", NativeID: "running", Cwd: "/work",
		Worker: &protocol.WorkerInfo{ID: "running", Owner: owner, Project: "shop"}})
	if err != nil {
		t.Fatal(err)
	}
	recent, err := s.UpsertSession(node, protocol.SessionInfo{Harness: "opencode", NativeID: "recent", Cwd: "/work",
		Worker: &protocol.WorkerInfo{ID: "recent", Owner: owner, Project: "shop"}})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetWorkerSessionEnded(node, recent); err != nil {
		t.Fatal(err)
	}
	c.t = now.Add(30 * time.Second)

	all, err := s.Agents(protocol.QueryAgents{IncludeOwners: true})
	if err != nil || len(all) != 23 {
		t.Fatalf("all: %d %v", len(all), err)
	}
	got, err := s.Agents(protocol.QueryAgents{IncludeOwners: true, WorkersOwnedBy: owner})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{owner: true, running: true, recent: true}
	if len(got) != len(want) {
		t.Fatalf("owner query: %d rows, want %d (%+v)", len(got), len(want), got)
	}
	for _, a := range got {
		if !want[a.SessionID] {
			t.Fatalf("owner query kept %s, which ended long ago", a.SessionID)
		}
		delete(want, a.SessionID)
	}
	if len(want) != 0 {
		t.Fatalf("owner query left out %v", want)
	}
	for _, id := range oldIDs {
		for _, a := range got {
			if a.SessionID == id {
				t.Fatalf("an old ended worker is in the owner query: %s", id)
			}
		}
	}
	beforeJSON, _ := json.Marshal(all)
	afterJSON, _ := json.Marshal(got)
	t.Logf("bytes per call: before %d, after %d", len(beforeJSON), len(afterJSON))
	if len(afterJSON) >= len(beforeJSON)/2 {
		t.Fatalf("the owner query read %d bytes, the full list is %d: the old ended workers are still there", len(afterJSON), len(beforeJSON))
	}
}
