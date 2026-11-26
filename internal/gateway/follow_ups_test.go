package gateway

import (
	"strings"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

type followUpAsk struct {
	id, next, session, text, ref string
}

func followUpAsks(t *testing.T, s *Store) []followUpAsk {
	t.Helper()
	rows, err := s.db.Query(`SELECT id, next_who, session_id, text, source_ref FROM inbox_items WHERE source_ref LIKE 'follow-up:%' ORDER BY created_at, id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []followUpAsk
	for rows.Next() {
		var a followUpAsk
		if err := rows.Scan(&a.id, &a.next, &a.session, &a.text, &a.ref); err != nil {
			t.Fatal(err)
		}
		out = append(out, a)
	}
	return out
}

func tickFollowUps(t *testing.T, s *Store) {
	t.Helper()
	if err := s.checkFollowUps(); err != nil {
		t.Fatal(err)
	}
}

func TestFollowUpSendRepliedBeforeTimeRaisesNothing(t *testing.T) {
	s, c, _, a, b, _ := followUpRig(t)
	req, _, err := s.StoreMessage(protocol.MsgSend{Sender: a, Recipient: b, Body: "Review ch_1", Type: protocol.TypeRequest, FollowUpAt: "45m"})
	if err != nil {
		t.Fatal(err)
	}
	c.advance(10 * time.Minute)
	if _, _, err := s.StoreMessage(protocol.MsgSend{Sender: b, Recipient: a, Body: "done", Type: protocol.TypeReply, ParentID: req.ID}); err != nil {
		t.Fatal(err)
	}
	c.advance(time.Hour)
	tickFollowUps(t, s)
	if got := followUpAsks(t, s); len(got) != 0 {
		t.Fatalf("raised after a reply: %+v", got)
	}
}

func TestFollowUpSendProgressReplyCountsAsReply(t *testing.T) {
	s, c, _, a, b, _ := followUpRig(t)
	req, _, err := s.StoreMessage(protocol.MsgSend{Sender: a, Recipient: b, Body: "Review ch_1", Type: protocol.TypeRequest, FollowUpAt: "45m"})
	if err != nil {
		t.Fatal(err)
	}
	c.advance(10 * time.Minute)
	if _, _, err := s.StoreMessage(protocol.MsgSend{Sender: b, Recipient: a, Body: "working", Type: protocol.TypeReply, ParentID: req.ID, Progress: true}); err != nil {
		t.Fatal(err)
	}
	c.advance(time.Hour)
	tickFollowUps(t, s)
	if got := followUpAsks(t, s); len(got) != 0 {
		t.Fatalf("a reply in the thread, progress or final, is the expected event: %+v", got)
	}
}

func TestFollowUpSendWithoutFollowUpAtStoresNothing(t *testing.T) {
	s, c, _, a, b, _ := followUpRig(t)
	if _, _, err := s.StoreMessage(protocol.MsgSend{Sender: a, Recipient: b, Body: "Review ch_1", Type: protocol.TypeRequest}); err != nil {
		t.Fatal(err)
	}
	c.advance(24 * time.Hour)
	tickFollowUps(t, s)
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM follow_ups`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("follow_ups rows = %d, %v", n, err)
	}
}

func TestFollowUpSendBadValueIsRefused(t *testing.T) {
	s, _, _, a, b, _ := followUpRig(t)
	_, _, err := s.StoreMessage(protocol.MsgSend{Sender: a, Recipient: b, Body: "x", Type: protocol.TypeRequest, FollowUpAt: "later"})
	if err == nil || !strings.Contains(err.Error(), "follow_up_at") {
		t.Fatalf("err = %v", err)
	}
}

// followUpRig is a store with a live chief (chief), two more sessions and the
// chief's node. The chief is the first session of twoSessions, so asks from
// the second session reach it.
func followUpRig(t *testing.T) (s *Store, c *clock, chief, a, b, node string) {
	t.Helper()
	s, c, chief, b, node = chiefRig(t)
	n3, _, err := s.CreateNode("h3", "linux")
	if err != nil {
		t.Fatal(err)
	}
	a, err = s.UpsertSession(n3, protocol.SessionInfo{Harness: "claude", NativeID: "nc", Name: "carol"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`UPDATE sessions SET online=1, archived_at=0 WHERE id=?`, a); err != nil {
		t.Fatal(err)
	}
	return s, c, chief, a, b, node
}

// Withdrawing a message ends its pending reply follow-up: the expected reply
// can no longer come, so no ask is raised when the follow-up falls due.
func TestWithdrawEndsThePendingFollowUpOfTheMessage(t *testing.T) {
	s, c, _, a, b, _ := followUpRig(t)
	req, _, err := s.StoreMessage(protocol.MsgSend{Sender: a, Recipient: b, Body: "Review ch_2 and flip it", Type: protocol.TypeRequest, FollowUpAt: "45m"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Withdraw(req.ID, a); err != nil {
		t.Fatal(err)
	}
	c.advance(time.Hour)
	tickFollowUps(t, s)
	if got := followUpAsks(t, s); len(got) != 0 {
		t.Fatalf("a withdrawn message raised a follow-up ask: %+v", got)
	}
}
