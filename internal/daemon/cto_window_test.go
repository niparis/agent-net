package daemon

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/client"
	"github.com/niparis/agent-net/internal/protocol"
)

func TestCTOWindowOwnerGate(t *testing.T) {
	t.Parallel()
	g := startGateway(t, t.TempDir(), "")
	n := startNode(t, g, nodeOpts{cmdline: "/usr/bin/pi", exe: "/opt/pi"})
	cli := cliClient(t, n)
	if _, err := cli.call("cto_graph_window", map[string]bool{"toggle": true}); err == nil {
		t.Fatal("non TUI opened window")
	}
	agent, _ := helloPi(t, n, "window-agent", "/work/window", true)
	if _, err := n.d.runCTOToolRaw(context.Background(), "cto_graph_window", json.RawMessage(`{"toggle":true}`)); err == nil {
		t.Fatal("CTO tool opened window")
	}
	if _, err := agent.call("cto_graph_window", map[string]bool{"toggle": true}); err == nil {
		t.Fatal("agent opened window")
	}
	gate := &Daemon{}
	gate.inSession = gate.insideSession
	gate.claudeCTO.root.Store(&claudeCTORoot{pid: os.Getpid()})
	cto := &localConn{d: gate, client: "cli", peerPID: os.Getpid(), tui: true}
	if _, _, err := cto.dispatchOperator(context.Background(), "cto_graph_window", json.RawMessage(`{"toggle":true}`)); err == nil {
		t.Fatal("CTO opened window")
	}
	c, err := client.Dial(n.sock)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	ctx := context.Background()
	if _, err = c.Call(ctx, "hello", map[string]any{"client": "cli", "tui": true}); err != nil {
		t.Fatal(err)
	}
	raw, err := c.Call(ctx, "cto_graph_window", map[string]bool{"toggle": true})
	if err != nil {
		t.Fatal(err)
	}
	var status protocol.CTOGraphWindow
	if err = json.Unmarshal(raw, &status); err != nil || status.Until.IsZero() {
		t.Fatalf("status=%+v %v", status, err)
	}
	raw, err = c.Call(ctx, "cto_graph_window", map[string]bool{"toggle": true})
	if err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(raw, &status); err != nil || !status.Until.IsZero() {
		t.Fatalf("close=%+v %v", status, err)
	}
}

func TestCTOWindowExpiryAndRestart(t *testing.T) {
	now := time.Unix(10000, 0)
	var w ctoGraphWindow
	w.toggle(now)
	if !w.active(now.Add(30*time.Minute - time.Nanosecond)) {
		t.Fatal("closed early")
	}
	if w.active(now.Add(30 * time.Minute)) {
		t.Fatal("not expired")
	}
	w.toggle(now)
	if !w.active(now) {
		t.Fatal("not reopened")
	}
	st, err := openState(seededStateDir(t))
	if err != nil {
		t.Fatal(err)
	}
	original := &Daemon{st: st}
	original.ctoGraph.toggle(now)
	restarted := &Daemon{st: st}
	defer st.close()
	if restarted.ctoGraph.active(now) {
		t.Fatal("window survived restart")
	}
}

func ctoGraphArgs(action, args string) json.RawMessage {
	if args == "" || args == "{}" {
		return json.RawMessage(`{"action":"` + action + `"}`)
	}
	return json.RawMessage(`{"action":"` + action + `",` + strings.TrimPrefix(args, "{"))
}

func TestCTOGraphMinorAndMajorWindow(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	n := startNode(t, g, nodeOpts{})
	cli := cliClient(t, n)
	ctx := context.Background()
	cli.mustCall("node_add", map[string]string{"name": "first"})
	cli.mustCall("node_add", map[string]string{"name": "second"})
	cli.mustCall("node_add", map[string]string{"name": "third"})
	if err := n.d.registerCTOSession(ctx); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ action, args string }{
		{"charter_set", `{"node":"first","attr":"maturity","value":"mature"}`},
		{"charter_set", `{"node":"first","attr":"refuses","value":"deployments"}`},
		{"edge_set", `{"from":"first","to":"second","kind":"depends","rule":"auto"}`},
	} {
		got, err := n.d.runCTOToolRaw(ctx, "graph_change", ctoGraphArgs(tc.action, tc.args))
		if err != nil {
			t.Fatalf("%s: %v", tc.action, err)
		}
		assertCTOImmediate(t, got)
	}
	items, err := inboxAndLog(g.gw.Store())
	if err != nil || len(items) != 3 {
		t.Fatalf("inbox: %+v %v", items, err)
	}
	n.d.ctoGraph.mu.Lock()
	n.d.ctoGraph.toggle(time.Now())
	n.d.ctoGraph.mu.Unlock()
	got, err := n.d.runCTOToolRaw(ctx, "graph_change", ctoGraphArgs("node_remove", `{"name":"third"}`))
	if err != nil {
		t.Fatal(err)
	}
	if text, ok := got.(string); !ok || !strings.HasPrefix(text, "proposal p_") {
		t.Fatalf("node removal bypassed approval: %v", got)
	}
}

func TestCTOWindowNonGraphToolsStayProposals(t *testing.T) {
	for _, name := range strings.Fields("graph_change session_change worker_change answer tools_change mcp_change mcp_call settings_change project_change cto_new message") {
		if isCTOGraphTool(name) && name != "graph_change" {
			t.Fatalf("non graph %s is covered by window", name)
		}
	}
	cases := map[string]json.RawMessage{
		"message":         json.RawMessage(`{"action":"send","as_owner":true}`),
		"graph_change":    json.RawMessage(`{"action":"charter_set","node":"first","attr":"purpose","value":"x"}`),
		"session_change":  json.RawMessage(`{"action":"rename","session_id":"s","name":"n"}`),
		"worker_change":   json.RawMessage(`{"action":"stop","session":"s"}`),
		"answer":          json.RawMessage(`{"kind":"held","message_id":"m","action":"a"}`),
		"tools_change":    json.RawMessage(`{"action":"undo","item":"i"}`),
		"mcp_change":      json.RawMessage(`{"action":"test","name":"n"}`),
		"mcp_call":        json.RawMessage(`{"server":"s","tool":"t"}`),
		"settings_change": json.RawMessage(`{"area":"notifications","disabled":true}`),
		"project_change":  json.RawMessage(`{"action":"remove","name":"p"}`),
		"cto_new":         json.RawMessage(`{}`),
	}
	for name, args := range cases {
		t.Run(name, func(t *testing.T) {
			d := Daemon{}
			d.ctoGraph.toggle(time.Now())
			if !d.ctoGraph.active(time.Now()) {
				t.Fatal("window missing")
			}
			ran, proposed := false, false
			_, err := routeCTOTool(context.Background(), name, args, func(context.Context, string, string, json.RawMessage, string) (string, error) {
				proposed = true
				return "p_1", nil
			}, func(context.Context, string, json.RawMessage) (any, error) { ran = true; return nil, nil })
			if err != nil || !ran || proposed {
				t.Fatalf("non graph %s ran=%v proposed=%v err=%v", name, ran, proposed, err)
			}
		})
	}
}

func TestCTOGraphWindowStillProposesMovedNodeRule(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	n := startNode(t, g, nodeOpts{})
	ctx := context.Background()
	cli := cliClient(t, n)
	cli.mustCall("node_add", map[string]string{"name": "alpha"})
	if err := n.d.registerCTOSession(ctx); err != nil {
		t.Fatal(err)
	}
	n.d.ctoGraph.mu.Lock()
	n.d.ctoGraph.toggle(time.Now())
	n.d.ctoGraph.mu.Unlock()
	got, err := n.d.runCTOToolRaw(ctx, "graph_change", ctoGraphArgs("settings_set", `{"node":"alpha","setting":"merge_authority","value":"ask"}`))
	if err != nil {
		t.Fatal(err)
	}
	assertCTOImmediate(t, got)
	if _, err = g.gw.Store().UpsertSession(n.d.cfg.NodeID, protocol.SessionInfo{Harness: "pi", NativeID: "component", Repo: protocol.RepoInfo{Project: "component", Root: "/work/component"}}); err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"deploys", "merges"} {
		args := `{"from":"alpha","to":"component","kind":"` + kind + `"}`
		got, err := n.d.runCTOToolRaw(ctx, "graph_change", ctoGraphArgs("edge_set", args))
		if err != nil {
			t.Fatalf("%s: %v", kind, err)
		}
		assertCTOImmediate(t, got)
	}
}
