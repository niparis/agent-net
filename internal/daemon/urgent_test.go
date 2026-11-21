package daemon

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

// DeliverInline makes the fake adapter one a hook can collect from, the way the
// Claude adapter is. It returns the text the model would be shown.
func (a *fakeAdapter) DeliverInline(ctx context.Context, target protocol.SessionInfo, m protocol.Message) (string, Result) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.inline = append(a.inline, m)
	return "inline: " + m.Body, Result{Delivered: true}
}

func (a *fakeAdapter) collected() []protocol.Message {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]protocol.Message(nil), a.inline...)
}

func urgentTake(t *testing.T, cli *localClient, native string) string {
	t.Helper()
	raw := cli.mustCall(protocol.MethodUrgentTake, protocol.UrgentTakeRequest{Harness: "fake", NativeID: native})
	var res protocol.UrgentTakeResult
	if err := json.Unmarshal(raw, &res); err != nil {
		t.Fatal(err)
	}
	return res.Text
}

// An urgent message to a busy session reaches the model at its next tool call,
// through the hook, is marked delivered then, and is not sent again at the
// turn end. A non-urgent message keeps its hold until the turn ends (decision
// 0180).
func TestUrgentMessageReachesBusySessionAtItsNextToolCall(t *testing.T) {
	t.Parallel()
	g := startGateway(t, t.TempDir(), "")
	fa := &fakeAdapter{harness: "fake"}
	n := startNode(t, g, nodeOpts{adapter: fa})
	busy := fakeSession("nc", "carol")
	busy.Status = "busy"
	fa.setSessions(fakeSession("na", "alice"), busy)
	eventually(t, "both sessions known", func() bool {
		return fa.host.SessionID("fake", "na") != "" && fa.host.SessionID("fake", "nc") != ""
	})
	alice, carol := fa.host.SessionID("fake", "na"), fa.host.SessionID("fake", "nc")
	eventually(t, "the gateway sees carol busy", func() bool {
		a, ok, err := g.gw.Store().Agent(carol)
		return err == nil && ok && a.Status == "busy"
	})
	send := func(body, key string, urgent bool) string {
		t.Helper()
		raw, err := n.d.send(context.Background(), protocol.MsgSend{Sender: alice, Recipient: carol,
			Type: protocol.TypeRequest, Body: body, IdempotencyKey: key, Urgent: urgent})
		if err != nil {
			t.Fatal(err)
		}
		return sendResult(t, raw).ID
	}
	calm := send("calm note", "calm", false)
	stop := send("STOP copying", "stop", true)
	eventually(t, "both wait at the gateway and the node knows an urgent one waits", func() bool {
		_, known := n.d.urgentHeld.Load(carol)
		for _, id := range []string{calm, stop} {
			m, ok, err := g.gw.Store().Message(id)
			if err != nil || !ok || !m.HeldForBusy {
				return false
			}
		}
		return known
	})

	cli := cliClient(t, n)
	if text := urgentTake(t, cli, "nc"); text != "inline: STOP copying" {
		t.Fatalf("the hook got %q, want the urgent message", text)
	}
	if got := fa.collected(); len(got) != 1 || got[0].ID != stop {
		t.Fatalf("collected %+v, want only the urgent message", got)
	}
	eventually(t, "the urgent message is delivered at the gateway", func() bool {
		m, ok, err := g.gw.Store().Message(stop)
		return err == nil && ok && m.State == protocol.StateDelivered
	})
	if text := urgentTake(t, cli, "nc"); text != "" {
		t.Fatalf("a second hook call got %q, want nothing", text)
	}
	if got := fa.delivered(); len(got) != 0 {
		t.Fatalf("the inbox got %+v during the turn", got)
	}

	// The turn ends: only the non-urgent message goes, once.
	idle := fakeSession("nc", "carol")
	fa.setSessions(fakeSession("na", "alice"), idle)
	eventually(t, "the calm message is delivered at the turn end", func() bool {
		m, ok, err := g.gw.Store().Message(calm)
		return err == nil && ok && m.State == protocol.StateDelivered
	})
	got := fa.delivered()
	if len(got) != 1 || got[0].ID != calm {
		t.Fatalf("the inbox got %+v at the turn end, want only the non-urgent message", got)
	}
}

// A plugin session (Pi, OpenCode) collects its urgent message with urgent_take
// at a tool call. The message comes back in the answer, is delivered once, and
// nothing is pushed to the plugin, now or at the turn end.
func TestUrgentMessageReachesBusyPluginAtItsNextToolCall(t *testing.T) {
	t.Parallel()
	g := startGateway(t, t.TempDir(), "")
	fa := &fakeAdapter{harness: "fake"}
	n := startNode(t, g, nodeOpts{adapter: fa, cmdline: "pi"})
	fa.setSessions(fakeSession("na", "alice"))
	eventually(t, "alice has an id", func() bool { return fa.host.SessionID("fake", "na") != "" })
	alice := fa.host.SessionID("fake", "na")
	pi, carol := helloPi(t, n, "nc", "/work/carol", false)
	pi.mustCall("status", map[string]any{"status": "busy"})
	eventually(t, "the gateway sees carol busy", func() bool {
		a, ok, err := g.gw.Store().Agent(carol)
		return err == nil && ok && a.Status == "busy"
	})
	raw, err := n.d.send(context.Background(), protocol.MsgSend{Sender: alice, Recipient: carol,
		Type: protocol.TypeRequest, Body: "STOP copying", IdempotencyKey: "stop", Urgent: true})
	if err != nil {
		t.Fatal(err)
	}
	stop := sendResult(t, raw).ID
	eventually(t, "it waits and the node knows", func() bool {
		_, known := n.d.urgentHeld.Load(carol)
		m, ok, err := g.gw.Store().Message(stop)
		return known && err == nil && ok && m.HeldForBusy
	})
	var res protocol.UrgentTakeResult
	if err := json.Unmarshal(pi.mustCall(protocol.MethodUrgentTake, map[string]any{}), &res); err != nil {
		t.Fatal(err)
	}
	if len(res.Messages) != 1 || res.Messages[0].ID != stop || res.Messages[0].Body != "STOP copying" {
		t.Fatalf("take = %+v, want the urgent message", res)
	}
	eventually(t, "delivered at the gateway", func() bool {
		m, ok, err := g.gw.Store().Message(stop)
		return err == nil && ok && m.State == protocol.StateDelivered
	})
	select {
	case m := <-pi.Pushes:
		t.Fatalf("the plugin was also pushed %+v", m)
	default:
	}
	var again protocol.UrgentTakeResult
	if err := json.Unmarshal(pi.mustCall(protocol.MethodUrgentTake, map[string]any{}), &again); err != nil || len(again.Messages) != 0 {
		t.Fatalf("second take = %+v, %v", again, err)
	}
}

// An urgent message to a busy Codex thread goes into the active turn with
// turn/steer, at once, and is not queued behind the turn. A message that is not
// urgent stays held (decision 0180).
func TestUrgentMessageSteersTheActiveCodexTurn(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	f := startFakeShared(t)
	f.set(fakeThread{ID: "t-open", Cwd: "/work/api", Status: "active", Source: "cli"})
	n, _ := codexConfirmNode(t, g, f, false)
	codex := n.d.SessionID("codex", "t-open")
	eventually(t, "the gateway sees the thread busy", func() bool {
		a, ok, err := g.gw.Store().Agent(codex)
		return err == nil && ok && a.Status == "busy"
	})
	f.notifyWith("turn/started", map[string]any{"threadId": "t-open", "turn": map[string]any{"id": "turn-9"}})
	a := n.d.adapters["codex"].(*codexAdapter)
	eventually(t, "the adapter knows the active turn", func() bool { return a.activeTurn("t-open") == "turn-9" })
	pi, sender := helloPi(t, n, "np", "/work/pi", true)
	_ = pi
	send := func(body, key string, urgent bool) string {
		t.Helper()
		raw, err := n.d.send(context.Background(), protocol.MsgSend{Sender: sender, Recipient: codex,
			Type: protocol.TypeRequest, Body: body, IdempotencyKey: key, Urgent: urgent})
		if err != nil {
			t.Fatal(err)
		}
		return sendResult(t, raw).ID
	}
	calm := send("calm note", "calm", false)
	stop := send("STOP copying", "stop", true)
	eventually(t, "the turn is steered with the urgent message", func() bool { return len(f.steered()) == 1 })
	st := f.steered()[0]
	if st.Thread != "t-open" || st.Turn != "turn-9" || st.ClientID != stop || !strings.Contains(st.Text, "STOP copying") || !strings.Contains(st.Text, "urgent message") {
		t.Fatalf("steer = %+v", st)
	}
	eventually(t, "the urgent message is delivered", func() bool {
		m, ok, err := g.gw.Store().Message(stop)
		return err == nil && ok && m.State == protocol.StateDelivered
	})
	if queuedCopies(f, stop) != 0 {
		t.Fatal("the urgent message was also queued behind the turn")
	}
	if m, ok, err := g.gw.Store().Message(calm); err != nil || !ok || !m.HeldForBusy {
		t.Fatalf("the non-urgent message left its hold: %+v %v", m, err)
	}
}

// When the turn ended before the steer, the message is queued as a normal
// delivery instead of being lost.
func TestCodexInlineDeliveryQueuesWhenNoTurnIsActive(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	f := startFakeShared(t)
	f.set(fakeThread{ID: "t-open", Cwd: "/work/api", Status: "idle", Source: "cli"})
	n, _ := codexConfirmNode(t, g, f, false)
	a := n.d.adapters["codex"].(*codexAdapter)
	info := protocol.SessionInfo{Harness: "codex", NativeID: "t-open", SessionID: n.d.SessionID("codex", "t-open")}
	_, res := a.DeliverInline(context.Background(), info, protocol.Message{ID: "msg-late", Body: "STOP", From: "agent-a", FromName: "agent-a"})
	if !res.Delivered || len(f.steered()) != 0 || queuedCopies(f, "msg-late") != 1 {
		t.Fatalf("result %+v, steers %v, queued %d", res, f.steered(), queuedCopies(f, "msg-late"))
	}
}
