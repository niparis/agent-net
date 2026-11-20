package gateway

import (
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

// A daemon reports the cause of a failed turn with the session. The store keeps
// it, shows it in the agent view, and drops it when the daemon reports none.
func TestSessionKeepsTheCauseOfAFailedTurn(t *testing.T) {
	s, c := newTestStore(t)
	_, b := twoSessions(t, s)
	var node string
	if err := s.db.QueryRow(`SELECT node_id FROM sessions WHERE id=?`, b).Scan(&node); err != nil {
		t.Fatal(err)
	}
	at := c.t.Unix()
	info := protocol.SessionInfo{Harness: "pi", NativeID: "nb", Name: "bob", TurnError: "blocked by our safety systems", TurnErrorAt: at}
	if _, err := s.UpsertSession(node, info); err != nil {
		t.Fatal(err)
	}
	if v, _, _ := s.Agent(b); v.TurnError != "blocked by our safety systems" || v.TurnErrorAt != at || v.LimitedUntil != 0 {
		t.Fatalf("view = %+v", v)
	}
	info.TurnError, info.TurnErrorAt = "", 0
	if _, err := s.UpsertSession(node, info); err != nil {
		t.Fatal(err)
	}
	if v, _, _ := s.Agent(b); v.TurnError != "" || v.TurnErrorAt != 0 {
		t.Fatalf("failure kept: %+v", v)
	}
}

// A message that reached its session but ran no turn gets one notice for its
// sender, and keeps its state.
func TestNotDeliveredTellsTheSenderOnce(t *testing.T) {
	s, _ := newTestStore(t)
	a, b := twoSessions(t, s)
	m, _, err := s.StoreMessage(protocol.MsgSend{Sender: a, Recipient: b, Body: "please run the check", Type: protocol.TypeRequest})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.ApplyDeliveryReport(m.ID, protocol.StateDelivered, ""); err != nil {
		t.Fatal(err)
	}
	notice, noticed, err := s.NotDelivered(m.ID, "the session's turn failed: blocked")
	if err != nil || !noticed {
		t.Fatalf("notice = %+v %v %v", notice, noticed, err)
	}
	if notice.To != a || !strings.Contains(notice.Body, "was not delivered: the session's turn failed: blocked.") {
		t.Fatalf("notice = %+v", notice)
	}
	if _, noticed, err := s.NotDelivered(m.ID, "the session's turn failed: blocked"); err != nil || noticed {
		t.Fatalf("second notice: %v %v", noticed, err)
	}
	if cur, _, _ := s.Message(m.ID); cur.State != protocol.StateDelivered {
		t.Fatalf("state = %s, want delivered", cur.State)
	}
}
