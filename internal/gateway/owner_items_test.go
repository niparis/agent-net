package gateway

import (
	"fmt"
	"strings"
	"testing"
	"time"

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

func TestWorkerStruggleNotifiesChiefOnce(t *testing.T) {
	s, c := newTestStore(t)
	node, _, err := s.CreateNode("box", "linux")
	if err != nil {
		t.Fatal(err)
	}
	chief, err := s.UpsertSession(node, protocol.SessionInfo{Harness: "claude", NativeID: "chief", Name: "chief"})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetChief(protocol.ChiefSetRequest{SessionID: chief}, "owner"); err != nil {
		t.Fatal(err)
	}
	record := func(sess string) {
		t.Helper()
		err := s.RecordWorkerRun(protocol.WorkerRun{
			NodeID: node, SessionID: sess, Harness: "claude", Model: "sonnet-x",
			Outcome: protocol.OutcomeFailed, EndedAt: c.t.Unix(),
			Struggle: &protocol.Struggle{Failed: protocol.StruggleFailedThreshold, Trace: "transcript"},
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	record("s_w1")
	record("s_w2")
	if got := messagesTo(t, s, chief); len(got) != 0 {
		t.Fatalf("2 workers should not notify yet, got %+v", got)
	}
	record("s_w3")
	got := messagesTo(t, s, chief)
	if len(got) != 1 || got[0].typ != protocol.TypeNotification || !strings.Contains(got[0].body, "sonnet-x") {
		t.Fatalf("3rd struggling worker should notify chief once, got %+v", got)
	}
	// A 4th struggling worker in the same 6h window must not spam a second notice.
	record("s_w4")
	if got2 := messagesTo(t, s, chief); len(got2) != 1 {
		t.Fatalf("4th worker in same window duplicated notice: %+v", got2)
	}
}

func TestTrainRunFailNotifiesLeadOrChief(t *testing.T) {
	s, c := newTestStore(t)
	node, _, err := s.CreateNode("box", "linux")
	if err != nil {
		t.Fatal(err)
	}
	chief, err := s.UpsertSession(node, protocol.SessionInfo{Harness: "claude", NativeID: "chief", Name: "chief"})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetChief(protocol.ChiefSetRequest{SessionID: chief}, "owner"); err != nil {
		t.Fatal(err)
	}
	lead, err := s.UpsertSession(node, protocol.SessionInfo{Harness: "claude", NativeID: "lead", Name: "lead"})
	if err != nil {
		t.Fatal(err)
	}
	now := c.t.UnixNano()
	if _, err := s.db.Exec(`INSERT INTO agents(id,display_name,lead_session,created_at,updated_at) VALUES('a_lead','lead',?,?,?)`, lead, now, now); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`INSERT INTO repos(id,display_name,lead_agent_id,created_at,updated_at) VALUES('app','app','a_lead',?,?)`, now, now); err != nil {
		t.Fatal(err)
	}
	report := func(result, run string) {
		t.Helper()
		tx, err := s.db.Begin()
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		e := protocol.Evidence{
			StepRef: protocol.StepRef{Step: protocol.StepTest},
			Result:  result,
			Fields:  map[string]string{protocol.FieldRun: run},
		}
		if err := s.trainRunNoticeTx(tx, "app", e, c.t); err != nil {
			t.Fatal(err)
		}
		if err := tx.Commit(); err != nil {
			t.Fatal(err)
		}
	}
	report(protocol.StepResultFail, "run-1")
	report(protocol.StepResultFail, "run-1") // same train run counts once
	report(protocol.StepResultFail, "run-2")
	if got := messagesTo(t, s, lead); len(got) != 0 {
		t.Fatalf("only 2 distinct failed runs should not notify yet, got %+v", got)
	}
	report(protocol.StepResultFail, "run-3")
	gotLead := messagesTo(t, s, lead)
	if len(gotLead) != 1 || !strings.Contains(gotLead[0].body, "3 test runs in a row") {
		t.Fatalf("3rd failed run should notify online repo lead, got %+v", gotLead)
	}
	if gotChief := messagesTo(t, s, chief); len(gotChief) != 0 {
		t.Fatalf("online repo lead should take notice instead of chief, got %+v", gotChief)
	}
	// Passing resets the failure streak.
	report(protocol.StepResultPass, "run-4")
	// Take lead offline; 3 fresh failures fall back to chief.
	if _, err := s.db.Exec(`UPDATE sessions SET online=0 WHERE id=?`, lead); err != nil {
		t.Fatal(err)
	}
	c.advance(time.Minute)
	report(protocol.StepResultFail, "run-5")
	report(protocol.StepResultFail, "run-6")
	report(protocol.StepResultFail, "run-7")
	gotChief := messagesTo(t, s, chief)
	if len(gotChief) != 1 || !strings.Contains(gotChief[0].body, "run-7") {
		t.Fatalf("offline lead should fall back to chief after 3 fresh failures, got %+v", gotChief)
	}
}
