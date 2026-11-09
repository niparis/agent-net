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
	got := logs.String()
	want := "mcp hello refused: codex t1: peer check failed: this daemon is not connected to a Codex app server"
	if c := strings.Count(got, want); c != 1 {
		t.Fatalf("want one refusal line, got %d; daemon log:\n%s", c, got)
	}
}
