package daemon

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

// Standing orders: the owner's durable instructions, kept at the gateway and
// read by the CTO at the start of every conversation.

func standingCall(t *testing.T, d *Daemon, args map[string]any) protocol.StandingOrdersResult {
	t.Helper()
	raw, _ := json.Marshal(args)
	out, err := d.runCTOToolRaw(context.Background(), "cto_standing_orders", raw)
	if err != nil {
		t.Fatalf("cto_standing_orders %v: %v", args, err)
	}
	b, _ := json.Marshal(out)
	var res protocol.StandingOrdersResult
	if err := json.Unmarshal(b, &res); err != nil {
		t.Fatal(err)
	}
	return res
}

func TestStandingOrdersSaveReadListRemoveAndUndo(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	n := startWorkerNode(t, g, shortSet, nil)
	if err := n.d.registerCTOSession(context.Background()); err != nil {
		t.Fatal(err)
	}
	text := "When the owner gives an instruction, do it."
	saved := standingCall(t, n.d, map[string]any{"action": "save", "text": text})
	if saved.Order == nil || saved.Order.Source != protocol.StandingSourceCTO || saved.Order.SavedAt == 0 || saved.Order.Text != text {
		t.Fatalf("save: %+v", saved)
	}
	// The gateway holds it, so another daemon sees it too.
	stored, err := g.gw.Store().StandingOrders()
	if err != nil || len(stored) != 1 || stored[0].ID != saved.Order.ID {
		t.Fatalf("not stored at the gateway: %+v %v", stored, err)
	}
	if list := standingCall(t, n.d, map[string]any{"action": "list"}); len(list.Orders) != 1 {
		t.Fatalf("list: %+v", list)
	}
	one := standingCall(t, n.d, map[string]any{"action": "read", "order_id": saved.Order.ID})
	if one.Order == nil || one.Order.Text != text {
		t.Fatalf("read: %+v", one)
	}
	// Audit: an Inbox notice with an undo that removes it again.
	item := lastInbox(t, g)
	if len(item.Options) != 2 || item.Options[0] != protocol.OpStandingOrders || !strings.Contains(item.Text, "standing order") {
		t.Fatalf("notice: %+v", item)
	}
	rm := standingCall(t, n.d, map[string]any{"action": "remove", "order_id": saved.Order.ID})
	if len(rm.Orders) != 0 {
		t.Fatalf("remove: %+v", rm)
	}
	item = lastInbox(t, g)
	var undo protocol.StandingOrdersRequest
	if err := json.Unmarshal([]byte(item.Options[1]), &undo); err != nil || undo.Restore == nil || undo.Restore.ID != saved.Order.ID {
		t.Fatalf("remove undo must restore the order: %+v %v", undo, err)
	}
	n.cli.mustCall(protocol.OpStandingOrders, undo)
	again, _ := g.gw.Store().StandingOrders()
	if len(again) != 1 || again[0].ID != saved.Order.ID || again[0].Source != protocol.StandingSourceCTO {
		t.Fatalf("undo did not restore: %+v", again)
	}
	if _, err := n.d.runCTOToolRaw(context.Background(), "cto_standing_orders", json.RawMessage(`{"action":"remove","order_id":"so_nope"}`)); err == nil {
		t.Fatal("removing an unknown order must fail")
	}
}

func TestStandingOrdersOwnerOpUsesOwnerSource(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	n := startWorkerNode(t, g, shortSet, nil)
	var res protocol.StandingOrdersResult
	mustJSON(t, n.cli.mustCall(protocol.OpStandingOrders, protocol.StandingOrdersRequest{Action: protocol.StandingActionSave, Text: "keep it simple", Source: protocol.StandingSourceCTO}), &res)
	if res.Order == nil || res.Order.Source != protocol.StandingSourceOwner {
		t.Fatalf("the owner's TUI save is the owner's words: %+v", res.Order)
	}
	mustJSON(t, n.cli.mustCall(protocol.OpStandingOrders, protocol.StandingOrdersRequest{Action: protocol.StandingActionList}), &res)
	if len(res.Orders) != 1 {
		t.Fatalf("list: %+v", res)
	}
	mustJSON(t, n.cli.mustCall(protocol.OpStandingOrders, protocol.StandingOrdersRequest{Action: protocol.StandingActionRemove, ID: res.Orders[0].ID}), &res)
	if len(res.Orders) != 0 {
		t.Fatalf("remove: %+v", res)
	}
}

func TestStandingOrdersAreInTheCTOFirstTurn(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	n := startWorkerNode(t, g, shortSet, nil)
	ctx := context.Background()
	if got := n.d.ctoSystemPrompt(ctx); got != ctoBrief {
		t.Fatal("with no orders the prompt is the plain brief")
	}
	if got := n.d.ctoFirstTurn(ctx, "hello"); got != "hello" {
		t.Fatalf("with no orders the first turn is unchanged: %q", got)
	}
	if _, err := g.gw.Store().SaveStandingOrder(protocol.StandingOrdersRequest{Text: "Do what the owner says.", Source: protocol.StandingSourceOwner}); err != nil {
		t.Fatal(err)
	}
	prompt := n.d.ctoSystemPrompt(ctx)
	if !strings.HasPrefix(prompt, ctoBrief) || !strings.Contains(prompt, "Do what the owner says.") || !strings.Contains(prompt, "standing orders") {
		t.Fatalf("prompt lacks the orders: %q", prompt)
	}
	first := n.d.ctoFirstTurn(ctx, "hello")
	if !strings.Contains(first, "Do what the owner says.") || !strings.HasSuffix(first, "hello") {
		t.Fatalf("first turn lacks the orders: %q", first)
	}
}
