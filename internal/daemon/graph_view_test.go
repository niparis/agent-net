package daemon

import (
	"encoding/json"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

// graph_view is one read through the gateway: the daemon adds nothing and
// changes nothing.
func TestGraphViewReadsTheGatewayGraph(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	n := startNode(t, g, nodeOpts{})
	eventually(t, "connected", func() bool { return n.d.Connected() })
	st := g.gw.Store()
	if _, err := st.AddProjects(n.d.cfg.NodeID, protocol.ProjectAdd{Projects: []protocol.ProjectEntry{
		{Name: "cogforge", Path: "/tmp/cogforge"},
		{Name: "tenebrae", Path: "/tmp/tenebrae"},
	}}); err != nil {
		t.Fatal(err)
	}
	if err := st.AddArchitect("trading-architect", "owner"); err != nil {
		t.Fatal(err)
	}
	if err := st.SetCharter("cogforge", "maturity", "early - prototype", "owner"); err != nil {
		t.Fatal(err)
	}
	if err := st.SetEdge(protocol.Edge{From: "trading-architect", To: "cogforge", Kind: "governs", Mode: "owner"}, "owner"); err != nil {
		t.Fatal(err)
	}

	cli := cliClient(t, n)
	raw, err := cli.call(protocol.OpGraphView, protocol.QueryGraph{})
	if err != nil {
		t.Fatal(err)
	}
	var view protocol.GraphView
	if err := json.Unmarshal(raw, &view); err != nil {
		t.Fatal(err)
	}
	if len(view.Nodes) != 3 || len(view.Edges) != 1 {
		t.Fatalf("graph through the daemon: %#v", view)
	}
	found := false
	for _, node := range view.Nodes {
		if node.Name == "cogforge" && node.Charter.Maturity == "early - prototype" {
			found = true
		}
	}
	if !found {
		t.Fatalf("no cogforge charter: %#v", view.Nodes)
	}
	if len(view.Changes) == 0 || view.Changes[0].Attr != "maturity" {
		t.Fatalf("no history: %#v", view.Changes)
	}
}
