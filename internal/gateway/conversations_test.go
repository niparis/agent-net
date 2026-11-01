package gateway

import (
	"fmt"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// conversationFixture makes one node with two sessions and returns their IDs.
func conversationFixture(t *testing.T, s *Store) (node, alice, bob string) {
	t.Helper()
	var err error
	node, _, err = s.CreateNode("h1", "linux")
	if err != nil {
		t.Fatal(err)
	}
	alice, err = s.UpsertSession(node, protocol.SessionInfo{Harness: "claude", NativeID: "na", Name: "alice"})
	if err != nil {
		t.Fatal(err)
	}
	bob, err = s.UpsertSession(node, protocol.SessionInfo{Harness: "pi", NativeID: "nb", Name: "bob"})
	if err != nil {
		t.Fatal(err)
	}
	return node, alice, bob
}

// insertConversationMessage writes one stored message without the send rules
// (rate and conversation budget), so a test can set up an old history.
func insertConversationMessage(t *testing.T, s *Store, id, conv, from, to, body string, at time.Time) {
	t.Helper()
	_, err := s.db.Exec(`INSERT INTO messages
		(id, conversation_id, parent_id, sender, recipient, type, body, hop, idem_key, created_at, expires_at, state)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?)`,
		id, conv, "", from, to, protocol.TypeRequest, body, 0, "k-"+id,
		at.UnixNano(), at.Add(protocol.DefaultTTL).UnixNano(), protocol.StateStored)
	if err != nil {
		t.Fatal(err)
	}
}

func TestConversationsPageCursorTotalAndOrder(t *testing.T) {
	s, c := newTestStore(t)
	_, alice, bob := conversationFixture(t, s)

	base := c.t
	insertConversationMessage(t, s, "m_c1a", "c_old", alice, bob, "old first line\nsecond line", base.Add(-3*time.Hour))
	insertConversationMessage(t, s, "m_c1b", "c_old", bob, alice, "old reply", base.Add(-2*time.Hour))
	insertConversationMessage(t, s, "m_c2", "c_mid", alice, bob, "middle message", base.Add(-time.Hour))
	insertConversationMessage(t, s, "m_c3a", "c_new", bob, alice, "newest message\nmore", base.Add(-2*time.Minute))
	insertConversationMessage(t, s, "m_c3b", "c_new", alice, bob, "newest reply", base.Add(-time.Minute))

	// A conversation between two sessions of another node is part of the
	// owner's gateway history: the list is not limited to this node.
	other, _, _ := s.CreateNode("h2", "linux")
	carol, _ := s.UpsertSession(other, protocol.SessionInfo{Harness: "pi", NativeID: "nc", Name: "carol"})
	dave, _ := s.UpsertSession(other, protocol.SessionInfo{Harness: "pi", NativeID: "nd", Name: "dave"})
	insertConversationMessage(t, s, "m_other", "c_other", carol, dave, "elsewhere", base)

	page, err := s.ConversationsPage(protocol.QueryConversations{Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	if page.Total != 4 {
		t.Fatalf("total = %d, want 4", page.Total)
	}
	if len(page.Conversations) != 2 {
		t.Fatalf("first page has %d rows, want 2", len(page.Conversations))
	}
	if page.Conversations[0].ID != "c_other" || page.Conversations[1].ID != "c_new" {
		t.Fatalf("first page order = %s, %s", page.Conversations[0].ID, page.Conversations[1].ID)
	}
	if page.Next == "" {
		t.Fatal("first page did not give a cursor")
	}
	first := page.Conversations[1]
	if first.From != alice || first.To != bob || first.FromName != "alice" || first.ToName != "bob" {
		t.Errorf("sides = %s/%s (%s/%s)", first.From, first.To, first.FromName, first.ToName)
	}
	if first.Count != 2 {
		t.Errorf("count = %d, want 2", first.Count)
	}
	if first.LastLine != "newest reply" {
		t.Errorf("last line = %q", first.LastLine)
	}
	if first.State != protocol.StateStored {
		t.Errorf("state = %q", first.State)
	}
	if !first.At.Equal(base.Add(-time.Minute)) {
		t.Errorf("at = %v", first.At)
	}
	if first.LastMessageID != "m_c3b" {
		t.Errorf("last message = %q", first.LastMessageID)
	}

	next, err := s.ConversationsPage(protocol.QueryConversations{Cursor: page.Next, Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	if next.Total != 4 || next.Next != "" {
		t.Fatalf("second page total=%d next=%q", next.Total, next.Next)
	}
	if len(next.Conversations) != 2 || next.Conversations[0].ID != "c_mid" || next.Conversations[1].ID != "c_old" {
		t.Fatalf("second page = %+v", next.Conversations)
	}
	if next.Conversations[1].LastLine != "old reply" {
		t.Errorf("last line = %q", next.Conversations[1].LastLine)
	}
}

func TestConversationMessagesCoverEveryNodeForTheOwner(t *testing.T) {
	s, c := newTestStore(t)
	other, _, _ := s.CreateNode("h2", "linux")
	carol, _ := s.UpsertSession(other, protocol.SessionInfo{Harness: "pi", NativeID: "nc", Name: "carol"})
	dave, _ := s.UpsertSession(other, protocol.SessionInfo{Harness: "pi", NativeID: "nd", Name: "dave"})
	base := c.t
	insertConversationMessage(t, s, "m_r1", "c_remote", carol, dave, "remote request", base.Add(-2*time.Minute))
	insertConversationMessage(t, s, "m_r2", "c_remote", dave, carol, "remote reply", base.Add(-time.Minute))

	msgs, err := s.ConversationMessages("c_remote", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 2 || msgs[0].ID != "m_r2" || msgs[1].ID != "m_r1" {
		t.Fatalf("messages = %+v", msgs)
	}
	if msgs[0].Body != "remote reply" || msgs[0].From != dave || msgs[0].To != carol {
		t.Fatalf("newest message = %+v", msgs[0])
	}
}

func TestConversationsPageKeepsTenThousandMessagesFast(t *testing.T) {
	s, c := newTestStore(t)
	_, alice, bob := conversationFixture(t, s)

	tx, err := s.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	for conv := 0; conv < 500; conv++ {
		for msg := 0; msg < 20; msg++ {
			id := fmt.Sprintf("m_%d_%d", conv, msg)
			at := c.t.Add(-time.Duration(conv*20+msg) * time.Minute)
			if _, err := tx.Exec(`INSERT INTO messages
				(id, conversation_id, parent_id, sender, recipient, type, body, hop, idem_key, created_at, expires_at, state)
				VALUES (?,?,?,?,?,?,?,?,?,?,?,?)`,
				id, fmt.Sprintf("c_%d", conv), "", alice, bob, protocol.TypeRequest,
				"line for "+id, 0, "k-"+id, at.UnixNano(), at.Add(protocol.DefaultTTL).UnixNano(), protocol.StateStored); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}

	start := time.Now()
	page, err := s.ConversationsPage(protocol.QueryConversations{Limit: 50})
	elapsed := time.Since(start)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("first conversation page over 10000 messages took %s", elapsed)
	if page.Total != 500 || len(page.Conversations) != 50 || page.Next == "" {
		t.Fatalf("page total=%d rows=%d next=%q", page.Total, len(page.Conversations), page.Next)
	}
	if elapsed > 2*time.Second {
		t.Errorf("first page is too slow: %s", elapsed)
	}
}
