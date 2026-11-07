package gateway

import (
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

func TestPendingRequests(t *testing.T) {
	s, c := newTestStore(t)
	a, b := twoSessions(t, s)
	req, _, err := s.StoreMessage(protocol.MsgSend{Sender: a, Recipient: b, Body: "request", IdempotencyKey: "request", Type: protocol.TypeRequest})
	if err != nil {
		t.Fatal(err)
	}
	check := func(want int) {
		t.Helper()
		agents, err := s.Agents(protocol.QueryAgents{})
		if err != nil {
			t.Fatal(err)
		}
		for _, agent := range agents {
			if (agent.SessionID == a || agent.SessionID == b) && len(agent.Pending) != want {
				t.Fatalf("%s pending = %+v, want %d", agent.SessionID, agent.Pending, want)
			}
		}
	}
	check(1)
	agents, err := s.Agents(protocol.QueryAgents{})
	if err != nil {
		t.Fatal(err)
	}
	for _, agent := range agents {
		if agent.SessionID != a && agent.SessionID != b {
			continue
		}
		p := agent.Pending[0]
		if p.MessageID != req.ID || p.Since != req.CreatedAt.Unix() || p.Outgoing != (agent.SessionID == a) || p.PeerName == "" {
			t.Fatalf("bad pending: %+v", p)
		}
	}
	_, _, err = s.StoreMessage(protocol.MsgSend{Sender: b, Recipient: a, Body: "reply", IdempotencyKey: "reply", ParentID: req.ID, Type: protocol.TypeReply})
	if err != nil {
		t.Fatal(err)
	}
	check(0)
	_, _, err = s.StoreMessage(protocol.MsgSend{Sender: a, Recipient: b, Body: "notice", IdempotencyKey: "notice", Type: protocol.TypeNotification})
	if err != nil {
		t.Fatal(err)
	}
	check(0)
	_, _, err = s.StoreMessage(protocol.MsgSend{Sender: a, Recipient: b, Body: "old", IdempotencyKey: "old", Type: protocol.TypeRequest})
	if err != nil {
		t.Fatal(err)
	}
	c.t = c.t.Add(24*time.Hour + time.Second)
	check(0)
}

func TestPendingStallCountOncePerIdlePeriod(t *testing.T) {
	s, c := newTestStore(t)
	a, b := twoSessions(t, s)
	if _, err := s.db.Exec(`UPDATE sessions SET status='idle', last_active=? WHERE id IN (?, ?)`, c.t.UnixNano(), a, b); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.StoreMessage(protocol.MsgSend{Sender: a, Recipient: b, Body: "request", IdempotencyKey: "stall", Type: protocol.TypeRequest}); err != nil {
		t.Fatal(err)
	}
	if err := s.samplePendingStalls(); err != nil {
		t.Fatal(err)
	}
	c.t = c.t.Add(21 * time.Minute)
	if err := s.samplePendingStalls(); err != nil {
		t.Fatal(err)
	}
	if err := s.samplePendingStalls(); err != nil {
		t.Fatal(err)
	}
	counts, err := s.SessionCounts(24 * time.Hour)
	if err != nil || counts.PendingStalls != 2 {
		t.Fatalf("counts = %+v, %v", counts, err)
	}
}

// A pending outgoing request describes a current wait, not every unanswered
// request in the retained message history.
func TestPendingWaitClearsOnProgress(t *testing.T) {
	for _, event := range []string{"busy", "busy then idle", "activity on status update", "send notification", "send request", "send reply", "peer reply in another conversation", "peer ended", "peer archived"} {
		t.Run(event, func(t *testing.T) {
			s, c := newTestStore(t)
			a, b := twoSessions(t, s)
			mustSend := func(from, to, kind, key string) protocol.Message {
				t.Helper()
				m, _, err := s.StoreMessage(protocol.MsgSend{Sender: from, Recipient: to, Body: key, IdempotencyKey: key, Type: kind})
				if err != nil {
					t.Fatal(err)
				}
				return m
			}
			req := mustSend(a, b, protocol.TypeRequest, "original")
			check := func(want bool) {
				t.Helper()
				agents, err := s.Agents(protocol.QueryAgents{})
				if err != nil {
					t.Fatal(err)
				}
				found := false
				for _, agent := range agents {
					if agent.SessionID == a {
						for _, p := range agent.Pending {
							found = found || p.MessageID == req.ID
						}
					}
				}
				if found != want {
					t.Fatalf("original outgoing wait present = %v, want %v", found, want)
				}
			}
			check(true)
			// Keep message timestamps equal to exercise ordering within a clock tick.
			switch event {
			case "busy", "busy then idle", "activity on status update":
				c.t = c.t.Add(10 * time.Hour)
				in := protocol.SessionInfo{Harness: "claude", NativeID: "na", Status: "busy"}
				if event == "activity on status update" {
					in.Status = "idle"
					in.LastActive = c.t.Unix()
				}
				if _, err := s.UpsertSession(mustNode(t, s, a), in); err != nil {
					t.Fatal(err)
				}
				if event == "busy then idle" {
					in.Status = "idle"
					if _, err := s.UpsertSession(mustNode(t, s, a), in); err != nil {
						t.Fatal(err)
					}
				}
			case "send notification", "send request", "send reply":
				node, _, err := s.CreateNode("third", "linux")
				if err != nil {
					t.Fatal(err)
				}
				other, err := s.UpsertSession(node, protocol.SessionInfo{Harness: "pi", NativeID: "third"})
				if err != nil {
					t.Fatal(err)
				}
				kind := map[string]string{"send notification": protocol.TypeNotification, "send request": protocol.TypeRequest, "send reply": protocol.TypeReply}[event]
				mustSend(a, other, kind, "next")
			case "peer reply in another conversation":
				m := mustSend(b, a, protocol.TypeReply, "other conversation")
				if m.ConversationID == req.ConversationID {
					t.Fatal("reply should have a different conversation")
				}
			case "peer ended":
				if err := s.SetWorkerSessionEnded(mustNode(t, s, b), b); err != nil {
					t.Fatal(err)
				}
			case "peer archived":
				if err := s.SetSessionOffline(mustNode(t, s, b), b); err != nil {
					t.Fatal(err)
				}
				if _, err := s.ArchiveSessions(protocol.SessionsArchiveRequest{IDs: []string{b}}); err != nil {
					t.Fatal(err)
				}
			}
			check(false)
			if event == "busy then idle" || event == "send notification" || event == "send request" {
				agents, err := s.Agents(protocol.QueryAgents{})
				if err != nil {
					t.Fatal(err)
				}
				for _, agent := range agents {
					if agent.SessionID == b && (len(agent.Pending) != 1 || agent.Pending[0].MessageID != req.ID || agent.Pending[0].Outgoing) {
						t.Fatalf("recipient still owes the original reply: %+v", agent.Pending)
					}
					if event == "send request" && agent.SessionID == a && (len(agent.Pending) != 1 || !agent.Pending[0].Outgoing) {
						t.Fatalf("new request should still wait: %+v", agent.Pending)
					}
				}
			}
		})
	}
}

func TestPendingWaitSurvivesUnrelatedReplyAndDisconnect(t *testing.T) {
	s, _ := newTestStore(t)
	a, b := twoSessions(t, s)
	// An older reply from the awaited peer must not answer a newer request.
	for _, p := range []protocol.MsgSend{
		{Sender: b, Recipient: a, Body: "old reply", IdempotencyKey: "old", Type: protocol.TypeReply},
		{Sender: a, Recipient: b, Body: "new request", IdempotencyKey: "new", Type: protocol.TypeRequest},
		{Sender: b, Recipient: a, Body: "notice", IdempotencyKey: "notice", Type: protocol.TypeNotification},
	} {
		if _, _, err := s.StoreMessage(p); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.SetSessionOffline(mustNode(t, s, b), b); err != nil {
		t.Fatal(err)
	}
	agents, err := s.Agents(protocol.QueryAgents{})
	if err != nil {
		t.Fatal(err)
	}
	for _, agent := range agents {
		if agent.SessionID == a && (len(agent.Pending) != 1 || !agent.Pending[0].Outgoing) {
			t.Fatalf("wait lost without an answer or an end: %+v", agent.Pending)
		}
	}
}
