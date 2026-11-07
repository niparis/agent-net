package gateway

import (
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

func TestSettingsIdleCompactionSessionNote(t *testing.T) {
	s, _ := newTestStore(t)
	n, _, err := s.CreateNode("test", "linux")
	if err != nil {
		t.Fatal(err)
	}
	in := protocol.SessionInfo{Harness: "claude", NativeID: "test", Status: "idle", IdleCompactedAt: 1700000000}
	id, err := s.UpsertSession(n, in)
	if err != nil {
		t.Fatal(err)
	}
	a, ok, err := s.Agent(id)
	if err != nil || !ok || a.IdleCompactedAt != in.IdleCompactedAt {
		t.Fatalf("note round trip: %+v %v", a, err)
	}
	in.IdleCompactedAt = 0
	in.Status = "busy"
	if _, err := s.UpsertSession(n, in); err != nil {
		t.Fatal(err)
	}
	a, _, err = s.Agent(id)
	if err != nil || a.IdleCompactedAt != 0 {
		t.Fatalf("clear note: %+v %v", a, err)
	}
}
