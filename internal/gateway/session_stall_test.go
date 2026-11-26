package gateway

import (
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

func TestSessionStallPendingAndWorkers(t *testing.T) {
	// The stall mark is read from the stored stall item, which agent-net no
	// longer stores (decision 0196). The follow-up of part 1b gives the mark
	// its own place and turns this test back on.
	t.Skip("stalled_since comes from the stall item, which decision 0196 no longer stores")
	s, c := newTestStore(t)
	a, b := twoSessions(t, s)
	if _, _, err := send(s, a, b, "pending-stall", ""); err != nil {
		t.Fatal(err)
	}
	workerNode, _, _ := s.CreateNode("worker-host", "linux")
	w, err := s.UpsertSession(workerNode, protocol.SessionInfo{Harness: "pi", NativeID: "worker", Status: "busy", Worker: &protocol.WorkerInfo{ID: "w_test"}})
	if err != nil {
		t.Fatal(err)
	}
	c.advance(21 * time.Minute)
	if err := s.checkSessionStalls(); err != nil {
		t.Fatal(err)
	}
	recipient, _, _ := s.Agent(b)
	worker, _, _ := s.Agent(w)
	if recipient.StalledSince == 0 || worker.StalledSince != 0 {
		t.Fatalf("recipient=%+v worker=%+v", recipient, worker)
	}
}

func TestSessionStallPendingDeliveryClearsOnAck(t *testing.T) {
	// The stall mark is read from the stored stall item, which agent-net no
	// longer stores (decision 0196). The follow-up of part 1b gives the mark
	// its own place and turns this test back on.
	t.Skip("stalled_since comes from the stall item, which decision 0196 no longer stores")
	s, c := newTestStore(t)
	a, b := twoSessions(t, s)
	m, _, err := send(s, a, b, "ack-stall", "")
	if err != nil {
		t.Fatal(err)
	}
	c.advance(21 * time.Minute)
	if err = s.checkSessionStalls(); err != nil {
		t.Fatal(err)
	}
	agent, _, _ := s.Agent(b)
	if agent.StalledSince == 0 {
		t.Fatal("pending delivery did not stall")
	}
	if _, err = s.db.Exec(`UPDATE messages SET state='delivered' WHERE id=?`, m.ID); err != nil {
		t.Fatal(err)
	}
	if err = s.checkSessionStalls(); err != nil {
		t.Fatal(err)
	}
	agent, _, _ = s.Agent(b)
	if agent.StalledSince != 0 {
		t.Fatal("ack did not clear stall")
	}
}

func TestSessionStallLostBusySessionGetsNoAsk(t *testing.T) {
	s, c := newTestStore(t)
	node, _, _ := s.CreateNode("test-host", "linux")
	in := protocol.SessionInfo{Harness: "claude", NativeID: "lost", Status: "busy"}
	id, err := s.UpsertSession(node, in)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetSessionLost(node, id); err != nil {
		t.Fatal(err)
	}
	c.advance(21 * time.Minute)
	if err := s.checkSessionStalls(); err != nil {
		t.Fatal(err)
	}
	a, _, _ := s.Agent(id)
	if a.Online || a.StalledSince != 0 || a.Status != "busy" {
		t.Fatalf("lost session got a stall ask: %+v", a)
	}
	if got := len(stallItems(t, s)); got != 0 {
		t.Fatalf("lost session raised %d stall asks, want 0", got)
	}
	if err := s.SetSessionOffline(node, id); err != nil {
		t.Fatal(err)
	}
	if err := s.checkSessionStalls(); err != nil {
		t.Fatal(err)
	}
	a, _, _ = s.Agent(id)
	if a.StalledSince != 0 || a.Status != "busy" {
		t.Fatalf("explicit end: %+v", a)
	}
}

// stallItems returns the session stall asks in the Inbox, open or closed.
func stallItems(t *testing.T, s *Store) []protocol.InboxItem {
	t.Helper()
	items, err := s.ListInbox()
	if err != nil {
		t.Fatal(err)
	}
	var out []protocol.InboxItem
	for _, item := range items {
		if item.Kind == "session_stall" {
			out = append(out, item)
		}
	}
	return out
}

func TestSessionStallSkipsOfflineAndIdleDone(t *testing.T) {
	t.Run("offline", func(t *testing.T) {
		s, c := newTestStore(t)
		node, _, err := s.CreateNode("test-host", "linux")
		if err != nil {
			t.Fatal(err)
		}
		id, err := s.UpsertSession(node, protocol.SessionInfo{Harness: "claude", NativeID: "offline", Status: "busy"})
		if err != nil {
			t.Fatal(err)
		}
		if err := s.SetSessionLost(node, id); err != nil {
			t.Fatal(err)
		}
		c.advance(21 * time.Minute)
		if err := s.checkSessionStalls(); err != nil {
			t.Fatal(err)
		}
		if got := len(stallItems(t, s)); got != 0 {
			t.Fatalf("offline session raised %d stall asks, want 0", got)
		}
	})
	t.Run("idle done", func(t *testing.T) {
		s, c := newTestStore(t)
		node, _, err := s.CreateNode("test-host", "linux")
		if err != nil {
			t.Fatal(err)
		}
		id, err := s.UpsertSession(node, protocol.SessionInfo{Harness: "claude", NativeID: "idle-done", Status: "idle"})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := s.Announce(node, protocol.ProfileAnnounce{SessionID: id, Task: "done"}); err != nil {
			t.Fatal(err)
		}
		sender, _ := twoSessions(t, s)
		if _, _, err := send(s, sender, id, "idle-done-pending", ""); err != nil {
			t.Fatal(err)
		}
		c.advance(21 * time.Minute)
		if err := s.checkSessionStalls(); err != nil {
			t.Fatal(err)
		}
		if got := len(stallItems(t, s)); got != 0 {
			t.Fatalf("idle done session raised %d stall asks, want 0", got)
		}
	})
}

func TestSessionStallLongBusyRunWithCPUProgress(t *testing.T) {
	// The stall mark is read from the stored stall item, which agent-net no
	// longer stores (decision 0196). The follow-up of part 1b gives the mark
	// its own place and turns this test back on.
	t.Skip("stalled_since comes from the stall item, which decision 0196 no longer stores")
	s, c := newTestStore(t)
	node, _, _ := s.CreateNode("test-host", "linux")
	in := protocol.SessionInfo{Harness: "claude", NativeID: "long-test", Status: "busy"}
	id, err := s.UpsertSession(node, in)
	if err != nil {
		t.Fatal(err)
	}
	c.advance(15 * time.Minute)
	in.HarnessActivity = c.t.Unix() // daemon reports a fresh busy process-tree CPU sample
	if _, err = s.UpsertSession(node, in); err != nil {
		t.Fatal(err)
	}
	c.advance(10 * time.Minute)
	if err = s.checkSessionStalls(); err != nil {
		t.Fatal(err)
	}
	a, _, _ := s.Agent(id)
	if a.StalledSince != 0 {
		t.Fatal("active long test incorrectly stalled")
	}
	c.advance(11 * time.Minute)
	if err = s.checkSessionStalls(); err != nil {
		t.Fatal(err)
	}
	a, _, _ = s.Agent(id)
	if a.StalledSince == 0 {
		t.Fatal("silent session did not stall after CPU stopped")
	}
}

// Messages carried in a delivered nested batch are not pending deliveries.
func TestSessionStallIgnoresNestedBatchMembers(t *testing.T) {
	s, c := newTestStore(t)
	a, b := twoSessions(t, s)
	busyStatus(t, s, b, "busy")
	for _, key := range []string{"one", "two"} {
		m, _, err := send(s, a, b, key, "")
		if err != nil || !s.HoldForBusy(m) {
			t.Fatalf("hold %s: %v", key, err)
		}
	}
	busyStatus(t, s, b, "idle")
	inner, _, err := releaseOne(t, s, b, false)
	if err != nil {
		t.Fatal(err)
	}
	busyStatus(t, s, b, "busy")
	if !s.HoldForBusy(inner) {
		t.Fatal("batch not held again")
	}
	third, _, err := send(s, a, b, "three", "")
	if err != nil || !s.HoldForBusy(third) {
		t.Fatalf("hold three: %v", err)
	}
	busyStatus(t, s, b, "idle")
	outer, _, err := releaseOne(t, s, b, false)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.ApplyDelivery(outer.ID, protocol.StateDelivered, ""); err != nil {
		t.Fatal(err)
	}
	c.advance(6 * time.Hour)
	if err := s.checkSessionStalls(); err != nil {
		t.Fatal(err)
	}
	items, err := s.ListInbox()
	if err != nil || len(items) != 0 {
		t.Fatalf("stall items for an idle session: %+v %v", items, err)
	}
}

// sessionStalls is stallItems for one session: the idle peer of a send gets
// its own ask for the delivery nobody acknowledges.
func sessionStalls(t *testing.T, s *Store, id string) []protocol.InboxItem {
	t.Helper()
	var out []protocol.InboxItem
	for _, item := range stallItems(t, s) {
		if item.SessionID == id {
			out = append(out, item)
		}
	}
	return out
}

// busySender registers a busy OpenCode session and a peer it can send to.
func busySender(t *testing.T, s *Store) (id, peer string) {
	t.Helper()
	node, _, err := s.CreateNode("test-host", "linux")
	if err != nil {
		t.Fatal(err)
	}
	id, err = s.UpsertSession(node, protocol.SessionInfo{Harness: "opencode", NativeID: "sender", Status: "busy"})
	if err != nil {
		t.Fatal(err)
	}
	peer, err = s.UpsertSession(node, protocol.SessionInfo{Harness: "claude", NativeID: "peer"})
	if err != nil {
		t.Fatal(err)
	}
	return id, peer
}

// A busy session that sends messages is working, even when its harness
// reports nothing. On 2026-11-20 an OpenCode session replied to its lead twice
// in a turn and still got two stall asks.
func TestSessionStallBusySessionThatSendsIsNotStalled(t *testing.T) {
	s, c := newTestStore(t)
	id, peer := busySender(t, s)
	c.advance(15 * time.Minute)
	if _, _, err := send(s, id, peer, "reply", ""); err != nil {
		t.Fatal(err)
	}
	c.advance(10 * time.Minute)
	if err := s.checkSessionStalls(); err != nil {
		t.Fatal(err)
	}
	if stalls := sessionStalls(t, s, id); len(stalls) != 0 {
		t.Fatalf("a session that sent 10 minutes ago was reported stalled: %+v", stalls)
	}
}
