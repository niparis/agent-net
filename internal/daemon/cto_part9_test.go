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

func TestCTOSessionPage(t *testing.T) {
	agents := make([]protocol.AgentView, 40)
	for i := range agents {
		agents[i] = protocol.AgentView{SessionID: fmt.Sprintf("session-%024d", i), Status: strings.Repeat("s", 40), Profile: protocol.Profile{Name: strings.Repeat("n", 80), Project: strings.Repeat("p", 80), Task: strings.Repeat("x", 200)}}
	}
	first := ctoSessionPage(agents, 0, 0)
	if first.Total != 40 || len(first.Items) != 20 || first.Next != 20 {
		t.Fatalf("first: %+v", first)
	}
	second := ctoSessionPage(agents, 0, first.Next)
	if len(second.Items) != 20 || second.Next != 0 || second.Items[0].ID == first.Items[0].ID {
		t.Fatalf("second: %+v", second)
	}
	b, _ := json.Marshal(first)
	for i := range agents {
		agents[i].Profile.Name = strings.Repeat("<", 80)
		agents[i].Profile.Task = strings.Repeat("<", 200)
		agents[i].Status = strings.Repeat("<", 40)
	}
	escaped, _ := json.Marshal(ctoSessionPage(agents, 0, 0))
	if len(escaped) >= 3000 {
		t.Fatalf("escaped page: %d bytes", len(escaped))
	}
	if len(b) >= 3000 {
		t.Fatalf("page: %d bytes", len(b))
	}
}

func TestCTOConversationRows(t *testing.T) {
	messages := []protocol.Message{{ID: "m1", Body: "private body", From: "a", To: "b"}, {ID: "m2", Body: "another"}}
	feed := ctoConversationRows(messages, true)
	raw, _ := json.Marshal(feed)
	if strings.Contains(string(raw), "private body") {
		t.Fatalf("feed leaked body: %s", raw)
	}
	rows := ctoConversationRows(messages, false)
	if len(rows) != 2 || rows[0].ID != "m1" {
		t.Fatalf("rows: %+v", rows)
	}
}

func TestCTOProjectRows(t *testing.T) {
	rows := ctoProjectRows([]protocol.ProjectView{{Name: "project", DisplayName: strings.Repeat("x", 100), NodeID: "node"}})
	if len(rows) != 1 || len([]rune(rows[0].Display)) > 80 {
		t.Fatalf("rows: %+v", rows)
	}
}

func TestCTOWorkerRows(t *testing.T) {
	rows := ctoWorkerRows([]protocol.AgentView{{SessionID: "s1", Online: true, Worker: &protocol.WorkerInfo{}, Profile: protocol.Profile{Task: strings.Repeat("t", 200)}}})
	if len(rows) != 1 || rows[0].ID != "s1" || len([]rune(rows[0].Task)) > 80 {
		t.Fatalf("rows: %+v", rows)
	}
}

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
		id, err := st.UpsertSession(w.cfg.NodeID, protocol.SessionInfo{Harness: "pi", NativeID: name, Cwd: "/tmp/" + name, Repo: protocol.RepoInfo{Project: name}, Worker: &protocol.WorkerInfo{Project: name, Task: name}})
		if err != nil {
			t.Fatal(err)
		}
		if _, _, err = st.StoreMessage(protocol.MsgSend{Sender: id, Recipient: w.d.ctoSessionID(), Body: name, Type: protocol.TypeRequest}); err != nil {
			t.Fatal(err)
		}
		if err = st.AddWorkerProfile(protocol.WorkerProfile{Name: name, Harness: "pi", Model: name, AddedBy: id}); err != nil {
			t.Fatal(err)
		}
		if err = st.AddMCPServer(protocol.MCPServer{Name: name, NodeID: w.cfg.NodeID, Transport: protocol.MCPHTTP, URL: "https://example.test/mcp"}); err != nil {
			t.Fatal(err)
		}
		runner := proposalRunner{state: w.d.st, now: time.Now, validate: func(context.Context, proposalCall) error { return nil }, run: func(context.Context, proposalCall, string) (json.RawMessage, error) { return nil, nil }}
		if _, err = runner.propose(ctx, proposalCall{Tool: name, Args: json.RawMessage(`{}`)}, ""); err != nil {
			t.Fatal(err)
		}
	}
	caller := w.d.ctoReadConn()
	source := func(method string, args any, dst any) {
		t.Helper()
		raw, _ := json.Marshal(args)
		value, err := caller.dispatch(ctx, method, raw)
		if err != nil {
			t.Fatal(err)
		}
		if err = ctoDecode(value, dst); err != nil {
			t.Fatal(err)
		}
	}
	var agents protocol.AgentsResult
	source("list", map[string]any{}, &agents)
	var nodes protocol.NodesInfo
	source("nodes", map[string]any{}, &nodes)
	var projects protocol.ProjectsResult
	source("projects", map[string]any{}, &projects)
	var messages struct {
		Messages []protocol.Message `json:"messages"`
		Total    int                `json:"total"`
	}
	source("messages", map[string]any{"limit": 1}, &messages)
	var mcp struct {
		Servers []protocol.MCPServer `json:"servers"`
	}
	source("mcp_list", map[string]any{}, &mcp)
	var profiles []protocol.WorkerProfile
	source(protocol.OpWorkerProfiles, map[string]any{}, &profiles)
	proposals, err := w.d.proposalRunner().list()
	if err != nil {
		t.Fatal(err)
	}
	graphNode, err := w.d.runCTOToolRaw(ctx, "graph", json.RawMessage(`{"node":"fixture-0"}`))
	if err != nil {
		t.Fatal(err)
	}
	graphJSON, _ := json.Marshal(graphNode)
	if !strings.Contains(string(graphJSON), `"node":"fixture-0"`) {
		t.Fatalf("graph node detail: %s", graphJSON)
	}
	overview, err := w.d.runCTOToolRaw(ctx, "overview", json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	summary := overview.(map[string]any)
	online, machinesOnline := 0, 0
	for _, a := range agents.Agents {
		if a.Online {
			online++
		}
	}
	for _, n := range nodes.Nodes {
		if n.Online {
			machinesOnline++
		}
	}
	if summary["sessions_online"] != online || summary["machines_online"] != machinesOnline || summary["workers_running"] != len(protocol.WorkersOf(agents.Agents)) || summary["proposals_open"] != len(proposals) {
		t.Fatalf("overview differs from public sources: %v", summary)
	}
	detailArgs, _ := json.Marshal(map[string]string{"message_id": messages.Messages[0].ID})
	detailMessage, err := w.d.runCTOToolRaw(ctx, "messages", detailArgs)
	if err != nil {
		t.Fatal(err)
	}
	direct, err := caller.dispatch(ctx, "message_status", detailArgs)
	if err != nil {
		t.Fatal(err)
	}
	gotJSON, _ := json.Marshal(detailMessage)
	directJSON, _ := json.Marshal(direct)
	if string(gotJSON) != string(directJSON) {
		t.Fatalf("message differs from agent source: %s / %s", gotJSON, directJSON)
	}
	totals := map[string]int{"sessions": len(agents.Agents), "proposals": len(proposals), "feed": messages.Total, "messages": messages.Total, "graph": len(projects.Projects), "machines": len(nodes.Nodes), "projects": len(projects.Projects), "workers": len(protocol.WorkersOf(agents.Agents)), "tools": len(mcp.Servers), "settings": len(profiles)}
	detail, err := w.d.runCTOToolRaw(ctx, "proposals", json.RawMessage(`{"id":"p_1"}`))
	if err != nil {
		t.Fatal(err)
	}
	db, _ := json.Marshal(detail)
	if !strings.Contains(string(db), `"status":"open"`) || !strings.Contains(string(db), `"note"`) {
		t.Fatalf("proposal detail missing status or outcome: %s", db)
	}
	for _, view := range []string{"sessions", "proposals", "feed", "messages", "graph", "machines", "projects", "workers", "tools", "settings"} {
		t.Run(view, func(t *testing.T) {
			args := map[string]any{"limit": 1}
			if view == "settings" {
				args["area"] = "worker_profiles"
			}
			call := func() ctoList[json.RawMessage] {
				raw, _ := json.Marshal(args)
				out, err := w.d.runCTOToolRaw(ctx, view, raw)
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
			if first.Total != totals[view] || first.Total < 2 || len(first.Items) != 1 || first.Next != 1 {
				t.Fatalf("first: %+v", first)
			}
			args["offset"] = first.Next
			second := call()
			if second.Total != first.Total || len(second.Items) != 1 || string(second.Items[0]) == string(first.Items[0]) {
				t.Fatalf("second: %+v", second)
			}
		})
	}
}

func TestCTOGraphGuideExplainsEdges(t *testing.T) {
	var d Daemon
	value, err := d.readCTOTool(context.Background(), "graph", json.RawMessage(`{"view":"guide"}`))
	if err != nil {
		t.Fatal(err)
	}
	guide := value.(string)
	for _, term := range []string{"depends", "governs", "deploys", "advisor", "arbiter", "owner", "normal", "auto", "ask", "never"} {
		if !strings.Contains(guide, term) {
			t.Errorf("guide missing %s", term)
		}
	}
}

func TestCTOMessagePageRequiresTotal(t *testing.T) {
	if _, err := ctoDecodeMessagePage(json.RawMessage(`{"messages":[]}`), 0, false); err == nil {
		t.Fatal("old gateway without total accepted")
	}
}
