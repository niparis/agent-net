package daemon

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

func TestProjectDisplayOwnerGateAndListings(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	n := startNodeCfg(t, g, nodeOpts{cmdline: "/usr/bin/pi"}, nil)
	agent, _ := helloPi(t, n, "display-agent", "/w/peer", true)
	cli := cliClient(t, n)
	sid, err := g.gw.Store().UpsertSession(n.cfg.NodeID, protocol.SessionInfo{Harness: "pi", NativeID: "project-session", Repo: protocol.RepoInfo{Root: "/src/a2a-ocs", Project: "agent-net"}})
	if err != nil {
		t.Fatal(err)
	}
	set := map[string]string{"project": "agent-net", "name": "Agent Network"}
	if _, err = agent.call("project_display_set", set); err == nil || !strings.Contains(err.Error(), "only available to the owner") {
		t.Fatalf("agent set: %v", err)
	}
	if _, err = agent.call("project_display_get", map[string]string{"project": "agent-net"}); err == nil || !strings.Contains(err.Error(), "only available to the owner") {
		t.Fatalf("agent get: %v", err)
	}
	cli.mustCall("project_display_set", set)
	for _, c := range []struct {
		method string
		args   any
	}{{"project_display_get", map[string]string{"project": "agent-net"}}, {"projects", map[string]string{"name": "agent-net"}}, {"list", map[string]any{}}, {"get", map[string]string{"session_id": sid}}} {
		raw := cli.mustCall(c.method, c.args)
		if !strings.Contains(string(raw), "Agent Network") || !strings.Contains(string(raw), "agent-net") {
			t.Errorf("%s: %s", c.method, raw)
		}
	}
	for _, call := range []struct {
		method string
		args   any
	}{{"list", map[string]any{}}, {"get", map[string]string{"session_id": sid}}} {
		raw := agent.mustCall(call.method, call.args)
		if !strings.Contains(string(raw), "Agent Network") || !strings.Contains(string(raw), "agent-net") {
			t.Errorf("agent %s: %s", call.method, raw)
		}
	}
	out, err := n.d.runCTOTool(context.Background(), "project_display_set", json.RawMessage(`{"project":"agent-net","name":"CTO Name"}`))
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(out)
	if !strings.Contains(string(raw), "CTO Name") {
		t.Fatalf("CTO response: %s", raw)
	}
	cli.mustCall("project_display_set", map[string]string{"project": "agent-net", "name": ""})
	raw = cli.mustCall("project_display_get", map[string]string{"project": "agent-net"})
	if strings.Contains(string(raw), "CTO Name") || strings.Contains(string(raw), "Agent Network") {
		t.Fatalf("clear: %s", raw)
	}
}
