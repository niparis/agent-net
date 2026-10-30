package gateway

import (
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// The gateway remembers when it first saw a session, and a later report does
// not move it: the TUI keeps a stable order from that time (decision 0017).
func TestFirstSeenStaysWhenTheSessionReportsAgain(t *testing.T) {
	s, c := newTestStore(t)
	node, _, err := s.CreateNode("h1", "linux")
	if err != nil {
		t.Fatal(err)
	}
	id, err := s.UpsertSession(node, protocol.SessionInfo{Harness: "claude", NativeID: "n1", Name: "builder", Status: "idle"})
	if err != nil {
		t.Fatal(err)
	}
	first := c.t.Unix()
	a, ok, err := s.Agent(id)
	if err != nil || !ok || a.FirstSeen != first || a.Title != "builder" {
		t.Fatalf("first report: %+v (ok %v, err %v), want first seen %d", a, ok, err, first)
	}
	c.t = c.t.Add(2 * time.Hour)
	if _, err := s.UpsertSession(node, protocol.SessionInfo{Harness: "claude", NativeID: "n1", Name: "builder", Status: "busy"}); err != nil {
		t.Fatal(err)
	}
	a, _, _ = s.Agent(id)
	if a.FirstSeen != first {
		t.Fatalf("first seen moved from %d to %d", first, a.FirstSeen)
	}
	if a.LastSeen == first {
		t.Fatal("last seen did not move with the new report")
	}
}
