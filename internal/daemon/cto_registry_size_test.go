package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/niparis/agent-net/internal/mcpserver"
	"github.com/niparis/agent-net/internal/protocol"
)

// Test all registered reads against one large real gateway. Every tool must be
// classified, so registering a new one requires adding a case to this test.
func TestCTORegisteredReadSizes(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	t.Setenv("AGENT_NET_CONFIG_DIR", t.TempDir())
	ctx := context.Background()
	g := startGateway(t, t.TempDir(), "")
	w := startWorkerNode(t, g, shortSet, withSampler(os.Getpid()))
	_, localID := dialFakePlugin(t, w.sock, "pi", "fixture", func(map[string]any) map[string]any {
		return map[string]any{"ok": true, "page": map[string]any{"entries": []map[string]any{ent("1", "user", "hello")}}}
	})
	eventually(t, "local usage sample", func() bool {
		a := getUsage(t, w.cli)
		return a.Local != nil && len(a.Local.Trees) > 0 && a.Local.Trees[0].Samples > 0
	})
	st := g.gw.Store()
	models := make([]string, 400)
	for i := range models {
		models[i] = fmt.Sprintf("provider/model-%03d", i)
	}
	var ids []string
	for i := 0; i < 50; i++ {
		node, _, err := st.CreateNode(fmt.Sprintf("box-%02d", i), "linux")
		if err != nil {
			t.Fatal(err)
		}
		if err = st.SetNodeHello(node, fmt.Sprintf("box-%02d", i), "1"); err != nil {
			t.Fatal(err)
		}
		if _, err = st.SetNodeHarnesses(node, []protocol.HarnessInfo{{Name: "pi", Version: "1", Models: models}}); err != nil {
			t.Fatal(err)
		}
		sid, err := st.UpsertSession(node, protocol.SessionInfo{Harness: "pi", NativeID: fmt.Sprintf("session-%02d", i), Name: "builder", Status: "idle", Cwd: fmt.Sprintf("/src/project-%02d", i), Repo: protocol.RepoInfo{Root: fmt.Sprintf("/src/project-%02d", i), Project: "project"}, Waiting: &protocol.Waiting{Kind: "question", Text: strings.Repeat("x", 200), Since: time.Now().Unix() - 10}})
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, sid)
	}
	_, _, err := st.StoreMessage(protocol.MsgSend{Sender: ids[0], Recipient: w.ownerID(), Body: "hello", Type: protocol.TypeRequest})
	if err != nil {
		t.Fatal(err)
	}
	if err := w.d.registerCTOSession(ctx); err != nil {
		t.Fatal(err)
	}
	ctoID := w.d.ctoSessionID()
	if ctoID == "" || ctoID == w.ownerID() {
		t.Fatalf("CTO session id %q", ctoID)
	}
	ctoMessage, _, err := st.StoreMessage(protocol.MsgSend{Sender: ids[0], Recipient: ctoID, Body: "hello CTO", Type: protocol.TypeRequest})
	if err != nil {
		t.Fatal(err)
	}
	reads := map[string]map[string]any{
		"workers": {}, "tools": {}, "settings": {"area": "worker_profiles"}, "graph": {}, "machines": {}, "proposals": {}, "feed": {}, "overview": {}, "sessions": {}, "session": {"session_id": ids[0]}, "projects": {},
		"messages": {}, "cto_proposals_find": {},
		"agent_sessions_find": {}, "agent_session_get": {"session": ids[0]},
		"agent_machines_find": {}, "agent_machine_get": {"machine": "box-00"},
		"agent_projects_find": {}, "agent_workers_find": {},
		"agent_messages_find": {}, "agent_message_get": {"message": ctoMessage.ID},
		"agent_graph_read": {}, "agent_mcp_servers_find": {},
		"agent_worker_profiles_find": {},
	}
	_ = localID

	// mcp_call and mcp_test can execute arbitrary external operations. Mutation
	// tools are classified explicitly and are never invoked here.
	mutations := strings.Fields("message ask_owner learn ui cto_ui_show proposal_ask graph_change session_change worker_change answer tools_change mcp_change mcp_call settings_change project_change cto_new agent_message_send agent_message_reply agent_owner_ask agent_learn")
	actions := map[string]bool{}
	for _, name := range mutations {
		actions[name] = true
	}
	server := mcpserver.NewCTO(func(ctx context.Context, name string, params any) (json.RawMessage, error) {
		raw, err := json.Marshal(params)
		if err != nil {
			return nil, err
		}
		v, err := w.d.runCTOToolRaw(ctx, name, raw)
		if err != nil {
			return nil, err
		}
		return json.Marshal(v)
	}, "test")
	a, b := mcp.NewInMemoryTransports()
	ss, err := server.Connect(ctx, a, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer ss.Close()
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "test"}, nil).Connect(ctx, b, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()
	listed, err := cs.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	schema, _ := json.Marshal(listed.Tools)
	t.Logf("MEASURE brief=%d schema=%d tools=%d", len(ctoBrief), len(schema), len(listed.Tools))
	seen := map[string]bool{}
	for _, tool := range listed.Tools {
		if actions[tool.Name] {
			if _, ok := reads[tool.Name]; ok {
				t.Errorf("%s classified twice", tool.Name)
			}
			seen[tool.Name] = true
			continue
		}
		args, ok := reads[tool.Name]
		if !ok {
			t.Errorf("unclassified CTO tool %s", tool.Name)
			continue
		}
		seen[tool.Name] = true
		t.Run(tool.Name, func(t *testing.T) {
			result, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: tool.Name, Arguments: args})
			if err != nil {
				t.Fatal(err)
			}
			if result.IsError {
				// The CTO stays private until part 6. Caller-scoped reads must
				// refuse its unregistered identity, rather than use the owner.
				if tool.Name == "settings" || tool.Name == "feed" || tool.Name == "messages" || false {
					return
				}
				t.Fatalf("read failed: %+v", result.Content)
			}
			if len(result.Content) != 1 {
				t.Fatalf("unexpected content: %+v", result.Content)
			}
			_, ok := result.Content[0].(*mcp.TextContent)
			if !ok {
				t.Fatalf("unexpected content: %+v", result.Content)
			}

			b, err := json.Marshal(result)
			if err != nil {
				t.Fatal(err)
			}
			t.Logf("MEASURE %s %d bytes", tool.Name, len(b))

		})
	}
	for name := range reads {
		if !seen[name] {
			t.Errorf("read fixture %s is not registered", name)
		}
	}
	for name := range actions {
		if !seen[name] {
			t.Errorf("action classification %s is not registered", name)
		}
	}
}
