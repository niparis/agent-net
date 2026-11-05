package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"strings"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// A ready task wakes only its explicitly assigned session. No task message is
// made until discovery brings the session online, so its backlog is batched.
func TestWakeOfflineMergerTaskBatched(t *testing.T) {
	r := newDeliveryRig(t)
	r.g.logger = log.New(io.Discard, "", 0)
	r.g.conns = map[string]*nodeConn{}
	wakes := make(chan string, 10)
	deliveries := make(chan protocol.Message, 10)
	fakeOwnerNode(t, r.g, r.a, func(h protocol.Header, raw []byte) error {
		switch h.Type {
		case protocol.FrameSessionWake:
			var p struct {
				SessionID string `json:"session_id"`
			}
			if err := json.Unmarshal(raw, &p); err != nil {
				return err
			}
			wakes <- p.SessionID
		case "msg.deliver":
			var p protocol.MsgDeliver
			if err := json.Unmarshal(raw, &p); err != nil {
				return err
			}
			if p.Message.To == r.sonnet {
				deliveries <- p.Message
			}
		}
		return nil
	})
	if err := r.s.SetMerger(protocol.MergerSetRequest{Project: "demo", NodeID: r.a, SessionID: r.sonnet}, "owner"); err != nil {
		t.Fatal(err)
	}
	if err := r.s.SetDeliveryOwner(protocol.DeliveryOwnerSetRequest{Project: "demo", Step: "test", Node: "@merger"}, "owner"); err != nil {
		t.Fatal(err)
	}
	if err := r.s.SetSessionOffline(r.a, r.sonnet); err != nil {
		t.Fatal(err)
	}
	var changes []string
	for i := 0; i < 3; i++ {
		changes = append(changes, r.submit(r.a, r.workerSubmit(fmt.Sprintf("feature/wake-%d", i), commitA)).Change.Change.ID)
	}
	var n int
	if err := r.s.db.QueryRow(`SELECT count(*) FROM delivery_step_messages WHERE session_id=?`, r.sonnet).Scan(&n); err != nil || n != 0 {
		t.Fatalf("offline step messages=%d, err=%v", n, err)
	}
	select {
	case id := <-wakes:
		if id != r.sonnet {
			t.Fatalf("woke %s, want %s", id, r.sonnet)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("merger was not woken")
	}
	if _, err := r.s.UpsertSession(r.a, protocol.SessionInfo{Harness: "claude", NativeID: "sonnet", Repo: protocol.RepoInfo{Project: "demo"}}); err != nil {
		t.Fatal(err)
	}
	r.g.dispatchStepTasks(context.Background())
	select {
	case m := <-deliveries:
		for _, id := range changes {
			if !strings.Contains(m.Body, id) {
				t.Fatalf("batch missing %s: %s", id, m.Body)
			}
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no batched task delivery")
	}
	if err := r.s.db.QueryRow(`SELECT count(DISTINCT message_id) FROM delivery_step_messages WHERE session_id=?`, r.sonnet).Scan(&n); err != nil || n != 1 {
		t.Fatalf("batched messages=%d, err=%v", n, err)
	}
	select {
	case id := <-wakes:
		t.Fatalf("extra wake for %s", id)
	case <-time.After(100 * time.Millisecond):
	}
	select {
	case m := <-deliveries:
		t.Fatalf("extra task delivery: %+v", m)
	default:
	}
}

func TestWakeOfflineCallerTaskBatched(t *testing.T) {
	r := newDeliveryRig(t)
	if err := r.s.AddArchitect("reviewer", "owner"); err != nil {
		t.Fatal(err)
	}
	if err := r.s.SetDeliveryOwner(protocol.DeliveryOwnerSetRequest{Project: "demo", Step: "review", Node: "reviewer"}, "owner"); err != nil {
		t.Fatal(err)
	}
	r.g.logger = log.New(io.Discard, "", 0)
	r.g.conns = map[string]*nodeConn{}
	wakes := make(chan string, 4)
	deliveries := make(chan protocol.Message, 4)
	fakeOwnerNode(t, r.g, r.a, func(h protocol.Header, raw []byte) error {
		switch h.Type {
		case protocol.FrameSessionWake:
			var p struct {
				SessionID string `json:"session_id"`
			}
			if err := json.Unmarshal(raw, &p); err != nil {
				return err
			}
			wakes <- p.SessionID
		case "msg.deliver":
			var p protocol.MsgDeliver
			if err := json.Unmarshal(raw, &p); err != nil {
				return err
			}
			if p.Message.To == r.caller {
				deliveries <- p.Message
			}
		}
		return nil
	})
	if err := r.s.SetSessionOffline(r.a, r.caller); err != nil {
		t.Fatal(err)
	}
	var changes []string
	for i := 0; i < 2; i++ {
		changes = append(changes, r.submit(r.a, r.workerSubmit(fmt.Sprintf("feature/caller-%d", i), commitA)).Change.Change.ID)
	}
	var n int
	if err := r.s.db.QueryRow(`SELECT count(*) FROM delivery_step_messages WHERE session_id=?`, r.caller).Scan(&n); err != nil || n != 0 {
		t.Fatalf("offline caller notices=%d err=%v", n, err)
	}
	select {
	case id := <-wakes:
		if id != r.caller {
			t.Fatalf("woke %s, want %s", id, r.caller)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("caller was not woken")
	}
	if _, err := r.s.UpsertSession(r.a, protocol.SessionInfo{Harness: "claude", NativeID: "caller", Repo: protocol.RepoInfo{Project: "demo"}}); err != nil {
		t.Fatal(err)
	}
	r.g.dispatchStepTasks(context.Background())
	select {
	case m := <-deliveries:
		for _, id := range changes {
			if !strings.Contains(m.Body, id) {
				t.Fatalf("batch missing %s: %s", id, m.Body)
			}
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no batched caller task delivery")
	}
	if err := r.s.db.QueryRow(`SELECT count(DISTINCT message_id) FROM delivery_step_messages WHERE session_id=?`, r.caller).Scan(&n); err != nil || n != 1 {
		t.Fatalf("batched caller messages=%d, err=%v", n, err)
	}
	select {
	case id := <-wakes:
		t.Fatalf("extra caller wake for %s", id)
	case <-time.After(100 * time.Millisecond):
	}
}

func TestWakeNodeOwnedTaskLeavesOfflineSessionsAsleep(t *testing.T) {
	r := newDeliveryRig(t)
	r.g.logger = log.New(io.Discard, "", 0)
	r.g.conns = map[string]*nodeConn{}
	fakeOwnerNode(t, r.g, r.b, func(h protocol.Header, raw []byte) error { return nil })
	if err := r.s.AddArchitect("reviewer", "owner"); err != nil {
		t.Fatal(err)
	}
	if err := r.s.SetEdge(protocol.Edge{From: "reviewer", To: "demo", Kind: "accepts"}, "owner"); err != nil {
		t.Fatal(err)
	}
	var ids []string
	for i := 0; i < 2; i++ {
		id, err := r.s.UpsertSession(r.b, protocol.SessionInfo{Harness: "claude", NativeID: fmt.Sprintf("old-%d", i), Repo: protocol.RepoInfo{Project: "reviewer"}})
		if err != nil {
			t.Fatal(err)
		}
		if err = r.s.SetSessionOffline(r.b, id); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	c := r.submit(r.a, r.workerSubmit("feature/node-owner", commitA)).Change.Change
	var owner string
	if err := r.s.db.QueryRow(`SELECT owner_node FROM delivery_step_tasks WHERE change_id=? AND step='accept'`, c.ID).Scan(&owner); err != nil || owner != "reviewer" {
		t.Fatalf("owner=%q, err=%v", owner, err)
	}
	for _, id := range ids {
		var wakes, messages int
		if err := r.s.db.QueryRow(`SELECT count(*) FROM retired_session_wakes WHERE session_id=?`, id).Scan(&wakes); err != nil {
			t.Fatal(err)
		}
		if err := r.s.db.QueryRow(`SELECT count(*) FROM delivery_step_messages WHERE session_id=?`, id).Scan(&messages); err != nil {
			t.Fatal(err)
		}
		if wakes != 0 || messages != 0 {
			t.Fatalf("offline project session: wakes=%d messages=%d", wakes, messages)
		}
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
