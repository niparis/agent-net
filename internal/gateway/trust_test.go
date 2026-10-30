package gateway

import (
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

func TestMigration9AddsTrustAndExpertise(t *testing.T) {
	path := filepath.Join(t.TempDir(), "gw.db")
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	// A database from before peer trust: migrations 1 to 8 applied.
	db.Exec(`CREATE TABLE schema_version (version INTEGER NOT NULL)`)
	for i := 0; i < 8; i++ {
		if _, err := db.Exec(migrations[i]); err != nil {
			t.Fatal(err)
		}
		db.Exec(`INSERT INTO schema_version (version) VALUES (?)`, i+1)
	}
	db.Exec(`INSERT INTO nodes (id, hostname, os, token_hash, created_at) VALUES ('n_1','old','linux','h',1)`)
	db.Exec(`INSERT INTO sessions (id, node_id, harness, native_id, cwd, last_seen, online) VALUES ('s_1','n_1','claude','x','/w/p',5,0)`)
	db.Exec(`INSERT INTO profiles (session_id, name, function, task, updated_at) VALUES ('s_1','n','','t',1)`)
	db.Close()

	s, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if v, _ := s.SchemaVersion(); v != len(migrations) || len(migrations) < 9 {
		t.Fatalf("schema version %d of %d", v, len(migrations))
	}
	a := agentOf(t, s, "s_1")
	if a.PeerTrust || a.Profile.Expertise != "" || a.Profile.Task != "t" {
		t.Fatalf("after migration: %+v", a)
	}
}

func TestPeerTrustIsStoredPerSessionAndSurvivesReupsert(t *testing.T) {
	s, _ := newTestStore(t)
	a, b := twoSessions(t, s)
	if ok, err := s.SetPeerTrust(a, true); err != nil || !ok {
		t.Fatalf("set: %v %v", ok, err)
	}
	if !agentOf(t, s, a).PeerTrust || agentOf(t, s, b).PeerTrust {
		t.Fatal("trust is not per session")
	}
	// The daemon reports the session again: the setting stays.
	node, _ := s.SessionNode(a)
	if _, err := s.UpsertSession(node, protocol.SessionInfo{Harness: "claude", NativeID: "na", Name: "alice"}); err != nil {
		t.Fatal(err)
	}
	if !agentOf(t, s, a).PeerTrust {
		t.Fatal("trust lost on a session update")
	}
	// A reopened database keeps it too.
	if ok, _ := s.SetPeerTrust("s_nope", true); ok {
		t.Fatal("unknown session accepted")
	}
	if _, err := s.SetPeerTrust(a, false); err != nil || agentOf(t, s, a).PeerTrust {
		t.Fatal("turn off")
	}
}

func TestOwnerSessionCannotBeTrusted(t *testing.T) {
	s, _ := newTestStore(t)
	node, _, _ := s.CreateNode("h", "linux")
	id, err := s.UpsertSession(node, protocol.SessionInfo{Harness: protocol.OwnerHarness, NativeID: protocol.OwnerNativeID, Name: "you"})
	if err != nil {
		t.Fatal(err)
	}
	if ok, _ := s.SetPeerTrust(id, true); ok || agentOf(t, s, id).PeerTrust {
		t.Fatal("the owner session was trusted")
	}
}

func TestAnnounceStoresExpertiseAndKeepsItWhenOmitted(t *testing.T) {
	s, _ := newTestStore(t)
	a, _ := twoSessions(t, s)
	node, _ := s.SessionNode(a)
	if _, err := s.Announce(node, protocol.ProfileAnnounce{SessionID: a, Task: "t1", Expertise: "  cogforge API\x00 and deploys "}); err != nil {
		t.Fatal(err)
	}
	if got := agentOf(t, s, a).Profile.Expertise; got != "cogforge API and deploys" {
		t.Fatalf("expertise %q", got)
	}
	if _, err := s.Announce(node, protocol.ProfileAnnounce{SessionID: a, Task: "t2"}); err != nil {
		t.Fatal(err)
	}
	if got := agentOf(t, s, a).Profile.Expertise; got != "cogforge API and deploys" {
		t.Fatalf("expertise lost on a new task: %q", got)
	}
	found, err := s.Agents(protocol.QueryAgents{Text: "deploys"})
	if err != nil || len(found) != 1 {
		t.Fatalf("text search on expertise: %v %d", err, len(found))
	}
}
