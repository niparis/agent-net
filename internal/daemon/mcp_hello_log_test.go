package daemon

import (
	"log"
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

// A Codex MCP server that the daemon refuses says hello again on every
// reconnect. The daemon log names the refusal once, not once per hello.
func TestMCPHelloRefusalIsLoggedOnce(t *testing.T) {
	t.Parallel()
	g := startGateway(t, t.TempDir(), "")
	logs := &logBuf{}
	n := startNode(t, g, nodeOpts{logger: log.New(logs, "", 0)})
	hello := map[string]any{"client": "mcp", "harness": "codex", "native_id": "t1", "protocol": protocol.LocalProtocol}
	for i := 0; i < 2; i++ {
		lc := dialLocal(t, n.sock)
		if _, err := lc.call("hello", hello); err == nil {
			t.Fatalf("hello %d from a non-child was accepted", i)
		}
	}
	want := "mcp hello refused: codex t1: peer check failed: this daemon is not connected to a Codex app server"
	// The daemon answers the hello before it writes the line, so the second
	// call can return before the line is in the log. Wait for it; a second
	// line means the once-only rule broke, which waiting cannot hide.
	eventually(t, "the refusal line in the daemon log", func() bool {
		return strings.Count(logs.String(), want) >= 1
	})
	if got := logs.String(); strings.Count(got, want) != 1 {
		t.Fatalf("want one refusal line, got %d; daemon log:\n%s", strings.Count(got, want), got)
	}
}
