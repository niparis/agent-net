package daemon

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

func TestGraphTeamOwnerRelayAndCTORead(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	w := startWorkerNode(t, g, shortSet, nil)
	agent, _ := helloPi(t, w.node, "team-reader", "/w/team-reader", true)
	q := map[string]any{"action": "create", "team_id": "alpha", "name": "Alpha", "purpose": "Shared work", "owner_session": "spoof"}
	if _, err := agent.call("team_write", q); err == nil || !strings.Contains(err.Error(), "only available to the owner") {
		t.Fatalf("owner gate: %v", err)
	}
	if _, err := w.cli.call("team_write", q); err == nil || !strings.Contains(err.Error(), "cutover") {
		t.Fatalf("cutover gate: %v", err)
	}
	if _, err := w.d.call(context.Background(), "hello", map[string]any{"hostname": "fixture", "version": "v0.4.145", "graph_refs": true}); err != nil {
		t.Fatal(err)
	}
	if _, err := w.d.call(context.Background(), "graph.refs.enable", map[string]any{"owner": w.d.ownerSessionID(), "backup": filepath.Join(t.TempDir(), "backup")}); err != nil {
		t.Fatal(err)
	}
	w.cli.mustCall("team_write", q)
	if err := w.d.registerCTOSession(context.Background()); err != nil {
		t.Fatal(err)
	}
	args := json.RawMessage(`{"ref":{"kind":"team","id":"alpha"}}`)
	result, err := w.d.runCTOToolRaw(context.Background(), "agent_graph_read", args)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(result)
	if !strings.Contains(string(raw), `"active":true`) || !strings.Contains(string(raw), "Shared work") {
		t.Fatalf("CTO team read: %s", raw)
	}
	w.cli.mustCall("team_write", map[string]any{"action": "archive", "team_id": "alpha"})
	var history []protocol.TeamChange
	if err := json.Unmarshal(w.cli.mustCall("team_history", map[string]any{"team_id": "alpha"}), &history); err != nil || len(history) != 2 {
		t.Fatalf("history: %+v %v", history, err)
	}
	if _, err := w.d.runCTOToolRaw(context.Background(), "graph_change", json.RawMessage(`{"action":"team_write","team_id":"alpha"}`)); err == nil {
		t.Fatal("CTO Team proposal accepted")
	}
}
