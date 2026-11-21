package daemon

import (
	"context"
	"encoding/json"
	"os"
	"sort"
	"strings"
	"sync/atomic"
	"testing"
	"time"

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

	marker := UrgentMarkerPath(n.d.cfg.StateDir, "fake", "nc")
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("no marker for the session with an urgent message held: %v", err)
	}
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
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("the marker is still there after the take: %v", err)
	}
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

// busyWithUrgent sets up a busy session "nc" that has an urgent message held,
// and returns the node, the adapter, the session ID and the message ID.
func busyWithUrgent(t *testing.T, cfg func(*Config)) (*testGateway, *node, *fakeAdapter, string, string) {
	t.Helper()
	g := startGateway(t, t.TempDir(), "")
	fa := &fakeAdapter{harness: "fake"}
	n := startNode(t, g, nodeOpts{adapter: fa, cfg: cfg})
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
	raw, err := n.d.send(context.Background(), protocol.MsgSend{Sender: alice, Recipient: carol,
		Type: protocol.TypeRequest, Body: "STOP copying", IdempotencyKey: "stop", Urgent: true})
	if err != nil {
		t.Fatal(err)
	}
	stop := sendResult(t, raw).ID
	marker := UrgentMarkerPath(n.d.cfg.StateDir, "fake", "nc")
	eventually(t, "the message is held and the marker is set", func() bool {
		m, ok, err := g.gw.Store().Message(stop)
		_, statErr := os.Stat(marker)
		return err == nil && ok && m.HeldForBusy && statErr == nil
	})
	return g, n, fa, carol, stop
}

// When the turn ends before any tool call took the message, the release sends
// it and the marker goes, so the hook is free again.
func TestUrgentMarkerGoesWhenTheTurnEndReleasesTheMessage(t *testing.T) {
	t.Parallel()
	g, n, fa, _, stop := busyWithUrgent(t, nil)
	fa.setSessions(fakeSession("na", "alice"), fakeSession("nc", "carol"))
	eventually(t, "the turn end delivers the urgent message", func() bool {
		m, ok, err := g.gw.Store().Message(stop)
		return err == nil && ok && m.State == protocol.StateDelivered
	})
	marker := UrgentMarkerPath(n.d.cfg.StateDir, "fake", "nc")
	eventually(t, "the marker is gone", func() bool {
		_, err := os.Stat(marker)
		return os.IsNotExist(err)
	})
}

// A marker left behind, for example by a message the sender withdrew, costs
// one take: the daemon finds nothing and removes it.
func TestUrgentTakeRemovesAStaleMarker(t *testing.T) {
	t.Parallel()
	_, n, _, carol, _ := busyWithUrgent(t, nil)
	n.d.urgentHeld.Delete(carol) // what a restart does to the memory flag
	cli := cliClient(t, n)
	if text := urgentTake(t, cli, "nc"); text != "" {
		t.Fatalf("take = %q, want nothing", text)
	}
	if _, err := os.Stat(UrgentMarkerPath(n.d.cfg.StateDir, "fake", "nc")); !os.IsNotExist(err) {
		t.Fatalf("the stale marker is still there: %v", err)
	}
}

// A take that answers after the hook's deadline must not show the message
// inside the answer: that would mark it delivered while nobody reads it. The
// message goes the normal way instead and still arrives (decision 0180).
func TestUrgentTakePastItsDeadlineDeliversTheNormalWay(t *testing.T) {
	t.Parallel()
	var held protocol.Message
	var late atomic.Bool
	g, n, fa, _, stop := busyWithUrgent(t, func(c *Config) {
		c.testGatewayCall = func(ctx context.Context, typ string, _ any) (json.RawMessage, bool, error) {
			if typ != protocol.OpUrgentTake || !late.Load() {
				return nil, false, nil
			}
			// The gateway claims the message but answers after the deadline.
			<-ctx.Done()
			raw, _ := json.Marshal(protocol.UrgentTaken{Messages: []protocol.Message{held}})
			return raw, true, nil
		}
	})
	m, ok, err := g.gw.Store().Message(stop)
	if err != nil || !ok {
		t.Fatal(err)
	}
	held = m
	late.Store(true)
	cli := cliClient(t, n)
	raw := cli.mustCall(protocol.MethodUrgentTake, protocol.UrgentTakeRequest{Harness: "fake", NativeID: "nc", DeadlineMS: 50})
	var res protocol.UrgentTakeResult
	if err := json.Unmarshal(raw, &res); err != nil {
		t.Fatal(err)
	}
	if res.Text != "" || len(res.Messages) != 0 {
		t.Fatalf("a take past its deadline returned %+v, want nothing", res)
	}
	if got := fa.collected(); len(got) != 0 {
		t.Fatalf("the message was shown inline after the deadline: %+v", got)
	}
	eventually(t, "the message reaches the inbox the normal way", func() bool {
		for _, d := range fa.delivered() {
			if d.ID == stop {
				return true
			}
		}
		return false
	})
}

// The cost of one urgent_take that finds nothing, as a Pi or OpenCode plugin
// pays it after every tool call: one request on its open local socket.
// It only measures, so it runs on request:
//
//	AGENT_NET_MEASURE=1 go test ./internal/daemon -run '^TestMeasureUrgentTakeWithNothingHeld$' -v
func TestMeasureUrgentTakeWithNothingHeld(t *testing.T) {
	if os.Getenv("AGENT_NET_MEASURE") == "" {
		t.Skip("set AGENT_NET_MEASURE=1 to measure")
	}
	g := startGateway(t, t.TempDir(), "")
	n := startNode(t, g, nodeOpts{adapter: &fakeAdapter{harness: "fake"}, cmdline: "pi"})
	pi, _ := helloPi(t, n, "nc", "/work/carol", false)
	const runs = 2000
	times := make([]time.Duration, 0, runs)
	for i := 0; i < runs; i++ {
		start := time.Now()
		pi.mustCall(protocol.MethodUrgentTake, map[string]any{})
		times = append(times, time.Since(start))
	}
	sort.Slice(times, func(i, j int) bool { return times[i] < times[j] })
	t.Logf("urgent_take with nothing held, %d calls on one open socket: p50=%v p95=%v max=%v",
		runs, times[runs/2], times[runs*95/100], times[runs-1])
}
