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
	msg, _, err := st.StoreMessage(protocol.MsgSend{Sender: ids[0], Recipient: w.ownerID(), Body: "hello", Type: protocol.TypeRequest})
	if err != nil {
		t.Fatal(err)
	}
	reads := map[string]map[string]any{
		"graph_get": {}, "charter_suggestions": {}, "charter_history": {"node": "project"}, "edge_history": {}, "agents": {}, "workers": {}, "needs_you": {}, "feed": {}, "worker_profile_list": {},
		"info": {}, "nodes": {}, "projects": {}, "get": {"session_id": ids[0]}, "messages": {}, "message_status": {"message_id": msg.ID}, "session_text": {"session_id": localID}, "usage": {}, "usage_top": {"session_id": localID}, "counts": {}, "graph_view": {}, "session_enter": {"session_id": localID}, "integrations_get": {}, "held_list": {}, "inbox_list": {}, "outcome_inbox": {}, "node_settings_get": {"node": "project"}, "playbook_get": {"model": "fake/m1"}, "mcp_list": {}, "cto_enter": {}, "cto_events": {}, "cto_graph_history": {}, "cto_settings": {}, "tools_list": {}, "tools_history": {"item": "missing"}, "mcp_import_candidates": {}, "tools_differences": {},
	}
	// mcp_call and mcp_test can execute arbitrary external operations. Mutation
	// tools are classified explicitly and are never invoked here.
	mutations := strings.Fields("charter_set node_add node_remove edge_set edge_remove graph_undo send worker_profile_add worker_profile_remove worker_providers_set protected_providers_set score_set worker_start ui_show ui_notice project_add project_remove reply mcp_call session_trust session_policy node_rename integrations_set worker_stop held_answer inbox_answer outcome_overrule node_settings_set playbook_set learn mcp_add mcp_remove mcp_test mcp_enable cto_send cto_new tools_plan tools_apply tools_undo mcp_import mcp_native_remove")
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
				t.Fatalf("read failed: %+v", result.Content)
			}
			if len(result.Content) != 1 {
				t.Fatalf("unexpected content: %+v", result.Content)
			}
			text, ok := result.Content[0].(*mcp.TextContent)
			if !ok {
				t.Fatalf("unexpected content: %+v", result.Content)
			}
			if tool.Name == "agents" {
				var r struct {
					Agents []json.RawMessage `json:"agents"`
				}
				if json.Unmarshal([]byte(text.Text), &r) != nil || len(r.Agents) < 50 {
					t.Fatalf("agent fixture missing: %s", text.Text)
				}
			}
			if tool.Name == "needs_you" {
				var r struct {
					Agents []json.RawMessage `json:"agents"`
				}
				if json.Unmarshal([]byte(text.Text), &r) != nil || len(r.Agents) != 50 {
					t.Fatalf("waiting fixture missing: %s", text.Text)
				}
			}
			if tool.Name == "nodes" {
				var r struct {
					Nodes []struct {
						Harnesses []struct {
							ModelCount int `json:"model_count"`
						} `json:"harnesses"`
					} `json:"nodes"`
				}
				if json.Unmarshal([]byte(text.Text), &r) != nil {
					t.Fatal("bad nodes")
				}
				count := 0
				for _, n := range r.Nodes {
					for _, h := range n.Harnesses {
						if h.ModelCount == 400 {
							count++
						}
					}
				}
				if count != 50 {
					t.Fatalf("only %d machines with 400 models", count)
				}
			}

			b, err := json.Marshal(result)
			if err != nil {
				t.Fatal(err)
			}
			if len(b) >= 20000 {
				t.Errorf("result is %d bytes", len(b))
			}
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
