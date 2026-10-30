package gateway

import (
	"database/sql"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

func TestSessionModeAndModelAreStored(t *testing.T) {
	s, _ := newTestStore(t)
	node, _, _ := s.CreateNode("h", "linux")
	id, err := s.UpsertSession(node, protocol.SessionInfo{Harness: "claude", NativeID: "n", Name: "x",
		Mode: protocol.ModeForeground, Model: "claude-opus-5-5"})
	if err != nil {
		t.Fatal(err)
	}
	a, _, _ := s.Agent(id)
	if a.Mode != protocol.ModeForeground || a.Model != "claude-opus-5-5" {
		t.Fatalf("agent: %+v", a)
	}
	// A new report replaces both, and /model shows at once.
	s.UpsertSession(node, protocol.SessionInfo{Harness: "claude", NativeID: "n", Name: "x",
		Mode: protocol.ModeBackground, Model: "claude-sonnet-5-5"})
	list, _ := s.Agents(protocol.QueryAgents{})
	if len(list) != 1 || list[0].Mode != protocol.ModeBackground || list[0].Model != "claude-sonnet-5-5" {
		t.Fatalf("agents: %+v", list)
	}
	// A full sync stores them too.
	s.SyncSessions(node, []protocol.SessionInfo{{Harness: "claude", NativeID: "n", Name: "x",
		Mode: protocol.ModeUnknown, Model: "m"}})
	if a, _, _ := s.Agent(id); a.Mode != protocol.ModeUnknown || a.Model != "m" {
		t.Fatalf("after sync: %+v", a)
	}
}

func TestSessionModeAndModelAreCleaned(t *testing.T) {
	s, _ := newTestStore(t)
	node, _, _ := s.CreateNode("h", "linux")
	id, _ := s.UpsertSession(node, protocol.SessionInfo{Harness: "pi", NativeID: "n",
		Mode: "sideways", Model: " a\x1b[31mb\nc " + strings.Repeat("é", 100)})
	a, _, _ := s.Agent(id)
	if a.Mode != "" {
		t.Fatalf("a bad mode was kept: %q", a.Mode)
	}
	if strings.ContainsAny(a.Model, "\x1b\n") || !strings.HasPrefix(a.Model, "a[31mbc é") {
		t.Fatalf("model not cleaned: %q", a.Model)
	}
	if len(a.Model) > protocol.MaxModelLen {
		t.Fatalf("model has %d bytes", len(a.Model))
	}
	if !strings.HasSuffix(a.Model, "é") {
		t.Fatalf("model cut in the middle of a character: %q", a.Model)
	}
}

// A daemon of the previous version sends no mode and no model.
func TestOlderDaemonSendsNoModeOrModel(t *testing.T) {
	var si protocol.SessionInfo
	if err := json.Unmarshal([]byte(`{"harness":"claude","native_id":"n","status":"idle"}`), &si); err != nil {
		t.Fatal(err)
	}
	s, _ := newTestStore(t)
	node, _, _ := s.CreateNode("h", "linux")
	id, _ := s.UpsertSession(node, si)
	a, _, _ := s.Agent(id)
	if a.Mode != "" || a.Model != "" {
		t.Fatalf("mode %q model %q", a.Mode, a.Model)
	}
}

func TestMigrationForModeAndModelKeepsExistingData(t *testing.T) {
	path := filepath.Join(t.TempDir(), "gw.db")
	// A database just before the mode and model migration.
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE schema_version (version INTEGER NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	oldCount := 0
	for i, migration := range migrations {
		if strings.Contains(migration, "ALTER TABLE sessions ADD COLUMN mode ") {
			oldCount = i
			break
		}
	}
	if oldCount == 0 {
		t.Fatal("missing mode migration")
	}
	for i := 0; i < oldCount; i++ {
		if _, err := db.Exec(migrations[i]); err != nil {
			t.Fatal(err)
		}
		db.Exec(`INSERT INTO schema_version (version) VALUES (?)`, i+1)
	}
	db.Exec(`INSERT INTO nodes (id, hostname, os, token_hash, created_at) VALUES ('n_1','old','linux','h',1)`)
	db.Exec(`INSERT INTO sessions (id, node_id, harness, native_id, cwd, last_seen, online) VALUES ('s_1','n_1','claude','x','/w/p',5,0)`)
	db.Close()

	s, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if v, _ := s.SchemaVersion(); v != len(migrations) {
		t.Fatalf("schema version %d of %d", v, len(migrations))
	}
	a, ok, _ := s.Agent("s_1")
	if !ok || a.Cwd != "/w/p" || a.Mode != "" || a.Model != "" {
		t.Fatalf("agent: %+v", a)
	}
}

// ---- event feed ----

func TestFeedEventsKindsAndOrder(t *testing.T) {
	s, _ := newTestStore(t)
	a, b := twoSessions(t, s)
	m, _, err := send(s, a, b, "k1", "")
	if err != nil {
		t.Fatal(err)
	}
	s.MarkDispatched(m.ID)
	s.ApplyDelivery(m.ID, protocol.StateDelivered, "")
	// Kinds that are not for every node.
	s.Event("mcp.added", "", a, "", "secret-server on n_1")
	s.Event("enroll.refused", "", "", "", "evil-host")
	s.Event("node.connected", "n_1", "", "", "203.0.113.9:5555")
	s.Event("retention", "", "", "", "messages=3")

	evs, err := s.FeedEvents(0, 0)
	if err != nil {
		t.Fatal(err)
	}
	var kinds []string
	for _, e := range evs {
		kinds = append(kinds, e.Kind)
		if strings.HasPrefix(e.Kind, "mcp.") || e.Kind == "enroll.refused" || e.Kind == "retention" {
			t.Errorf("kind %s must not be in the feed", e.Kind)
		}
		if e.Kind == "node.connected" && e.Detail != "" {
			t.Errorf("address in the feed: %q", e.Detail)
		}
	}
	want := "node.enrolled node.enrolled session.new session.new msg.stored msg.dispatched msg.delivered node.connected"
	if got := strings.Join(kinds, " "); got != want {
		t.Fatalf("kinds %q\nwant  %q", got, want)
	}
	for _, e := range evs {
		if strings.HasPrefix(e.Kind, "msg.") && (e.Parent != "" || e.From != a || e.To != b || e.Type != protocol.TypeRequest) {
			t.Errorf("%s: from %q to %q type %q", e.Kind, e.From, e.To, e.Type)
		}
	}
}

func TestFeedEventsCursorAndLimit(t *testing.T) {
	s, _ := newTestStore(t)
	for i := 0; i < 12; i++ {
		s.Event("session.new", "", "s_"+string(rune('a'+i)), "", "")
	}
	newest, _ := s.FeedEvents(0, 5)
	if len(newest) != 5 || newest[4].SessionID != "s_l" || newest[0].SessionID != "s_h" {
		t.Fatalf("newest five, oldest first: %+v", newest)
	}
	older, err := s.feedEventsBefore(0, newest[0].ID, 5)
	if err != nil || len(older) != 5 || older[4].SessionID != "s_g" {
		t.Fatalf("older page: %+v %v", older, err)
	}
	first, _ := s.FeedEvents(1, 3)
	if len(first) != 3 || first[0].ID != 2 {
		t.Fatalf("after 1: %+v", first)
	}
	if none, _ := s.FeedEvents(newest[4].ID, 5); len(none) != 0 {
		t.Fatalf("after the last: %+v", none)
	}
	if all, _ := s.FeedEvents(0, 100000); len(all) != 12 {
		t.Fatalf("limit is capped, got %d", len(all))
	}
}

func TestFeedEventsCutLongDetail(t *testing.T) {
	s, _ := newTestStore(t)
	s.Event("msg.failed", "", "s_x", "m_none", strings.Repeat("é", 500))
	evs, _ := s.FeedEvents(0, 10)
	if len(evs) != 1 || len(evs[0].Detail) > 200 || !strings.HasSuffix(evs[0].Detail, "é") {
		t.Fatalf("detail: %q", evs[0].Detail)
	}
	// The message is gone (retention) but the event stays.
	if evs[0].From != "" || evs[0].To != "" {
		t.Fatalf("ends of a message that does not exist: %+v", evs[0])
	}
}

func TestFeedNamesTheParentOfAReply(t *testing.T) {
	s, _ := newTestStore(t)
	a, b := twoSessions(t, s)
	m, _, _ := send(s, a, b, "k1", "")
	r, _, err := s.StoreMessage(protocol.MsgSend{Sender: b, Recipient: a, Body: "ok", IdempotencyKey: "k2", ParentID: m.ID})
	if err != nil {
		t.Fatal(err)
	}
	evs, _ := s.FeedEvents(0, 50)
	var got protocol.FeedEvent
	for _, e := range evs {
		if e.Kind == "msg.stored" && e.MessageID == r.ID {
			got = e
		}
	}
	if got.Parent != m.ID || got.Type != protocol.TypeReply || got.From != b || got.To != a || got.ConversationID != m.ConversationID {
		t.Fatalf("reply event: %+v", got)
	}
}

func TestAgentViewHasThePID(t *testing.T) {
	s, _ := newTestStore(t)
	node, _, _ := s.CreateNode("h", "linux")
	id, _ := s.UpsertSession(node, protocol.SessionInfo{Harness: "claude", NativeID: "n", PID: 4242})
	if a, _, _ := s.Agent(id); a.PID != 4242 {
		t.Fatalf("pid %d", a.PID)
	}
}
