package gateway

import (
	"context"
	"encoding/json"
	"sort"
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

// scopedSession registers a session on node whose project is project.
func scopedSession(t *testing.T, g *Gateway, node, native, project string) string {
	t.Helper()
	info := protocol.SessionInfo{Harness: "claude", NativeID: native, Name: native, Status: "idle", Cwd: "/repo/" + project}
	if project != "" {
		info.Repo = protocol.RepoInfo{Root: "/repo/" + project, Project: project}
	}
	sid, err := g.store.UpsertSession(node, info)
	if err != nil {
		t.Fatal(err)
	}
	return sid
}

func catalogNames(t *testing.T, g *Gateway, node, session string) ([]string, error) {
	t.Helper()
	raw, _ := json.Marshal(protocol.MCPCatalog{SessionID: session})
	out, err := g.handleMCP(context.Background(), &nodeConn{id: node}, "mcp.catalog", raw)
	if err != nil {
		return nil, err
	}
	b, _ := json.Marshal(out)
	var r struct {
		Servers []protocol.MCPServer `json:"servers"`
	}
	if err = json.Unmarshal(b, &r); err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, s := range r.Servers {
		names = append(names, s.Name)
	}
	sort.Strings(names)
	return names, nil
}

func assignMCP(t *testing.T, g *Gateway, name string, a protocol.ToolAssignment) {
	t.Helper()
	id, ok, err := g.store.MCPItemID(name)
	if err != nil || !ok {
		t.Fatalf("no item for %s: %v", name, err)
	}
	if _, err = g.store.AssignTool(id, a, "owner", "test"); err != nil {
		t.Fatal(err)
	}
}

func TestCatalogShowsOnlyServersAssignedToTheSessionsProject(t *testing.T) {
	g, node := scopeGateway(t)
	if err := g.store.AddMCPServer(mcpSpec("wiki", node)); err != nil {
		t.Fatal(err)
	}
	if err := g.store.AddMCPServer(mcpSpec("gone", node)); err != nil {
		t.Fatal(err)
	}
	alpha := scopedSession(t, g, node, "a", "alpha")
	beta := scopedSession(t, g, node, "b", "beta")
	none := scopedSession(t, g, node, "n", "")

	// No assignment means global: every session sees every server.
	for _, sid := range []string{alpha, beta, none} {
		if got, err := catalogNames(t, g, node, sid); err != nil || strings.Join(got, ",") != "files,gone,wiki" {
			t.Fatalf("global catalog for %s: %v %v", sid, got, err)
		}
	}
	assignMCP(t, g, "files", protocol.ToolAssignment{Scope: "projects", Projects: []string{"alpha"}})
	assignMCP(t, g, "gone", protocol.ToolAssignment{Scope: "uninstalled"})
	for sid, want := range map[string]string{alpha: "files,wiki", beta: "wiki", none: "wiki"} {
		if got, err := catalogNames(t, g, node, sid); err != nil || strings.Join(got, ",") != want {
			t.Fatalf("catalog for %s = %v (%v), want %s", sid, got, err, want)
		}
	}
	// A caller with no session at all is a session with no project.
	if got, err := catalogNames(t, g, node, ""); err != nil || strings.Join(got, ",") != "wiki" {
		t.Fatalf("catalog without a session: %v %v", got, err)
	}
	// A disabled server is not in the catalog.
	if _, err := g.store.SetMCPEnabled("wiki", false, node, ""); err != nil {
		t.Fatal(err)
	}
	if got, _ := catalogNames(t, g, node, alpha); strings.Join(got, ",") != "files" {
		t.Fatalf("disabled server listed: %v", got)
	}
}

func TestCatalogRefusesASessionOfAnotherNode(t *testing.T) {
	g, node := scopeGateway(t)
	other, _, err := g.store.CreateNode("laptop", "linux")
	if err != nil {
		t.Fatal(err)
	}
	foreign := scopedSession(t, g, other, "x", "alpha")
	assignMCP(t, g, "files", protocol.ToolAssignment{Scope: "projects", Projects: []string{"alpha"}})
	if got, err := catalogNames(t, g, node, foreign); err == nil {
		t.Fatalf("a session of another node got a catalog: %v", got)
	}
	if _, err := catalogNames(t, g, node, "s_doesnotexist"); err == nil {
		t.Fatal("an unknown session got a catalog")
	}
}
