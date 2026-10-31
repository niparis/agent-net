package daemon

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

// Only the owner renames a session: the CTO tool and an owner CLI may, an
// agent connection is refused. The name survives reports and an empty name
// returns to the automatic project name (decision 0032).
func TestSessionRenameIsOwnerOnlyAndTheCTOUsesIt(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	n := startNodeCfg(t, g, nodeOpts{cmdline: "/usr/bin/pi"}, nil)
	agent, sid := helloPi(t, n, "rename-agent", "/code/shop", true)
	other, otherID := helloPi(t, n, "rename-other", "/code/other", true)
	cli := cliClient(t, n)

	req := protocol.SessionRenameRequest{SessionID: sid, Name: "planner"}
	if _, err := agent.call(protocol.OpSessionRename, req); err == nil || !strings.Contains(err.Error(), "only available to the owner") {
		t.Fatalf("agent rename: %v", err)
	}
	raw := cli.mustCall(protocol.OpSessionRename, req)
	var res protocol.SessionRenameResult
	if err := json.Unmarshal(raw, &res); err != nil || res.SessionID != sid || res.Name != "planner" {
		t.Fatalf("rename: %s %v", raw, err)
	}
	if list := string(cli.mustCall("list", map[string]any{})); !strings.Contains(list, "planner") {
		t.Fatalf("the list does not show the name: %s", list)
	}
	// A report keeps the name.
	agent.mustCall("status", map[string]any{"status": "busy"})
	if list := string(cli.mustCall("list", map[string]any{})); !strings.Contains(list, "planner") {
		t.Fatalf("a report replaced the name: %s", list)
	}

	// The CTO uses the same operator method through its private channel.
	out, err := n.d.runCTOTool(context.Background(), "session_rename", json.RawMessage(`{"session_id":"`+sid+`","name":"cto-name"}`))
	if err != nil {
		t.Fatal(err)
	}
	ctoRaw, _ := json.Marshal(out)
	if !strings.Contains(string(ctoRaw), "cto-name") {
		t.Fatalf("CTO rename: %s", ctoRaw)
	}
	// An empty name clears it and returns the automatic name.
	out, err = n.d.runCTOTool(context.Background(), "session_rename", json.RawMessage(`{"session_id":"`+sid+`","name":""}`))
	if err != nil {
		t.Fatal(err)
	}
	ctoRaw, _ = json.Marshal(out)
	if !strings.Contains(string(ctoRaw), `"name":"shop"`) {
		t.Fatalf("CTO clear: %s", ctoRaw)
	}

	// Another live session's name is refused with a clear error.
	_, err = cli.call(protocol.OpSessionRename, protocol.SessionRenameRequest{SessionID: otherID, Name: "shop"})
	if err == nil || !strings.Contains(err.Error(), "live session") || !strings.Contains(err.Error(), "shop") {
		t.Fatalf("duplicate name: %v", err)
	}
	_ = other
}
