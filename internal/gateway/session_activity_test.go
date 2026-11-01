package gateway

import (
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

func TestLastActiveIsStoredAndNotMovedByAReport(t *testing.T) {
	s, c := newTestStore(t)
	n, _, err := s.CreateNode("mac", "darwin")
	if err != nil {
		t.Fatal(err)
	}
	active := c.t.Add(-2 * time.Hour).Unix()
	id, err := s.UpsertSession(n, protocol.SessionInfo{Harness: "claude", NativeID: "n1", Status: "idle", LastActive: active})
	if err != nil {
		t.Fatal(err)
	}
	a, ok, err := s.Agent(id)
	if err != nil || !ok {
		t.Fatalf("agent: %v ok=%v", err, ok)
	}
	if a.LastActive != active {
		t.Fatalf("last active %d, want %d", a.LastActive, active)
	}

	// A daemon restart re-reports the idle session with no activity time.
	c.t = c.t.Add(time.Hour)
	if _, err := s.UpsertSession(n, protocol.SessionInfo{Harness: "claude", NativeID: "n1", Status: "idle"}); err != nil {
		t.Fatal(err)
	}
	a, _, _ = s.Agent(id)
	if a.LastActive != active {
		t.Fatalf("a report moved last active to %d, want %d", a.LastActive, active)
	}

	// A newer real activity does move it.
	if _, err := s.UpsertSession(n, protocol.SessionInfo{Harness: "claude", NativeID: "n1", Status: "busy", LastActive: active + 60}); err != nil {
		t.Fatal(err)
	}
	a, _, _ = s.Agent(id)
	if a.LastActive != active+60 {
		t.Fatalf("last active %d, want %d", a.LastActive, active+60)
	}
}
