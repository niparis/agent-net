package gateway

import (
	"fmt"
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

// sentToChief is one message an owner item sent to a session.
type sentToChief struct {
	from, typ, body, key string
}

// messagesTo returns every message stored for a session.
func messagesTo(t *testing.T, s *Store, to string) []sentToChief {
	t.Helper()
	rows, err := s.db.Query(`SELECT sender,type,body,idem_key FROM messages WHERE recipient=? ORDER BY created_at,rowid`, to)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []sentToChief
	for rows.Next() {
		var m sentToChief
		if err := rows.Scan(&m.from, &m.typ, &m.body, &m.key); err != nil {
			t.Fatal(err)
		}
		out = append(out, m)
	}
	return out
}

// captureLog collects the store's log lines as "event k=v ...".
func captureLog(s *Store) *[]string {
	var lines []string
	s.logf = func(event string, kv ...any) { lines = append(lines, event+" "+fmt.Sprintln(kv...)) }
	return &lines
}

// A message an 'ask the owner' rule holds reaches the chief as a request that
// names the held message and tells how the CTO releases it.
func TestHeldMessageRequestNamesHeldAnswer(t *testing.T) {
	s, _ := newTestStore(t)
	a, b := twoSessions(t, s)
	if _, err := s.db.Exec(`UPDATE sessions SET repo_project=CASE id WHEN ? THEN 'consumer' ELSE 'provider' END WHERE id IN (?,?)`, a, a, b); err != nil {
		t.Fatal(err)
	}
	if err := s.SetEdge(protocol.Edge{From: "*", To: "provider", Kind: "depends", Rule: "ask"}, "owner"); err != nil {
		t.Fatal(err)
	}
	node, _, _ := s.CreateNode("chief-box", "linux")
	chief, err := s.UpsertSession(node, protocol.SessionInfo{Harness: "claude", NativeID: "chief", Name: "chief"})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetChief(protocol.ChiefSetRequest{SessionID: chief}, "owner"); err != nil {
		t.Fatal(err)
	}
	m, _, err := send(s, a, b, "k1", "")
	if err != nil || m.State != protocol.StateHeld {
		t.Fatalf("message %+v %v", m, err)
	}
	got := messagesTo(t, s, chief)
	if len(got) != 1 {
		t.Fatalf("want one request to the chief, got %+v", got)
	}
	want := "If the owner agrees, the CTO answers it with held_answer, message_id " + m.ID + ", action a to deliver or r to refuse."
	if !strings.HasSuffix(got[0].body, want) {
		t.Fatalf("request does not end with the held_answer line:\n%s", got[0].body)
	}
}
