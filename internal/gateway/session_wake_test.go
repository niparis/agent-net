package gateway

import (
	"context"
	"github.com/niparis/agent-net/internal/protocol"
	"testing"
	"time"
)

func TestWakeOfflineMergerTaskQueued(t *testing.T) {
	r := newDeliveryRig(t)
	if err := r.s.SetMerger(protocol.MergerSetRequest{Project: "demo", NodeID: r.a, SessionID: r.sonnet}, "owner"); err != nil {
		t.Fatal(err)
	}
	if err := r.s.SetDeliveryOwner(protocol.DeliveryOwnerSetRequest{Project: "demo", Step: "test", Node: "@merger"}, "owner"); err != nil {
		t.Fatal(err)
	}
	if err := r.s.SetSessionOffline(r.a, r.sonnet); err != nil {
		t.Fatal(err)
	}
	c := r.submit(r.a, r.workerSubmit("feature", commitA)).Change.Change
	r.g.dispatchStepTasks(context.Background())
	var n int
	if err := r.s.db.QueryRow(`SELECT count(*) FROM delivery_step_messages WHERE change_id=? AND step='test' AND session_id=?`, c.ID, r.sonnet).Scan(&n); err != nil || n != 1 {
		t.Fatalf("offline merger notices=%d, err=%v", n, err)
	}
}

func TestWakeBackoffAndExpiry(t *testing.T) {
	s, clock := newTestStore(t)
	from, to := twoSessions(t, s)
	m, _, err := send(s, from, to, "wake-expiry", "")
	if err != nil {
		t.Fatal(err)
	}
	clock.t = m.ExpiresAt.Add(-time.Second)
	ok, err := s.claimWake(to)
	if err != nil || !ok {
		t.Fatalf("first claim: %v %v", ok, err)
	}
	ok, err = s.claimWake(to)
	if err != nil || ok {
		t.Fatalf("second claim: %v %v", ok, err)
	}
	clock.t = m.ExpiresAt.Add(wakeTimeout - time.Second)
	if n, err := s.Expire(); err != nil || n != 0 {
		t.Fatalf("expired during wake: %d %v", n, err)
	}
	clock.t = m.ExpiresAt.Add(wakeBackoff)
	ok, err = s.claimWake(to)
	if err != nil || !ok {
		t.Fatalf("claim after backoff: %v %v", ok, err)
	}
}

func TestWakeRebindRequiresActiveClaim(t *testing.T) {
	s, _ := newTestStore(t)
	node, _, err := s.CreateNode("host", "linux")
	if err != nil {
		t.Fatal(err)
	}
	id, err := s.UpsertSession(node, protocol.SessionInfo{Harness: "claude", NativeID: "old-native"})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.SetSessionOffline(node, id); err != nil {
		t.Fatal(err)
	}
	g := &Gateway{store: s, wakeClaims: map[string]wakeClaim{id: {token: "nonce", node: node, native: "old-native"}}}
	p := wakeRebind{SessionID: id, Token: "nonce", NativeID: "new-native"}
	if err = g.rebindWake("other-node", p); err == nil {
		t.Fatal("another machine rebound the session")
	}
	bad := p
	bad.Token = "wrong"
	if err = g.rebindWake(node, bad); err == nil {
		t.Fatal("wrong wake token rebound the session")
	}
	if err = g.rebindWake(node, p); err != nil {
		t.Fatal(err)
	}
	a, ok, err := s.Agent(id)
	if err != nil || !ok || a.NativeID != "new-native" || a.Online {
		t.Fatalf("rebound agent: %+v %v", a, err)
	}
	if err = g.rebindWake(node, p); err == nil {
		t.Fatal("replayed rebind accepted")
	}
}
