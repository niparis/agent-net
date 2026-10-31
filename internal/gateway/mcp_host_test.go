package gateway

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

// With the host offline a call says which host, the tools stay in the catalog
// marked offline, and the assignment is kept.
func TestOfflineHostKeepsAssignmentAndSaysSo(t *testing.T) {
	g, node := scopeGateway(t)
	alpha := scopedSession(t, g, node, "a", "alpha")
	assignMCP(t, g, "files", protocol.ToolAssignment{Scope: "projects", Projects: []string{"alpha"}})

	err := callAs(g, node, alpha)
	if err == nil || !strings.Contains(err.Error(), "host nas offline") {
		t.Fatalf("want a clear host offline error, got %v", err)
	}
	if strings.Contains(err.Error(), "not assigned") {
		t.Fatalf("an offline host looks like an authorization failure: %v", err)
	}

	raw, _ := json.Marshal(protocol.MCPCatalog{SessionID: alpha})
	out, err := g.handleMCP(context.Background(), &nodeConn{id: node}, "mcp.catalog", raw)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(out)
	var r struct {
		Servers []protocol.MCPServer `json:"servers"`
	}
	json.Unmarshal(b, &r)
	if len(r.Servers) != 1 || r.Servers[0].HostOnline || len(r.Servers[0].Tools) != 2 || r.Servers[0].Scope != "projects" {
		t.Fatalf("catalog with an offline host: %+v", r.Servers)
	}
	if a, _ := g.store.MCPAssignment("files"); a.Scope != "projects" || len(a.Projects) != 1 {
		t.Fatalf("assignment dropped: %+v", a)
	}
}
