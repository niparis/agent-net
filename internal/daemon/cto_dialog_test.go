package daemon

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

func TestCTODialogAnswerGate(t *testing.T) {
	s, err := openState(seededStateDir(t))
	if err != nil {
		t.Fatal(err)
	}
	defer s.close()
	d := &Daemon{st: s}
	d.cto.pid.Store(int64(os.Getpid()))
	runner := proposalRunner{state: s, now: time.Now, validate: func(context.Context, proposalCall) error { return nil }, run: func(context.Context, proposalCall, string) (json.RawMessage, error) { return nil, nil }}
	id, err := runner.propose(context.Background(), proposalCall{Tool: "fake", Action: "set", Args: json.RawMessage(`{}`)}, "reason")
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(map[string]any{"id": id, "approve": false})
	outsider := &localConn{d: d, client: "cli", peerPID: os.Getpid()}
	if _, err = outsider.dispatch(context.Background(), "cto_dialog_answer", raw); err == nil {
		t.Fatal("non CTO accepted")
	}
	impostor := &localConn{d: d, client: "cto-mcp", peerPID: 1}
	if _, err = impostor.dispatch(context.Background(), "cto_dialog_answer", raw); err == nil {
		t.Fatal("unrelated process accepted")
	}
	cto := &localConn{d: d, client: "cto-mcp", peerPID: os.Getpid()}
	if _, err = cto.dispatch(context.Background(), "cto_dialog_answer", json.RawMessage(`{"id":"p_99999","approve":false}`)); err == nil {
		t.Fatal("unknown accepted")
	}
	if _, err = cto.dispatch(context.Background(), "cto_dialog_answer", raw); err != nil {
		t.Fatal(err)
	}
	if _, err = cto.dispatch(context.Background(), "cto_dialog_answer", raw); err == nil {
		t.Fatal("decided accepted")
	}
	id, err = runner.propose(context.Background(), proposalCall{Tool: "fake", Action: "set", Args: json.RawMessage(`{}`)}, "reason")
	if err != nil {
		t.Fatal(err)
	}
	raw, _ = json.Marshal(map[string]any{"id": id, "approve": true})
	note, err := cto.dispatch(context.Background(), "cto_dialog_answer", raw)
	if err != nil || note == nil {
		t.Fatalf("failed approval outcome: %v %v", note, err)
	}
	items, err := runner.list()
	if err != nil || items[len(items)-1].Status != "failed" {
		t.Fatalf("approval status: %+v %v", items, err)
	}
}

func TestCTODialogApprovalAppliesProposal(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	n := startNode(t, g, nodeOpts{})
	cli := cliClient(t, n)
	cli.mustCall("node_add", map[string]string{"name": "dialog-node"})
	ctx := context.Background()
	id, err := n.d.Propose(ctx, "graph_change", "charter_set", json.RawMessage(`{"node":"dialog-node","attr":"purpose","value":"dialog purpose"}`), "test")
	if err != nil {
		t.Fatal(err)
	}
	n.d.cto.pid.Store(int64(os.Getpid()))
	defer n.d.cto.pid.Store(0)
	cto := &localConn{d: n.d, client: "cto-mcp", peerPID: os.Getpid()}
	raw, _ := json.Marshal(map[string]any{"id": id, "approve": true})
	if _, err := cto.dispatch(ctx, "cto_dialog_answer", raw); err != nil {
		t.Fatal(err)
	}
	var history []protocol.CharterChange
	if err := json.Unmarshal(cli.mustCall("charter_history", map[string]string{"node": "dialog-node"}), &history); err != nil || len(history) != 1 || history[0].By != "owner (CTO proposal "+id+")" {
		t.Fatalf("history: %+v %v", history, err)
	}
}
