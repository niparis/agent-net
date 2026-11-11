package daemon

import (
	"strings"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// A Claude session that agent-net sees as online, but whose `agent-net mcp`
// process never said hello, gets one Desk item that says so. The owner can
// type /mcp there. Further discovery polls add no second item.
func TestClaudeSessionWithoutMCPHelloRaisesOneDeskAsk(t *testing.T) {
	t.Parallel()
	g := startGateway(t, t.TempDir(), "")
	const after = 150 * time.Millisecond
	fa := &fakeAdapter{harness: "claude"}
	n := startNode(t, g, nodeOpts{adapter: fa, cfg: func(c *Config) {
		c.MCPLinkGrace = 50 * time.Millisecond
		c.testMCPDeskAfter = after
	}})
	info := protocol.SessionInfo{Harness: "claude", NativeID: "sess-no-mcp", Name: "research", Status: "idle"}
	// setSessions stands in for the discovery poll.
	deskAsks := func() map[string]protocol.Ask {
		fa.setSessions(info)
		asks, err := g.gw.Store().ListAsks()
		if err != nil {
			t.Fatal(err)
		}
		found := map[string]protocol.Ask{}
		for _, a := range asks {
			if a.Kind == protocol.AskKindCheck && strings.Contains(a.Question, "/mcp") {
				found[a.ID] = a
			}
		}
		return found
	}

	var ask protocol.Ask
	eventually(t, "one Desk ask about the missing agent-net tools", func() bool {
		for _, a := range deskAsks() {
			ask = a
			return true
		}
		return false
	})
	if !strings.Contains(ask.Question, "research") {
		t.Fatalf("the ask does not name the session: %q", ask.Question)
	}
	if !strings.Contains(ask.Question, "not connected") {
		t.Fatalf("the ask does not say the tools are not connected: %q", ask.Question)
	}

	// The session stays broken through several polls: still one item.
	deadline := time.Now().Add(2 * after)
	for time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
		if count := len(deskAsks()); count != 1 {
			t.Fatalf("%d Desk asks for one session, want 1", count)
		}
	}

	// The owner dismissed it and the daemon restarted: no new item.
	if err := g.gw.Store().CloseExternalAsk(n.d.cfg.NodeID, protocol.AskClose{
		Ref: "mcp:sess-no-mcp", Reason: protocol.AskReasonOwnerReplied}); err != nil {
		t.Fatal(err)
	}
	n.restart()
	deadline = time.Now().Add(3 * after)
	for time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
		if asks := deskAsks(); len(asks) != 0 {
			t.Fatalf("the Desk item came back after a daemon restart: %+v", asks)
		}
	}
}

// A session that said hello before the wait is over gets no item.
func TestClaudeSessionWithMCPHelloGetsNoDeskAsk(t *testing.T) {
	t.Parallel()
	d := &Daemon{cfg: Config{MCPLinkGrace: time.Millisecond, testMCPDeskAfter: time.Millisecond},
		mcpHello: map[string]time.Time{"sess-linked": time.Now()}}
	d.mu.Lock()
	d.noteMCPDeskLocked(protocol.SessionInfo{Harness: "claude", NativeID: "sess-linked", Name: "linked"}, time.Now().Add(-time.Hour), time.Now())
	pending := len(d.pendingMCPDesk)
	d.mu.Unlock()
	if pending != 0 {
		t.Fatalf("a session with an MCP hello was reported: %d pending", pending)
	}
}

// Inside the wait nothing is queued: a normal start says hello after a moment.
func TestClaudeSessionInsideTheMCPDeskWaitGetsNoAsk(t *testing.T) {
	t.Parallel()
	d := &Daemon{cfg: Config{MCPLinkGrace: time.Millisecond, testMCPDeskAfter: time.Minute}}
	d.mu.Lock()
	d.noteMCPDeskLocked(protocol.SessionInfo{Harness: "claude", NativeID: "sess-new", Name: "new"}, time.Now().Add(-time.Second), time.Now())
	pending := len(d.pendingMCPDesk)
	d.mu.Unlock()
	if pending != 0 {
		t.Fatalf("a new session was reported: %d pending", pending)
	}
}
