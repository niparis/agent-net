package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"testing"
)

// newCTOStateTestDaemon returns a daemon with a state store. Its fake gateway
// answers cto.graph_latest and runs onLatest first, as a call that is in flight
// while the test changes the CTO state.
func newCTOStateTestDaemon(t *testing.T, onLatest func(d *Daemon)) *Daemon {
	t.Helper()
	st, err := openState(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.close() })
	d := &Daemon{st: st, log: log.New(io.Discard, "", 0)}
	d.cfg.testGatewayCall = func(_ context.Context, typ string, _ any) (json.RawMessage, bool, error) {
		if typ != "cto.graph_latest" {
			return nil, false, nil
		}
		onLatest(d)
		return json.RawMessage(`{"id":7}`), true, nil
	}
	return d
}

// The gateway call must not run under the CTO state lock (B12): a state change
// on another goroutine would otherwise wait for the gateway.
func TestOpenCodeCTONewDoesNotHoldTheStateLockAcrossTheCall(t *testing.T) {
	heldDuringCall := false
	d := newCTOStateTestDaemon(t, func(dd *Daemon) {
		if dd.cto.mu.TryLock() {
			dd.cto.mu.Unlock()
		} else {
			heldDuringCall = true
		}
	})
	if _, err := d.openCodeCTONew(context.Background()); err != nil {
		t.Fatal(err)
	}
	if heldDuringCall {
		t.Fatal("the CTO state lock was held during the gateway call")
	}
}

// A conversation that starts during the call changes the state. The call runs
// once more, so the reset is recorded against a graph id read after that start.
func TestOpenCodeCTONewRepeatsCallWhenConversationMoved(t *testing.T) {
	calls := 0
	d := newCTOStateTestDaemon(t, func(dd *Daemon) {
		calls++
		if calls == 1 {
			dd.cto.session, dd.cto.model = "ses_started", "fixture-model"
		}
	})
	if _, err := d.openCodeCTONew(context.Background()); err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Fatalf("gateway calls = %d, want 2", calls)
	}
	if d.cto.session != "" || d.cto.model != "" {
		t.Fatalf("state after new conversation: session %q model %q", d.cto.session, d.cto.model)
	}
}

// When the state moves again during the repeated call, the new conversation is
// refused rather than applied to a state it did not read.
func TestOpenCodeCTONewGivesUpWhenConversationKeepsMoving(t *testing.T) {
	calls := 0
	d := newCTOStateTestDaemon(t, func(dd *Daemon) {
		calls++
		dd.cto.session = fmt.Sprintf("ses_%d", calls)
	})
	if _, err := d.openCodeCTONew(context.Background()); err == nil {
		t.Fatal("want an error when the conversation moved on both attempts")
	}
	if calls != 2 {
		t.Fatalf("gateway calls = %d, want 2", calls)
	}
}

func TestClaudeCTONewDoesNotHoldTheStateLockAcrossTheCall(t *testing.T) {
	heldDuringCall := false
	d := newCTOStateTestDaemon(t, func(dd *Daemon) {
		if dd.cto.mu.TryLock() {
			dd.cto.mu.Unlock()
		} else {
			heldDuringCall = true
		}
	})
	if _, err := (claudeCTOHarness{}).newConversation(context.Background(), d); err != nil {
		t.Fatal(err)
	}
	if heldDuringCall {
		t.Fatal("the CTO state lock was held during the gateway call")
	}
}

func TestClaudeCTONewRepeatsCallWhenConversationMoved(t *testing.T) {
	calls := 0
	d := newCTOStateTestDaemon(t, func(dd *Daemon) {
		calls++
		if calls == 1 {
			dd.claudeCTO.setSession("claude_started")
		}
	})
	if _, err := (claudeCTOHarness{}).newConversation(context.Background(), d); err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Fatalf("gateway calls = %d, want 2", calls)
	}
}
