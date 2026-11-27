package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

func TestCTOListCursorsAndParity(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	g := startGateway(t, t.TempDir(), "")
	w := startWorkerNode(t, g, shortSet, nil)
	if err := w.d.registerCTOSession(ctx); err != nil {
		t.Fatal(err)
	}
	st := g.gw.Store()
	for i := 0; i < 3; i++ {
		name := fmt.Sprintf("fixture-%d", i)
		node, _, err := st.CreateNode(name, "linux")
		if err != nil {
			t.Fatal(err)
		}
		if err = st.SetNodeHello(node, name, "1"); err != nil {
			t.Fatal(err)
		}
		if _, err = st.UpsertSession(w.cfg.NodeID, protocol.SessionInfo{Harness: "pi", NativeID: name, Cwd: "/src/" + name, Repo: protocol.RepoInfo{Root: "/src/" + name, Project: name}, Worker: &protocol.WorkerInfo{Project: name, Task: name}}); err != nil {
			t.Fatal(err)
		}
	}
	graphNode, err := w.d.runCTOToolRaw(ctx, "agent_graph_read", json.RawMessage(`{"node":"fixture-0"}`))
	if err != nil {
		t.Fatal(err)
	}
	graphJSON, _ := json.Marshal(graphNode)
	if !strings.Contains(string(graphJSON), `"name":"fixture-0"`) {
		t.Fatalf("graph node detail: %s", graphJSON)
	}
	// Owner item lists page by offset: the next page starts where this one
	// ended and the total stays the same.
	rows := []string{"a", "b", "c"}
	first := ctoOffsetPage(rows, 1, 0)
	if first.Total != 3 || len(first.Items) != 1 || first.Next != 1 {
		t.Fatalf("first: %+v", first)
	}
	second := ctoOffsetPage(rows, 1, first.Next)
	if second.Total != 3 || len(second.Items) != 1 || second.Items[0] == first.Items[0] {
		t.Fatalf("second: %+v", second)
	}
}

func TestCTOGraphGuideExplainsEdges(t *testing.T) {
	var d Daemon
	value, err := d.readCTOTool(context.Background(), "agent_graph_read", json.RawMessage(`{"view":"guide"}`))
	if err != nil {
		t.Fatal(err)
	}
	guide := value.(map[string]any)["note"].(string)
	if !strings.HasPrefix(guide, protocol.CharterGuideText()) {
		t.Errorf("guide does not start with the charter guide")
	}
	for _, term := range []string{"depends", "deploys", "owner", "normal", "auto", "ask", "never"} {
		if !strings.Contains(guide, term) {
			t.Errorf("guide missing %s", term)
		}
	}
}
