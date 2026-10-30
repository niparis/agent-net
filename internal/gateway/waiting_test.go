package gateway

import (
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

// The waiting state goes with the session, so every machine's TUI sees it
// (decision 0016).
func TestWaitingGoesWithTheSession(t *testing.T) {
	s, _ := newTestStore(t)
	node, _, err := s.CreateNode("h1", "linux")
	if err != nil {
		t.Fatal(err)
	}
	id, err := s.UpsertSession(node, protocol.SessionInfo{Harness: "claude", NativeID: "n1", Name: "alice",
		Waiting: &protocol.Waiting{Kind: protocol.WaitingPermission, Text: "Deploy?\nplease", Since: 10}})
	if err != nil {
		t.Fatal(err)
	}
	a, ok, err := s.Agent(id)
	if err != nil || !ok {
		t.Fatalf("agent: %v ok=%v", err, ok)
	}
	if a.Waiting == nil || a.Waiting.Kind != protocol.WaitingPermission || a.Waiting.Text != "Deploy? please" || a.Waiting.Since != 10 {
		t.Fatalf("stored waiting = %+v", a.Waiting)
	}
	// An upsert without the field clears it, like a later announce does.
	if _, err := s.UpsertSession(node, protocol.SessionInfo{Harness: "claude", NativeID: "n1", Name: "alice"}); err != nil {
		t.Fatal(err)
	}
	a, _, _ = s.Agent(id)
	if a.Waiting != nil {
		t.Fatalf("waiting not cleared: %+v", a.Waiting)
	}
}

// The gateway cleans the text again: a daemon is not the only writer on the
// wire, and the state is shown on one line of every machine's TUI.
func TestWaitingIsCleanedAtTheGateway(t *testing.T) {
	s, _ := newTestStore(t)
	node, _, _ := s.CreateNode("h1", "linux")
	id, err := s.UpsertSession(node, protocol.SessionInfo{Harness: "pi", NativeID: "n2", Name: "bob",
		Waiting: &protocol.Waiting{Kind: "weird", Text: "one\nline\x00", Since: 5}})
	if err != nil {
		t.Fatal(err)
	}
	a, _, _ := s.Agent(id)
	if a.Waiting == nil || a.Waiting.Kind != protocol.WaitingInput || a.Waiting.Text != "one line" {
		t.Fatalf("cleaned waiting = %+v", a.Waiting)
	}
}
