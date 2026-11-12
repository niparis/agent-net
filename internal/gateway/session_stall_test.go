package gateway

import (
	"fmt"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

func TestSessionStallHarnessesAndInbox(t *testing.T) {
	for _, harness := range []string{"claude", "codex", "opencode", "pi"} {
		t.Run(harness, func(t *testing.T) {
			s, c := newTestStore(t)
			node, _, err := s.CreateNode("test-host", "linux")
			if err != nil {
				t.Fatal(err)
			}
			in := protocol.SessionInfo{Harness: harness, NativeID: "test", Status: "busy"}
			id, err := s.UpsertSession(node, in)
			if err != nil {
				t.Fatal(err)
			}
			c.t = c.t.Add(21 * time.Minute)
			if err := s.checkSessionStalls(); err != nil {
				t.Fatal(err)
			}
			a, _, err := s.Agent(id)
			if err != nil || a.StalledSince == 0 || a.Status != "busy" {
				t.Fatalf("stalled agent: %+v %v", a, err)
			}
			if err := s.checkSessionStalls(); err != nil {
				t.Fatal(err)
			}
			items, err := s.ListInbox()
			if err != nil || len(items) != 1 || items[0].Status != "open" {
				t.Fatalf("items: %+v %v", items, err)
			}
			in.HarnessActivity = c.t.Unix()
			if _, err := s.UpsertSession(node, in); err != nil {
				t.Fatal(err)
			}
			if err := s.checkSessionStalls(); err != nil {
				t.Fatal(err)
			}
			a, _, _ = s.Agent(id)
			items, _ = s.ListInbox()
			if a.StalledSince != 0 || items[0].Status == "open" {
				t.Fatalf("activity did not clear stall: %+v %+v", a, items)
			}
			c.t = c.t.Add(21 * time.Minute)
			if err := s.checkSessionStalls(); err != nil {
				t.Fatal(err)
			}
			items, _ = s.ListInbox()
			if len(items) != 2 || items[1].Status != "open" {
				t.Fatalf("second stall: %+v", items)
			}
			if err := s.SetSessionOffline(node, id); err != nil {
				t.Fatal(err)
			}
			if err := s.checkSessionStalls(); err != nil {
				t.Fatal(err)
			}
			items, _ = s.ListInbox()
			if items[1].Status == "open" {
				t.Fatalf("ended session retains notice: %+v", items)
			}
		})
	}
}

func TestSessionStallPendingAndWorkers(t *testing.T) {
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
	c.t = c.t.Add(21 * time.Minute)
	if err := s.checkSessionStalls(); err != nil {
		t.Fatal(err)
	}
	recipient, _, _ := s.Agent(b)
	worker, _, _ := s.Agent(w)
	if recipient.StalledSince == 0 || worker.StalledSince != 0 {
		t.Fatalf("recipient=%+v worker=%+v", recipient, worker)
	}
}

func TestSessionStallSettingsAndDismissal(t *testing.T) {
	s, c := newTestStore(t)
	node, _, _ := s.CreateNode("test-host", "linux")
	in := protocol.SessionInfo{Harness: "claude", NativeID: "one", Status: "busy", Repo: protocol.RepoInfo{Project: "demo"}}
	id, err := s.UpsertSession(node, in)
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.WriteSettings([]SettingsChange{{Key: "sessions.stall_timeout", Level: "repo", Scope: "demo", Value: []byte(`"30m"`)}}, "owner", "test")
	if err != nil {
		t.Fatal(err)
	}
	c.t = c.t.Add(21 * time.Minute)
	// A repeated discovery is not progress.
	if _, err := s.UpsertSession(node, in); err != nil {
		t.Fatal(err)
	}
	if err := s.checkSessionStalls(); err != nil {
		t.Fatal(err)
	}
	items, _ := s.ListInbox()
	if len(items) != 0 {
		t.Fatalf("repo timeout ignored: %+v", items)
	}
	c.t = c.t.Add(10 * time.Minute)
	if err := s.checkSessionStalls(); err != nil {
		t.Fatal(err)
	}
	items, _ = s.ListInbox()
	if len(items) != 1 {
		t.Fatalf("want one notice: %+v", items)
	}
	if _, err := s.AnswerInbox(items[0].ID, "dismiss"); err != nil {
		t.Fatal(err)
	}
	if err := s.checkSessionStalls(); err != nil {
		t.Fatal(err)
	}
	items, _ = s.ListInbox()
	a, _, _ := s.Agent(id)
	if len(items) != 1 || a.StalledSince == 0 {
		t.Fatalf("dismissal repeated or hid stall: %+v %+v", items, a)
	}
	in.Status = "idle"
	if _, err := s.UpsertSession(node, in); err != nil {
		t.Fatal(err)
	}
	if err := s.checkSessionStalls(); err != nil {
		t.Fatal(err)
	}
	a, _, _ = s.Agent(id)
	if a.StalledSince != 0 {
		t.Fatalf("idle did not clear: %+v", a)
	}
}

func TestSessionStallPendingDeliveryClearsOnAck(t *testing.T) {
	s, c := newTestStore(t)
	a, b := twoSessions(t, s)
	m, _, err := send(s, a, b, "ack-stall", "")
	if err != nil {
		t.Fatal(err)
	}
	c.t = c.t.Add(21 * time.Minute)
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

func TestSessionStallNewPendingDoesNotClearBusyNotice(t *testing.T) {
	s, c := newTestStore(t)
	a, b := twoSessions(t, s)
	node, _ := s.SessionNode(b)
	in := protocol.SessionInfo{Harness: "pi", NativeID: "nb", Status: "busy"}
	if _, err := s.UpsertSession(node, in); err != nil {
		t.Fatal(err)
	}
	c.t = c.t.Add(21 * time.Minute)
	if err := s.checkSessionStalls(); err != nil {
		t.Fatal(err)
	}
	if _, _, err := send(s, a, b, "stall-new-pending", ""); err != nil {
		t.Fatal(err)
	}
	if err := s.checkSessionStalls(); err != nil {
		t.Fatal(err)
	}
	agent, _, _ := s.Agent(b)
	items, _ := s.ListInbox()
	if agent.StalledSince == 0 || len(items) != 1 || items[0].Status != "open" {
		t.Fatalf("new delivery is not harness activity: %+v %+v", agent, items)
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
	c.t = c.t.Add(21 * time.Minute)
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

// closeByChief closes an ask the way the chief of staff does.
func closeByChief(t *testing.T, s *Store, id string) {
	t.Helper()
	if _, err := s.db.Exec(`UPDATE inbox_items SET status='closed',answer='chief: looked at it',answered_at=? WHERE id=?`, s.now().UnixNano(), id); err != nil {
		t.Fatal(err)
	}
}

func TestSessionStallOneAskPerActivity(t *testing.T) {
	s, c := newTestStore(t)
	node, _, err := s.CreateNode("test-host", "linux")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.UpsertSession(node, protocol.SessionInfo{Harness: "claude", NativeID: "once", Status: "busy"}); err != nil {
		t.Fatal(err)
	}
	c.t = c.t.Add(21 * time.Minute)
	if err := s.checkSessionStalls(); err != nil {
		t.Fatal(err)
	}
	stalls := stallItems(t, s)
	if len(stalls) != 1 || stalls[0].Status != "open" {
		t.Fatalf("first ask: %+v", stalls)
	}
	closeByChief(t, s, stalls[0].ID)
	// Every later minute sees the same activity time and must add nothing.
	for i := 0; i < 3; i++ {
		c.t = c.t.Add(time.Minute)
		if err := s.checkSessionStalls(); err != nil {
			t.Fatal(err)
		}
	}
	if got := len(stallItems(t, s)); got != 1 {
		t.Fatalf("%d stall asks for one activity time, want 1", got)
	}
}

func TestSessionStallNoReRaiseAfterClose(t *testing.T) {
	s, c := newTestStore(t)
	node, _, err := s.CreateNode("test-host", "linux")
	if err != nil {
		t.Fatal(err)
	}
	in := protocol.SessionInfo{Harness: "pi", NativeID: "closed", Status: "busy"}
	if _, err := s.UpsertSession(node, in); err != nil {
		t.Fatal(err)
	}
	c.t = c.t.Add(21 * time.Minute)
	if err := s.checkSessionStalls(); err != nil {
		t.Fatal(err)
	}
	stalls := stallItems(t, s)
	if len(stalls) != 1 {
		t.Fatalf("first ask: %+v", stalls)
	}
	closeByChief(t, s, stalls[0].ID)
	c.t = c.t.Add(time.Minute)
	if err := s.checkSessionStalls(); err != nil {
		t.Fatal(err)
	}
	if got := len(stallItems(t, s)); got != 1 {
		t.Fatalf("ask raised again after the chief closed it: %d asks", got)
	}
	// New activity allows exactly one new ask for the new activity time.
	in.HarnessActivity = c.t.Unix()
	if _, err := s.UpsertSession(node, in); err != nil {
		t.Fatal(err)
	}
	c.t = c.t.Add(21 * time.Minute)
	if err := s.checkSessionStalls(); err != nil {
		t.Fatal(err)
	}
	stalls = stallItems(t, s)
	open := 0
	for _, item := range stalls {
		if item.Status == "open" {
			open++
		}
	}
	if len(stalls) != 2 || open != 1 {
		t.Fatalf("new activity: %d asks, %d open, want 2 and 1", len(stalls), open)
	}
}

func TestSessionStallNoReRaiseAfterOwnerAnswer(t *testing.T) {
	s, c := newTestStore(t)
	node, _, err := s.CreateNode("test-host", "linux")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.UpsertSession(node, protocol.SessionInfo{Harness: "claude", NativeID: "answered", Status: "busy"}); err != nil {
		t.Fatal(err)
	}
	c.t = c.t.Add(21 * time.Minute)
	if err := s.checkSessionStalls(); err != nil {
		t.Fatal(err)
	}
	stalls := stallItems(t, s)
	if len(stalls) != 1 {
		t.Fatalf("first ask: %+v", stalls)
	}
	if _, err := s.AnswerInbox(stalls[0].ID, "restarted it"); err != nil {
		t.Fatal(err)
	}
	c.t = c.t.Add(time.Minute)
	if err := s.checkSessionStalls(); err != nil {
		t.Fatal(err)
	}
	if got := len(stallItems(t, s)); got != 1 {
		t.Fatalf("ask raised again after the owner answered it: %d asks", got)
	}
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
		c.t = c.t.Add(21 * time.Minute)
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
		c.t = c.t.Add(21 * time.Minute)
		if err := s.checkSessionStalls(); err != nil {
			t.Fatal(err)
		}
		if got := len(stallItems(t, s)); got != 0 {
			t.Fatalf("idle done session raised %d stall asks, want 0", got)
		}
	})
}

func TestSessionStallIdlePendingDeliveryStillRaises(t *testing.T) {
	s, c := newTestStore(t)
	node, _, err := s.CreateNode("test-host", "linux")
	if err != nil {
		t.Fatal(err)
	}
	id, err := s.UpsertSession(node, protocol.SessionInfo{Harness: "claude", NativeID: "idle-pending", Status: "idle"})
	if err != nil {
		t.Fatal(err)
	}
	sender, _ := twoSessions(t, s)
	if _, _, err := send(s, sender, id, "idle-pending-delivery", ""); err != nil {
		t.Fatal(err)
	}
	c.t = c.t.Add(21 * time.Minute)
	if err := s.checkSessionStalls(); err != nil {
		t.Fatal(err)
	}
	stalls := stallItems(t, s)
	if len(stalls) != 1 || stalls[0].Status != "open" || stalls[0].SessionID != id {
		t.Fatalf("idle pending stall: %+v", stalls)
	}
	c.t = c.t.Add(time.Minute)
	if err := s.checkSessionStalls(); err != nil {
		t.Fatal(err)
	}
	if got := len(stallItems(t, s)); got != 1 {
		t.Fatalf("idle pending stall repeated: %d asks", got)
	}
}

func TestSessionStallBusySessionStillRaises(t *testing.T) {
	s, c := newTestStore(t)
	node, _, err := s.CreateNode("test-host", "linux")
	if err != nil {
		t.Fatal(err)
	}
	id, err := s.UpsertSession(node, protocol.SessionInfo{Harness: "codex", NativeID: "busy", Status: "busy"})
	if err != nil {
		t.Fatal(err)
	}
	c.t = c.t.Add(21 * time.Minute)
	if err := s.checkSessionStalls(); err != nil {
		t.Fatal(err)
	}
	stalls := stallItems(t, s)
	if len(stalls) != 1 || stalls[0].Status != "open" || stalls[0].SessionID != id {
		t.Fatalf("busy stall: %+v", stalls)
	}
	if err := s.checkSessionStalls(); err != nil {
		t.Fatal(err)
	}
	if got := len(stallItems(t, s)); got != 1 {
		t.Fatalf("busy stall repeated while open: %d asks", got)
	}
}

func TestSessionStallBusyWhileWaiting(t *testing.T) {
	for _, kind := range []string{protocol.WaitingQuestion, protocol.WaitingPermission} {
		for _, pending := range []bool{false, true} {
			t.Run(kind+fmt.Sprint(pending), func(t *testing.T) {
				s, c := newTestStore(t)
				node, _, _ := s.CreateNode("test-host", "linux")
				in := protocol.SessionInfo{Harness: "codex", NativeID: "waiting", Status: "busy"}
				id, err := s.UpsertSession(node, in)
				if err != nil {
					t.Fatal(err)
				}
				c.t = c.t.Add(21 * time.Minute)
				if err = s.checkSessionStalls(); err != nil {
					t.Fatal(err)
				}
				if a, _, _ := s.Agent(id); a.StalledSince == 0 {
					t.Fatal("expected initial stall")
				}
				in.Waiting = &protocol.Waiting{Kind: kind, Text: "Continue?"}
				if _, err = s.UpsertSession(node, in); err != nil {
					t.Fatal(err)
				}
				if pending {
					a, _ := twoSessions(t, s)
					if _, _, err = send(s, a, id, "pending", ""); err != nil {
						t.Fatal(err)
					}
				}
				c.t = c.t.Add(time.Hour)
				if err = s.checkSessionStalls(); err != nil {
					t.Fatal(err)
				}
				a, _, _ := s.Agent(id)
				all, _ := s.ListInbox()
				// A session that waits also has a waiting ask (decision 0090);
				// this test is about the stall item.
				var items []protocol.InboxItem
				for _, it := range all {
					if it.Kind == "session_stall" {
						items = append(items, it)
					}
				}
				if a.StalledSince != 0 || len(items) != 1 || items[0].Status == "open" {
					t.Fatalf("waiting session retained stall: %+v %+v", a, items)
				}
			})
		}
	}
}

func TestSessionStallLongBusyRunWithCPUProgress(t *testing.T) {
	s, c := newTestStore(t)
	node, _, _ := s.CreateNode("test-host", "linux")
	in := protocol.SessionInfo{Harness: "claude", NativeID: "long-test", Status: "busy"}
	id, err := s.UpsertSession(node, in)
	if err != nil {
		t.Fatal(err)
	}
	c.t = c.t.Add(15 * time.Minute)
	in.HarnessActivity = c.t.Unix() // daemon reports a fresh busy process-tree CPU sample
	if _, err = s.UpsertSession(node, in); err != nil {
		t.Fatal(err)
	}
	c.t = c.t.Add(10 * time.Minute)
	if err = s.checkSessionStalls(); err != nil {
		t.Fatal(err)
	}
	a, _, _ := s.Agent(id)
	if a.StalledSince != 0 {
		t.Fatal("active long test incorrectly stalled")
	}
	c.t = c.t.Add(11 * time.Minute)
	if err = s.checkSessionStalls(); err != nil {
		t.Fatal(err)
	}
	a, _, _ = s.Agent(id)
	if a.StalledSince == 0 {
		t.Fatal("silent session did not stall after CPU stopped")
	}
}

// A session that started workers waits for them: no output for 20 minutes is
// its normal state, not a stall. The 2026-11-12 asks about the lead session
// came about once an hour while it waited for workers and step tasks.
func TestSessionStallNoAskWhileSessionWaitsForItsWorkers(t *testing.T) {
	s, c := newTestStore(t)
	node, _, _ := s.CreateNode("test-host", "linux")
	lead, err := s.UpsertSession(node, protocol.SessionInfo{Harness: "claude", NativeID: "lead", Status: "busy"})
	if err != nil {
		t.Fatal(err)
	}
	worker, err := s.UpsertSession(node, protocol.SessionInfo{Harness: "claude", NativeID: "worker", Status: "busy",
		Worker: &protocol.WorkerInfo{ID: "w_1", Owner: lead, Project: "demo"}})
	if err != nil {
		t.Fatal(err)
	}
	c.t = c.t.Add(21 * time.Minute)
	if err := s.checkSessionStalls(); err != nil {
		t.Fatal(err)
	}
	if got := stallItems(t, s); len(got) != 0 {
		t.Fatalf("stall ask while the session waits for its worker: %+v", got)
	}
	// The worker ended: the session has nothing to wait for and is silent.
	if err := s.SetSessionOffline(node, worker); err != nil {
		t.Fatal(err)
	}
	if err := s.checkSessionStalls(); err != nil {
		t.Fatal(err)
	}
	if got := stallItems(t, s); len(got) != 1 || got[0].SessionID != lead {
		t.Fatalf("silent session with no worker running: %+v", got)
	}
}

func TestSessionStallNoAskWhileItsStepTaskIsClaimed(t *testing.T) {
	s, c := newTestStore(t)
	node, _, _ := s.CreateNode("test-host", "linux")
	lead, err := s.UpsertSession(node, protocol.SessionInfo{Harness: "claude", NativeID: "lead", Status: "busy"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`INSERT INTO delivery_changes(id,project,branch,head_commit,revision,origin,caller,state,created_at) VALUES('ch_1','demo','b','h',1,'{}',?,'open',1)`, lead); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`INSERT INTO delivery_step_tasks(change_id,revision,step,owner_node,claimed_by,completed,created_at) VALUES('ch_1',1,'test','n','s_merger',0,1)`); err != nil {
		t.Fatal(err)
	}
	c.t = c.t.Add(21 * time.Minute)
	if err := s.checkSessionStalls(); err != nil {
		t.Fatal(err)
	}
	if got := stallItems(t, s); len(got) != 0 {
		t.Fatalf("stall ask while the merger works on the session's change: %+v", got)
	}
	if _, err := s.db.Exec(`UPDATE delivery_step_tasks SET completed=1`); err != nil {
		t.Fatal(err)
	}
	if err := s.checkSessionStalls(); err != nil {
		t.Fatal(err)
	}
	if got := stallItems(t, s); len(got) != 1 {
		t.Fatalf("silent session with no step task claimed: %+v", got)
	}
}

// No new ask while one is open, and none within the stall window after one
// closed. Activity that the harness reports late has a time before the close,
// so the session can look stalled again at once.
func TestSessionStallNoNewAskWithinTheWindowAfterAClose(t *testing.T) {
	s, c := newTestStore(t)
	node, _, _ := s.CreateNode("test-host", "linux")
	in := protocol.SessionInfo{Harness: "claude", NativeID: "late", Status: "busy"}
	if _, err := s.UpsertSession(node, in); err != nil {
		t.Fatal(err)
	}
	c.t = c.t.Add(21 * time.Minute)
	if err := s.checkSessionStalls(); err != nil {
		t.Fatal(err)
	}
	if got := stallItems(t, s); len(got) != 1 || got[0].Status != "open" {
		t.Fatalf("first ask: %+v", got)
	}
	// Activity from 5 minutes ago arrives now: it closes the ask.
	in.HarnessActivity = c.t.Add(-5 * time.Minute).Unix()
	if _, err := s.UpsertSession(node, in); err != nil {
		t.Fatal(err)
	}
	if err := s.checkSessionStalls(); err != nil {
		t.Fatal(err)
	}
	if got := stallItems(t, s); len(got) != 1 || got[0].Status == "open" {
		t.Fatalf("activity did not close the ask: %+v", got)
	}
	// 16 minutes later that activity is 21 minutes old, but the ask closed 16
	// minutes ago: still inside the window.
	c.t = c.t.Add(16 * time.Minute)
	if err := s.checkSessionStalls(); err != nil {
		t.Fatal(err)
	}
	if got := stallItems(t, s); len(got) != 1 {
		t.Fatalf("new ask %d minutes after the last one closed: %+v", 16, got)
	}
	// Once the window has passed the session may be asked about again.
	c.t = c.t.Add(5 * time.Minute)
	if err := s.checkSessionStalls(); err != nil {
		t.Fatal(err)
	}
	if got := stallItems(t, s); len(got) != 2 || got[1].Status != "open" {
		t.Fatalf("no ask after the window passed: %+v", got)
	}
}
