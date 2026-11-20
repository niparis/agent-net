package gateway

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"strings"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

func TestOwedReplyNudgeAndAskLifecycle(t *testing.T) {
	s, c := newTestStore(t)
	a, b := twoSessions(t, s)

	req, _, err := s.StoreMessage(protocol.MsgSend{Sender: a, Recipient: b, Body: "Fix the gateway stall bug", Type: protocol.TypeRequest})
	if err != nil {
		t.Fatal(err)
	}

	start := c.t
	if _, err := s.db.Exec("UPDATE sessions SET status='idle', last_active=? WHERE id=?", start.UnixNano(), b); err != nil {
		t.Fatal(err)
	}

	// Advance 15 minutes: idle duration reached.
	c.t = start.Add(15 * time.Minute)
	if _, err := s.checkOwedReplies(); err != nil {
		t.Fatal(err)
	}

	// Exactly one nudge message to recipient b.
	var nudges []protocol.Message
	rows, err := s.db.Query(`SELECT id, parent_id, recipient, body, created_at FROM messages WHERE parent_id=? AND sender='agent-net'`, req.ID)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var m protocol.Message
		var at int64
		if err := rows.Scan(&m.ID, &m.ParentID, &m.To, &m.Body, &at); err != nil {
			t.Fatal(err)
		}
		m.CreatedAt = time.Unix(0, at).UTC()
		nudges = append(nudges, m)
	}
	rows.Close()

	if len(nudges) != 1 {
		t.Fatalf("got %d nudges, want 1", len(nudges))
	}
	nudge := nudges[0]
	if nudge.To != b {
		t.Errorf("nudge recipient = %q, want %q", nudge.To, b)
	}
	wantBody := fmt.Sprintf("You still owe alice a final reply to %s (Fix the gateway stall bug). Reply with the result, or hand back.", req.ID)
	if nudge.Body != wantBody {
		t.Errorf("nudge body = %q, want %q", nudge.Body, wantBody)
	}

	// Immediate repeat tick does not send another nudge.
	if _, err := s.checkOwedReplies(); err != nil {
		t.Fatal(err)
	}
	var nudgeCount int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM messages WHERE parent_id=? AND sender='agent-net'`, req.ID).Scan(&nudgeCount); err != nil || nudgeCount != 1 {
		t.Fatalf("nudgeCount after recheck = %d, want 1", nudgeCount)
	}

	// 10 minutes later (25 minutes total): raise one ask routed to the chief.
	c.t = start.Add(25 * time.Minute)
	if _, err := s.checkOwedReplies(); err != nil {
		t.Fatal(err)
	}

	var asks []protocol.InboxItem
	rows, err = s.db.Query(`SELECT id, next_who, text, detail FROM inbox_items WHERE source_ref=?`, "owed-ask:"+req.ID)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var item protocol.InboxItem
		if err := rows.Scan(&item.ID, &item.NextWho, &item.Text, &item.Detail); err != nil {
			t.Fatal(err)
		}
		asks = append(asks, item)
	}
	rows.Close()

	if len(asks) != 1 {
		t.Fatalf("got %d asks, want 1", len(asks))
	}
	ask := asks[0]
	if ask.NextWho != protocol.AskNextChief {
		t.Errorf("ask next_who = %q, want %q", ask.NextWho, protocol.AskNextChief)
	}
	// Check that the ask names sender, recipient, message id, since-time and nudge time.
	for _, want := range []string{a, b, req.ID, start.UTC().Format(time.RFC3339), nudge.CreatedAt.Format(time.RFC3339)} {
		if !strings.Contains(ask.Text, want) {
			t.Errorf("ask text %q missing %q", ask.Text, want)
		}
	}

	// A later tick adds nothing more.
	c.t = start.Add(35 * time.Minute)
	if _, err := s.checkOwedReplies(); err != nil {
		t.Fatal(err)
	}
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM messages WHERE parent_id=? AND sender='agent-net'`, req.ID).Scan(&nudgeCount); err != nil || nudgeCount != 1 {
		t.Fatalf("nudgeCount after later tick = %d, want 1", nudgeCount)
	}
	var askCount int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM inbox_items WHERE source_ref=?`, "owed-ask:"+req.ID).Scan(&askCount); err != nil || askCount != 1 {
		t.Fatalf("askCount after later tick = %d, want 1", askCount)
	}
}

func TestOwedReplyProgressReplyKeepsRequestOpen(t *testing.T) {
	s, _ := newTestStore(t)
	a, b := twoSessions(t, s)

	req, _, err := s.StoreMessage(protocol.MsgSend{Sender: a, Recipient: b, Body: "Do task", Type: protocol.TypeRequest})
	if err != nil {
		t.Fatal(err)
	}

	// Progress reply leaves parent open.
	progReply, _, err := s.StoreMessage(protocol.MsgSend{Sender: b, Recipient: a, ParentID: req.ID, Body: "Progress: 50% done", Progress: true})
	if err != nil {
		t.Fatal(err)
	}
	if progReply.ParentID != req.ID {
		t.Errorf("progress reply parent = %q, want %q", progReply.ParentID, req.ID)
	}

	var state string
	if err := s.db.QueryRow(`SELECT state FROM messages WHERE id=?`, req.ID).Scan(&state); err != nil {
		t.Fatal(err)
	}
	if state == protocol.StateReplied {
		t.Fatalf("request state after progress reply is %q, want open state", state)
	}

	// Final reply closes the parent request.
	_, _, err = s.StoreMessage(protocol.MsgSend{Sender: b, Recipient: a, ParentID: req.ID, Body: "Done: all finished", Progress: false})
	if err != nil {
		t.Fatal(err)
	}

	if err := s.db.QueryRow(`SELECT state FROM messages WHERE id=?`, req.ID).Scan(&state); err != nil {
		t.Fatal(err)
	}
	if state != protocol.StateReplied {
		t.Fatalf("request state after final reply is %q, want %q", state, protocol.StateReplied)
	}
}

func TestOwedReplySkips(t *testing.T) {
	cases := []struct {
		name  string
		setup func(t *testing.T, s *Store, c *clock, a, b, reqID string)
	}{
		{
			name: "busy",
			setup: func(t *testing.T, s *Store, c *clock, a, b, reqID string) {
				if _, err := s.db.Exec("UPDATE sessions SET status='busy' WHERE id=?", b); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "offline_recipient",
			setup: func(t *testing.T, s *Store, c *clock, a, b, reqID string) {
				if _, err := s.db.Exec("UPDATE sessions SET online=0 WHERE id=?", b); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "worker_recipient",
			setup: func(t *testing.T, s *Store, c *clock, a, b, reqID string) {
				workerJSON := `{"id":"w_test","owner":"` + a + `","project":"proj"}`
				if _, err := s.db.Exec("UPDATE sessions SET worker=? WHERE id=?", workerJSON, b); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "waiting_owner_explicit",
			setup: func(t *testing.T, s *Store, c *clock, a, b, reqID string) {
				waitJSON := `{"kind":"permission","since":` + fmt.Sprintf("%d", c.t.Unix()) + `,"text":"allow tool"}`
				if _, err := s.db.Exec("UPDATE sessions SET status='idle', waiting=? WHERE id=?", waitJSON, b); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "waiting_owner_open_ask",
			setup: func(t *testing.T, s *Store, c *clock, a, b, reqID string) {
				if _, err := s.createInboxItem(s.db, protocol.InboxItem{
					Kind:      protocol.AskKindQuestion,
					SessionID: b,
					Text:      "what database to use?",
				}); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "waiting_running_worker",
			setup: func(t *testing.T, s *Store, c *clock, a, b, reqID string) {
				node, _, _ := s.CreateNode("host-w", "linux")
				_, err := s.UpsertSession(node, protocol.SessionInfo{
					Harness:  "claude",
					NativeID: "worker1",
					Status:   "busy",
					Worker:   &protocol.WorkerInfo{ID: "w_run", Owner: b, Project: "demo"},
				})
				if err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "final_reply_before_nudge",
			setup: func(t *testing.T, s *Store, c *clock, a, b, reqID string) {
				if _, _, err := s.StoreMessage(protocol.MsgSend{Sender: b, Recipient: a, ParentID: reqID, Body: "quick final reply"}); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "withdrawn",
			setup: func(t *testing.T, s *Store, c *clock, a, b, reqID string) {
				if _, err := s.Withdraw(reqID, a); err != nil {
					t.Fatal(err)
				}
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s, c := newTestStore(t)
			a, b := twoSessions(t, s)

			req, _, err := s.StoreMessage(protocol.MsgSend{Sender: a, Recipient: b, Body: "Task for skip test", Type: protocol.TypeRequest})
			if err != nil {
				t.Fatal(err)
			}

			start := c.t
			if _, err := s.db.Exec("UPDATE sessions SET status='idle', last_active=? WHERE id=?", start.UnixNano(), b); err != nil {
				t.Fatal(err)
			}

			tc.setup(t, s, c, a, b, req.ID)

			// Advance 15 min
			c.t = start.Add(15 * time.Minute)
			if _, err := s.checkOwedReplies(); err != nil {
				t.Fatal(err)
			}

			var nudges int
			if err := s.db.QueryRow(`SELECT COUNT(*) FROM messages WHERE parent_id=? AND sender='agent-net'`, req.ID).Scan(&nudges); err != nil {
				t.Fatal(err)
			}
			if nudges != 0 {
				t.Fatalf("case %s: got %d nudges, want 0", tc.name, nudges)
			}
		})
	}
}

func TestOwedReplyFinalReplyAfterNudgeStopsAsk(t *testing.T) {
	s, c := newTestStore(t)
	a, b := twoSessions(t, s)

	req, _, err := s.StoreMessage(protocol.MsgSend{Sender: a, Recipient: b, Body: "Task awaiting reply", Type: protocol.TypeRequest})
	if err != nil {
		t.Fatal(err)
	}

	start := c.t
	if _, err := s.db.Exec("UPDATE sessions SET status='idle', last_active=? WHERE id=?", start.UnixNano(), b); err != nil {
		t.Fatal(err)
	}

	// 15 min: nudge fires.
	c.t = start.Add(15 * time.Minute)
	if _, err := s.checkOwedReplies(); err != nil {
		t.Fatal(err)
	}
	var nudges int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM messages WHERE parent_id=? AND sender='agent-net'`, req.ID).Scan(&nudges); err != nil || nudges != 1 {
		t.Fatalf("want 1 nudge, got %d (err: %v)", nudges, err)
	}

	// Recipient sends final reply at 18 minutes.
	c.t = start.Add(18 * time.Minute)
	if _, _, err := s.StoreMessage(protocol.MsgSend{Sender: b, Recipient: a, ParentID: req.ID, Body: "Here is the final answer"}); err != nil {
		t.Fatal(err)
	}

	// Advance to 25 minutes (10 min after nudge).
	c.t = start.Add(25 * time.Minute)
	if _, err := s.checkOwedReplies(); err != nil {
		t.Fatal(err)
	}

	// No ask should have been created because final reply stopped the chain.
	var asks int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM inbox_items WHERE source_ref=?`, "owed-ask:"+req.ID).Scan(&asks); err != nil {
		t.Fatal(err)
	}
	if asks != 0 {
		t.Fatalf("got %d asks after final reply, want 0", asks)
	}
}

func TestOwedReplyRequestCreatedBeforeLiveSinceNeverNudged(t *testing.T) {
	s, c := newTestStore(t)
	a, b := twoSessions(t, s)

	// Old request created at start
	start := c.t
	oldReq, _, err := s.StoreMessage(protocol.MsgSend{Sender: a, Recipient: b, Body: "Old request before live since", Type: protocol.TypeRequest})
	if err != nil {
		t.Fatal(err)
	}

	// Record start time 1 hour after old request
	liveSince := start.Add(time.Hour)
	if _, err := s.db.Exec(`INSERT INTO gateway_state(key,value,updated_at) VALUES('owed_replies_live_since',?,?)
		ON CONFLICT(key) DO UPDATE SET value=excluded.value, updated_at=excluded.updated_at`,
		fmt.Sprintf("%d", liveSince.UnixNano()), liveSince.UnixNano()); err != nil {
		t.Fatal(err)
	}

	// Recipient is idle
	if _, err := s.db.Exec("UPDATE sessions SET status='idle', last_active=? WHERE id=?", start.UnixNano(), b); err != nil {
		t.Fatal(err)
	}

	// Advance clock 20m (past 15m idle threshold for oldReq)
	c.t = start.Add(20 * time.Minute)
	nudges, err := s.checkOwedReplies()
	if err != nil {
		t.Fatal(err)
	}
	if len(nudges) != 0 {
		t.Fatalf("got %d nudges for old request, want 0", len(nudges))
	}
	var nudgeCount int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM messages WHERE parent_id=? AND sender='agent-net'`, oldReq.ID).Scan(&nudgeCount); err != nil || nudgeCount != 0 {
		t.Fatalf("old request nudge count in db = %d, want 0", nudgeCount)
	}

	// Advance clock past liveSince and create new request
	c.t = liveSince.Add(time.Minute)
	newReq, _, err := s.StoreMessage(protocol.MsgSend{Sender: a, Recipient: b, Body: "New request after live since", Type: protocol.TypeRequest})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec("UPDATE sessions SET status='idle', last_active=? WHERE id=?", c.t.UnixNano(), b); err != nil {
		t.Fatal(err)
	}

	// Advance clock 15 min after new request
	c.t = c.t.Add(15 * time.Minute)
	nudges, err = s.checkOwedReplies()
	if err != nil {
		t.Fatal(err)
	}
	if len(nudges) != 1 {
		t.Fatalf("got %d nudges for new request, want 1", len(nudges))
	}
	if nudges[0].ParentID != newReq.ID {
		t.Fatalf("nudge parent = %q, want %q", nudges[0].ParentID, newReq.ID)
	}
}

func TestOwedReplyReplyInSameConversationNoParentCountsAsFinal(t *testing.T) {
	s, c := newTestStore(t)
	a, b := twoSessions(t, s)

	req, _, err := s.StoreMessage(protocol.MsgSend{Sender: a, Recipient: b, Body: "Initial task", Type: protocol.TypeRequest})
	if err != nil {
		t.Fatal(err)
	}

	start := c.t
	if _, err := s.db.Exec("UPDATE sessions SET status='idle', last_active=? WHERE id=?", start.UnixNano(), b); err != nil {
		t.Fatal(err)
	}

	// Recipient b replies in the same conversation with no parent_id (e.g. Claude session SendMessage)
	c.t = start.Add(5 * time.Minute)
	if _, _, err := s.StoreMessage(protocol.MsgSend{
		Sender:         b,
		Recipient:      a,
		ConversationID: req.ConversationID,
		Type:           protocol.TypeReply,
		Body:           "Final answer without parent id",
	}); err != nil {
		t.Fatal(err)
	}

	// Advance to 15 min
	c.t = start.Add(15 * time.Minute)
	nudges, err := s.checkOwedReplies()
	if err != nil {
		t.Fatal(err)
	}
	if len(nudges) != 0 {
		t.Fatalf("got %d nudges after reply with no parent_id, want 0", len(nudges))
	}

	// Also check that if an ask was open, sending a message in the same conversation closes it.
	c.t = start.Add(20 * time.Minute)
	req2, _, err := s.StoreMessage(protocol.MsgSend{Sender: a, Recipient: b, Body: "Second task", Type: protocol.TypeRequest})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec("UPDATE sessions SET status='idle', last_active=? WHERE id=?", c.t.UnixNano(), b); err != nil {
		t.Fatal(err)
	}

	// Advance to nudge time (15m later)
	c.t = c.t.Add(15 * time.Minute)
	if _, err := s.checkOwedReplies(); err != nil {
		t.Fatal(err)
	}

	// Advance to ask time (10m later)
	c.t = c.t.Add(10 * time.Minute)
	if _, err := s.checkOwedReplies(); err != nil {
		t.Fatal(err)
	}

	// Verify ask exists and is open
	var askStatus string
	if err := s.db.QueryRow(`SELECT status FROM inbox_items WHERE source_ref=?`, "owed-ask:"+req2.ID).Scan(&askStatus); err != nil || askStatus != "open" {
		t.Fatalf("ask status = %q (err %v), want open", askStatus, err)
	}

	// Now recipient b sends non-progress reply in same conversation with no parent_id
	c.t = c.t.Add(time.Minute)
	if _, _, err := s.StoreMessage(protocol.MsgSend{
		Sender:         b,
		Recipient:      a,
		ConversationID: req2.ConversationID,
		Type:           protocol.TypeReply,
		Body:           "Late answer closing the ask",
	}); err != nil {
		t.Fatal(err)
	}

	// Verify ask was closed
	if err := s.db.QueryRow(`SELECT status FROM inbox_items WHERE source_ref=?`, "owed-ask:"+req2.ID).Scan(&askStatus); err != nil || askStatus != "closed" {
		t.Fatalf("ask status after reply = %q (err %v), want closed", askStatus, err)
	}
}

func TestOwedReplyNudgeDispatchedOnSameTick(t *testing.T) {
	s, c := newTestStore(t)
	g := &Gateway{store: s, logger: log.New(io.Discard, "", 0), conns: map[string]*nodeConn{}}
	nodeB, _, _ := s.CreateNode("nb", "linux")
	a, _ := s.UpsertSession(nodeB, protocol.SessionInfo{Harness: "claude", NativeID: "na", Name: "alice"})
	b, _ := s.UpsertSession(nodeB, protocol.SessionInfo{Harness: "pi", NativeID: "nb", Name: "bob"})

	delivered := make(chan protocol.Message, 1)
	fakeOwnerNode(t, g, nodeB, func(h protocol.Header, raw []byte) error {
		if h.Type == "msg.deliver" {
			var p protocol.MsgDeliver
			if err := json.Unmarshal(raw, &p); err == nil && p.Message.To == b {
				delivered <- p.Message
			}
		}
		return nil
	})

	req, _, err := s.StoreMessage(protocol.MsgSend{Sender: a, Recipient: b, Body: "Do work", Type: protocol.TypeRequest})
	if err != nil {
		t.Fatal(err)
	}

	start := c.t
	if _, err := s.db.Exec("UPDATE sessions SET status='idle', last_active=? WHERE id=?", start.UnixNano(), b); err != nil {
		t.Fatal(err)
	}

	c.t = start.Add(15 * time.Minute)
	nudges, err := s.checkOwedReplies()
	if err != nil {
		t.Fatal(err)
	}
	if len(nudges) != 1 {
		t.Fatalf("got %d nudges, want 1", len(nudges))
	}

	// Dispatch each nudge on the same tick, as RunExpiry does.
	for _, m := range nudges {
		g.dispatch(t.Context(), m)
	}

	select {
	case m := <-delivered:
		if m.ParentID != req.ID {
			t.Fatalf("delivered message parent = %q, want %q", m.ParentID, req.ID)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("nudge message was not delivered to node")
	}

	// Message state in DB is dispatched or delivered, not left stored.
	var state string
	if err := s.db.QueryRow(`SELECT state FROM messages WHERE parent_id=? AND sender='agent-net'`, req.ID).Scan(&state); err != nil {
		t.Fatal(err)
	}
	if state != protocol.StateDispatched && state != protocol.StateDelivered {
		t.Fatalf("nudge state = %q, want dispatched or delivered", state)
	}
}
