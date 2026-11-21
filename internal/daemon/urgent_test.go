package daemon

import (
	"context"
	"encoding/json"
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
