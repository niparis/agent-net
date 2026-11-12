package gateway

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// insertThreadPeer makes one session to pair with alice, so every thread has
// its own pair_gid.
func insertThreadPeer(t *testing.T, s *Store, node string, i int, at time.Time) string {
	t.Helper()
	peer := fmt.Sprintf("s_peer_%03d", i)
	if _, err := s.db.Exec(`INSERT INTO sessions (id,node_id,harness,native_id,name,last_seen)
		VALUES (?,?,?,?,?,?)`, peer, node, "pi", "n_"+peer, fmt.Sprintf("peer %03d", i), at.UnixNano()); err != nil {
		t.Fatal(err)
	}
	return peer
}

func TestConversationsLiteListsEveryThreadWithoutBodies(t *testing.T) {
	s, c := newTestStore(t)
	node, alice, _ := conversationFixture(t, s)
	base := c.t
	const count = 500
	for i := 0; i < count; i++ {
		peer := insertThreadPeer(t, s, node, i, base)
		at := base.Add(-time.Duration(i) * 30 * 24 * time.Hour / count)
		insertConversationMessage(t, s, fmt.Sprintf("m_%03d", i), fmt.Sprintf("c_%03d", i),
			alice, peer, fmt.Sprintf("thread %03d last line", i), at)
	}
	// An older message of a thread stays in the store; no list row may carry
	// its body.
	insertConversationMessage(t, s, "m_old_body", "c_000", alice, "s_peer_000",
		"BODY-SENTINEL not for a list", base.Add(-time.Hour))

	var rows []protocol.ConversationView
	seen := map[string]bool{}
	cursor := ""
	for page := 0; ; page++ {
		if page > count {
			t.Fatal("the pages never ended")
		}
		got, err := s.ConversationsPage(protocol.QueryConversations{Cursor: cursor, Limit: 400, Lite: true})
		if err != nil {
			t.Fatal(err)
		}
		if got.Total != count {
			t.Fatalf("page %d total = %d, want %d", page, got.Total, count)
		}
		b, err := json.Marshal(got)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(b), "BODY-SENTINEL") {
			t.Fatalf("a list page carries a message body: %s", b)
		}
		for _, row := range got.Conversations {
			if seen[row.ID] {
				t.Fatalf("thread %q came twice", row.ID)
			}
			seen[row.ID] = true
			rows = append(rows, row)
		}
		if got.Next == "" {
			break
		}
		cursor = got.Next
	}
	if len(rows) != count {
		t.Fatalf("the list has %d threads, want %d", len(rows), count)
	}
	oldest := rows[len(rows)-1]
	if oldest.LastLine != "thread 499 last line" {
		t.Fatalf("the oldest thread is %q", oldest.LastLine)
	}
	if got := base.Sub(oldest.At); got < 29*24*time.Hour {
		t.Fatalf("the oldest thread is only %s old", got)
	}
}

func TestConversationsVersionIsNotReusedByAnotherStore(t *testing.T) {
	path := seededStorePath(t, "gw.db")
	first, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	before, err := first.ConversationsPage(protocol.QueryConversations{Lite: true})
	if err != nil {
		t.Fatal(err)
	}
	// A second gateway process opens the same database. Its write counter
	// starts from zero and climbs through the same values, so a bare counter
	// would match a version the first process handed out.
	second, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	after, err := second.ConversationsPage(protocol.QueryConversations{Lite: true})
	if err != nil {
		t.Fatal(err)
	}
	if after.Version == before.Version {
		t.Fatalf("two stores handed out the same version %d", before.Version)
	}
	again, err := second.ConversationsPage(protocol.QueryConversations{Lite: true, IfVersion: before.Version})
	if err != nil {
		t.Fatal(err)
	}
	if again.NotModified {
		t.Fatal("the second store accepted a version from the first store")
	}
}

func TestConversationsLitePollIsNotModifiedWhileUnchanged(t *testing.T) {
	s, c := newTestStore(t)
	node, alice, _ := conversationFixture(t, s)
	base := c.t
	for i := 0; i < 5; i++ {
		peer := insertThreadPeer(t, s, node, i, base)
		insertConversationMessage(t, s, fmt.Sprintf("m_%03d", i), fmt.Sprintf("c_%03d", i),
			alice, peer, fmt.Sprintf("thread %03d", i), base.Add(-time.Duration(i)*time.Minute))
	}
	first, err := s.ConversationsPage(protocol.QueryConversations{Limit: 400, Lite: true})
	if err != nil {
		t.Fatal(err)
	}
	if first.Version == 0 {
		t.Fatal("the page carries no version")
	}
	again, err := s.ConversationsPage(protocol.QueryConversations{Limit: 400, Lite: true, IfVersion: first.Version})
	if err != nil {
		t.Fatal(err)
	}
	if !again.NotModified {
		t.Fatal("an unchanged poll was not NotModified")
	}
	if len(again.Conversations) != 0 || again.Total != 0 {
		t.Fatalf("an unchanged poll sent rows: %+v", again)
	}
	// A session report writes last_seen many times a minute. It does not change
	// a list row, so the list version must not move with it.
	if _, err := s.db.Exec(`UPDATE sessions SET last_seen = last_seen + 1 WHERE id = ?`, alice); err != nil {
		t.Fatal(err)
	}
	heartbeat, err := s.ConversationsPage(protocol.QueryConversations{Limit: 400, Lite: true, IfVersion: first.Version})
	if err != nil {
		t.Fatal(err)
	}
	if !heartbeat.NotModified {
		t.Fatal("a session heartbeat moved the list version")
	}

	insertConversationMessage(t, s, "m_new", "c_new", alice, "s_peer_000", "a newer line", base.Add(time.Minute))
	changed, err := s.ConversationsPage(protocol.QueryConversations{Limit: 400, Lite: true, IfVersion: first.Version})
	if err != nil {
		t.Fatal(err)
	}
	if changed.NotModified {
		t.Fatal("a changed list was answered NotModified")
	}
	if changed.Version == first.Version {
		t.Fatal("the version did not move with the new message")
	}
}
