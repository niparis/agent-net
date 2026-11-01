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
	st, err := openState(t.TempDir())
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

func TestCTOGraphMinorAndMajorWindow(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	n := startNode(t, g, nodeOpts{})
	cli := cliClient(t, n)
	ctx := context.Background()
	for _, name := range []string{"first", "second"} {
		cli.mustCall("node_add", map[string]string{"name": name})
	}
	if _, err := g.gw.Store().UpsertSession(n.d.cfg.NodeID, protocol.SessionInfo{Harness: "pi", NativeID: "component", Name: "component", Repo: protocol.RepoInfo{Project: "component", Root: "/work/component"}}); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct{ tool, args string }{
		{"charter_set", `{"node":"first","attr":"purpose","value":"Help with work"}`},
		{"charter_set", `{"node":"first","attr":"accepts","value":"features"}`},
		{"charter_set", `{"node":"first","attr":"needs","value":"help"}`},
		{"charter_set", `{"node":"first","attr":"consumers","value":"clients"}`},
		{"node_add", `{"name":"helper"}`},
		{"edge_set", `{"from":"first","to":"second","kind":"depends"}`},
		{"edge_remove", `{"from":"first","to":"second","kind":"depends"}`},
		{"graph_undo", `{"id":0}`},
	} {
		t.Run(c.tool+c.args, func(t *testing.T) {
			if c.tool == "edge_remove" {
				cli.mustCall("edge_set", map[string]string{"from": "first", "to": "second", "kind": "depends"})
			}
			if c.tool == "graph_undo" {
				cli.mustCall("charter_set", map[string]string{"node": "first", "attr": "notes", "value": "base note"})
				cli.mustCall("charter_set", map[string]string{"node": "first", "attr": "notes", "value": "temporary note"})
			}
			var before int
			if err := n.d.st.db.QueryRow(`SELECT count(*) FROM cto_actions WHERE text LIKE 'CTO (minor): applied %'`).Scan(&before); err != nil {
				t.Fatal(err)
			}
			got, err := n.d.runCTOToolRaw(ctx, c.tool, json.RawMessage(c.args))
			if err != nil {
				t.Fatal(err)
			}
			if s, ok := got.(string); ok && strings.HasPrefix(s, "proposal ") {
				t.Fatalf("minor proposed: %s", s)
			}
			var h []protocol.CTOGraphChange
			if err = json.Unmarshal(cli.mustCall("cto_graph_history", nil), &h); err != nil || len(h) == 0 {
				t.Fatalf("history: %v %v", h, err)
			}
			var notices int
			if err = n.d.st.db.QueryRow(`SELECT count(*) FROM cto_actions WHERE text LIKE 'CTO (minor): applied %'`).Scan(&notices); err != nil || notices != before+1 {
				t.Fatalf("notice %d %v", notices, err)
			}
			cli.mustCall("graph_undo", protocol.GraphUndoRequest{ID: h[len(h)-1].ID})
			if c.tool == "edge_remove" {
				cli.mustCall("edge_remove", map[string]string{"from": "first", "to": "second", "kind": "depends"})
			}
		})
	}
	for _, c := range []struct{ tool, args string }{
		{"charter_set", `{"node":"first","attr":"purpose","value":"Only local work"}`},
		{"charter_set", `{"node":"first","attr":"refuses","value":"deployments"}`},
		{"charter_set", `{"node":"first","attr":"escalate","value":"owner"}`},
		{"charter_set", `{"node":"first","attr":"maturity","value":"mature"}`},
		{"node_settings_set", `{"node":"first","setting":"deploy_rule","value":"brief"}`},
		{"edge_set", `{"from":"first","to":"second","kind":"depends","rule":"auto"}`},
		{"edge_set", `{"from":"first","to":"component","kind":"governs","mode":"owner"}`},
		{"edge_set", `{"from":"first","to":"second","kind":"deploys"}`},
		{"edge_set", `{"from":"first","to":"second","kind":"depends","rule":"ask"}`},
		{"edge_set", `{"from":"first","to":"second","kind":"depends","rule":"never"}`},
		{"edge_remove", `{"from":"first","to":"second","kind":"depends"}`},
		{"graph_undo", `{"id":0}`},
		{"node_remove", `{"name":"second"}`},
	} {
		t.Run("major/"+c.tool+c.args, func(t *testing.T) {
			if c.tool == "edge_remove" {
				cli.mustCall("edge_set", map[string]string{"from": "first", "to": "second", "kind": "depends", "rule": "auto"})
			}
			if c.tool == "graph_undo" {
				cli.mustCall("charter_set", map[string]string{"node": "first", "attr": "refuses", "value": "deployments"})
			}
			got, err := n.d.runCTOToolRaw(ctx, c.tool, json.RawMessage(c.args))
			if err != nil {
				t.Fatal(err)
			}
			if s, ok := got.(string); !ok || !strings.HasPrefix(s, "proposal ") {
				t.Fatalf("major applied: %v", got)
			}
			var before int
			if err := n.d.st.db.QueryRow(`SELECT count(*) FROM cto_actions WHERE text LIKE 'CTO (owner window): applied %'`).Scan(&before); err != nil {
				t.Fatal(err)
			}
			n.d.ctoGraph.mu.Lock()
			n.d.ctoGraph.toggle(time.Now())
			n.d.ctoGraph.mu.Unlock()
			_, err = n.d.runCTOToolRaw(ctx, c.tool, json.RawMessage(c.args))
			if err != nil {
				t.Fatal(err)
			}
			var notices int
			if err := n.d.st.db.QueryRow(`SELECT count(*) FROM cto_actions WHERE text LIKE 'CTO (owner window): applied %'`).Scan(&notices); err != nil || notices != before+1 {
				t.Fatalf("window notice %d %v", notices, err)
			}
			n.d.ctoGraph.mu.Lock()
			n.d.ctoGraph.toggle(time.Now())
			n.d.ctoGraph.mu.Unlock()
			var h []protocol.CTOGraphChange
			if err = json.Unmarshal(cli.mustCall("cto_graph_history", nil), &h); err != nil || len(h) == 0 {
				t.Fatalf("history %v %v", h, err)
			}
			cli.mustCall("graph_undo", protocol.GraphUndoRequest{ID: h[len(h)-1].ID})
			if c.tool == "edge_remove" {
				cli.mustCall("edge_remove", map[string]string{"from": "first", "to": "second", "kind": "depends"})
			}
		})
	}
	cli.mustCall("charter_set", map[string]string{"node": "first", "attr": "maturity", "value": "critical - production"})
	got, err := n.d.runCTOToolRaw(ctx, "charter_set", json.RawMessage(`{"node":"first","attr":"purpose","value":"Helpful wording"}`))
	if err != nil {
		t.Fatal(err)
	}
	if s, ok := got.(string); !ok || !strings.HasPrefix(s, "proposal ") {
		t.Fatalf("critical applied: %v", got)
	}
	n.d.ctoGraph.mu.Lock()
	n.d.ctoGraph.toggle(time.Now())
	n.d.ctoGraph.mu.Unlock()
	if _, err = n.d.runCTOToolRaw(ctx, "charter_set", json.RawMessage(`{"node":"first","attr":"purpose","value":"Helpful wording"}`)); err != nil {
		t.Fatal(err)
	}
	got, err = n.d.runCTOToolRaw(ctx, "notification_settings", json.RawMessage(`{"disabled":true}`))
	if err != nil {
		t.Fatal(err)
	}
	if s, ok := got.(string); !ok || !strings.HasPrefix(s, "proposal ") {
		t.Fatalf("non graph applied: %v", got)
	}
	n.d.ctoGraph.mu.Lock()
	n.d.ctoGraph.until = time.Now().Add(-time.Second)
	n.d.ctoGraph.mu.Unlock()
	got, err = n.d.runCTOToolRaw(ctx, "charter_set", json.RawMessage(`{"node":"first","attr":"purpose","value":"Only after expiry"}`))
	if err != nil {
		t.Fatal(err)
	}
	if s, ok := got.(string); !ok || !strings.HasPrefix(s, "proposal ") {
		t.Fatalf("expired window applied: %v", got)
	}
}

func TestCTOWindowNonGraphToolsStayProposals(t *testing.T) {
	for _, name := range strings.Fields("worker_start worker_stop tools_plan tools_apply tools_undo mcp_add mcp_remove mcp_call mcp_test mcp_enable cto_settings notification_settings session_trust session_policy message") {
		if isCTOGraphTool(name) {
			t.Fatalf("non graph %s is covered by window", name)
		}
	}
	cases := map[string]json.RawMessage{"message": json.RawMessage(`{"action":"send","as_owner":true}`)}
	for name, class := range ctoToolClasses {
		if class == ctoProposal && !isCTOGraphTool(name) {
			cases[name] = json.RawMessage(`{}`)
		}
	}
	for name, args := range cases {
		if isCTOGraphTool(name) {
			continue
		}
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
			if err != nil || ran || !proposed {
				t.Fatalf("non graph %s ran=%v proposed=%v err=%v", name, ran, proposed, err)
			}
		})
	}
}
