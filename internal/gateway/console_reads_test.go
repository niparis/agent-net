package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/phone"
	"github.com/niparis/agent-net/internal/protocol"
)

type conversationConsole struct {
	e2ePhoneOperator
	store   *Store
	calls   int
	elapsed time.Duration
	bytes   int
}

func (f *conversationConsole) Call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	switch method {
	case protocol.OpInfo:
		return json.Marshal(protocol.OperatorInfo{Connected: true})
	case protocol.OpAgents:
		return json.Marshal(protocol.AgentsResult{})
	case protocol.OpCounts:
		return json.Marshal(protocol.SessionCounts{})
	case protocol.OpConversations:
		f.calls++
		start := time.Now()
		page, err := f.store.ConversationsPage(params.(protocol.QueryConversations))
		f.elapsed += time.Since(start)
		if err != nil {
			return nil, err
		}
		raw, err := json.Marshal(page)
		f.bytes += len(raw)
		return raw, err
	}
	return json.RawMessage(`{}`), nil
}
func TestConsoleCachesLatestConversationsWithLargeHistory(t *testing.T) {
	s, c := newTestStore(t)
	_, alice, bob := conversationFixture(t, s)
	tx, err := s.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	const conversations = 5000
	for conv := 0; conv < conversations; conv++ {
		id := fmt.Sprintf("m_console_%05d", conv)
		at := c.t.Add(-time.Duration(conv) * time.Minute)
		if _, err := tx.Exec(`INSERT INTO messages
  (id,conversation_id,parent_id,sender,recipient,type,body,hop,idem_key,created_at,expires_at,state)
  VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`, id, fmt.Sprintf("c_console_%05d", conv), "", alice, bob, protocol.TypeRequest, "line for "+id, 0, "k-"+id, at.UnixNano(), at.Add(protocol.DefaultTTL).UnixNano(), protocol.StateStored); err != nil {
			t.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	f := &conversationConsole{store: s}
	handler := phone.New(f)
	for i := 0; i < 2; i++ {
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, httptest.NewRequest("GET", "/messages", nil))
		if w.Code != 200 {
			t.Fatal(w.Code)
		}
	}
	t.Logf("two console loads, %d conversations: query calls=%d query time=%s returned bytes=%d", conversations, f.calls, f.elapsed, f.bytes)
	if f.calls != 1 {
		t.Errorf("conversation queries=%d want=1", f.calls)
	}
	if f.bytes > 100000 {
		t.Errorf("page bytes=%d; expected only latest 50 rows", f.bytes)
	}
	page, err := s.ConversationsPage(protocol.QueryConversations{Limit: 50})
	if err != nil {
		t.Fatal(err)
	}
	// Every conversation is between the same two parties, so the latest page
	// holds one pair row.
	if page.Total != 1 || len(page.Conversations) != 1 || !strings.HasPrefix(page.Conversations[0].ID, "pair:") || page.Next != "" {
		t.Fatalf("latest page total=%d rows=%d next=%q", page.Total, len(page.Conversations), page.Next)
	}
}
