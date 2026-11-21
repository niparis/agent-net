package gateway

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

// callRaw sends an mcp.call frame body as the daemon of node would.
func callRaw(g *Gateway, node, body string) error {
	_, err := g.handleMCP(context.Background(), &nodeConn{id: node}, "mcp.call", []byte(body))
	return err
}

func TestMCPRegistryRejectsSessionAttributedChanges(t *testing.T) {
	g, node := mcpGateway(t)
	session := plainSession(t, g, node, "mcp-agent")

	add, _ := json.Marshal(protocol.MCPAdd{
		Server:         protocol.MCPServer{Name: "agent-added", Transport: protocol.MCPHTTP, URL: "https://example.invalid/mcp"},
		AddedBySession: session,
	})
	if _, err := g.handleMCP(context.Background(), &nodeConn{id: node}, "mcp.add", add); err == nil || !strings.Contains(err.Error(), "owner-only") {
		t.Fatalf("session-attributed add: %v", err)
	}
	if _, ok, err := g.store.MCPServer("agent-added"); err != nil || ok {
		t.Fatalf("server after refused add: exists=%v err=%v", ok, err)
	}

	remove := []byte(`{"name":"files","added_by_session":"` + session + `"}`)
	if _, err := g.handleMCP(context.Background(), &nodeConn{id: node}, "mcp.remove", remove); err == nil || !strings.Contains(err.Error(), "owner-only") {
		t.Fatalf("session-attributed remove: %v", err)
	}
	if _, ok, err := g.store.MCPServer("files"); err != nil || !ok {
		t.Fatalf("server after refused remove: exists=%v err=%v", ok, err)
	}
}
