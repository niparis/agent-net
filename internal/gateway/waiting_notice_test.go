package gateway

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// waitingNotices lists agent-net's waiting notices addressed to a session.
func waitingNotices(t *testing.T, s *Store, to string) []protocol.Message {
	t.Helper()
	rows, err := s.db.Query(msgSelect+` WHERE m.sender='agent-net' AND m.recipient=? AND m.idem_key LIKE 'waiting:%' ORDER BY m.rowid`, to)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []protocol.Message
	for rows.Next() {
		m, err := scanMessage(rows)
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, m)
	}
	return out
}

// setWait stores bob's wait as his daemon reports it with a session sync.
func setWait(t *testing.T, s *Store, session string, w *protocol.Waiting) {
	t.Helper()
	node := mustNode(t, s, session)
	if _, err := s.UpsertSession(node, protocol.SessionInfo{Harness: "pi", NativeID: "nb", Name: "bob", Waiting: w}); err != nil {
		t.Fatal(err)
	}
}

// deliver sends a request and reports it delivered, as bob's daemon does.
func deliver(t *testing.T, s *Store, from, to, key string) (protocol.Message, protocol.Message, bool) {
	t.Helper()
	m := sendBody(t, s, from, to, key, "please review the branch")
	notice, noticed, err := s.ApplyDeliveryReport(m.ID, protocol.StateDelivered, "")
	if err != nil {
		t.Fatal(err)
	}
	return m, notice, noticed
}

// thirdSession is carol, another agent on her own machine.
func thirdSession(t *testing.T, s *Store) string {
	t.Helper()
	n, _, err := s.CreateNode("h3", "linux")
	if err != nil {
		t.Fatal(err)
	}
	c, err := s.UpsertSession(n, protocol.SessionInfo{Harness: "codex", NativeID: "nc", Name: "carol"})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestWaitingNoticeOncePerSenderPerWait(t *testing.T) {
	s, c := newTestStore(t)
	a, b := twoSessions(t, s)
	carol := thirdSession(t, s)
	since := c.t.Add(-3 * time.Minute).Unix()
	setWait(t, s, b, &protocol.Waiting{Kind: protocol.WaitingQuestion, Text: "OWNER_ONLY merge branch w_1 into main?", Since: since})

	m, notice, noticed := deliver(t, s, a, b, "first")
	if !noticed {
		t.Fatal("a request to a waiting session got no notice")
	}
	// The question is for the owner only: the notice never quotes it.
	want := "[agent-net] bob is waiting for its user's answer (since 2026-10-28 11:57 UTC). " +
		"Your message was delivered and will be read when the user answers. No reply is needed."
	if notice.Body != want {
		t.Fatalf("notice body:\n got %q\nwant %q", notice.Body, want)
	}
	if notice.From != "agent-net" || notice.To != a || notice.Type != protocol.TypeNotification ||
		notice.ConversationID != m.ConversationID || notice.ParentID != m.ID || notice.State != protocol.StateStored {
		t.Fatalf("notice fields: %+v", notice)
	}
	if got := waitingNotices(t, s, a); len(got) != 1 || got[0].ID != notice.ID {
		t.Fatalf("stored notices: %+v", got)
	}
	// The notice is queued for the sender like any message, not held back
	// as a reminder.
	pending, err := s.Pending(mustNode(t, s, a), false)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 1 || pending[0].ID != notice.ID {
		t.Fatalf("pending for sender: %+v", pending)
	}
	// The original request was delivered normally.
	if got, _, _ := s.Message(m.ID); got.State != protocol.StateDelivered {
		t.Fatalf("request state: %s", got.State)
	}

	// A second request from the same sender in the same wait: no notice.
	c.t = c.t.Add(time.Minute)
	if _, _, noticed := deliver(t, s, a, b, "second"); noticed {
		t.Fatal("second request from the same sender got a notice")
	}
	if got := waitingNotices(t, s, a); len(got) != 1 {
		t.Fatalf("want one notice for a, got %d", len(got))
	}
	// A report about the same message again adds nothing.
	if _, noticed, err := s.ApplyDeliveryReport(m.ID, protocol.StateDelivered, ""); err != nil || noticed {
		t.Fatalf("repeated report: noticed=%v err=%v", noticed, err)
	}

	// A different sender gets its own notice.
	if _, n, noticed := deliver(t, s, carol, b, "carol"); !noticed || n.To != carol {
		t.Fatalf("carol: noticed=%v %+v", noticed, n)
	}

	// Notifications and replies never get one.
	note, _, err := s.StoreMessage(protocol.MsgSend{Sender: carol, Recipient: b, Type: protocol.TypeNotification, Body: "fyi", IdempotencyKey: "fyi"})
	if err != nil {
		t.Fatal(err)
	}
	if _, noticed, err := s.ApplyDeliveryReport(note.ID, protocol.StateDelivered, ""); err != nil || noticed {
		t.Fatalf("notification: noticed=%v err=%v", noticed, err)
	}
	ask, _, err := s.StoreMessage(protocol.MsgSend{Sender: b, Recipient: a, Body: "question for alice", IdempotencyKey: "ask"})
	if err != nil {
		t.Fatal(err)
	}
	reply, _, err := s.StoreMessage(protocol.MsgSend{Sender: a, Recipient: b, ParentID: ask.ID, Body: "answer", IdempotencyKey: "answer"})
	if err != nil {
		t.Fatal(err)
	}
	if _, noticed, err := s.ApplyDeliveryReport(reply.ID, protocol.StateDelivered, ""); err != nil || noticed {
		t.Fatalf("reply: noticed=%v err=%v", noticed, err)
	}
	if got := waitingNotices(t, s, carol); len(got) != 1 {
		t.Fatalf("carol notices: %d", len(got))
	}
}

func TestWaitingNoticeStopsWhenTheWaitEnds(t *testing.T) {
	s, c := newTestStore(t)
	a, b := twoSessions(t, s)
	setWait(t, s, b, &protocol.Waiting{Kind: protocol.WaitingPermission, Text: "Bash(git push origin main)", Since: c.t.Unix()})
	_, n, noticed := deliver(t, s, a, b, "one")
	if !noticed || !strings.HasPrefix(n.Body, "[agent-net] bob is waiting for its user's approval (since 2026-10-28 12:00 UTC).") ||
		strings.Contains(n.Body, "git push") {
		t.Fatalf("permission wait notice: %v %q", noticed, n.Body)
	}

	// The user answered: no more notices.
	setWait(t, s, b, nil)
	c.t = c.t.Add(time.Minute)
	if _, _, noticed := deliver(t, s, a, b, "two"); noticed {
		t.Fatal("a session that no longer waits sent a notice")
	}

	// A new wait is a new period: the same sender hears once more.
	c.t = c.t.Add(time.Minute)
	setWait(t, s, b, &protocol.Waiting{Kind: protocol.WaitingQuestion, Text: "Deploy now?", Since: c.t.Unix()})
	if _, n, noticed := deliver(t, s, a, b, "three"); !noticed || !strings.Contains(n.Body, "(since 2026-10-28 12:02 UTC)") {
		t.Fatalf("new wait: %v %q", noticed, n.Body)
	}

	// An offline session's stale wait does not answer for it.
	if err := s.SetSessionOffline(mustNode(t, s, b), b); err != nil {
		t.Fatal(err)
	}
	carol := thirdSession(t, s)
	if _, _, noticed := deliver(t, s, carol, b, "offline"); noticed {
		t.Fatal("offline session sent a notice")
	}
}

func TestWaitingNoticeWithoutQuestionText(t *testing.T) {
	s, c := newTestStore(t)
	a, b := twoSessions(t, s)
	setWait(t, s, b, &protocol.Waiting{Kind: protocol.WaitingInput, Since: c.t.Unix()})
	_, n, noticed := deliver(t, s, a, b, "bare")
	want := "[agent-net] bob is waiting for its user's answer (since 2026-10-28 12:00 UTC). " +
		"Your message was delivered and will be read when the user answers. No reply is needed."
	if !noticed || n.Body != want {
		t.Fatalf("bare wait notice: %v\n got %q\nwant %q", noticed, n.Body, want)
	}
}

func TestWaitingNoticeNeverForOwnerBotOrAgentNet(t *testing.T) {
	g, app, _, c := botFixture(t)
	s := g.store
	a, b := twoSessions(t, s)
	setWait(t, s, b, &protocol.Waiting{Kind: protocol.WaitingQuestion, Text: "Merge?", Since: c.t.Unix()})

	owner, err := s.UpsertSession(mustNode(t, s, a), protocol.SessionInfo{Harness: protocol.OwnerHarness, NativeID: protocol.OwnerNativeID})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, noticed := deliver(t, s, owner, b, "owner"); noticed {
		t.Fatal("the owner got a waiting notice")
	}

	bot, err := s.touchBot(app)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, noticed := deliver(t, s, bot, b, "bot"); noticed {
		t.Fatal("a bot got a stored waiting notice")
	}

	// agent-net's own notice to a waiting session never makes another one.
	m := sendBody(t, s, a, b, "fails", "hi")
	failure, noticed, err := s.ApplyDeliveryReport(m.ID, protocol.StateFailed, "plugin refused: busy")
	if err != nil || !noticed || failure.To != a {
		t.Fatalf("undelivered notice: %v %v %+v", noticed, err, failure)
	}
	if _, err := s.UpsertSession(mustNode(t, s, a), protocol.SessionInfo{Harness: "claude", NativeID: "na", Name: "alice",
		Waiting: &protocol.Waiting{Kind: protocol.WaitingQuestion, Text: "Ship it?", Since: c.t.Unix()}}); err != nil {
		t.Fatal(err)
	}
	if _, noticed, err := s.ApplyDeliveryReport(failure.ID, protocol.StateDelivered, ""); err != nil || noticed {
		t.Fatalf("agent-net notice: noticed=%v err=%v", noticed, err)
	}
	var total int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM messages WHERE sender='agent-net' AND idem_key LIKE 'waiting:%'`).Scan(&total); err != nil {
		t.Fatal(err)
	}
	if total != 0 {
		t.Fatalf("want no waiting notices, got %d", total)
	}
}

// A bot reads no queue of its own: the send receipt tells it at once.
func TestBotReceiptSaysTheRecipientWaits(t *testing.T) {
	g, app, _, c := botFixture(t)
	_, b := twoSessions(t, g.store)
	sid, err := g.store.touchBot(app)
	if err != nil {
		t.Fatal(err)
	}
	out, err := g.botToolCall(t.Context(), sid, "agent_message_send", map[string]any{"to": b, "body": "status?"})
	if err != nil {
		t.Fatal(err)
	}
	checkOutput(t, "agent_message_send", out)
	if note := out.(map[string]any)["sent"].([]botReceipt)[0].Note; note != "" {
		t.Fatalf("receipt note without a wait: %q", note)
	}
	setWait(t, g.store, b, &protocol.Waiting{Kind: protocol.WaitingQuestion, Text: "OWNER_ONLY merge now?", Since: c.t.Unix()})
	out, err = g.botToolCall(t.Context(), sid, "agent_message_send", map[string]any{"to": b, "body": "status again?"})
	if err != nil {
		t.Fatal(err)
	}
	checkOutput(t, "agent_message_send", out)
	note := out.(map[string]any)["sent"].([]botReceipt)[0].Note
	if !strings.HasPrefix(note, "bob is waiting for its user's answer (since 2026-10-28 12:00 UTC). Your message was sent") ||
		!strings.Contains(note, "will be read when the user answers") || strings.Contains(note, "OWNER_ONLY") {
		t.Fatalf("receipt note: %q", note)
	}
}

func TestWaitingNoticeNotResentAfterRestart(t *testing.T) {
	s, c := newTestStore(t)
	a, b := twoSessions(t, s)
	setWait(t, s, b, &protocol.Waiting{Kind: protocol.WaitingQuestion, Text: "Merge?", Since: c.t.Unix()})
	if _, _, noticed := deliver(t, s, a, b, "before"); !noticed {
		t.Fatal("no first notice")
	}
	var path string
	if err := s.db.QueryRow(`PRAGMA database_list`).Scan(new(int), new(string), &path); err != nil {
		t.Fatal(err)
	}
	s.Close()
	other, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	other.now = c.now
	other.tempFolders = []string{}
	// The gateway restarted and the node synced again: the wait is kept.
	if err := other.SetAllOffline(); err != nil {
		t.Fatal(err)
	}
	c.t = c.t.Add(time.Minute)
	setWait(t, other, b, &protocol.Waiting{Kind: protocol.WaitingQuestion, Text: "Merge?", Since: c.t.Unix()})
	if _, _, noticed := deliver(t, other, a, b, "after"); noticed {
		t.Fatal("a restart sent the same sender a second notice for the same wait")
	}
	if got := waitingNotices(t, other, a); len(got) != 1 {
		t.Fatalf("notices after restart: %d", len(got))
	}
}

// A daemon that restarts reports the same wait with a new start time. The
// gateway keeps the first one, so the wait is one period.
func TestSessionSyncKeepsTheStartOfTheSameWait(t *testing.T) {
	s, c := newTestStore(t)
	_, b := twoSessions(t, s)
	first := c.t.Add(-10 * time.Minute).Unix()
	setWait(t, s, b, &protocol.Waiting{Kind: protocol.WaitingQuestion, Text: "Merge?", Since: first})
	setWait(t, s, b, &protocol.Waiting{Kind: protocol.WaitingQuestion, Text: "Merge?", Since: c.t.Unix()})
	got, _, err := s.Agent(b)
	if err != nil || got.Waiting == nil || got.Waiting.Since != first {
		t.Fatalf("same wait: %+v %v", got.Waiting, err)
	}
	setWait(t, s, b, &protocol.Waiting{Kind: protocol.WaitingQuestion, Text: "Deploy?", Since: c.t.Unix()})
	got, _, err = s.Agent(b)
	if err != nil || got.Waiting == nil || got.Waiting.Since != c.t.Unix() {
		t.Fatalf("new question: %+v %v", got.Waiting, err)
	}
}

func TestSessionRowsSayWaitingForItsUser(t *testing.T) {
	s, c := newTestStore(t)
	_, b := twoSessions(t, s)
	setWait(t, s, b, &protocol.Waiting{Kind: protocol.WaitingQuestion, Text: "OWNER_ONLY merge?", Since: c.t.Unix()})
	page, err := s.FindSessions(protocol.SessionsFindRequest{Text: "bob"})
	if err != nil {
		t.Fatal(err)
	}
	checkOutput(t, "agent_sessions_find", page)
	if len(page.Items) != 1 {
		t.Fatalf("rows: %+v", page.Items)
	}
	row := page.Items[0]
	if row.Status != protocol.StatusWaitingForUser || row.Waiting == nil ||
		row.Waiting.Kind != protocol.WaitingQuestion || row.Waiting.Since != "2026-10-28T12:00:00Z" {
		t.Fatalf("waiting row: %+v %+v", row, row.Waiting)
	}
	full, err := s.GetSession(protocol.SessionGetRequest{Session: b})
	if err != nil {
		t.Fatal(err)
	}
	checkOutput(t, "agent_session_get", full)
	if full.Session.Status != protocol.StatusWaitingForUser || full.Session.Waiting == nil {
		t.Fatalf("full row: %+v", full.Session)
	}
	// The question is for the owner only.
	for _, v := range []any{page, full} {
		if raw, _ := json.Marshal(v); strings.Contains(string(raw), "OWNER_ONLY") {
			t.Fatalf("an agent read quotes the question: %s", raw)
		}
	}
	setWait(t, s, b, nil)
	full, err = s.GetSession(protocol.SessionGetRequest{Session: b})
	if err != nil {
		t.Fatal(err)
	}
	if full.Session.Status == protocol.StatusWaitingForUser || full.Session.Waiting != nil {
		t.Fatalf("row after the wait: %+v", full.Session)
	}
}

// The reminder count counts reminders only, not agent-net's other notices.
func TestReminderCountLeavesOutOtherNotices(t *testing.T) {
	s, c := newTestStore(t)
	a, b := twoSessions(t, s)
	setWait(t, s, b, &protocol.Waiting{Kind: protocol.WaitingQuestion, Text: "Merge?", Since: c.t.Unix()})
	if _, _, noticed := deliver(t, s, a, b, "waits"); !noticed {
		t.Fatal("no waiting notice")
	}
	m := sendBody(t, s, a, b, "fails", "hi")
	if _, noticed, err := s.ApplyDeliveryReport(m.ID, protocol.StateFailed, "plugin refused: busy"); err != nil || !noticed {
		t.Fatalf("undelivered notice: %v %v", noticed, err)
	}
	counts, err := s.SessionCounts(24 * time.Hour)
	if err != nil || counts.PendingReminders != 0 {
		t.Fatalf("counts: %+v %v", counts, err)
	}
}
