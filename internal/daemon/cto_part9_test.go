package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

func TestCTOListCursorsAndParity(t *testing.T) {
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
		runner := proposalRunner{state: w.d.st, now: time.Now, validate: func(context.Context, proposalCall) error { return nil }, run: func(context.Context, proposalCall, string) (json.RawMessage, error) { return nil, nil }}
		if _, err = runner.propose(ctx, proposalCall{Tool: name, Args: json.RawMessage(`{}`)}, ""); err != nil {
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
	proposals, err := w.d.proposalRunner().list()
	if err != nil {
		t.Fatal(err)
	}
	detail, err := w.d.runCTOToolRaw(ctx, "cto_proposals_find", json.RawMessage(`{"id":"p_1"}`))
	if err != nil {
		t.Fatal(err)
	}
	db, _ := json.Marshal(detail)
	if !strings.Contains(string(db), `"status":"open"`) || !strings.Contains(string(db), `"note"`) {
		t.Fatalf("proposal detail missing status or outcome: %s", db)
	}
	args := map[string]any{"limit": 1}
	call := func() ctoList[json.RawMessage] {
		raw, _ := json.Marshal(args)
		out, err := w.d.runCTOToolRaw(ctx, "cto_proposals_find", raw)
		if err != nil {
			t.Fatal(err)
		}
		b, _ := json.Marshal(out)
		var p ctoList[json.RawMessage]
		if err = json.Unmarshal(b, &p); err != nil {
			t.Fatal(err)
		}
		return p
	}
	first := call()
	if first.Total != len(proposals) || first.Total < 2 || len(first.Items) != 1 || first.Next != 1 {
		t.Fatalf("first: %+v", first)
	}
	args["offset"] = first.Next
	second := call()
	if second.Total != first.Total || len(second.Items) != 1 || string(second.Items[0]) == string(first.Items[0]) {
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
	for _, term := range []string{"depends", "governs", "deploys", "advisor", "arbiter", "owner", "normal", "auto", "ask", "never"} {
		if !strings.Contains(guide, term) {
			t.Errorf("guide missing %s", term)
		}
	}
}
