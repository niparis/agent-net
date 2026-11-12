package gateway

import (
	"context"
	"encoding/json"
	"io"
	"log"
	"strings"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// replaceRedirectRig is a delivery rig whose machine a records the messages it
// is given, so a test can see where a message went.
type replaceRedirectRig struct {
	*deliveryRig
	delivered chan protocol.Message
}

func newReplaceRedirectRig(t *testing.T) *replaceRedirectRig {
	t.Helper()
	r := &replaceRedirectRig{deliveryRig: newDeliveryRig(t), delivered: make(chan protocol.Message, 16)}
	r.g.logger = log.New(io.Discard, "", 0)
	r.g.conns = map[string]*nodeConn{}
	fakeOwnerNode(t, r.g, r.a, func(h protocol.Header, raw []byte) error {
		if h.Type == "msg.deliver" {
			var p protocol.MsgDeliver
			if err := json.Unmarshal(raw, &p); err != nil {
				return err
			}
			r.delivered <- p.Message
		}
		return nil
	})
	return r
}

// replace stops old and moves its work to a new session that registers on
// machine a, as the owner's session_replace does.
func (r *replaceRedirectRig) replace(old, native string) string {
	r.t.Helper()
	if err := r.s.SetSessionOffline(r.a, old); err != nil {
		r.t.Fatal(err)
	}
	fresh, err := r.s.UpsertSession(r.a, protocol.SessionInfo{Harness: "claude", NativeID: native,
		Repo: protocol.RepoInfo{Root: "/code/demo", Project: "demo"}})
	if err != nil {
		r.t.Fatal(err)
	}
	if moved, err := r.s.ReplaceSession(r.a, old, fresh); err != nil || !moved {
		r.t.Fatalf("replace %s: moved=%v err=%v", old, moved, err)
	}
	return fresh
}

// waitDelivered waits for the machine to be given message id and returns it.
func (r *replaceRedirectRig) waitDelivered(id string) protocol.Message {
	r.t.Helper()
	deadline := time.After(2 * time.Second)
	for {
		select {
		case m := <-r.delivered:
			if m.ID == id {
				return m
			}
		case <-deadline:
			r.t.Fatalf("message %s was not delivered", id)
		}
	}
}

func (r *replaceRedirectRig) wakeClaims(session string) int {
	r.t.Helper()
	var n int
	if err := r.s.db.QueryRow(`SELECT count(*) FROM retired_session_wakes WHERE session_id=?`, session).Scan(&n); err != nil {
		r.t.Fatal(err)
	}
	return n
}

// A message sent to a replaced session's ID after the replace (a worker's
// daemon keeps the old caller ID) is stored for the session that replaced it
// and delivered there. The stopped session is never woken for it.
func TestMessagesToAReplacedSessionGoToItsReplacement(t *testing.T) {
	r := newReplaceRedirectRig(t)
	old := r.caller
	// The worker's task, sent by the old session before it was replaced.
	task, _, err := r.s.StoreMessage(protocol.MsgSend{Sender: old, Recipient: r.worker, Type: protocol.TypeRequest, Body: "the task"})
	if err != nil {
		t.Fatal(err)
	}
	fresh := r.replace(old, "caller-copy")

	send := func(p protocol.MsgSend) string {
		t.Helper()
		res, err := deliveryCall[struct {
			Message protocol.Message `json:"message"`
			Routing string           `json:"routing"`
		}](r.deliveryRig, r.a, "msg.send", p)
		if err != nil {
			t.Fatalf("send from %s: %v", p.Sender, err)
		}
		// The sender learns the new ID.
		if !strings.Contains(res.Routing, "replaced by "+fresh) {
			t.Fatalf("send from %s: the routing note does not name %s: %q", p.Sender, fresh, res.Routing)
		}
		return res.Message.ID
	}
	automatic := func() string {
		t.Helper()
		m, _, err := r.s.storeAutomaticMessage(protocol.MsgSend{Recipient: old, Body: "a start failed"})
		if err != nil {
			t.Fatal(err)
		}
		r.g.dispatch(context.Background(), m)
		return m.ID
	}
	// A notice written straight into the store, as most agent-net notices are.
	direct := func() string {
		t.Helper()
		m, err := r.s.storeAgentNetMessage(r.s.db, old, "the chief's batch", "", r.s.now())
		if err != nil {
			t.Fatal(err)
		}
		r.g.dispatch(context.Background(), m)
		return m.ID
	}
	for _, c := range []struct {
		name string
		send func() string
	}{
		{"an agent", func() string {
			return send(protocol.MsgSend{Sender: r.sonnet, Recipient: old, Body: "a question"})
		}},
		{"a worker hand-back", func() string {
			return send(protocol.MsgSend{Sender: r.worker, Recipient: old, ParentID: task.ID, Type: protocol.TypeReply,
				Body: "status: done", WorkerNotice: true})
		}},
		{"an agent-net notice", automatic},
		{"a stored agent-net notice", direct},
	} {
		id := c.send()
		stored, ok, err := r.s.Message(id)
		if err != nil || !ok || stored.To != fresh {
			t.Fatalf("%s: the message is stored for %q, want %s (%v)", c.name, stored.To, fresh, err)
		}
		if got := r.waitDelivered(id); got.To != fresh {
			t.Fatalf("%s: delivered to %s, want %s", c.name, got.To, fresh)
		}
	}
	if n := r.wakeClaims(old); n != 0 {
		t.Fatalf("the replaced session was woken %d times", n)
	}
}

// Replacements can chain (a -> b -> c): a message to the first ID goes to the
// last live session. A loop of replacements keeps the message under the ID it
// was sent to, and nothing wakes a replaced session.
func TestReplacedSessionChainDeliversToTheLastLiveSession(t *testing.T) {
	r := newReplaceRedirectRig(t)
	a := r.caller
	b := r.replace(a, "caller-b")
	c := r.replace(b, "caller-c")
	m, _, err := r.s.StoreMessage(protocol.MsgSend{Sender: r.sonnet, Recipient: a, Body: "for the caller"})
	if err != nil {
		t.Fatal(err)
	}
	if m.To != c {
		t.Fatalf("stored for %s, want the last session %s", m.To, c)
	}
	r.g.dispatch(context.Background(), m)
	if got := r.waitDelivered(m.ID); got.To != c {
		t.Fatalf("delivered to %s, want %s", got.To, c)
	}

	// x -> y -> x: no live session holds the conversation.
	x := r.replace(c, "caller-x")
	y := r.replace(x, "caller-y")
	if err := r.s.SetSessionOffline(r.a, y); err != nil {
		t.Fatal(err)
	}
	if moved, err := r.s.ReplaceSession(r.a, y, x); err != nil || !moved {
		t.Fatalf("replace back: moved=%v err=%v", moved, err)
	}
	m, _, err = r.s.StoreMessage(protocol.MsgSend{Sender: r.sonnet, Recipient: x, Body: "into the loop"})
	if err != nil {
		t.Fatal(err)
	}
	if m.To != x {
		t.Fatalf("a loop stored the message for %s, want %s", m.To, x)
	}
	if r.g.dispatch(context.Background(), m) {
		t.Fatal("dispatch started a wake inside a loop of replaced sessions")
	}
	for _, id := range []string{a, b, x, y} {
		if n := r.wakeClaims(id); n != 0 {
			t.Fatalf("replaced session %s was woken", id)
		}
	}
}

// A worker's daemon still names the old caller when it hands back its change.
// The change and its step messages belong to the session that replaced it.
func TestWorkerSubmitAfterReplaceNamesTheNewCaller(t *testing.T) {
	r := newReplaceRedirectRig(t)
	fresh := r.replace(r.caller, "caller-copy")
	ch := r.submit(r.a, r.workerSubmit("feature/late", commitA)).Change.Change
	if ch.Caller != fresh {
		t.Fatalf("the late change names caller %s, want %s", ch.Caller, fresh)
	}
	if _, err := r.report(r.a, protocol.StepReportRequest{SessionID: fresh, ChangeID: ch.ID, StepRef: stepAccept,
		Result: protocol.StepResultPass, Commit: commitA}); err != nil {
		t.Fatalf("the new session could not accept the late change: %v", err)
	}
}
