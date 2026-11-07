package daemon

import (
	"context"
	"encoding/json"
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
	waitGraphCutover(t, g)
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

func TestCTOGraphTeamProposalLifecycle(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	w := startWorkerNode(t, g, shortSet, nil)
	agent, _ := helloPi(t, w.node, "member-agent", "/w/member-agent", true)
	_ = agent
	if _, err := w.d.call(context.Background(), "hello", map[string]any{"hostname": "fixture", "version": "v0.4.145", "graph_refs": true}); err != nil {
		t.Fatal(err)
	}
	waitGraphCutover(t, g)
	if err := w.d.registerCTOSession(context.Background()); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()

	// 1. team_create proposal
	res, err := w.d.runCTOToolRaw(ctx, "graph_change", json.RawMessage(`{"action":"team_create","name":"omega","purpose":"Omega team"}`))
	if err != nil {
		t.Fatalf("team_create proposal: %v", err)
	}
	approveCTOToolResult(t, w.d, res)

	// Verify team exists via agent_graph_read
	readRes, err := w.d.runCTOToolRaw(ctx, "agent_graph_read", json.RawMessage(`{"ref":{"kind":"team","id":"omega"}}`))
	if err != nil {
		t.Fatalf("agent_graph_read team: %v", err)
	}
	readRaw, _ := json.Marshal(readRes)
	if !strings.Contains(string(readRaw), `"name":"omega"`) || !strings.Contains(string(readRaw), "Omega team") {
		t.Fatalf("agent_graph_read omega: %s", readRaw)
	}

	// 2. team_member_add proposal
	res, err = w.d.runCTOToolRaw(ctx, "graph_change", json.RawMessage(`{"action":"team_member_add","team":"omega","member":{"kind":"agent","id":"member-agent"}}`))
	if err != nil {
		t.Fatalf("team_member_add proposal: %v", err)
	}
	approveCTOToolResult(t, w.d, res)

	// Verify member exists in team
	readRes, err = w.d.runCTOToolRaw(ctx, "agent_graph_read", json.RawMessage(`{"ref":{"kind":"team","id":"omega"}}`))
	if err != nil {
		t.Fatal(err)
	}
	readRaw, _ = json.Marshal(readRes)
	if !strings.Contains(string(readRaw), "member-agent") {
		t.Fatalf("agent_graph_read member-agent: %s", readRaw)
	}

	// 3. team_rename proposal
	res, err = w.d.runCTOToolRaw(ctx, "graph_change", json.RawMessage(`{"action":"team_rename","team":"omega","name":"omega-renamed"}`))
	if err != nil {
		t.Fatalf("team_rename proposal: %v", err)
	}
	approveCTOToolResult(t, w.d, res)

	// Verify rename via agent_graph_read
	readRes, err = w.d.runCTOToolRaw(ctx, "agent_graph_read", json.RawMessage(`{"ref":{"kind":"team","id":"omega"}}`))
	if err != nil {
		t.Fatal(err)
	}
	readRaw, _ = json.Marshal(readRes)
	if !strings.Contains(string(readRaw), `"name":"omega-renamed"`) {
		t.Fatalf("agent_graph_read renamed: %s", readRaw)
	}

	// 4. team_member_remove proposal
	res, err = w.d.runCTOToolRaw(ctx, "graph_change", json.RawMessage(`{"action":"team_member_remove","team":"omega","member":{"kind":"agent","id":"member-agent"}}`))
	if err != nil {
		t.Fatalf("team_member_remove proposal: %v", err)
	}
	approveCTOToolResult(t, w.d, res)

	// Verify member removed
	readRes, err = w.d.runCTOToolRaw(ctx, "agent_graph_read", json.RawMessage(`{"ref":{"kind":"team","id":"omega"}}`))
	if err != nil {
		t.Fatal(err)
	}
	readRaw, _ = json.Marshal(readRes)
	if strings.Contains(string(readRaw), "member-agent") {
		t.Fatalf("agent_graph_read member not removed: %s", readRaw)
	}

	// 5. team_archive proposal
	res, err = w.d.runCTOToolRaw(ctx, "graph_change", json.RawMessage(`{"action":"team_archive","team":"omega"}`))
	if err != nil {
		t.Fatalf("team_archive proposal: %v", err)
	}
	approveCTOToolResult(t, w.d, res)

	// Verify archived
	readRes, err = w.d.runCTOToolRaw(ctx, "agent_graph_read", json.RawMessage(`{"ref":{"kind":"team","id":"omega"}}`))
	if err != nil {
		t.Fatal(err)
	}
	readRaw, _ = json.Marshal(readRes)
	if !strings.Contains(string(readRaw), `"active":false`) {
		t.Fatalf("agent_graph_read archived: %s", readRaw)
	}
}
