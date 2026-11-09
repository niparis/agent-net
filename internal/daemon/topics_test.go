package daemon

import (
	"context"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/niparis/agent-net/internal/operator"
	"github.com/niparis/agent-net/internal/tui"

	"github.com/niparis/agent-net/internal/protocol"
)

func TestTopicHubSharesGatewaySubscription(t *testing.T) {
	var hub topicHub
	link := &link{}
	calls := 0
	subscribe := func(context.Context) (map[string]uint64, error) {
		calls++
		return map[string]uint64{protocol.TopicAgents: 3}, nil
	}
	a, b := &localConn{}, &localConn{}
	var wg sync.WaitGroup
	for _, lc := range []*localConn{a, b} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := hub.subscribe(context.Background(), link, lc, subscribe); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if calls != 1 {
		t.Fatalf("gateway subscriptions = %d", calls)
	}
	hub.publish(link, protocol.TopicEvent{Topic: protocol.TopicAgents, Version: 4})
	for _, lc := range []*localConn{a, b} {
		events := hub.take(lc)
		if events[protocol.TopicAgents] != 4 {
			t.Fatalf("missing event: %v", events)
		}
	}
	hub.remove(a)
	hub.publish(link, protocol.TopicEvent{Topic: protocol.TopicAgents, Version: 5})
	if len(hub.take(a)) != 0 || hub.take(b)[protocol.TopicAgents] != 5 {
		t.Fatal("bad subscriber cleanup")
	}
}

func TestTopicSubscriptionRelaysAndDetectsDisconnect(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	n := startNode(t, g, nodeOpts{interval: time.Hour})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	clientA, clientB := operator.New(n.sock), operator.New(n.sock)
	defer clientA.Close()
	defer clientB.Close()
	a, err := clientA.SubscribeTopics(ctx)
	if err != nil {
		t.Fatal(err)
	}
	b, err := clientB.SubscribeTopics(ctx)
	if err != nil {
		t.Fatal(err)
	}
	read := func(sub *operator.TopicSubscription) (map[string]uint64, bool) {
		t.Helper()
		type result struct {
			versions map[string]uint64
			ok       bool
		}
		ch := make(chan result, 1)
		go func() { v, ok := sub.Wait(); ch <- result{v, ok} }()
		select {
		case r := <-ch:
			return r.versions, r.ok
		case <-time.After(2 * time.Second):
			t.Fatal("timed out waiting for topic event")
			return nil, false
		}
	}
	firstA, ok := read(a)
	if !ok || len(firstA) != len(protocol.Topics) {
		t.Fatalf("initial A: %v", firstA)
	}
	firstB, ok := read(b)
	if !ok || len(firstB) != len(protocol.Topics) {
		t.Fatalf("initial B: %v", firstB)
	}
	if _, err = clientA.SetHiddenItems(ctx, protocol.HiddenItems{Sessions: []string{"test-session"}}); err != nil {
		t.Fatal(err)
	}
	for _, sub := range []*operator.TopicSubscription{a, b} {
		found := false
		for !found {
			events, ok := read(sub)
			if !ok {
				t.Fatal("subscription closed")
			}
			found = events[protocol.TopicAgents] > firstA[protocol.TopicAgents]
		}
	}
	g.stop()
	for ok {
		_, ok = read(a)
	}
}

func TestTopicIdleTUIGatewayCalls(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	n := startNode(t, g, nodeOpts{interval: time.Hour})
	c := operator.New(n.sock)
	c.SetUIPush(func(protocol.UIRequest) {})
	defer c.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	calls := func() int64 {
		out, err := g.gw.MachineGet(ctx, protocol.MachineGetRequest{Machine: n.d.hostName()})
		if err != nil {
			t.Fatal(err)
		}
		if out.Requests == nil {
			t.Fatal("missing gateway counters")
		}
		for _, origin := range out.Requests.Since.ByOrigin {
			if origin.Origin == protocol.ViaTUI {
				return origin.Calls
			}
		}
		return 0
	}
	run := func(n int) int64 {
		before := calls()
		model := tui.New(c)
		for i := 0; i < n; i++ {
			// Run the TUI's actual refresh command without waiting for its tick.
			batch, ok := model.Init()().(tea.BatchMsg)
			if !ok || len(batch) < 2 {
				t.Fatal("missing TUI refresh command")
			}
			result := batch[1]()
			next, _ := model.Update(result)
			model = next.(tui.Model)
		}
		return calls() - before
	}
	before := run(60)
	after := run(2)
	if before <= after || after == 0 {
		t.Fatalf("gateway calls before=%d after=%d", before, after)
	}
	t.Logf("60 accelerated idle TUI refresh intervals, gateway calls with worker log and CTO polling closed: before=%d after=%d", before, after)
}

func TestTopicSubscriptionUsesOwnerGate(t *testing.T) {
	d := &Daemon{inSession: func(int) bool { return true }}
	for _, lc := range []*localConn{{d: d, client: "pi"}, {d: d, client: "cli", peerPID: 123}} {
		if _, err := lc.subscribeTopics(context.Background()); err == nil {
			t.Fatal("subscriber bypassed owner gate")
		}
	}
}

func TestTopicHubRejectsDuplicateLocalSubscription(t *testing.T) {
	var hub topicHub
	link := &link{}
	lc := &localConn{}
	calls := 0
	request := func(context.Context) (map[string]uint64, error) {
		calls++
		return map[string]uint64{protocol.TopicNodes: 1}, nil
	}
	if _, err := hub.subscribe(context.Background(), link, lc, request); err != nil {
		t.Fatal(err)
	}
	first := hub.subs[lc]
	if _, err := hub.subscribe(context.Background(), link, lc, request); err == nil {
		t.Fatal("duplicate subscription accepted")
	}
	if hub.subs[lc] != first || calls != 1 {
		t.Fatal("duplicate replaced the subscriber or repeated the gateway request")
	}
	hub.publish(link, protocol.TopicEvent{Topic: protocol.TopicNodes, Version: 2})
	select {
	case <-first.wake:
	default:
		t.Fatal("original subscriber was not woken")
	}
	if hub.take(lc)[protocol.TopicNodes] != 2 {
		t.Fatal("original subscriber lost its event")
	}
}
