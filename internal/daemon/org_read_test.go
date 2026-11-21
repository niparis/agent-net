package daemon

import (
	"context"
	"encoding/json"
	"sort"
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

// The CTO's agent_graph_read with root reads one subtree of the org tree,
// and each Agent row says whom it reports to; agent_session_get says it for
// a session. Run with -v to see both answers.
func TestCTOGraphReadRootIsOneSubtree(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	w := startWorkerNode(t, g, shortSet, nil)
	sessions := map[string]string{}
	for _, name := range []string{"org-lead", "org-member", "org-other"} {
		_, sessions[name] = helloPi(t, w.node, name, "/w/"+name, true)
	}
	ctx := context.Background()
	if _, err := w.d.call(ctx, "hello", map[string]any{"hostname": "fixture", "version": "v0.4.145", "graph_refs": true}); err != nil {
		t.Fatal(err)
	}
	waitGraphCutover(t, g)
	if err := g.gw.Store().WriteTeam(protocol.TeamWrite{Action: "create", ID: "t_org", Name: "Org", Owner: &protocol.GraphRef{Kind: "agent", ID: "org-lead"}}, "owner"); err != nil {
		t.Fatal(err)
	}
	if err := g.gw.Store().WriteTeam(protocol.TeamWrite{Action: "membership", ID: "t_org", Member: &protocol.GraphRef{Kind: "agent", ID: "org-member"}}, "owner"); err != nil {
		t.Fatal(err)
	}
	if err := w.d.registerCTOSession(ctx); err != nil {
		t.Fatal(err)
	}
	res, err := w.d.runCTOToolRaw(ctx, "agent_graph_read", json.RawMessage(`{"root":"org-lead"}`))
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(res)
	var out struct {
		Nodes []struct {
			Name      string `json:"name"`
			ReportsTo string `json:"reports_to"`
		} `json:"nodes"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("%s: %v", raw, err)
	}
	var got []string
	for _, n := range out.Nodes {
		got = append(got, n.Name+"<"+n.ReportsTo)
	}
	sort.Strings(got)
	if strings.Join(got, " ") != "org-lead<cto org-member<org-lead" {
		t.Fatalf("root org-lead read %v: %s", got, raw)
	}
	t.Logf("agent_graph_read {\"root\":\"org-lead\"}: %s", raw)

	res, err = w.d.runCTOToolRaw(ctx, "agent_session_get", json.RawMessage(`{"session":"`+sessions["org-member"]+`"}`))
	if err != nil {
		t.Fatal(err)
	}
	raw, _ = json.Marshal(res)
	var row struct {
		Session struct {
			ReportsTo string `json:"reports_to"`
		} `json:"session"`
	}
	if err := json.Unmarshal(raw, &row); err != nil || row.Session.ReportsTo != "org-lead (lead session org-lead)" {
		t.Fatalf("agent_session_get: %s %v", raw, err)
	}
	t.Logf("agent_session_get org-member: %s", raw)
}
