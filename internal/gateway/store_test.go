package gateway

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

type clock struct{ t time.Time }

func (c *clock) now() time.Time { return c.t }

// advance moves the test clock forward, for a machine that stays connected
// while the gateway's silence windows pass.
func (c *clock) advance(d time.Duration) { c.t = c.t.Add(d) }

func newTestStore(t testing.TB) (*Store, *clock) {
	t.Helper()
	s, err := OpenStore(seededStorePath(t, "gw.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	c := &clock{t: time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC)}
	s.now = c.now
	// Test sessions live in the test's own temporary folders. A test that
	// wants the temp rule sets s.tempFolders itself.
	s.tempFolders = []string{}
	return s, c
}

// twoSessions makes two online sessions on two nodes.
// disableInboxLimit turns messages.inbox_limit off for a test that sends many
// peer messages to one recipient to exercise another limit.
func disableInboxLimit(t *testing.T, s *Store) {
	t.Helper()
	if _, err := s.db.Exec(`INSERT INTO setting_values(key,level,scope,item,value,revision)
		VALUES('messages.inbox_limit','network','network','','0',1)`); err != nil {
		t.Fatal(err)
	}
}

func twoSessions(t *testing.T, s *Store) (a, b string) {
	t.Helper()
	n1, _, err := s.CreateNode("h1", "linux")
	if err != nil {
		t.Fatal(err)
	}
	n2, _, _ := s.CreateNode("h2", "linux")
	a, err = s.UpsertSession(n1, protocol.SessionInfo{Harness: "claude", NativeID: "na", Name: "alice"})
	if err != nil {
		t.Fatal(err)
	}
	b, err = s.UpsertSession(n2, protocol.SessionInfo{Harness: "pi", NativeID: "nb", Name: "bob"})
	if err != nil {
		t.Fatal(err)
	}
	return a, b
}

func send(s *Store, from, to, key, parent string) (protocol.Message, bool, error) {
	return s.StoreMessage(protocol.MsgSend{Sender: from, Recipient: to, Body: "hi", IdempotencyKey: key, ParentID: parent})
}

func TestMigrationsFromEmpty(t *testing.T) {
	path := seededStorePath(t, "gw.db")
	s, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	v, _ := s.SchemaVersion()
	if v != len(migrations) {
		t.Fatalf("schema version %d, want %d", v, len(migrations))
	}
	s.Close()
	// Opening again must not re-apply anything.
	s, err = OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if v2, _ := s.SchemaVersion(); v2 != v {
		t.Fatalf("version changed on reopen: %d -> %d", v, v2)
	}
}

func TestIdempotency(t *testing.T) {
	s, _ := newTestStore(t)
	a, b := twoSessions(t, s)
	m1, created, err := send(s, a, b, "k1", "")
	if err != nil || !created {
		t.Fatalf("first send: %v created=%v", err, created)
	}
	m2, created, err := send(s, a, b, "k1", "")
	if err != nil || created {
		t.Fatalf("repeat: %v created=%v", err, created)
	}
	if m1.ID != m2.ID {
		t.Fatalf("repeat returned a new message: %s vs %s", m1.ID, m2.ID)
	}
	all, _ := s.Messages(mustNode(t, s, a), protocol.QueryMessages{})
	if len(all) != 1 {
		t.Fatalf("stored %d messages, want 1", len(all))
	}
}

func TestWorkerIdleNoticeRejectsSupersededTask(t *testing.T) {
	s, _ := newTestStore(t)
	owner, peer := twoSessions(t, s)
	node, err := s.SessionNode(peer)
	if err != nil {
		t.Fatal(err)
	}
	worker, err := s.UpsertSession(node, protocol.SessionInfo{
		Harness: "opencode", NativeID: "worker", Name: "shop worker",
		Worker: &protocol.WorkerInfo{ID: "w_test", Owner: owner},
	})
	if err != nil {
		t.Fatal(err)
	}
	task, _, err := s.StoreMessage(protocol.MsgSend{Sender: owner, Recipient: worker, Body: "first task", IdempotencyKey: "task-1"})
	if err != nil {
		t.Fatal(err)
	}
	firstNotice, created, err := s.StoreMessage(protocol.MsgSend{
		Sender: worker, Recipient: owner, Type: protocol.TypeReply, Body: "idle notice",
		ParentID: task.ID, IdempotencyKey: "idle-1", WorkerIdleNotice: true,
	})
	if err != nil || !created {
		t.Fatalf("first idle notice: %v created=%v", err, created)
	}
	followup, _, err := s.StoreMessage(protocol.MsgSend{Sender: owner, Recipient: worker, Body: "continue", IdempotencyKey: "task-2"})
	if err != nil {
		t.Fatal(err)
	}
	if again, created, err := s.StoreMessage(protocol.MsgSend{
		Sender: worker, Recipient: owner, Type: protocol.TypeReply, Body: "idle notice",
		ParentID: task.ID, IdempotencyKey: "idle-1", WorkerIdleNotice: true,
	}); err != nil || created || again.ID != firstNotice.ID {
		t.Fatalf("same-key retry after follow-up: message=%+v created=%v err=%v", again, created, err)
	}
	if _, _, err := s.StoreMessage(protocol.MsgSend{
		Sender: worker, Recipient: owner, Type: protocol.TypeReply, Body: "stale idle notice",
		ParentID: task.ID, IdempotencyKey: "idle-2", WorkerIdleNotice: true,
	}); err == nil {
		t.Fatal("accepted an idle notice for a superseded task")
	}
	if _, created, err := s.StoreMessage(protocol.MsgSend{
		Sender: worker, Recipient: owner, Type: protocol.TypeReply, Body: "next idle notice",
		ParentID: followup.ID, IdempotencyKey: "idle-3", WorkerIdleNotice: true,
	}); err != nil || !created {
		t.Fatalf("idle notice for current task: created=%v err=%v", created, err)
	}
}

func TestWorkerIdleNoticeSurvivesOwnerReplyFollowup(t *testing.T) {
	s, _ := newTestStore(t)
	owner, peer := twoSessions(t, s)
	node, err := s.SessionNode(peer)
	if err != nil {
		t.Fatal(err)
	}
	worker, err := s.UpsertSession(node, protocol.SessionInfo{
		Harness: "opencode", NativeID: "worker", Name: "shop worker",
		Worker: &protocol.WorkerInfo{ID: "w_test", Owner: owner},
	})
	if err != nil {
		t.Fatal(err)
	}
	task, _, err := s.StoreMessage(protocol.MsgSend{Sender: owner, Recipient: worker, Body: "first task", IdempotencyKey: "task-1"})
	if err != nil {
		t.Fatal(err)
	}
	handback, _, err := s.StoreMessage(protocol.MsgSend{
		Sender: worker, Recipient: owner, Type: protocol.TypeReply, Body: "finished the task",
		ParentID: task.ID, IdempotencyKey: "handback-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.StoreMessage(protocol.MsgSend{
		Sender: owner, Recipient: worker, Type: protocol.TypeReply, Body: "please continue",
		ParentID: handback.ID, IdempotencyKey: "owner-reply-1",
	}); err != nil {
		t.Fatal(err)
	}
	next, created, err := s.StoreMessage(protocol.MsgSend{
		Sender: worker, Recipient: owner, Type: protocol.TypeReply, Body: "idle notice after reply",
		ParentID: task.ID, IdempotencyKey: "idle-2", WorkerIdleNotice: true,
	})
	if err != nil || !created {
		t.Fatalf("idle notice after owner reply: message=%+v created=%v err=%v", next, created, err)
	}
	if again, created, err := s.StoreMessage(protocol.MsgSend{
		Sender: worker, Recipient: owner, Type: protocol.TypeReply, Body: "idle notice after reply",
		ParentID: task.ID, IdempotencyKey: "idle-2", WorkerIdleNotice: true,
	}); err != nil || created || again.ID != next.ID {
		t.Fatalf("idle notice retry after owner reply: message=%+v created=%v err=%v", again, created, err)
	}
}

func mustNode(t *testing.T, s *Store, session string) string {
	n, err := s.SessionNode(session)
	if err != nil || n == "" {
		t.Fatalf("session node: %q %v", n, err)
	}
	return n
}

func TestHopLimit(t *testing.T) {
	s, c := newTestStore(t)
	a, b := twoSessions(t, s)
	m, _, err := send(s, a, b, "k0", "")
	if err != nil {
		t.Fatal(err)
	}
	from, to := b, a
	// Hops 1..8 are fine, hop 9 is refused. Keep under the rate limit.
	for i := 1; i <= protocol.MaxHop; i++ {
		c.t = c.t.Add(time.Minute)
		m, _, err = send(s, from, to, "k"+string(rune('a'+i)), m.ID)
		if err != nil {
			t.Fatalf("hop %d: %v", i, err)
		}
		if m.Hop != i {
			t.Fatalf("hop %d stored as %d", i, m.Hop)
		}
		from, to = to, from
	}
	c.t = c.t.Add(time.Minute)
	_, _, err = send(s, from, to, "kz", m.ID)
	var rj Rejection
	if !errors.As(err, &rj) || !strings.Contains(err.Error(), "hop") {
		t.Fatalf("hop 9: got %v, want hop rejection", err)
	}
}

func TestConversationBudget(t *testing.T) {
	s, c := newTestStore(t)
	a, b := twoSessions(t, s)
	m, _, _ := send(s, a, b, "first", "")
	// Notifications chain on the first message's conversation without growing the hop:
	// reply to the root each time (b -> a), so hop stays 1.
	count := 1
	for ; count < protocol.MaxConvMessages; count++ {
		if count%15 == 0 {
			c.t = c.t.Add(time.Minute)
		}
		_, _, err := send(s, b, a, "r"+string(rune('A'+count/26))+string(rune('a'+count%26)), m.ID)
		if err != nil {
			t.Fatalf("message %d: %v", count+1, err)
		}
	}
	c.t = c.t.Add(time.Minute)
	_, _, err := send(s, b, a, "overflow", m.ID)
	if err == nil || !strings.Contains(err.Error(), "budget") {
		t.Fatalf("51st message: got %v, want budget rejection", err)
	}
}

func TestRateLimit(t *testing.T) {
	s, c := newTestStore(t)
	disableInboxLimit(t, s)
	a, b := twoSessions(t, s)
	for i := 0; i < protocol.RatePerMinute; i++ {
		if _, _, err := send(s, a, b, "k"+string(rune('a'+i)), ""); err != nil {
			t.Fatalf("message %d: %v", i+1, err)
		}
	}
	if _, _, err := send(s, a, b, "extra", ""); err == nil || !strings.Contains(err.Error(), "rate") {
		t.Fatalf("21st message: got %v, want rate limit", err)
	}
	c.t = c.t.Add(10 * time.Second) // refills ~3 tokens
	if _, _, err := send(s, a, b, "later", ""); err != nil {
		t.Fatalf("after refill: %v", err)
	}
}

func TestBodyAndRecipientRules(t *testing.T) {
	s, _ := newTestStore(t)
	a, b := twoSessions(t, s)
	big := protocol.MsgSend{Sender: a, Recipient: b, Body: strings.Repeat("x", protocol.MaxBodyBytes+1)}
	if _, _, err := s.StoreMessage(big); err == nil {
		t.Fatal("65 KiB body accepted")
	}
	big.Body = strings.Repeat("x", protocol.MaxBodyBytes)
	if _, _, err := s.StoreMessage(big); err != nil {
		t.Fatalf("64 KiB body refused: %v", err)
	}
	if _, _, err := send(s, a, "s_nope", "k", ""); err == nil {
		t.Fatal("unknown recipient accepted")
	}
	if _, _, err := send(s, a, a, "k2", ""); err == nil {
		t.Fatal("message to self accepted")
	}
}

func TestExpiry(t *testing.T) {
	s, c := newTestStore(t)
	a, b := twoSessions(t, s)
	m, _, _ := send(s, a, b, "k", "")
	if n, _ := s.Expire(); n != 0 {
		t.Fatalf("expired %d messages too early", n)
	}
	nodeB := mustNode(t, s, b)
	if p, _ := s.Pending(nodeB, true); len(p) != 1 {
		t.Fatalf("pending = %d, want 1", len(p))
	}
	c.t = c.t.Add(protocol.DefaultTTL + time.Second)
	if n, _ := s.Expire(); n != 1 {
		t.Fatalf("expired %d, want 1", n)
	}
	got, _, _ := s.Message(m.ID)
	if got.State != protocol.StateExpired {
		t.Fatalf("state %s, want expired", got.State)
	}
	if p, _ := s.Pending(nodeB, true); len(p) != 0 {
		t.Fatal("expired message is still pending")
	}
}

func TestRepliedOnParent(t *testing.T) {
	s, _ := newTestStore(t)
	a, b := twoSessions(t, s)
	m, _, _ := send(s, a, b, "k", "")
	if err := s.ApplyDelivery(m.ID, protocol.StateDelivered, ""); err != nil {
		t.Fatal(err)
	}
	r, _, err := send(s, b, a, "k2", m.ID)
	if err != nil {
		t.Fatal(err)
	}
	if r.Type != protocol.TypeReply || r.ConversationID != m.ConversationID || r.Hop != 1 {
		t.Fatalf("reply not linked: %+v", r)
	}
	parent, _, _ := s.Message(m.ID)
	if parent.State != protocol.StateReplied {
		t.Fatalf("parent state %s, want replied", parent.State)
	}
	// A late delivery report must not undo replied.
	s.ApplyDelivery(m.ID, protocol.StateDelivered, "")
	parent, _, _ = s.Message(m.ID)
	if parent.State != protocol.StateReplied {
		t.Fatalf("state fell back to %s", parent.State)
	}
	// Only the parent's recipient may reply.
	if _, _, err := send(s, a, b, "k3", m.ID); err == nil {
		t.Fatal("the original sender replied to its own message")
	}
}

func TestEnrollCodeRules(t *testing.T) {
	s, c := newTestStore(t)
	code, err := s.CreateEnrollCode(10 * time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if len(code) != 8 {
		t.Fatalf("code %q is not 8 characters", code)
	}
	if ok, _ := s.ConsumeEnrollCode(code); !ok {
		t.Fatal("fresh code refused")
	}
	if ok, _ := s.ConsumeEnrollCode(code); ok {
		t.Fatal("code worked twice")
	}
	code2, _ := s.CreateEnrollCode(10 * time.Minute)
	c.t = c.t.Add(10*time.Minute + time.Second)
	if ok, _ := s.ConsumeEnrollCode(code2); ok {
		t.Fatal("expired code accepted")
	}
	if ok, _ := s.ConsumeEnrollCode("WRONGONE"); ok {
		t.Fatal("unknown code accepted")
	}
}

func TestMessagesPageTotalAndOffset(t *testing.T) {
	s, _ := newTestStore(t)
	a, b := twoSessions(t, s)
	for _, key := range []string{"page1", "page2", "page3"} {
		if _, _, err := send(s, a, b, key, ""); err != nil {
			t.Fatal(err)
		}
	}
	q := protocol.QueryMessages{SessionID: a, Limit: 2}
	first, total, err := s.MessagesPage(mustNode(t, s, a), q)
	if err != nil || total != 3 || len(first) != 2 {
		t.Fatalf("first: %v %d %v", first, total, err)
	}
	q.Offset = 2
	second, total, err := s.MessagesPage(mustNode(t, s, a), q)
	if err != nil || total != 3 || len(second) != 1 || second[0].ID == first[0].ID {
		t.Fatalf("second: %v %d %v", second, total, err)
	}
}

func TestAgyWaitingDeliveryState(t *testing.T) {
	s, _ := newTestStore(t)
	from, to := twoSessions(t, s)
	m, _, err := send(s, from, to, "wait-key", "")
	if err != nil {
		t.Fatal(err)
	}
	if err = s.ApplyDelivery(m.ID, protocol.StateDispatched, "waiting for the session's next turn"); err != nil {
		t.Fatal(err)
	}
	got, _, err := s.Message(m.ID)
	if err != nil || got.State != protocol.StateDispatched || got.DeliveryDetail != "waiting for the session's next turn" {
		t.Fatalf("waiting: %+v %v", got, err)
	}
	if err = s.ApplyDelivery(m.ID, protocol.StateDelivered, "hook"); err != nil {
		t.Fatal(err)
	}
	if err = s.ApplyDelivery(m.ID, protocol.StateDispatched, "late waiting"); err != nil {
		t.Fatal(err)
	}
	got, _, _ = s.Message(m.ID)
	if got.State != protocol.StateDelivered || got.DeliveryDetail != "hook" {
		t.Fatalf("late update: %+v", got)
	}
}
