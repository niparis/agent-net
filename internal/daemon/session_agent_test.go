package daemon

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

func TestSessionAgentOwnerGate(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	n := startNode(t, g, nodeOpts{cmdline: "pi"})
	agent, sid := helloPi(t, n, "agent", "/work/project", true)
	req := map[string]string{"session_id": sid, "agent": "advisor"}
	if _, err := agent.call("session_agent_set", req); err == nil || !strings.Contains(err.Error(), "only available to the owner") {
		t.Fatalf("agent call: %v", err)
	}
	if _, err := agent.call("session_agent_list", map[string]string{}); err == nil || !strings.Contains(err.Error(), "only available to the owner") {
		t.Fatalf("agent list: %v", err)
	}
	if err := g.gw.Store().AddArchitect("advisor", "owner"); err != nil {
		t.Fatal(err)
	}
	cli := cliClient(t, n)
	eventually(t, "owner session", func() bool { return n.d.ownerSessionID() != "" })
	raw, err := cli.call("session_agent_list", map[string]string{})
	if err != nil {
		t.Fatal(err)
	}
	var options protocol.SessionAgentOptions
	if err := json.Unmarshal(raw, &options); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, option := range options.Agents {
		if option.ID == "advisor" && option.Name == "advisor" {
			found = true
		}
	}
	if !found {
		t.Fatalf("owner options %+v", options)
	}
	if _, err := cli.call("session_agent_set", req); err != nil {
		t.Fatalf("owner call: %v", err)
	}
}
