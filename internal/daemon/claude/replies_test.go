package claude

import (
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/daemon"
	"github.com/niparis/agent-net/internal/protocol"
)

// memBook is an in-memory daemon.ReplyBook. The daemon's own tests check the
// daemon.db version; this one lets a test give one book to two adapters, as a
// restarted daemon gets the same daemon.db.
type memBook struct {
	mu     sync.Mutex
	rows   []daemon.ClaudeDelivery
	closed map[string]string
}

func (b *memBook) SaveDelivery(r daemon.ClaudeDelivery) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, x := range b.rows {
		if x.MessageID == r.MessageID {
			return nil
		}
	}
	if r.DeliveredAt.IsZero() {
		r.DeliveredAt = time.Now()
	}
	if r.ExpiresAt.IsZero() {
		r.ExpiresAt = r.DeliveredAt.Add(protocol.DefaultTTL)
	}
	b.rows = append(b.rows, r)
	return nil
}

func (b *memBook) ForgetDelivery(id string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	kept := b.rows[:0]
	for _, x := range b.rows {
		if x.MessageID != id {
			kept = append(kept, x)
		}
	}
	b.rows = kept
	return nil
}

func (b *memBook) OpenRequests(recipient, sender, conv string, now time.Time) ([]daemon.ClaudeDelivery, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	var out []daemon.ClaudeDelivery
	for _, x := range b.rows {
		if x.Recipient == recipient && x.Sender == sender && (conv == "" || x.ConversationID == conv) &&
			x.Type == protocol.TypeRequest && b.closed[x.MessageID] == "" && x.ExpiresAt.After(now) {
			out = append(out, x)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].DeliveredAt.After(out[j].DeliveredAt) })
	return out, nil
}

func (b *memBook) CloseRequests(ids []string, match string, at time.Time) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed == nil {
		b.closed = map[string]string{}
	}
	for _, id := range ids {
		b.closed[id] = match
	}
	return nil
}

func (b *memBook) ReplyAddresses() ([]daemon.ReplyAddress, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	seen := map[daemon.ReplyAddress]bool{}
	var out []daemon.ReplyAddress
	for _, x := range b.rows {
		a := daemon.ReplyAddress{Sender: x.Sender, ConversationID: x.ConversationID}
		if a.ConversationID != "" && !seen[a] {
			seen[a] = true
			out = append(out, a)
		}
	}
	return out, nil
}

func (b *memBook) closedAs(id string) string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.closed[id]
}

func request(id, from, conv string) protocol.Message {
	return protocol.Message{ID: id, From: from, To: "s_claude", ConversationID: conv, Type: protocol.TypeRequest,
		Body: "please " + id, FromName: from, FromHarness: "pi"}
}

// deliver sends m to the rig's Claude session and returns the reply address
// its frame names.
func (r *rig) deliver(m protocol.Message) string {
	r.t.Helper()
	res := r.a.Deliver(context.Background(), protocol.SessionInfo{NativeID: "sess-1", SessionID: "s_claude"}, m)
	if !res.Delivered {
		r.t.Fatalf("deliver %s: %+v", m.ID, res)
	}
	var f frame
	select {
	case line := <-r.lines:
		if err := json.Unmarshal([]byte(line), &f); err != nil {
			r.t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		r.t.Fatal("no frame in the inbox")
	}
	if !strings.HasPrefix(f.From, "uds:") {
		r.t.Fatalf("frame has no reply address: %q", f.From)
	}
	return strings.TrimPrefix(f.From, "uds:")
}

// answer writes Claude's SendMessage frame to a reply address and returns
// what the adapter forwarded.
func (r *rig) answer(addr, body string) protocol.MsgSend {
	r.t.Helper()
	line, _ := json.Marshal(map[string]any{"type": "user", "msgV": 1, "msg_id": "uuid-" + body, "from": "uds:" + r.inboxPath,
		"message": map[string]any{"role": "user", "content": body}})
	c, err := net.Dial("unix", addr)
	if err != nil {
		r.t.Fatal(err)
	}
	c.Write(append(line, '\n'))
	c.Close()
	select {
	case p := <-r.host.sent:
		return p
	case <-time.After(5 * time.Second):
		r.t.Fatal("reply was not forwarded")
	}
	return protocol.MsgSend{}
}

func newBookRig(t *testing.T) *rig {
	r := newRig(t)
	eventually(t, "session found", func() bool { return len(r.host.current()) == 1 })
	return r
}

// Two open requests from two senders: each answer goes to its own sender's
// request.
func TestClaudeRepliesTwoSendersDoNotCross(t *testing.T) {
	r := newBookRig(t)
	bob := r.deliver(request("m_bob", "s_bob", "c_bob"))
	carol := r.deliver(request("m_carol", "s_carol", "c_carol"))
	if bob == carol {
		t.Fatal("two senders share a reply address")
	}
	if p := r.answer(carol, "to carol"); p.Recipient != "s_carol" || p.ParentID != "m_carol" || p.Type != protocol.TypeReply {
		t.Fatalf("carol's answer = %+v", p)
	}
	if p := r.answer(bob, "to bob"); p.Recipient != "s_bob" || p.ParentID != "m_bob" || p.Type != protocol.TypeReply {
		t.Fatalf("bob's answer = %+v", p)
	}
}

// Two open requests from one sender in two conversations: each conversation
// has its own reply address, so the answers do not cross. The in-memory map
// used to send both to the last message delivered.
func TestClaudeRepliesSameSenderTwoConversationsDoNotCross(t *testing.T) {
	r := newBookRig(t)
	first := r.deliver(request("m_first", "s_bob", "c_first"))
	second := r.deliver(request("m_second", "s_bob", "c_second"))
	if first == second {
		t.Fatal("two conversations share a reply address")
	}
	if p := r.answer(first, "first answer"); p.ParentID != "m_first" {
		t.Fatalf("answer to the first conversation = %+v, want parent m_first", p)
	}
	if p := r.answer(second, "second answer"); p.ParentID != "m_second" {
		t.Fatalf("answer to the second conversation = %+v, want parent m_second", p)
	}
	eventually(t, "m_first closed", func() bool { return r.book.closedAs("m_first") == daemon.ReplyMatchExact })
	eventually(t, "m_second closed", func() bool { return r.book.closedAs("m_second") == daemon.ReplyMatchExact })
	// Both are answered now: a further message in the conversation has no
	// request to answer, and stays in the conversation.
	if p := r.answer(second, "one more thing"); p.ParentID != "" || p.ConversationID != "c_second" || p.Type != protocol.TypeReply {
		t.Fatalf("follow-up = %+v, want the conversation c_second with no parent", p)
	}
}

// Two open requests from one sender in one conversation and no clear signal:
// the answer goes to the conversation, not to a guessed message, and the
// record says so. A body that names exactly one of them is a clear signal.
func TestClaudeRepliesUnsureMatchThreadsToConversation(t *testing.T) {
	r := newBookRig(t)
	addr := r.deliver(request("m_one", "s_bob", "c_1"))
	if again := r.deliver(request("m_two", "s_bob", "c_1")); again != addr {
		t.Fatalf("one conversation has two reply addresses: %s and %s", addr, again)
	}
	p := r.answer(addr, "done")
	if p.ParentID != "" || p.ConversationID != "c_1" || p.Type != protocol.TypeReply || p.Recipient != "s_bob" {
		t.Fatalf("unsure answer = %+v, want a reply in c_1 with no parent", p)
	}
	for _, id := range []string{"m_one", "m_two"} {
		eventually(t, id+" closed as conversation", func() bool { return r.book.closedAs(id) == daemon.ReplyMatchConversation })
	}
	r.host.mu.Lock()
	logs := strings.Join(r.host.logs, "\n")
	r.host.mu.Unlock()
	if !strings.Contains(logs, "threaded to conversation c_1") {
		t.Fatalf("the unsure match is not logged:\n%s", logs)
	}

	r.deliver(request("m_three", "s_bob", "c_1"))
	r.deliver(request("m_four", "s_bob", "c_1"))
	if p := r.answer(addr, "about m_three: done"); p.ParentID != "m_three" {
		t.Fatalf("answer naming m_three = %+v", p)
	}
	// m_three's close comes from the same call as the answer's close, so once
	// it has landed, m_four's state is final.
	eventually(t, "m_three closed as exact", func() bool { return r.book.closedAs("m_three") == daemon.ReplyMatchExact })
	if got := r.book.closedAs("m_four"); got != "" {
		t.Fatalf("m_four closed as %q by an answer to m_three", got)
	}
}

// A restarted daemon has the same daemon.db: the new adapter listens on the
// reply addresses again and matches the answer to the request delivered
// before the restart.
func TestClaudeRepliesSurviveRestart(t *testing.T) {
	r := newBookRig(t)
	addr := r.deliver(request("m_before", "s_bob", "c_1"))
	r.stop()
	eventually(t, "old reply socket closed", func() bool { _, err := os.Stat(addr); return os.IsNotExist(err) })

	r.a = New(r.a.cfg)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	if err := r.a.Start(ctx, r.host); err != nil {
		t.Fatal(err)
	}
	eventually(t, "session found again", func() bool { return len(r.host.current()) == 1 })
	if p := r.answer(addr, "after restart"); p.ParentID != "m_before" || p.Recipient != "s_bob" {
		t.Fatalf("answer after restart = %+v, want parent m_before", p)
	}
}

// An inbox that refuses the connection never saw the message: its row goes.
func TestClaudeRepliesForgetUndeliveredRequest(t *testing.T) {
	r := newBookRig(t)
	r.inbox.Close()
	res := r.a.Deliver(context.Background(), protocol.SessionInfo{NativeID: "sess-1", SessionID: "s_claude"}, request("m_lost", "s_bob", "c_1"))
	if res.Delivered {
		t.Fatal("delivered to a closed inbox")
	}
	if open, _ := r.book.OpenRequests("s_claude", "s_bob", "", time.Now()); len(open) != 0 {
		t.Fatalf("an undelivered request is open: %+v", open)
	}
}

// When housekeeping prunes the rows of a conversation, its reply socket is
// closed at the next sweep.
func TestClaudeRepliesSweepClosesPrunedAddresses(t *testing.T) {
	r := newBookRig(t)
	addr := r.deliver(request("m_gone", "s_bob", "c_gone"))
	keep := r.deliver(request("m_kept", "s_bob", "c_kept"))
	r.book.ForgetDelivery("m_gone")
	r.a.sweepReplies()
	if _, err := os.Stat(addr); !os.IsNotExist(err) {
		t.Fatalf("reply socket of a pruned conversation is still there: %v", err)
	}
	if _, err := os.Stat(keep); err != nil {
		t.Fatalf("reply socket of a kept conversation went: %v", err)
	}
	if legacy := filepath.Join(r.a.cfg.ReplyDir, "s_bob.sock"); !exists(legacy) {
		t.Fatal("the sender's own reply socket went")
	}
}

func exists(p string) bool { _, err := os.Stat(p); return err == nil }
