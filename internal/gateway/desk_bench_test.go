package gateway

import (
	"fmt"
	"path/filepath"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

func benchStore(b *testing.B) *Store {
	b.Helper()
	s, err := OpenStore(filepath.Join(b.TempDir(), "gw.db"))
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { s.Close() })
	s.tempFolders = []string{}
	return s
}

// seedOwnerQueue fills a store the way a busy gateway looks: many sessions, a
// long Inbox history with a few open items, held requests, unresolved
// outcomes, and learned suggestions that wait for the owner.
func seedOwnerQueue(tb testing.TB, s *Store) {
	tb.Helper()
	node, _, err := s.CreateNode("bench", "linux")
	if err != nil {
		tb.Fatal(err)
	}
	var sessions []string
	for i := 0; i < 60; i++ {
		id, err := s.UpsertSession(node, protocol.SessionInfo{Harness: "claude", NativeID: fmt.Sprintf("n%d", i), Name: fmt.Sprintf("agent-%d", i)})
		if err != nil {
			tb.Fatal(err)
		}
		sessions = append(sessions, id)
	}
	now := s.now()
	// 600 old items, most closed: the history the Inbox list keeps returning.
	for i := 0; i < 600; i++ {
		item, err := s.CreateInboxItem(protocol.InboxItem{Kind: "check", SessionID: sessions[i%60], Node: "repo", Text: fmt.Sprintf("old %d", i)})
		if err != nil {
			tb.Fatal(err)
		}
		if i%15 != 0 {
			if _, err := s.db.Exec(`UPDATE inbox_items SET status='closed',answer='done',answered_at=? WHERE id=?`, now.UnixNano(), item.ID); err != nil {
				tb.Fatal(err)
			}
		}
	}
	for i := 0; i < 12; i++ {
		kind := "question"
		if i%2 == 1 {
			kind = "approval"
		}
		if _, err := s.CreateInboxItem(protocol.InboxItem{Kind: kind, SessionID: sessions[i], Node: "repo", Text: fmt.Sprintf("open %d", i), Options: []string{"yes", "no"}}); err != nil {
			tb.Fatal(err)
		}
	}
	for i := 0; i < 20; i++ {
		a, b := sessions[i], sessions[i+20]
		if _, err := s.db.Exec(`INSERT INTO messages (id,conversation_id,parent_id,sender,recipient,type,body,hop,idem_key,created_at,expires_at,state) VALUES (?,?,?,?,?,?,?,?,?,?,?,?)`,
			protocol.NewID("m"), protocol.NewID("c"), "", a, b, protocol.TypeRequest, "held body", 0, protocol.NewID("k"), now.UnixNano(), now.Add(protocol.DefaultTTL).UnixNano(), protocol.StateHeld); err != nil {
			tb.Fatal(err)
		}
	}
	for i := 0; i < 10; i++ {
		a, b := sessions[i], sessions[i+30]
		req, _, err := s.StoreMessage(protocol.MsgSend{Sender: a, Recipient: b, Body: fmt.Sprintf("do %d", i)})
		if err != nil {
			tb.Fatal(err)
		}
		if _, _, err := s.StoreMessage(protocol.MsgSend{Sender: b, Recipient: a, Body: "no", ParentID: req.ID, ReplyOutcome: protocol.ReplyOutcome{Outcome: "escalate", Reason: "unclear", Recommendation: "ask owner"}}); err != nil {
			tb.Fatal(err)
		}
	}
	for i := 0; i < 200; i++ {
		status, severity := "applied", "minor"
		if i%10 == 0 {
			status, severity = "pending", "major"
		}
		if _, err := s.db.Exec(`INSERT INTO learned_suggestions(node,field,edge_kind,kind,phrase,evidence,status,severity,created_at,ready_at) VALUES(?,?,?,'add',?,'[]',?,?,?,?)`,
			fmt.Sprintf("repo-%d", i%8), "accepts", "", fmt.Sprintf("phrase %d", i), status, severity, now.UnixNano(), now.UnixNano()); err != nil {
			tb.Fatal(err)
		}
	}
}

// BenchmarkOwnerQueueLegacy is the work behind the old Inbox refresh on the
// gateway: four separate calls over the Inbox history, held messages,
// outcomes and learned suggestions.
func BenchmarkOwnerQueueLegacy(b *testing.B) {
	s := benchStore(b)
	seedOwnerQueue(b, s)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := s.ListInbox(); err != nil {
			b.Fatal(err)
		}
		if _, err := s.HeldRequests(); err != nil {
			b.Fatal(err)
		}
		if _, err := s.OutcomeInbox(); err != nil {
			b.Fatal(err)
		}
		if _, err := s.OwnerLearned(); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkOwnerQueueDesk is the same refresh after decision 0090: one query
// for the one list of open asks. The seed is the same as the legacy benchmark;
// the held requests and learned suggestions it holds become asks first.
func BenchmarkOwnerQueueDesk(b *testing.B) {
	s := benchStore(b)
	seedOwnerQueue(b, s)
	if err := s.reconcileAsks(); err != nil {
		b.Fatal(err)
	}
	asks, err := s.ListAsks()
	if err != nil {
		b.Fatal(err)
	}
	b.Logf("open asks: %d", len(asks))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := s.ListAsks(); err != nil {
			b.Fatal(err)
		}
	}
}
