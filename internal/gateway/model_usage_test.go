package gateway

import (
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// The model usage of a machine counts the sessions of the last 30 days, per
// harness, and nothing from another machine (decision 0002, W17).
func TestModelUsageCountsRecentSessionsOfTheNode(t *testing.T) {
	s, clock := newTestStore(t)
	node, _, _ := s.CreateNode("h", "linux")
	other, _, _ := s.CreateNode("o", "linux")
	up := func(n, native, harness, model string) {
		if _, err := s.UpsertSession(n, protocol.SessionInfo{Harness: harness, NativeID: native, Model: model}); err != nil {
			t.Fatal(err)
		}
	}
	up(node, "old", "opencode", "p/old")
	clock.t = clock.t.Add(40 * 24 * time.Hour)
	up(node, "a", "opencode", "p/m")
	up(node, "b", "opencode", "p/m")
	up(node, "c", "opencode", "p/n")
	up(node, "d", "opencode", "")
	up(node, "e", "claude", "claude-opus-5-5")
	up(other, "f", "opencode", "p/m")
	got, err := s.ModelUsage(node, "opencode", 30*24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got["p/m"] != 2 || got["p/n"] != 1 {
		t.Fatalf("usage: %v", got)
	}
}
