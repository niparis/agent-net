package daemon

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

func TestProposalConcreteValidationAndRemoteApproval(t *testing.T) {
	t.Parallel()
	g := startGateway(t, t.TempDir(), "")
	holder := startNode(t, g, nodeOpts{})
	other := startNodeCfg(t, g, nodeOpts{cmdline: "/usr/bin/pi"}, nil)
	cli := cliClient(t, other)
	agent, _ := helloPi(t, other, "proposal-agent", "/work/proposal", true)
	for _, method := range []string{"proposal_list", "proposal_answer"} {
		if _, err := agent.call(method, map[string]any{}); err == nil || !strings.Contains(err.Error(), "only available to the owner") {
			t.Fatalf("%s gate: %v", method, err)
		}
	}
	cto := &localConn{d: holder.d, client: "cto-mcp"}
	if _, _, err := cto.dispatchOperator(context.Background(), "proposal_answer", json.RawMessage(`{}`)); err == nil {
		t.Fatal("CTO process passed owner gate")
	}
	gateDaemon := &Daemon{}
	gateDaemon.inSession = gateDaemon.insideSession
	gateDaemon.claudeCTO.root.Store(&claudeCTORoot{pid: os.Getpid()})
	ctoCLI := &localConn{d: gateDaemon, client: "cli", peerPID: os.Getpid()}
	_, _, gateErr := ctoCLI.dispatchOperator(context.Background(), "proposal_answer", json.RawMessage(`{}`))
	if gateErr == nil || !strings.Contains(gateErr.Error(), "inside an agent session") {
		t.Fatalf("CTO process gate: %v", gateErr)
	}
	ctx := context.Background()
	if _, err := holder.d.Propose(ctx, "graph_change", "charter_set", json.RawMessage(`{"node":"missing","attr":"purpose","value":null}`), ""); err == nil || !strings.Contains(err.Error(), "value") {
		t.Fatalf("schema: %v", err)
	}
	_, err := holder.d.Propose(ctx, "graph_change", "charter_set", json.RawMessage(`{"node":"missing","attr":"purpose","value":"hello"}`), "reason")
	if err == nil || !strings.Contains(err.Error(), "unknown node") {
		t.Fatalf("bad ID: %v", err)
	}
	cli.mustCall("node_add", map[string]string{"name": "proposal-architect"})
	id, err := holder.d.Propose(ctx, "graph_change", "charter_set", json.RawMessage(`{"node":"proposal-architect","attr":"purpose","value":"hello"}`), "reason")
	if err != nil {
		t.Fatal(err)
	}
	var list []protocol.CTOProposal
	if err := json.Unmarshal(cli.mustCall("proposal_list", map[string]string{"node_id": holder.d.cfg.NodeID}), &list); err != nil || len(list) != 1 || list[0].ID != id {
		t.Fatalf("list: %+v %v", list, err)
	}
	list = nil
	if err := json.Unmarshal(cli.mustCall("proposal_list", nil), &list); err != nil || len(list) != 1 || list[0].ID != id || list[0].NodeID != holder.d.cfg.NodeID {
		t.Fatalf("network list: %+v %v", list, err)
	}
	cli.mustCall("proposal_answer", protocol.CTOProposalAnswer{NodeID: holder.d.cfg.NodeID, ID: id, Action: "a"})
	var history []protocol.CharterChange
	if err := json.Unmarshal(cli.mustCall("charter_history", map[string]string{"node": "proposal-architect"}), &history); err != nil || len(history) != 1 || history[0].By != "owner (CTO proposal "+id+")" {
		t.Fatalf("history: %+v %v", history, err)
	}
	if _, err := cli.call("proposal_answer", protocol.CTOProposalAnswer{NodeID: holder.d.cfg.NodeID, ID: id, Action: "a"}); err == nil {
		t.Fatal("second approval accepted")
	}
	id, err = holder.d.Propose(ctx, "graph_change", "charter_set", json.RawMessage(`{"node":"proposal-architect","attr":"purpose","value":"later"}`), "")
	if err != nil {
		t.Fatal(err)
	}
	cli.mustCall("node_remove", map[string]string{"name": "proposal-architect"})
	if _, err = cli.call("proposal_answer", protocol.CTOProposalAnswer{NodeID: holder.d.cfg.NodeID, ID: id, Action: "a"}); err == nil || !strings.Contains(err.Error(), "unknown node") {
		t.Fatalf("revalidation: %v", err)
	}
}

func TestProposalChecksMachineEnrollment(t *testing.T) {
	t.Parallel()
	g := startGateway(t, t.TempDir(), "")
	n := startNode(t, g, nodeOpts{})
	_, err := n.d.Propose(context.Background(), "node_rename", "", json.RawMessage(`{"node_id":"missing-machine","name":"test"}`), "")
	if err == nil || !strings.Contains(err.Error(), "unknown machine") {
		t.Fatalf("missing machine: %v", err)
	}
}

func TestProposalGraphEdge(t *testing.T) {
	t.Parallel()
	g := startGateway(t, t.TempDir(), "")
	n := startNode(t, g, nodeOpts{})
	cli := cliClient(t, n)
	cli.mustCall("node_add", map[string]string{"name": "first"})
	cli.mustCall("node_add", map[string]string{"name": "second"})
	args := json.RawMessage(`{"from":"first","to":"second","kind":"depends","rule":"auto"}`)
	id, err := n.d.Propose(context.Background(), "graph_change", "edge_set", args, "")
	if err != nil {
		t.Fatal(err)
	}
	cli.mustCall("proposal_answer", protocol.CTOProposalAnswer{ID: id, Action: "a"})
	var changes []protocol.EdgeChange
	if err = json.Unmarshal(cli.mustCall("edge_history", map[string]string{"node": "first"}), &changes); err != nil || len(changes) != 1 || changes[0].By != "owner (CTO proposal "+id+")" {
		t.Fatalf("history: %+v %v", changes, err)
	}
}

func TestProposalApproveAllAcrossMachines(t *testing.T) {
	t.Parallel()
	g := startGateway(t, t.TempDir(), "")
	a := startNode(t, g, nodeOpts{})
	b := startNode(t, g, nodeOpts{})
	cli := cliClient(t, b)
	cli.mustCall("node_add", map[string]string{"name": "first"})
	cli.mustCall("node_add", map[string]string{"name": "second"})
	first, err := a.d.Propose(context.Background(), "graph_change", "charter_set", json.RawMessage(`{"node":"first","attr":"purpose","value":"one"}`), "")
	if err != nil {
		t.Fatal(err)
	}
	second, err := b.d.Propose(context.Background(), "graph_change", "charter_set", json.RawMessage(`{"node":"second","attr":"purpose","value":"two"}`), "")
	if err != nil {
		t.Fatal(err)
	}
	cli.mustCall("proposal_answer", protocol.CTOProposalAnswer{Action: "A"})
	var items []protocol.CTOProposal
	if err = json.Unmarshal(cli.mustCall("proposal_list", nil), &items); err != nil || len(items) != 2 || items[0].ID != first || items[1].ID != second || items[0].Status != "applied" || items[1].Status != "applied" {
		t.Fatalf("all: %+v %v", items, err)
	}
}

func TestProposalApprovalConfirmMustMatch(t *testing.T) {
	t.Parallel()
	g := startGateway(t, t.TempDir(), "")
	n := startNode(t, g, nodeOpts{})
	cli := cliClient(t, n)
	cli.mustCall("node_add", map[string]string{"name": "first"})
	_, err := n.d.Propose(context.Background(), "graph_change", "charter_set", json.RawMessage(`{"node":"first","attr":"purpose","value":"new"}`), "")
	if err != nil {
		t.Fatal(err)
	}
	_, err = cli.call("proposal_answer", protocol.CTOProposalAnswer{Action: "A", Confirm: []protocol.CTOProposalRef{{NodeID: n.d.cfg.NodeID, ID: "p_999"}}})
	if err == nil || !strings.Contains(err.Error(), "review") {
		t.Fatalf("confirmation: %v", err)
	}
	var items []protocol.CTOProposal
	if err = json.Unmarshal(cli.mustCall("proposal_list", nil), &items); err != nil || len(items) != 1 || items[0].Status != "open" {
		t.Fatalf("proposal changed: %+v %v", items, err)
	}
}
