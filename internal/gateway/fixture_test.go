package gateway

import "testing"

// mcpGateway returns a gateway with one node, two registered projects and one
// shared MCP server named "files".
func mcpGateway(t *testing.T) (*Gateway, string) {
	t.Helper()
	g, node := archiveFixture(t)
	if err := g.store.AddMCPServer(mcpSpec("files", node)); err != nil {
		t.Fatal(err)
	}
	return g, node
}

// archiveFixture returns a gateway with one node and two registered projects.
func archiveFixture(t *testing.T) (*Gateway, string) {
	t.Helper()
	g, err := New(Options{DataDir: seededDataDir(t), TempFolders: []string{}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { g.Close() })
	node, _, err := g.store.CreateNode("fixture", "linux")
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"alpha", "beta"} {
		if _, err = g.store.db.Exec(`INSERT INTO projects(node_id,path,name,added_at,checked_at) VALUES(?,?,?,0,0)`, node, "/repo/"+p, p); err != nil {
			t.Fatal(err)
		}
	}
	return g, node
}
