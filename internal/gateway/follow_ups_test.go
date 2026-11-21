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

func TestFollowUpSendWithoutReplyRaisesOneChiefAsk(t *testing.T) {
	s, c, chief, a, b, _ := followUpRig(t)
	req, _, err := s.StoreMessage(protocol.MsgSend{Sender: a, Recipient: b, Body: "Review ch_1 and flip it", Type: protocol.TypeRequest, FollowUpAt: "45m"})
	if err != nil {
		t.Fatal(err)
	}
	set := c.t

	c.t = set.Add(44 * time.Minute)
	tickFollowUps(t, s)
	if got := followUpAsks(t, s); len(got) != 0 {
		t.Fatalf("raised before the time: %+v", got)
	}

	c.t = set.Add(46 * time.Minute)
	tickFollowUps(t, s)
	tickFollowUps(t, s)
	got := followUpAsks(t, s)
	if len(got) != 1 {
		t.Fatalf("got %d asks, want exactly 1: %+v", len(got), got)
	}
	if got[0].next != protocol.AskNextChief {
		t.Errorf("next = %q, want chief", got[0].next)
	}
	if got[0].session != a {
		t.Errorf("asker = %q, want the sender %q", got[0].session, a)
	}
	for _, want := range []string{req.ID, "bob", b, "reply", set.UTC().Format(time.RFC3339), set.Add(45 * time.Minute).UTC().Format(time.RFC3339)} {
		if !strings.Contains(got[0].text, want) {
			t.Errorf("ask text %q lacks %q", got[0].text, want)
		}
	}
	_ = chief
}

func TestFollowUpSendRepliedBeforeTimeRaisesNothing(t *testing.T) {
	s, c, _, a, b, _ := followUpRig(t)
	req, _, err := s.StoreMessage(protocol.MsgSend{Sender: a, Recipient: b, Body: "Review ch_1", Type: protocol.TypeRequest, FollowUpAt: "45m"})
	if err != nil {
		t.Fatal(err)
	}
	c.t = c.t.Add(10 * time.Minute)
	if _, _, err := s.StoreMessage(protocol.MsgSend{Sender: b, Recipient: a, Body: "done", Type: protocol.TypeReply, ParentID: req.ID}); err != nil {
		t.Fatal(err)
	}
	c.t = c.t.Add(time.Hour)
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
	c.t = c.t.Add(10 * time.Minute)
	if _, _, err := s.StoreMessage(protocol.MsgSend{Sender: b, Recipient: a, Body: "working", Type: protocol.TypeReply, ParentID: req.ID, Progress: true}); err != nil {
		t.Fatal(err)
	}
	c.t = c.t.Add(time.Hour)
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
	c.t = c.t.Add(24 * time.Hour)
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

func TestFollowUpSortChangeMovedRaisesNothing(t *testing.T) {
	r := newDeliveryRig(t)
	chief, node := followUpChief(t, r)
	ask := chiefAskFrom(t, r.s, r.caller, "question", "Who flips ch?", protocol.AskFromSession)
	ch := r.submit(r.a, r.workerSubmit("agent-net/w_1", commitA)).Change.Change
	_, _, err := r.s.SortAsk(node, protocol.AskSortRequest{SessionID: chief, AskSortArgs: protocol.AskSortArgs{
		Ask: ask, Result: protocol.AskSortClose, Reason: "the lead reviews, then it flips", FollowUpAt: "45m", Change: ch.ID}})
	if err != nil {
		t.Fatal(err)
	}
	r.clk.t = r.clk.t.Add(10 * time.Minute)
	r.mustReport(r.a, protocol.StepReportRequest{SessionID: r.caller, ChangeID: ch.ID, StepRef: stepAccept, Result: protocol.StepResultPass, Commit: commitA})
	r.clk.t = r.clk.t.Add(time.Hour)
	tickFollowUps(t, r.s)
	if got := followUpAsks(t, r.s); len(got) != 0 {
		t.Fatalf("raised although the step moved: %+v", got)
	}
}

func TestFollowUpSortChangeNotMovedRaisesOneAsk(t *testing.T) {
	r := newDeliveryRig(t)
	chief, node := followUpChief(t, r)
	ask := chiefAskFrom(t, r.s, r.caller, "question", "Who flips ch?", protocol.AskFromSession)
	ch := r.submit(r.a, r.workerSubmit("agent-net/w_1", commitA)).Change.Change
	set := r.clk.t
	_, _, err := r.s.SortAsk(node, protocol.AskSortRequest{SessionID: chief, AskSortArgs: protocol.AskSortArgs{
		Ask: ask, Result: protocol.AskSortClose, Reason: "the lead reviews, then it flips", FollowUpAt: "45m", Change: ch.ID}})
	if err != nil {
		t.Fatal(err)
	}
	r.clk.t = set.Add(time.Hour)
	tickFollowUps(t, r.s)
	tickFollowUps(t, r.s)
	got := followUpAsks(t, r.s)
	if len(got) != 1 {
		t.Fatalf("got %d asks, want 1: %+v", len(got), got)
	}
	if got[0].next != protocol.AskNextChief {
		t.Errorf("next = %q", got[0].next)
	}
	for _, want := range []string{ch.ID, "step", set.UTC().Format(time.RFC3339)} {
		if !strings.Contains(got[0].text, want) {
			t.Errorf("ask text %q lacks %q", got[0].text, want)
		}
	}
}

func TestFollowUpSortUsesTheChangeTheAskIsAbout(t *testing.T) {
	r := newDeliveryRig(t)
	chief, node := followUpChief(t, r)
	ch := r.submit(r.a, r.workerSubmit("agent-net/w_1", commitA)).Change.Change
	item, err := r.s.CreateInboxItem(protocol.InboxItem{Kind: "question", SessionID: r.caller, Node: "demo", Text: "About the change",
		FromKind: protocol.AskFromSession, AboutKind: protocol.AskAboutChange, AboutID: ch.ID})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := r.s.SortAsk(node, protocol.AskSortRequest{SessionID: chief, AskSortArgs: protocol.AskSortArgs{
		Ask: item.ID, Result: protocol.AskSortClose, Reason: "wait for it", FollowUpAt: "45m"}}); err != nil {
		t.Fatal(err)
	}
	r.clk.t = r.clk.t.Add(time.Hour)
	tickFollowUps(t, r.s)
	got := followUpAsks(t, r.s)
	if len(got) != 1 || !strings.Contains(got[0].text, ch.ID) {
		t.Fatalf("asks %+v", got)
	}
}

func TestFollowUpSortWithoutChangeWatchesTheConversation(t *testing.T) {
	s, c, chief, a, b, node := followUpRig(t)
	req, _, err := s.StoreMessage(protocol.MsgSend{Sender: a, Recipient: b, Body: "Please look", Type: protocol.TypeRequest})
	if err != nil {
		t.Fatal(err)
	}
	item, err := s.CreateInboxItem(protocol.InboxItem{Kind: "question", SessionID: a, Node: "demo", Text: "Waiting on bob", MessageID: req.ID,
		ConversationID: req.ConversationID, FromKind: protocol.AskFromSession, AboutKind: protocol.AskAboutProject, AboutID: "demo"})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.SortAsk(node, protocol.AskSortRequest{SessionID: chief, AskSortArgs: protocol.AskSortArgs{
		Ask: item.ID, Result: protocol.AskSortClose, Reason: "bob will answer", FollowUpAt: "30m"}}); err != nil {
		t.Fatal(err)
	}
	set := c.t
	c.t = set.Add(40 * time.Minute)
	tickFollowUps(t, s)
	if got := followUpAsks(t, s); len(got) != 1 {
		t.Fatalf("no later message in the conversation: want 1 ask, got %+v", got)
	}
}

func TestFollowUpSortConversationMessageRaisesNothing(t *testing.T) {
	s, c, chief, a, b, node := followUpRig(t)
	req, _, err := s.StoreMessage(protocol.MsgSend{Sender: a, Recipient: b, Body: "Please look", Type: protocol.TypeRequest})
	if err != nil {
		t.Fatal(err)
	}
	item, err := s.CreateInboxItem(protocol.InboxItem{Kind: "question", SessionID: a, Node: "demo", Text: "Waiting on bob", MessageID: req.ID,
		ConversationID: req.ConversationID, FromKind: protocol.AskFromSession, AboutKind: protocol.AskAboutProject, AboutID: "demo"})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.SortAsk(node, protocol.AskSortRequest{SessionID: chief, AskSortArgs: protocol.AskSortArgs{
		Ask: item.ID, Result: protocol.AskSortClose, Reason: "bob will answer", FollowUpAt: "30m"}}); err != nil {
		t.Fatal(err)
	}
	c.t = c.t.Add(10 * time.Minute)
	if _, _, err := s.StoreMessage(protocol.MsgSend{Sender: b, Recipient: a, Body: "on it", Type: protocol.TypeReply, ParentID: req.ID, Progress: true}); err != nil {
		t.Fatal(err)
	}
	c.t = c.t.Add(time.Hour)
	tickFollowUps(t, s)
	if got := followUpAsks(t, s); len(got) != 0 {
		t.Fatalf("raised after a later message: %+v", got)
	}
}

func TestFollowUpSortSendStoresFollowUp(t *testing.T) {
	s, c, chief, a, b, node := followUpRig(t)
	item, err := s.CreateInboxItem(protocol.InboxItem{Kind: "question", SessionID: a, Node: "demo", Text: "Who decides?",
		FromKind: protocol.AskFromSession, AboutKind: protocol.AskAboutProject, AboutID: "demo"})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.SortAsk(node, protocol.AskSortRequest{SessionID: chief, AskSortArgs: protocol.AskSortArgs{
		Ask: item.ID, Result: protocol.AskSortSend, To: b, Reason: "bob owns it", FollowUpAt: "30m"}}); err != nil {
		t.Fatal(err)
	}
	c.t = c.t.Add(40 * time.Minute)
	tickFollowUps(t, s)
	if got := followUpAsks(t, s); len(got) != 1 {
		t.Fatalf("want 1 ask, got %+v", got)
	}
}

func TestFollowUpSortWithoutFollowUpAtStoresNothing(t *testing.T) {
	s, _, chief, a, _, node := followUpRig(t)
	item, err := s.CreateInboxItem(protocol.InboxItem{Kind: "question", SessionID: a, Node: "demo", Text: "Who decides?",
		FromKind: protocol.AskFromSession, AboutKind: protocol.AskAboutProject, AboutID: "demo"})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.SortAsk(node, protocol.AskSortRequest{SessionID: chief, AskSortArgs: protocol.AskSortArgs{
		Ask: item.ID, Result: protocol.AskSortClose, Reason: "done"}}); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM follow_ups`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("follow_ups rows = %d, %v", n, err)
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

// followUpChief makes a live chief for a delivery rig and returns it with the
// node it runs on.
func followUpChief(t *testing.T, r *deliveryRig) (chief, node string) {
	t.Helper()
	chief = r.reviewer
	if _, err := r.s.db.Exec(`UPDATE sessions SET online=1, archived_at=0, worker='' WHERE id=?`, chief); err != nil {
		t.Fatal(err)
	}
	if err := r.s.SetChief(protocol.ChiefSetRequest{SessionID: chief}, "owner"); err != nil {
		t.Fatal(err)
	}
	if err := r.s.db.QueryRow(`SELECT node_id FROM sessions WHERE id=?`, chief).Scan(&node); err != nil {
		t.Fatal(err)
	}
	return chief, node
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
	c.t = c.t.Add(time.Hour)
	tickFollowUps(t, s)
	if got := followUpAsks(t, s); len(got) != 0 {
		t.Fatalf("a withdrawn message raised a follow-up ask: %+v", got)
	}
}
