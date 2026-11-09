package daemon

import (
	"context"
	"encoding/json"
	"sync"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

// A send that fails on a live link is queued. The link's outbox loop must
// send it on that same link, without waiting for a reconnect.
func TestQueuedSendGoesOutOnLiveLink(t *testing.T) {
	t.Parallel()
	g := startGateway(t, t.TempDir(), "")
	fa := &fakeAdapter{harness: "fake"}
	var mu sync.Mutex
	// The direct send and the first flush fail. The third attempt reaches the gateway.
	failures := 2
	a := startNode(t, g, nodeOpts{adapter: fa, cfg: func(cfg *Config) {
		cfg.testGatewayCall = func(_ context.Context, typ string, params any) (json.RawMessage, bool, error) {
			p, ok := params.(protocol.MsgSend)
			if typ != "msg.send" || !ok || p.IdempotencyKey != "k_first" {
				return nil, false, nil
			}
			mu.Lock()
			defer mu.Unlock()
			if failures > 0 {
				failures--
				return nil, true, errGatewayTimeout
			}
			return nil, false, nil
		}
	}})
	b := startNode(t, g, nodeOpts{cmdline: "pi"})
	fa.setSessions(fakeSession("na", "alice"))
	eventually(t, "alice id", func() bool { return fa.host.SessionID("fake", "na") != "" })
	aliceID := fa.host.SessionID("fake", "na")
	pi, bobID := helloPi(t, b, "nb", "/work/bob", true)

	d := a.d
	d.mu.Lock()
	before := d.lk
	d.mu.Unlock()
	if err := d.Send(context.Background(), protocol.MsgSend{Sender: aliceID, Recipient: bobID, Body: "first", IdempotencyKey: "k_first"}); err != nil {
		t.Fatal(err)
	}
	if err := d.Send(context.Background(), protocol.MsgSend{Sender: aliceID, Recipient: bobID, Body: "second", IdempotencyKey: "k_second"}); err != nil {
		t.Fatal(err)
	}
	// nextPush waits up to 5 s, so a message stuck in the outbox fails here.
	if got := pi.nextPush(); got.Body != "first" {
		t.Fatalf("first push is %q, want %q", got.Body, "first")
	}
	if got := pi.nextPush(); got.Body != "second" {
		t.Fatalf("second push is %q, want %q", got.Body, "second")
	}
	d.mu.Lock()
	after := d.lk
	d.mu.Unlock()
	if after != before {
		t.Fatal("the daemon reconnected: the queued send did not go out on the live link")
	}
}

// A gateway fault that the gateway marks as retryable must not drop a queued
// send. The message stays in the outbox, behind nothing and ahead of nothing:
// the next flush sends it first, then the messages after it.
func TestOutboxKeepsSendOnRetryableGatewayFault(t *testing.T) {
	t.Parallel()
	var mu sync.Mutex
	fault := true
	var attempts []string
	d, err := New(Config{StateDir: t.TempDir(), HomeDir: t.TempDir(), testGatewayCall: func(_ context.Context, typ string, params any) (json.RawMessage, bool, error) {
		p, ok := params.(protocol.MsgSend)
		if typ != "msg.send" || !ok {
			return nil, false, nil
		}
		mu.Lock()
		defer mu.Unlock()
		attempts = append(attempts, p.IdempotencyKey)
		if fault && p.IdempotencyKey == "k1" {
			return nil, true, retryableRemote{RemoteError("gateway storage fault")}
		}
		return json.RawMessage(`{}`), true, nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(d.Stop)
	for _, k := range []string{"k1", "k2"} {
		raw, err := json.Marshal(protocol.MsgSend{Sender: "s_a", Recipient: "s_b", Body: k, IdempotencyKey: k})
		if err != nil {
			t.Fatal(err)
		}
		if err := d.st.enqueue(raw); err != nil {
			t.Fatal(err)
		}
	}

	d.flushOutbox(context.Background())
	if n := d.st.outboxLen(); n != 2 {
		t.Fatalf("after a retryable gateway fault the outbox holds %d messages, want 2", n)
	}
	mu.Lock()
	fault = false
	mu.Unlock()
	d.flushOutbox(context.Background())
	if n := d.st.outboxLen(); n != 0 {
		t.Fatalf("outbox holds %d messages after the retry, want 0", n)
	}
	mu.Lock()
	defer mu.Unlock()
	if want := []string{"k1", "k1", "k2"}; len(attempts) != len(want) || attempts[0] != want[0] || attempts[1] != want[1] || attempts[2] != want[2] {
		t.Fatalf("send attempts %v, want %v: k2 must wait behind k1", attempts, want)
	}
}
