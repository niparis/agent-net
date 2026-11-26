package daemon

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// mcpCheckAsks is the open CTO intake entries about a failed agent-net MCP
// repair. The daemon's ask about it is no item for the owner (decision 0196);
// the intake entry is what stays.
func mcpCheckAsks(t *testing.T, g *testGateway) []protocol.IntakeEntry {
	t.Helper()
	res, err := g.gw.Store().CTOIntake(0)
	if err != nil {
		t.Fatal(err)
	}
	var found []protocol.IntakeEntry
	for _, e := range res.Entries {
		if e.Kind == protocol.IntakeMCPRepair {
			found = append(found, e)
		}
	}
	return found
}

// A Claude session that agent-net sees as online, but whose `agent-net mcp`
// process never said hello, is restarted once, and only while it is idle. If
// the restarted session still has no hello, exactly one intake entry is raised.
// The restart does not come back after the daemon restarts, and the entry counts
// no new failure.
func TestClaudeSessionWithoutMCPHelloIsRestartedOnce(t *testing.T) {
	t.Parallel()
	g := startGateway(t, t.TempDir(), "")
	const after = 150 * time.Millisecond
	var mu sync.Mutex
	restarts := 0
	fa := &fakeAdapter{harness: "claude"}
	n := startNode(t, g, nodeOpts{adapter: fa, cfg: func(c *Config) {
		c.MCPLinkGrace = 50 * time.Millisecond
		c.testMCPRepairAfter = after
		c.testMCPRestart = func(context.Context, string) error {
			mu.Lock()
			restarts++
			mu.Unlock()
			return nil
		}
	}})
	info := protocol.SessionInfo{Harness: "claude", NativeID: "sess-repair", Name: "research", Status: "busy"}
	poll := func() { fa.setSessions(info) }
	restartCount := func() int {
		mu.Lock()
		defer mu.Unlock()
		return restarts
	}

	// A busy session is never restarted mid-turn, however long it stays without
	// a hello. The next check after it is idle may try.
	deadline := time.Now().Add(3 * after)
	for time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
		poll()
	}
	if got := restartCount(); got != 0 {
		t.Fatalf("restarted a busy session %d times", got)
	}

	info.Status = "idle"
	eventually(t, "one restart of the idle session", func() bool {
		poll()
		return restartCount() == 1
	})

	// The restarted session still has no hello: one intake entry, and never a
	// second restart.
	var ask protocol.IntakeEntry
	eventually(t, "one intake entry", func() bool {
		poll()
		for _, a := range mcpCheckAsks(t, g) {
			ask = a
			return true
		}
		return false
	})
	if !strings.Contains(ask.Text, "research") || !strings.Contains(ask.Text, "could not be connected") {
		t.Fatalf("unexpected item: %q", ask.Text)
	}
	deadline = time.Now().Add(3 * after)
	for time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
		poll()
		if got := restartCount(); got != 1 {
			t.Fatalf("%d restarts, want 1", got)
		}
		if got := len(mcpCheckAsks(t, g)); got != 1 {
			t.Fatalf("%d intake entries, want 1", got)
		}
	}

	// The daemon restarted: the restart does not come back, and the entry
	// counts no new failure.
	n.restart()
	deadline = time.Now().Add(3 * after)
	for time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
		poll()
		if got := restartCount(); got != 1 {
			t.Fatalf("restarted again after a daemon restart: %d", got)
		}
		if got := mcpCheckAsks(t, g); len(got) != 1 || got[0].Count != 1 {
			t.Fatalf("a new failure was counted after a daemon restart: %+v", got)
		}
	}
}

// A restart that fails raises one intake entry at once and is not tried again.
func TestFailedMCPRestartRaisesOneDeskAsk(t *testing.T) {
	t.Parallel()
	g := startGateway(t, t.TempDir(), "")
	const after = 100 * time.Millisecond
	var mu sync.Mutex
	restarts := 0
	fa := &fakeAdapter{harness: "claude"}
	startNode(t, g, nodeOpts{adapter: fa, cfg: func(c *Config) {
		c.MCPLinkGrace = 50 * time.Millisecond
		c.testMCPRepairAfter = after
		c.testMCPRestart = func(context.Context, string) error {
			mu.Lock()
			restarts++
			mu.Unlock()
			return errors.New("claude stop failed")
		}
	}})
	info := protocol.SessionInfo{Harness: "claude", NativeID: "sess-failed", Name: "research", Status: "idle"}
	poll := func() { fa.setSessions(info) }
	restartCount := func() int {
		mu.Lock()
		defer mu.Unlock()
		return restarts
	}

	eventually(t, "one restart attempt", func() bool {
		poll()
		return restartCount() == 1
	})
	var ask protocol.IntakeEntry
	eventually(t, "one intake entry", func() bool {
		poll()
		for _, a := range mcpCheckAsks(t, g) {
			ask = a
			return true
		}
		return false
	})
	if !strings.Contains(ask.Text, "research") || !strings.Contains(ask.Text, "could not be connected") {
		t.Fatalf("unexpected item: %q", ask.Text)
	}
	if !strings.Contains(ask.Detail, "claude stop failed") {
		t.Fatalf("the item does not carry the failure: %q", ask.Detail)
	}
	deadline := time.Now().Add(3 * after)
	for time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
		poll()
		if got := restartCount(); got != 1 {
			t.Fatalf("%d restarts after a failure, want 1", got)
		}
		if got := len(mcpCheckAsks(t, g)); got != 1 {
			t.Fatalf("%d intake entries after a failure, want 1", got)
		}
	}
}

// A session that said hello is never restarted.
func TestClaudeSessionWithMCPHelloIsNotRestarted(t *testing.T) {
	t.Parallel()
	d := &Daemon{cfg: Config{MCPLinkGrace: time.Millisecond, testMCPRepairAfter: time.Millisecond},
		mcpHello: map[string]time.Time{"sess-linked": time.Now()}}
	d.mu.Lock()
	d.noteMCPRepairLocked(protocol.SessionInfo{Harness: "claude", NativeID: "sess-linked", Name: "linked", Status: "idle"},
		"s_1", time.Now().Add(-time.Hour), time.Now())
	pending := len(d.pendingMCPRepairs)
	d.mu.Unlock()
	if pending != 0 {
		t.Fatalf("a session with an MCP hello was restarted: %d pending", pending)
	}
}

// Inside the wait nothing happens: a normal start says hello after a moment.
func TestClaudeSessionInsideTheMCPRepairWaitIsNotRestarted(t *testing.T) {
	t.Parallel()
	d := &Daemon{cfg: Config{MCPLinkGrace: time.Millisecond, testMCPRepairAfter: time.Minute}}
	d.mu.Lock()
	d.noteMCPRepairLocked(protocol.SessionInfo{Harness: "claude", NativeID: "sess-new", Name: "new", Status: "idle"},
		"s_0x01", time.Now().Add(-time.Second), time.Now())
	pending := len(d.pendingMCPRepairs)
	d.mu.Unlock()
	if pending != 0 {
		t.Fatalf("a new session was restarted: %d pending", pending)
	}
}
