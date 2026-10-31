package daemon

import (
	"encoding/json"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

func TestToolsAcceptIsOwnerOnlyAndDurable(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	n := startNode(t, g, nodeOpts{cmdline: "pi", detect: fakeInventory})
	cli := cliClient(t, n)
	pi, _ := helloPi(t, n, "agent-accept", "/work", true)
	if _, err := pi.call("tools_accept", protocol.ToolAccept{Key: "drift:x", Accepted: true}); err == nil {
		t.Fatal("agent accepted a difference")
	}
	if _, err := pi.call("tools_accepted", struct{}{}); err == nil {
		t.Fatal("agent read the accepted list")
	}
	if _, err := cli.call("tools_accept", protocol.ToolAccept{Key: "drift:x", Accepted: true}); err != nil {
		t.Fatal(err)
	}
	read := func() []string {
		raw, err := cli.call("tools_accepted", struct{}{})
		if err != nil {
			t.Fatal(err)
		}
		var keys []string
		if err = json.Unmarshal(raw, &keys); err != nil {
			t.Fatal(err)
		}
		return keys
	}
	if keys := read(); len(keys) != 1 || keys[0] != "drift:x" {
		t.Fatalf("keys %v", keys)
	}
	if _, err := cli.call("tools_accept", protocol.ToolAccept{Key: "drift:x"}); err != nil {
		t.Fatal(err)
	}
	if keys := read(); len(keys) != 0 {
		t.Fatalf("keys %v", keys)
	}
}
