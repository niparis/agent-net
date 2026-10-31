package daemon

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestHiddenItemsAreOwnerOnlyAndSharedByEveryMachine(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	n1 := startNodeCfg(t, g, nodeOpts{cmdline: "/usr/bin/pi", interval: time.Hour}, nil)
	agent, sid := helloPi(t, n1, "hidden-agent", "/w/hidden", true)
	cli1 := cliClient(t, n1)
	cli1.mustCall("hidden_set", map[string]any{"sessions": []string{sid}, "nodes": []string{"cogforge"}})
	var got struct {
		Sessions []string `json:"sessions"`
		Nodes    []string `json:"nodes"`
	}
	if err := json.Unmarshal(cli1.mustCall("hidden_get", nil), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Sessions) != 1 || got.Sessions[0] != sid || len(got.Nodes) != 1 || got.Nodes[0] != "cogforge" {
		t.Fatalf("owner preference: %+v", got)
	}
	for _, method := range []string{"hidden_get", "hidden_set"} {
		if _, err := agent.call(method, map[string]any{}); err == nil || !strings.Contains(err.Error(), "only available to the owner") {
			t.Fatalf("agent %s gate: %v", method, err)
		}
	}
	// A CLI launched in an agent session's process tree is refused by the same
	// owner gate as session_policy.
	n1.d.inSession = func(int) bool { return true }
	if _, err := cli1.call("hidden_get", nil); err == nil || !strings.Contains(err.Error(), "inside an agent session") {
		t.Fatalf("in-session CLI gate: %v", err)
	}
	n1.d.inSession = func(int) bool { return false }

	n2 := startNodeCfg(t, g, nodeOpts{cmdline: "/usr/bin/pi", interval: time.Hour}, nil)
	var fromOtherMachine struct {
		Sessions []string `json:"sessions"`
		Nodes    []string `json:"nodes"`
	}
	if err := json.Unmarshal(cliClient(t, n2).mustCall("hidden_get", nil), &fromOtherMachine); err != nil {
		t.Fatal(err)
	}
	if len(fromOtherMachine.Sessions) != 1 || fromOtherMachine.Sessions[0] != sid || len(fromOtherMachine.Nodes) != 1 {
		t.Fatalf("other machine did not read shared preference: %+v", fromOtherMachine)
	}
}
