package gateway

import (
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

// A start that failed after it was answered as starting is recorded as an event.
// Only the owner of the machine may report one, and a long error is cut short.
func TestRecordSessionStartFailure(t *testing.T) {
	s, _ := newTestStore(t)
	node, _, _ := s.CreateNode("test", "linux")
	owner, err := s.UpsertSession(node, protocol.SessionInfo{Harness: "owner", NativeID: protocol.OwnerNativeID})
	if err != nil {
		t.Fatal(err)
	}
	worker, err := s.UpsertSession(node, protocol.SessionInfo{Harness: "claude", NativeID: "claude-1", Cwd: "/code/repo"})
	if err != nil {
		t.Fatal(err)
	}
	for _, from := range []string{"no-such-session", worker} {
		if err := s.RecordSessionStartFailure(node, protocol.SessionStartFailure{Owner: from, StartID: "start_1", Project: "garden", Harness: "claude", Error: "bad"}); err == nil {
			t.Fatalf("accepted a failure report from %q", from)
		}
	}
	if events, err := s.Events("session.start_failed"); err != nil || len(events) != 0 {
		t.Fatalf("refused reports were recorded: %v %v", events, err)
	}

	long := strings.Repeat("x", 500)
	if err := s.RecordSessionStartFailure(node, protocol.SessionStartFailure{Owner: owner, StartID: "start_1", Project: "garden", Harness: "claude", Error: long}); err != nil {
		t.Fatal(err)
	}
	events, err := s.Events("session.start_failed")
	if err != nil || len(events) != 1 {
		t.Fatalf("events %v %v", events, err)
	}
	if !strings.Contains(events[0], "start=start_1") || !strings.Contains(events[0], "project=garden") || !strings.Contains(events[0], "harness=claude") {
		t.Fatalf("event %q", events[0])
	}
	if got := strings.Count(events[0], "x"); got != 300 {
		t.Fatalf("error kept %d characters, want 300", got)
	}
}
