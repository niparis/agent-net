package gateway

import (
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

// An exited harness keeps its process and session file (a Claude background
// session after /exit), so the machine still lists it. The record stays for
// resume, but it is offline: nothing can be delivered or attached to it.
func TestSyncSessionsRecordsAnExitedSessionOffline(t *testing.T) {
	s, _ := newTestStore(t)
	node, _, err := s.CreateNode("h1", "linux")
	if err != nil {
		t.Fatal(err)
	}
	info := protocol.SessionInfo{Harness: "claude", NativeID: "bg-exited", Status: "exited", Name: "orchestrator"}
	out, err := s.SyncSessions(node, []protocol.SessionInfo{info})
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 1 {
		t.Fatalf("synced %d sessions, want 1", len(out))
	}
	a, ok, err := s.Agent(out[0].SessionID)
	if err != nil || !ok {
		t.Fatalf("the exited row is gone: ok %v err %v", ok, err)
	}
	if a.Online || a.Status != "exited" {
		t.Fatalf("an exited session is recorded online: %+v", a)
	}
	// A live report brings the row back: this is the resumed harness.
	info.Status = "idle"
	if _, err := s.SyncSessions(node, []protocol.SessionInfo{info}); err != nil {
		t.Fatal(err)
	}
	a, _, _ = s.Agent(out[0].SessionID)
	if !a.Online || a.Status != "idle" {
		t.Fatalf("a live session stayed offline: %+v", a)
	}
}
