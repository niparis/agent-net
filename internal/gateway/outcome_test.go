package gateway

import (
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

func TestRecordOutcome(t *testing.T) {
	s, _ := newTestStore(t)
	a, b := twoSessions(t, s)
	if _, err := s.db.Exec(`UPDATE sessions SET repo_project=CASE id WHEN ? THEN 'consumer' ELSE 'provider' END WHERE id IN (?,?)`, a, a, b); err != nil {
		t.Fatal(err)
	}
	edge := protocol.Edge{From: "consumer", To: "provider", Kind: "depends"}
	if err := s.SetEdge(edge, "owner"); err != nil {
		t.Fatal(err)
	}
	graph, err := s.Graph()
	if err != nil || len(graph.Edges) != 1 || graph.Edges[0].From != edge.From || graph.Edges[0].To != edge.To {
		t.Fatalf("graph: %+v %v", graph, err)
	}
	req, _, err := s.StoreMessage(protocol.MsgSend{Sender: a, Recipient: b, Body: "request"})
	if err != nil {
		t.Fatal(err)
	}
	rep, _, err := s.StoreMessage(protocol.MsgSend{Sender: b, Recipient: a, Body: "no", ParentID: req.ID, ReplyOutcome: protocol.ReplyOutcome{Outcome: "reject", Reason: "outside charter"}})
	if err != nil {
		t.Fatal(err)
	}
	list, err := s.Outcomes("consumer", "provider", 10)
	if err != nil || len(list) != 1 {
		t.Fatalf("outcomes: %+v %v", list, err)
	}
	if list[0].MessageID != req.ID || list[0].ReplyID != rep.ID || list[0].Reason != "outside charter" {
		t.Fatalf("outcome: %+v", list[0])
	}
}

func TestOutcomeInbox(t *testing.T) {
	s, _ := newTestStore(t)
	a, b := twoSessions(t, s)
	req, _, err := s.StoreMessage(protocol.MsgSend{Sender: a, Recipient: b, Body: "first line\nmore"})
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = s.StoreMessage(protocol.MsgSend{Sender: b, Recipient: a, Body: "unclear", ParentID: req.ID, ReplyOutcome: protocol.ReplyOutcome{Outcome: "escalate", Reason: "unclear", Recommendation: "ask owner"}})
	if err != nil {
		t.Fatal(err)
	}
	rows, err := s.OutcomeInbox()
	if err != nil || len(rows) != 1 {
		t.Fatalf("inbox: %+v %v", rows, err)
	}
	if rows[0].Request != "first line" || rows[0].Reason != "unclear" || rows[0].Recommendation != "ask owner" || rows[0].Sender != a || rows[0].Recipient != b {
		t.Fatalf("entry: %+v", rows[0])
	}
}

func TestOverruleOutcome(t *testing.T) {
	s, _ := newTestStore(t)
	a, b := twoSessions(t, s)
	node, _, err := s.CreateNode("owner-host", "linux")
	if err != nil {
		t.Fatal(err)
	}
	owner, err := s.UpsertSession(node, protocol.SessionInfo{Harness: protocol.OwnerHarness, NativeID: "owner"})
	if err != nil {
		t.Fatal(err)
	}
	req, _, _ := s.StoreMessage(protocol.MsgSend{Sender: a, Recipient: b, Body: "do work"})
	rep, _, _ := s.StoreMessage(protocol.MsgSend{Sender: b, Recipient: a, Body: "no", ParentID: req.ID, ReplyOutcome: protocol.ReplyOutcome{Outcome: "reject", Reason: "no"}})
	if _, err = s.ResolveOutcome(rep.ID, a, "overrule"); err == nil {
		t.Fatal("agent overruled")
	}
	msg, err := s.ResolveOutcome(rep.ID, owner, "overrule")
	if err != nil {
		t.Fatal(err)
	}
	if msg.Body != "do work" || !msg.Overruled || msg.To != b {
		t.Fatalf("message: %+v", msg)
	}
	if _, err = s.ResolveOutcome(rep.ID, owner, "overrule"); err == nil {
		t.Fatal("resolved twice")
	}
	rows, err := s.OutcomeInbox()
	if err != nil || len(rows) != 0 {
		t.Fatalf("inbox: %+v %v", rows, err)
	}
}

func TestLetStandNotifiesAndOwnerRequestsStayOutOfInbox(t *testing.T) {
	s, _ := newTestStore(t)
	a, b := twoSessions(t, s)
	node, _, err := s.CreateNode("host", "linux")
	if err != nil {
		t.Fatal(err)
	}
	owner, err := s.UpsertSession(node, protocol.SessionInfo{Harness: protocol.OwnerHarness, NativeID: "owner"})
	if err != nil {
		t.Fatal(err)
	}
	req, _, err := s.StoreMessage(protocol.MsgSend{Sender: a, Recipient: b, Body: "work"})
	if err != nil {
		t.Fatal(err)
	}
	rep, _, err := s.StoreMessage(protocol.MsgSend{Sender: b, Recipient: a, ParentID: req.ID, Body: "no", ReplyOutcome: protocol.ReplyOutcome{Outcome: "reject", Reason: "reason"}})
	if err != nil {
		t.Fatal(err)
	}
	m, err := s.ResolveOutcome(rep.ID, owner, "let_stand")
	if err != nil || m.ID == "" || m.To != b || m.Overruled || m.Body != "Your decision stands for: work" {
		t.Fatalf("resolution: %+v %v", m, err)
	}
	req, _, err = s.StoreMessage(protocol.MsgSend{Sender: owner, Recipient: b, Body: "work"})
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = s.StoreMessage(protocol.MsgSend{Sender: b, Recipient: owner, ParentID: req.ID, Body: "no", ReplyOutcome: protocol.ReplyOutcome{Outcome: "reject", Reason: "reason"}})
	if err != nil {
		t.Fatal(err)
	}
	entries, err := s.OutcomeInbox()
	if err != nil || len(entries) != 0 {
		t.Fatalf("inbox: %+v %v", entries, err)
	}
}

func TestPendingOutcomeSurvivesRetention(t *testing.T) {
	s, clock := newTestStore(t)
	a, b := twoSessions(t, s)
	req, _, err := s.StoreMessage(protocol.MsgSend{Sender: a, Recipient: b, Body: "work"})
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = s.StoreMessage(protocol.MsgSend{Sender: b, Recipient: a, ParentID: req.ID, Body: "no", ReplyOutcome: protocol.ReplyOutcome{Outcome: "reject", Reason: "reason"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.Exec(`UPDATE sessions SET online=0`); err != nil {
		t.Fatal(err)
	}
	clock.t = clock.t.Add(48 * time.Hour)
	if _, err = s.Prune(Retention{Messages: 24 * time.Hour, Sessions: 24 * time.Hour}); err != nil {
		t.Fatal(err)
	}
	entries, err := s.OutcomeInbox()
	if err != nil || len(entries) != 1 {
		t.Fatalf("lost pending entry: %+v %v", entries, err)
	}
}

// The graph foundation's last migration is the predecessor of the outcome
// migrations, so upgrading an existing gateway preserves its graph.
func TestOutcomeMigrationsAfterGraph(t *testing.T) {
	path := filepath.Join(t.TempDir(), "upgrade.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`CREATE TABLE schema_version (version INTEGER NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	oldCount := 0
	for i, migration := range migrations {
		if strings.Contains(migration, "CREATE TABLE graph_outcomes") {
			oldCount = i
			break
		}
	}
	if oldCount == 0 {
		t.Fatal("missing outcome migration")
	}
	for i, migration := range migrations[:oldCount] {
		if _, err = db.Exec(migration); err != nil {
			t.Fatalf("migration %d: %v", i+1, err)
		}
		if _, err = db.Exec(`INSERT INTO schema_version VALUES (?)`, i+1); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = db.Exec(`INSERT INTO graph_nodes VALUES ('architect','architect',1)`); err != nil {
		t.Fatal(err)
	}
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
	s, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	graph, err := s.Graph()
	if err != nil || len(graph.Nodes) != 1 || graph.Nodes[0].Name != "architect" {
		t.Fatalf("graph: %+v %v", graph, err)
	}
	if _, err = s.Outcomes("", "", 10); err != nil {
		t.Fatal(err)
	}
	v, err := s.SchemaVersion()
	if err != nil || v != len(migrations) {
		t.Fatalf("version: %d %v", v, err)
	}
}

func TestOutcomeCreatesAndResolvesUnifiedInboxItem(t *testing.T) {
	s, _ := newTestStore(t)
	a, b := twoSessions(t, s)
	req, _, err := s.StoreMessage(protocol.MsgSend{Sender: a, Recipient: b, Body: "request context"})
	if err != nil {
		t.Fatal(err)
	}
	rep, _, err := s.StoreMessage(protocol.MsgSend{Sender: b, Recipient: a, ParentID: req.ID, Body: "refused", ReplyOutcome: protocol.ReplyOutcome{Outcome: "reject", Reason: "outside charter"}})
	if err != nil {
		t.Fatal(err)
	}
	items, err := s.ListInbox()
	if err != nil || len(items) != 1 || items[0].Kind != "rejection" || items[0].SessionID != b || items[0].MessageID != rep.ID || items[0].ConversationID != req.ConversationID || !strings.Contains(items[0].Text, "request context") || !strings.Contains(items[0].Text, "outside charter") {
		t.Fatalf("items %+v, %v", items, err)
	}
	node, _, _ := s.CreateNode("owner", "linux")
	oid, _ := s.UpsertSession(node, owner("owner"))
	if _, err = s.ResolveOutcome(rep.ID, oid, "let_stand"); err != nil {
		t.Fatal(err)
	}
	items, err = s.ListInbox()
	if err != nil || items[0].Status == "open" {
		t.Fatalf("unresolved %+v, %v", items, err)
	}
}

func TestAnswerUnifiedOutcomeClearsOutcomeInbox(t *testing.T) {
	s, _ := newTestStore(t)
	a, b := twoSessions(t, s)
	node := mustNode(t, s, b)
	_, err := s.UpsertSession(node, owner("test"))
	if err != nil {
		t.Fatal(err)
	}
	req, _, _ := s.StoreMessage(protocol.MsgSend{Sender: a, Recipient: b, Body: "question"})
	_, _, err = s.StoreMessage(protocol.MsgSend{Sender: b, Recipient: a, ParentID: req.ID, Body: "no", ReplyOutcome: protocol.ReplyOutcome{Outcome: "reject", Reason: "no"}})
	if err != nil {
		t.Fatal(err)
	}
	items, err := s.ListInbox()
	if err != nil || len(items) != 1 {
		t.Fatalf("items %v %v", items, err)
	}
	if _, err = s.AnswerInbox(items[0].ID, "please reconsider"); err != nil {
		t.Fatal(err)
	}
	rows, err := s.OutcomeInbox()
	if err != nil || len(rows) != 0 {
		t.Fatalf("outcomes: %+v %v", rows, err)
	}
}

func TestExistingOutcomesMigrateToInbox(t *testing.T) {
	path := filepath.Join(t.TempDir(), "old.db")
	s, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	node, _, _ := s.CreateNode("host", "linux")
	a, _ := s.UpsertSession(node, protocol.SessionInfo{Harness: "pi", NativeID: "a"})
	b, _ := s.UpsertSession(node, protocol.SessionInfo{Harness: "pi", NativeID: "b"})
	req, _, _ := s.StoreMessage(protocol.MsgSend{Sender: a, Recipient: b, Body: "request"})
	rep, _, err := s.StoreMessage(protocol.MsgSend{Sender: b, Recipient: a, ParentID: req.ID, Body: "no", ReplyOutcome: protocol.ReplyOutcome{Outcome: "reject", Reason: "no"}})
	if err != nil {
		t.Fatal(err)
	}
	// Simulate a database from before the inbox migration: drop what it
	// created, and forget that migration and every later one (model_successes
	// follows it), so they run again.
	inboxVersion := 0
	for i, m := range migrations {
		if strings.Contains(m, "CREATE TABLE inbox_items") {
			inboxVersion = i + 1
			break
		}
	}
	if inboxVersion == 0 {
		t.Fatal("missing inbox migration")
	}
	if _, err = s.db.Exec(`DROP TABLE inbox_items`); err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.Exec(`ALTER TABLE messages DROP COLUMN approval_note`); err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.Exec(`DROP TABLE IF EXISTS model_successes`); err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.Exec(`DELETE FROM schema_version WHERE version >= ?`, inboxVersion); err != nil {
		t.Fatal(err)
	}
	s.Close()
	s, err = OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	items, err := s.ListInbox()
	if err != nil || len(items) != 1 || items[0].MessageID != rep.ID {
		t.Fatalf("items %+v, %v", items, err)
	}
}

func TestOutcomeInboxFailureRollsBackReply(t *testing.T) {
	s, _ := newTestStore(t)
	a, b := twoSessions(t, s)
	req, _, err := s.StoreMessage(protocol.MsgSend{Sender: a, Recipient: b, Body: "request"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.Exec(`CREATE TRIGGER fail_outcome_inbox BEFORE INSERT ON inbox_items BEGIN SELECT RAISE(ABORT,'fail'); END`); err != nil {
		t.Fatal(err)
	}
	_, _, err = s.StoreMessage(protocol.MsgSend{Sender: b, Recipient: a, ParentID: req.ID, Body: "no", ReplyOutcome: protocol.ReplyOutcome{Outcome: "reject", Reason: "no"}})
	if err == nil {
		t.Fatal("expected inbox failure")
	}
	outcomes, err := s.Outcomes("", "", 10)
	if err != nil || len(outcomes) != 0 {
		t.Fatalf("outcomes %+v %v", outcomes, err)
	}
	req, ok, err := s.Message(req.ID)
	if err != nil || !ok || req.State != protocol.StateStored {
		t.Fatalf("request %+v %v", req, err)
	}
}
