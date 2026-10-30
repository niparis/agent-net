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

// A model that finished a worker successfully is remembered per node and
// harness for protocol.ModelSuccessDays.
func TestModelSuccessesAreRememberedPerNodeAndHarness(t *testing.T) {
	s, clock := newTestStore(t)
	node, _, _ := s.CreateNode("h", "linux")
	other, _, _ := s.CreateNode("o", "linux")
	if err := s.RecordModelSuccess(node, "codex", "gpt-6-luna"); err != nil {
		t.Fatal(err)
	}
	if err := s.RecordModelSuccess(node, "codex", ""); err != nil {
		t.Fatal(err)
	}
	got, err := s.SucceededModels(node, "codex")
	if err != nil || len(got) != 1 || got["gpt-6-luna"].IsZero() {
		t.Fatalf("succeeded models: %v, %v", got, err)
	}
	if g, _ := s.SucceededModels(other, "codex"); len(g) != 0 {
		t.Errorf("another machine sees it: %v", g)
	}
	if g, _ := s.SucceededModels(node, "opencode"); len(g) != 0 {
		t.Errorf("another harness sees it: %v", g)
	}
	clock.t = clock.t.Add(time.Duration(protocol.ModelSuccessDays-1) * 24 * time.Hour)
	if g, _ := s.SucceededModels(node, "codex"); len(g) != 1 {
		t.Errorf("before the window ends: %v", g)
	}
	clock.t = clock.t.Add(25 * time.Hour)
	if g, _ := s.SucceededModels(node, "codex"); len(g) != 0 {
		t.Errorf("after the window: %v", g)
	}
}

// A model that failed a worker is remembered per node and harness for 24
// hours, and a new failure restarts the time.
func TestModelFailuresAreRemembered24Hours(t *testing.T) {
	s, clock := newTestStore(t)
	node, _, _ := s.CreateNode("h", "linux")
	other, _, _ := s.CreateNode("o", "linux")
	if err := s.RecordModelFailure(node, "opencode", "p/m"); err != nil {
		t.Fatal(err)
	}
	if err := s.RecordModelFailure(node, "opencode", ""); err != nil {
		t.Fatal(err)
	}
	got, err := s.FailedModels(node, "opencode")
	if err != nil || len(got) != 1 || got["p/m"].IsZero() {
		t.Fatalf("failed models: %v, %v", got, err)
	}
	if g, _ := s.FailedModels(other, "opencode"); len(g) != 0 {
		t.Errorf("another machine sees it: %v", g)
	}
	if g, _ := s.FailedModels(node, "pi"); len(g) != 0 {
		t.Errorf("another harness sees it: %v", g)
	}
	clock.t = clock.t.Add(23 * time.Hour)
	if g, _ := s.FailedModels(node, "opencode"); len(g) != 1 {
		t.Errorf("after 23 hours: %v", g)
	}
	s.RecordModelFailure(node, "opencode", "p/m") // fails again: the time restarts
	clock.t = clock.t.Add(23 * time.Hour)
	if g, _ := s.FailedModels(node, "opencode"); len(g) != 1 {
		t.Errorf("a new failure did not restart the time: %v", g)
	}
	clock.t = clock.t.Add(2 * time.Hour)
	if g, _ := s.FailedModels(node, "opencode"); len(g) != 0 {
		t.Errorf("after 25 hours: %v", g)
	}
}
