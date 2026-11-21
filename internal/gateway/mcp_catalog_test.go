package gateway

import (
	"context"
	"encoding/json"
	"sort"
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

// plainSession registers a session on node.
func plainSession(t *testing.T, g *Gateway, node, native string) string {
	t.Helper()
	sid, err := g.store.UpsertSession(node, protocol.SessionInfo{Harness: "claude", NativeID: native, Name: native, Status: "idle", Cwd: "/repo/alpha"})
	if err != nil {
		t.Fatal(err)
	}
	return sid
}

func catalogNames(t *testing.T, g *Gateway, node string) []string {
	t.Helper()
	out, err := g.handleMCP(context.Background(), &nodeConn{id: node}, "mcp.catalog", []byte(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(out)
	var r struct {
		Servers []protocol.MCPServer `json:"servers"`
	}
	if err := json.Unmarshal(b, &r); err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, s := range r.Servers {
		names = append(names, s.Name)
	}
	sort.Strings(names)
	return names
}

// Every registered server is global: the catalog lists each enabled one, and a
// disabled one is left out and refused.
func TestCatalogListsEveryEnabledServer(t *testing.T) {
	g, node := mcpGateway(t)
	if err := g.store.AddMCPServer(mcpSpec("wiki", node)); err != nil {
		t.Fatal(err)
	}
	if err := g.store.AddMCPServer(mcpSpec("off", node)); err != nil {
		t.Fatal(err)
	}
	if _, err := g.store.SetMCPEnabled("off", false, "", ""); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(catalogNames(t, g, node), ","); got != "files,wiki" {
		t.Fatalf("catalog: %s", got)
	}
	raw, _ := json.Marshal(protocol.MCPCall{Server: "off", Tool: "read"})
	if err := callRaw(g, node, string(raw)); err == nil || !strings.Contains(err.Error(), "disabled") {
		t.Fatalf("call of a disabled server: %v", err)
	}
}

// With the host offline a call says which host, and the tools stay in the
// catalog marked offline.
func TestOfflineHostSaysSo(t *testing.T) {
	g, node := mcpGateway(t)
	raw, _ := json.Marshal(protocol.MCPCall{Server: "files", Tool: "read"})
	err := callRaw(g, node, string(raw))
	if err == nil || !strings.Contains(err.Error(), "host fixture offline") {
		t.Fatalf("want a clear host offline error, got %v", err)
	}
	out, err := g.handleMCP(context.Background(), &nodeConn{id: node}, "mcp.catalog", []byte(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(out)
	var r struct {
		Servers []protocol.MCPServer `json:"servers"`
	}
	json.Unmarshal(b, &r)
	if len(r.Servers) != 1 || r.Servers[0].HostOnline || len(r.Servers[0].Tools) != 2 {
		t.Fatalf("catalog with an offline host: %+v", r.Servers)
	}
}
