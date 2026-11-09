package gateway

import (
	"fmt"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

// The hash of an owner's workers panel rows moves when those rows change and
// stays when anything else on the network is written.
func TestWorkerPanelHashMovesOnlyWithTheOwnersRows(t *testing.T) {
	s, _ := newTestStore(t)
	node, _, _ := s.CreateNode("host", "linux")
	ownerA, _ := s.UpsertSession(node, protocol.SessionInfo{Harness: "claude", NativeID: "owner-a", Cwd: "/repo/a", Name: "a"})
	ownerB, _ := s.UpsertSession(node, protocol.SessionInfo{Harness: "claude", NativeID: "owner-b", Cwd: "/repo/b", Name: "b"})
	worker := func(native, owner string, tokens int64) string {
		t.Helper()
		id, err := s.UpsertSession(node, protocol.SessionInfo{Harness: "opencode", NativeID: native, Cwd: "/work",
			Worker: &protocol.WorkerInfo{ID: native, Owner: owner, Project: "shop", Tokens: tokens}})
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	hashes := func() map[string]string {
		t.Helper()
		h, err := s.workerPanelHashes([]string{ownerA, ownerB})
		if err != nil {
			t.Fatal(err)
		}
		return h
	}
	wa := worker("wa", ownerA, 10)
	worker("wb", ownerB, 10)
	base := hashes()
	if base[ownerA] == base[ownerB] {
		t.Fatal("two owners with different rows have the same hash")
	}

	// Writes of other sessions, and a write that changes nothing, leave both.
	for i := 0; i < 5; i++ {
		if _, err := s.UpsertSession(node, protocol.SessionInfo{Harness: "codex", NativeID: fmt.Sprintf("plain%d", i), Cwd: "/x", Name: "plain"}); err != nil {
			t.Fatal(err)
		}
	}
	worker("wa", ownerA, 10)
	if got := hashes(); got[ownerA] != base[ownerA] || got[ownerB] != base[ownerB] {
		t.Fatalf("unrelated writes moved a hash: %v vs %v", got, base)
	}

	// A change of one worker moves its owner's hash only.
	worker("wa", ownerA, 99)
	got := hashes()
	if got[ownerA] == base[ownerA] || got[ownerB] != base[ownerB] {
		t.Fatalf("a token change of owner A's worker: %v vs %v", got, base)
	}
	// A new worker, and a worker that goes offline, move it too.
	moved := got[ownerA]
	worker("wc", ownerA, 1)
	if hashes()[ownerA] == moved {
		t.Fatal("a new worker did not move the hash")
	}
	moved = hashes()[ownerA]
	if err := s.SetSessionOffline(node, wa); err != nil {
		t.Fatal(err)
	}
	if hashes()[ownerA] == moved {
		t.Fatal("a worker going offline did not move the hash")
	}
}
