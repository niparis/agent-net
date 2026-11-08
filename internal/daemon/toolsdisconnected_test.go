package daemon

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// A live Claude Code session whose `agent-net mcp` process has no connection to
// the daemon for longer than the grace period is marked tools: disconnected.
// A connect clears the mark, a lost connection brings it back, and the turn
// text carries one line only while it is on.
func TestClaudeSessionWithoutMCPLinkIsMarkedDisconnected(t *testing.T) {
	t.Parallel()
	g := startGateway(t, t.TempDir(), "")
	sessDir := t.TempDir()
	// The test process stands in for `agent-net mcp`; its parent for Claude Code.
	file := filepath.Join(sessDir, strconv.Itoa(os.Getppid())+".json")
	if err := os.WriteFile(file, []byte(`{"pid":1,"sessionId":"sess-1"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	const grace = 400 * time.Millisecond
	fa := &fakeAdapter{harness: "claude"}
	n := startNode(t, g, nodeOpts{adapter: fa, claudeDir: sessDir, cfg: func(c *Config) { c.MCPLinkGrace = grace }})
	info := protocol.SessionInfo{Harness: "claude", NativeID: "sess-1", Name: "one", Status: "idle"}
	fa.setSessions(info)
	eventually(t, "session id", func() bool { return fa.host.SessionID("claude", "sess-1") != "" })
	id := fa.host.SessionID("claude", "sess-1")
	cli := cliClient(t, n)
	policy := func() protocol.PolicyResult {
		return askPolicy(t, cli, protocol.PolicyRequest{Harness: "claude", NativeID: "sess-1", Prompt: "go"})
	}
	// resync stands in for the one-second discovery poll.
	resync := func() { fa.setSessions(info) }

	// Inside the grace period nothing is marked: the MCP server of a new
	// session says hello a moment after the session appears.
	if a := agentByID(t, cli, id); a.ToolsDisconnected() {
		t.Fatalf("marked inside the grace period: %+v", a)
	}
	if got := policy().Notice; got != "" {
		t.Fatalf("notice inside the grace period: %q", got)
	}

	// No MCP link after the grace period: disconnected.
	time.Sleep(grace + 100*time.Millisecond)
	resync()
	eventually(t, "marked disconnected", func() bool {
		return agentByID(t, cli, id).ToolsVersion == protocol.ToolsDisconnected
	})
	if got := policy().Notice; got != protocol.ToolsDisconnectedNotice {
		t.Fatalf("notice = %q", got)
	}

	// An MCP connect clears the mark and the notice.
	lc := dialLocal(t, n.sock)
	var res map[string]any
	hello := map[string]any{"client": "mcp", "harness": "claude", "native_id": "sess-1", "version": "v0.5.0"}
	if err := json.Unmarshal(lc.mustCall("hello", hello), &res); err != nil {
		t.Fatal(err)
	}
	eventually(t, "mark cleared", func() bool {
		a := agentByID(t, cli, id)
		return !a.ToolsDisconnected() && a.ToolsVersion == "v0.5.0"
	})
	resync()
	time.Sleep(grace + 100*time.Millisecond)
	resync() // a poll after the grace period must not mark a linked session
	if a := agentByID(t, cli, id); a.ToolsDisconnected() {
		t.Fatalf("a linked session was marked: %+v", a)
	}
	if got := policy().Notice; got != "" {
		t.Fatalf("notice while linked: %q", got)
	}

	// The link drops (Claude Code stopped the process): after the grace
	// period the mark is back.
	lc.c.Close()
	time.Sleep(grace + 100*time.Millisecond)
	eventually(t, "marked again", func() bool {
		resync()
		return agentByID(t, cli, id).ToolsVersion == protocol.ToolsDisconnected
	})
	if got := policy().Notice; got != protocol.ToolsDisconnectedNotice {
		t.Fatalf("notice after the link dropped = %q", got)
	}
}

// A worker is never marked: agent-net starts its MCP server and watches it.
func TestClaudeWorkerIsNotMarkedDisconnected(t *testing.T) {
	t.Parallel()
	d := &Daemon{cfg: Config{MCPLinkGrace: time.Millisecond}}
	ls := &localSession{mcpSince: time.Now().Add(-time.Hour)}
	in := protocol.SessionInfo{Harness: "claude", NativeID: "w", Worker: &protocol.WorkerInfo{}}
	if got := d.toolsLinkInfoLocked(ls, in, time.Now()); got.ToolsVersion != "" {
		t.Fatalf("worker marked: %+v", got)
	}
	in.Worker = nil
	if got := d.toolsLinkInfoLocked(ls, in, time.Now()); got.ToolsVersion != protocol.ToolsDisconnected {
		t.Fatalf("session not marked: %+v", got)
	}
	in.Harness = "codex"
	if got := d.toolsLinkInfoLocked(ls, in, time.Now()); got.ToolsVersion != "" {
		t.Fatalf("a non-Claude session was marked: %+v", got)
	}
}
