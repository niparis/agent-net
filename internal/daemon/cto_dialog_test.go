package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
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
	t.Parallel()
	g := startGateway(t, t.TempDir(), "")
	n := startNode(t, g, nodeOpts{})
	cli := cliClient(t, n)
	cli.mustCall("node_add", map[string]string{"name": "dialog-node"})
	ctx := context.Background()
	cli.mustCall("charter_set", map[string]string{"node": "dialog-node", "attr": "maturity", "value": "critical - production"})
	n.d.cto.pid.Store(int64(os.Getpid()))
	defer n.d.cto.pid.Store(0)
	cto := &localConn{d: n.d, client: "cto-mcp", peerPID: os.Getpid()}
	response, err := cto.dispatch(ctx, "cto_tool", json.RawMessage(`{"name":"graph_change","arguments":{"action":"charter_set","node":"dialog-node","attr":"purpose","value":"dialog purpose"}}`))
	if err != nil {
		t.Fatal(err)
	}
	payload, ok := response.(map[string]any)
	if !ok || payload["kind"] != "proposal" {
		t.Fatalf("proposal payload: %v", response)
	}
	id, ok := payload["proposal_id"].(string)
	message, msgOK := payload["message"].(string)
	if !ok || !msgOK || !strings.Contains(message, "  value: dialog purpose") || !strings.Contains(message, "  node: dialog-node") || !strings.Contains(message, "Reason: ") || strings.Contains(message, `"reason"`) {
		t.Fatalf("stored proposal payload: %v", response)
	}
	raw, _ := json.Marshal(map[string]any{"id": id, "approve": true})
	if _, err := cto.dispatch(ctx, "cto_dialog_answer", raw); err != nil {
		t.Fatal(err)
	}
	var history []protocol.CharterChange
	if err := json.Unmarshal(cli.mustCall("charter_history", map[string]string{"node": "dialog-node"}), &history); err != nil || len(history) != 2 || history[0].By != "owner (CTO proposal "+id+")" {
		t.Fatalf("history: %+v %v", history, err)
	}
}

func TestCTOProposalAsk(t *testing.T) {
	t.Parallel()
	g := startGateway(t, t.TempDir(), "")
	n := startNode(t, g, nodeOpts{})
	cli := cliClient(t, n)
	cli.mustCall("node_add", map[string]string{"name": "dialog-node"})
	ctx := context.Background()
	cli.mustCall("charter_set", map[string]string{"node": "dialog-node", "attr": "maturity", "value": "critical - production"})
	n.d.cto.pid.Store(int64(os.Getpid()))
	defer n.d.cto.pid.Store(0)

	outsider := &localConn{d: n.d, client: "cli", peerPID: os.Getpid()}
	if _, err := outsider.dispatch(ctx, "cto_tool", json.RawMessage(`{"name":"proposal_ask","arguments":{"id":"p_1"}}`)); err == nil {
		t.Fatal("non CTO caller accepted")
	}

	cto := &localConn{d: n.d, client: "cto-mcp", peerPID: os.Getpid()}
	for _, bad := range []string{
		`{"name":"proposal_ask","arguments":{}}`,
		`{"name":"proposal_ask","arguments":{"id":"   "}}`,
		`{"name":"proposal_ask","arguments":{"id":"p_99999"}}`,
	} {
		if _, err := cto.dispatch(ctx, "cto_tool", json.RawMessage(bad)); err == nil {
			t.Fatalf("accepted bad proposal_ask %s", bad)
		}
	}

	created, err := cto.dispatch(ctx, "cto_tool", json.RawMessage(`{"name":"graph_change","arguments":{"action":"charter_set","node":"dialog-node","attr":"purpose","value":"waiting purpose"}}`))
	if err != nil {
		t.Fatal(err)
	}
	firstPayload, ok := created.(map[string]any)
	if !ok || firstPayload["kind"] != "proposal" {
		t.Fatalf("created payload: %v", created)
	}
	id, _ := firstPayload["proposal_id"].(string)
	if id == "" {
		t.Fatalf("missing proposal id: %v", firstPayload)
	}

	reopened, err := cto.dispatch(ctx, "cto_tool", json.RawMessage(`{"name":"proposal_ask","arguments":{"id":"`+id+`"}}`))
	if err != nil {
		t.Fatal(err)
	}
	payload, ok := reopened.(map[string]any)
	if !ok || payload["kind"] != "proposal" || payload["proposal_id"] != id || payload["text"] != ProposalToolResult(id) {
		t.Fatalf("reopened payload: %v", reopened)
	}
	message, msgOK := payload["message"].(string)
	if !msgOK || message != firstPayload["message"] || !strings.Contains(message, "  node: dialog-node") || !strings.Contains(message, "  value: waiting purpose") || !strings.Contains(message, "Reason: ") {
		t.Fatalf("reopened message mismatch: got %q want %v", message, firstPayload["message"])
	}
	items, err := n.d.proposalRunner().list()
	if err != nil || len(items) != 1 {
		t.Fatalf("proposal_ask created duplicate proposals: %+v %v", items, err)
	}

	raw, _ := json.Marshal(map[string]any{"id": id, "approve": true})
	if _, err := cto.dispatch(ctx, "cto_dialog_answer", raw); err != nil {
		t.Fatal(err)
	}
	var history []protocol.CharterChange
	if err := json.Unmarshal(cli.mustCall("charter_history", map[string]string{"node": "dialog-node"}), &history); err != nil || len(history) != 2 || history[0].By != "owner (CTO proposal "+id+")" {
		t.Fatalf("history: %+v %v", history, err)
	}

	if _, err := cto.dispatch(ctx, "cto_tool", json.RawMessage(`{"name":"proposal_ask","arguments":{"id":"`+id+`"}}`)); err == nil || !strings.Contains(err.Error(), "not open") {
		t.Fatalf("applied proposal reopened: %v", err)
	}

	second, err := cto.dispatch(ctx, "cto_tool", json.RawMessage(`{"name":"graph_change","arguments":{"action":"charter_set","node":"dialog-node","attr":"purpose","value":"declined purpose"}}`))
	if err != nil {
		t.Fatal(err)
	}
	id2 := second.(map[string]any)["proposal_id"].(string)
	declineRaw, _ := json.Marshal(map[string]any{"id": id2, "approve": false})
	if _, err := cto.dispatch(ctx, "cto_dialog_answer", declineRaw); err != nil {
		t.Fatal(err)
	}
	if _, err := cto.dispatch(ctx, "cto_tool", json.RawMessage(`{"name":"proposal_ask","arguments":{"id":"`+id2+`"}}`)); err == nil || !strings.Contains(err.Error(), "not open") {
		t.Fatalf("declined proposal reopened: %v", err)
	}
}

func TestCTODialogProjectCreateReportsReady(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	home := pathTestHome(t)
	c := startClaudeNode(t, g, shortSet)
	c.d.cto.pid.Store(int64(os.Getpid()))
	defer c.d.cto.pid.Store(0)
	path := filepath.Join(home, "code", "company-rag")
	t.Cleanup(func() { killFakeClaude(t, path) })
	cto := &localConn{d: c.d, client: "cto-mcp", peerPID: os.Getpid()}
	args, _ := json.Marshal(map[string]any{"name": "project_change", "arguments": map[string]any{"action": "create", "path": path, "harness": "claude"}})
	v, err := cto.dispatch(context.Background(), "cto_tool", args)
	if err != nil {
		t.Fatal(err)
	}
	p := v.(map[string]any)
	if p["kind"] != "proposal" {
		t.Fatalf("response: %v", p)
	}
	answer, _ := json.Marshal(map[string]any{"id": p["proposal_id"], "approve": true})
	out, err := cto.dispatch(context.Background(), "cto_dialog_answer", answer)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(fmt.Sprint(out), "company-rag is ready: session company-rag is waiting in tab 1 (Sessions). Press Enter on it to dive in.") {
		t.Fatalf("outcome: %v", out)
	}
}
