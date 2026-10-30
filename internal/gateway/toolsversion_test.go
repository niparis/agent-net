package gateway

import (
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

func TestSessionToolsVersionIsStored(t *testing.T) {
	s, _ := newTestStore(t)
	node, _, _ := s.CreateNode("h", "linux")
	id, err := s.UpsertSession(node, protocol.SessionInfo{Harness: "claude", NativeID: "n", Name: "x",
		ToolsVersion: "v0.4.5", ToolsOld: true})
	if err != nil {
		t.Fatal(err)
	}
	a, _, _ := s.Agent(id)
	if a.ToolsVersion != "v0.4.5" || !a.ToolsOld {
		t.Fatalf("agent: %+v", a)
	}
	// A report with the current version clears the mark; an older daemon
	// sends neither field and reads as empty.
	s.UpsertSession(node, protocol.SessionInfo{Harness: "claude", NativeID: "n", Name: "x", ToolsVersion: "v0.5.0"})
	if a, _, _ := s.Agent(id); a.ToolsVersion != "v0.5.0" || a.ToolsOld {
		t.Fatalf("after current report: %+v", a)
	}
	s.UpsertSession(node, protocol.SessionInfo{Harness: "claude", NativeID: "n", Name: "x"})
	if a, _, _ := s.Agent(id); a.ToolsVersion != "" || a.ToolsOld {
		t.Fatalf("after silent report: %+v", a)
	}
}
