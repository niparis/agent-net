package daemon

import (
	"reflect"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

func replyIDs(rs []ClaudeDelivery) []string {
	var out []string
	for _, r := range rs {
		out = append(out, r.MessageID)
	}
	return out
}

// The record of messages delivered to Claude sessions lives in daemon.db: a
// new open of the database (a daemon restart) still finds the open requests,
// newest first, filtered by recipient, sender and conversation (W4.4).
func TestClaudeRepliesSurviveReopen(t *testing.T) {
	dir := t.TempDir()
	st, err := openState(dir)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	rows := []ClaudeDelivery{
		{MessageID: "m_1", Recipient: "s_r", Sender: "s_a", ConversationID: "c_1", Type: protocol.TypeRequest, DeliveredAt: now.Add(-3 * time.Minute)},
		{MessageID: "m_2", Recipient: "s_r", Sender: "s_a", ConversationID: "c_1", Type: protocol.TypeRequest, DeliveredAt: now.Add(-2 * time.Minute)},
		{MessageID: "m_3", Recipient: "s_r", Sender: "s_a", ConversationID: "c_2", Type: protocol.TypeRequest, DeliveredAt: now.Add(-time.Minute)},
		{MessageID: "m_4", Recipient: "s_r", Sender: "s_b", ConversationID: "c_1", Type: protocol.TypeRequest, DeliveredAt: now},
		{MessageID: "m_5", Recipient: "s_r", Sender: "s_a", ConversationID: "c_1", Type: protocol.TypeReply, DeliveredAt: now},
		{MessageID: "m_6", Recipient: "s_other", Sender: "s_a", ConversationID: "c_1", Type: protocol.TypeRequest, DeliveredAt: now},
	}
	for _, r := range rows {
		if err := st.SaveDelivery(r); err != nil {
			t.Fatal(err)
		}
	}
	st.close()

	st, err = openState(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer st.close()
	open, err := st.OpenRequests("s_r", "s_a", "c_1", now)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := replyIDs(open), []string{"m_2", "m_1"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("open requests from s_a in c_1 = %v, want %v (newest first, requests only)", got, want)
	}
	any, err := st.OpenRequests("s_r", "s_a", "", now)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := replyIDs(any), []string{"m_3", "m_2", "m_1"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("open requests from s_a in any conversation = %v, want %v", got, want)
	}

	if err := st.CloseRequests([]string{"m_2"}, ReplyMatchExact, now); err != nil {
		t.Fatal(err)
	}
	open, _ = st.OpenRequests("s_r", "s_a", "c_1", now)
	if got, want := replyIDs(open), []string{"m_1"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("after closing m_2: %v, want %v", got, want)
	}
	var match string
	var answered int64
	if err := st.db.QueryRow(`SELECT answer, answered_at FROM claude_deliveries WHERE message_id='m_2'`).Scan(&match, &answered); err != nil {
		t.Fatal(err)
	}
	if match != ReplyMatchExact || answered == 0 {
		t.Fatalf("closed row answer=%q answered_at=%d", match, answered)
	}

	addrs, err := st.ReplyAddresses()
	if err != nil {
		t.Fatal(err)
	}
	want := []ReplyAddress{{"s_a", "c_1"}, {"s_a", "c_2"}, {"s_b", "c_1"}}
	if !reflect.DeepEqual(addrs, want) {
		t.Fatalf("reply addresses = %v, want %v", addrs, want)
	}

	// A failed write to the inbox forgets the row: Claude never saw it.
	if err := st.ForgetDelivery("m_1"); err != nil {
		t.Fatal(err)
	}
	if open, _ := st.OpenRequests("s_r", "s_a", "c_1", now); len(open) != 0 {
		t.Fatalf("forgotten row still open: %v", replyIDs(open))
	}
}

// A request is open until its message expires; the row is pruned at the
// expiry plus a margin, and its reply address goes with it.
func TestClaudeRepliesExpiryClosesAndPrunes(t *testing.T) {
	st, err := openState(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.close()
	now := time.Now()
	old := ClaudeDelivery{MessageID: "m_old", Recipient: "s_r", Sender: "s_a", ConversationID: "c_1", Type: protocol.TypeRequest,
		DeliveredAt: now.Add(-25 * time.Hour), ExpiresAt: now.Add(-time.Hour)}
	fresh := ClaudeDelivery{MessageID: "m_new", Recipient: "s_r", Sender: "s_a", ConversationID: "c_2", Type: protocol.TypeRequest,
		DeliveredAt: now}
	for _, r := range []ClaudeDelivery{old, fresh} {
		if err := st.SaveDelivery(r); err != nil {
			t.Fatal(err)
		}
	}
	if open, _ := st.OpenRequests("s_r", "s_a", "c_1", now); len(open) != 0 {
		t.Fatalf("an expired request is still open: %v", replyIDs(open))
	}
	// A missing expiry is the message expiry from delivery.
	if open, _ := st.OpenRequests("s_r", "s_a", "c_2", now.Add(protocol.DefaultTTL-time.Minute)); len(open) != 1 {
		t.Fatal("a request without an expiry closed before the message expiry")
	}
	if open, _ := st.OpenRequests("s_r", "s_a", "c_2", now.Add(protocol.DefaultTTL+time.Minute)); len(open) != 0 {
		t.Fatal("a request without an expiry is open after the message expiry")
	}

	// Within the margin the row, and so its reply address, is kept.
	if n, err := st.pruneClaudeDeliveries(now); err != nil || n != 0 {
		t.Fatalf("pruned %d rows (err %v) inside the margin", n, err)
	}
	if n, err := st.pruneClaudeDeliveries(now.Add(claudeReplyMargin + time.Minute)); err != nil || n != 1 {
		t.Fatalf("pruned %d rows (err %v), want the expired one", n, err)
	}
	addrs, _ := st.ReplyAddresses()
	if want := []ReplyAddress{{"s_a", "c_2"}}; !reflect.DeepEqual(addrs, want) {
		t.Fatalf("reply addresses after prune = %v, want %v", addrs, want)
	}
}

// The hourly housekeeping prunes the Claude rows with the delivery records.
func TestHousekeepingPrunesClaudeDeliveries(t *testing.T) {
	st, err := openState(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.close()
	now := time.Now()
	if err := st.SaveDelivery(ClaudeDelivery{MessageID: "m_old", Recipient: "s_r", Sender: "s_a", ConversationID: "c_1",
		Type: protocol.TypeRequest, DeliveredAt: now.Add(-50 * time.Hour)}); err != nil {
		t.Fatal(err)
	}
	d := &Daemon{st: st}
	d.pruneHousekeeping(now)
	if addrs, _ := st.ReplyAddresses(); len(addrs) != 0 {
		t.Fatalf("housekeeping kept %v", addrs)
	}
}
