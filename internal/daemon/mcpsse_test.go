package daemon

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/niparis/agent-net/internal/protocol"
	"github.com/niparis/agent-net/internal/testmcp/tools"
)

// An SSE server works for the first check and for the long-lived connection,
// and a call from another node reaches it.
func TestMCPSSE(t *testing.T) {
	h := mcp.NewSSEHandler(func(*http.Request) *mcp.Server { return tools.New() }, nil)
	ts := httptest.NewServer(h)
	t.Cleanup(ts.Close)
	g := startGateway(t, t.TempDir(), "")
	a := startNode(t, g, nodeOpts{})
	b := startNode(t, g, nodeOpts{})
	cliA, cliB := cliClient(t, a), cliClient(t, b)
	cliA.mustCall("mcp_add", protocol.MCPAdd{Server: protocol.MCPServer{Name: "sse-one", Transport: protocol.MCPSSE, URL: ts.URL}})
	out, err := callTool(t, cliB, "sse-one", "echo", map[string]string{"text": "over sse"})
	if err != nil || out.Text != "echo: over sse" {
		t.Fatalf("echo = %+v, %v", out, err)
	}
}
