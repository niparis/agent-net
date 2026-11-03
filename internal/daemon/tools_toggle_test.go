package daemon

import (
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

func TestToolToggleOperatorGateAndRead(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	n := startNode(t, g, nodeOpts{cmdline: "pi", detect: fakeInventory})
	agent, _ := helloPi(t, n, "toggle-agent", "/work", true)
	for _, method := range []string{"tools_toggle", "tools_new_projects", "tools_projects"} {
		if _, err := agent.call(method, map[string]any{}); err == nil || !strings.Contains(err.Error(), "owner") {
			t.Fatalf("%s agent gate: %v", method, err)
		}
	}
	cli := cliClient(t, n)
	if _, err := cli.call("tools_projects", map[string]any{}); err != nil {
		t.Fatal(err)
	}
	for _, method := range []string{"tools_toggle", "tools_new_projects"} {
		if _, err := cli.call(method, protocol.ToolToggleRequest{Item: "missing", Project: "demo", On: false}); err == nil || !strings.Contains(err.Error(), "skill item not found") {
			t.Fatalf("%s route: %v", method, err)
		}
	}
}
