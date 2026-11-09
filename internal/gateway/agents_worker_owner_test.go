package gateway

import (
	"fmt"
	"reflect"
	"testing"

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
