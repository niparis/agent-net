package gateway

import (
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// The gateway remembers when it first saw a session, and a later report does
// not move it: the TUI keeps a stable order from that time (decision 0017).
func TestFirstSeenStaysWhenTheSessionReportsAgain(t *testing.T) {
	s, c := newTestStore(t)
	node, _, err := s.CreateNode("h1", "linux")
	if err != nil {
		t.Fatal(err)
	}
	id, err := s.UpsertSession(node, protocol.SessionInfo{Harness: "claude", NativeID: "n1", Name: "builder", Status: "idle"})
	if err != nil {
		t.Fatal(err)
	}
	first := c.t.Unix()
	a, ok, err := s.Agent(id)
	if err != nil || !ok || a.FirstSeen != first || a.Title != "builder" {
		t.Fatalf("first report: %+v (ok %v, err %v), want first seen %d", a, ok, err, first)
	}
	c.t = c.t.Add(2 * time.Hour)
	if _, err := s.UpsertSession(node, protocol.SessionInfo{Harness: "claude", NativeID: "n1", Name: "builder", Status: "busy"}); err != nil {
		t.Fatal(err)
	}
	a, _, _ = s.Agent(id)
	if a.FirstSeen != first {
		t.Fatalf("first seen moved from %d to %d", first, a.FirstSeen)
	}
	if a.LastSeen == first {
		t.Fatal("last seen did not move with the new report")
	}
}

// A database from before the created_at column takes last_seen as the first
// seen: the oldest sessions keep the order they had.
func TestFirstSeenMigrationUsesLastSeen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "gw.db")
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	db.Exec(`CREATE TABLE schema_version (version INTEGER NOT NULL)`)
	before := -1
	for i, m := range migrations {
		if strings.Contains(m, "ALTER TABLE sessions ADD COLUMN created_at") {
			before = i
			break
		}
	}
	if before < 0 {
		t.Fatal("the migration for created_at is gone")
	}
	for i := 0; i < before; i++ {
		if _, err := db.Exec(migrations[i]); err != nil {
			t.Fatal(err)
		}
		db.Exec(`INSERT INTO schema_version (version) VALUES (?)`, i+1)
	}
	if _, err := db.Exec(`INSERT INTO nodes (id, hostname, os, token_hash, created_at) VALUES ('n_1','old','linux','h',1)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO sessions (id, node_id, harness, native_id, last_seen, online) VALUES ('s_1','n_1','claude','old',1234000000000,1)`); err != nil {
		t.Fatal(err)
	}
	db.Close()

	s, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	a, ok, err := s.Agent("s_1")
	if err != nil || !ok {
		t.Fatalf("session after migration: ok %v, err %v", ok, err)
	}
	if a.FirstSeen != 1234 {
		t.Fatalf("first seen = %d, want last seen in seconds, 1234", a.FirstSeen)
	}
}
