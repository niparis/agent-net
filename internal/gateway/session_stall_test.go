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

func TestSessionStallLostBusySessionAndExplicitEnd(t *testing.T) {
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
	if a.Online || a.StalledSince == 0 || a.Status != "busy" {
		t.Fatalf("lost session: %+v", a)
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
