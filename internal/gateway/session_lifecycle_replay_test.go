package gateway

import (
	"fmt"
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

func TestSessionLifecycleMigrationReplay(t *testing.T) {
	for _, version := range []int{113, 114} {
		t.Run(fmt.Sprint(version), func(t *testing.T) {
			s, _ := newTestStore(t)
			// Match old fixtures: the later identity migration must add these back.
			if _, err := s.db.Exec(`DROP TRIGGER session_identity_repo_update;
 ALTER TABLE sessions DROP COLUMN agent_id; ALTER TABLE sessions DROP COLUMN repo_id;
 ALTER TABLE worker_runs DROP COLUMN caller_agent_id; ALTER TABLE worker_starts DROP COLUMN caller_agent_id;
 DROP TABLE session_agent_history; DELETE FROM schema_version WHERE version>?`, version); err != nil {
				t.Fatalf("rewind: %v", err)
			}
			if err := migrateOldFixture(t, s); err != nil {
				t.Fatalf("replay from %d: %v", version, err)
			}
			var columns int
			if err := s.db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('sessions') WHERE name IN ('repo_id','agent_id')`).Scan(&columns); err != nil || columns != 2 {
				t.Fatalf("identity columns %d: %v", columns, err)
			}
		})
	}
}

func TestSessionLifecycleRejectsInactiveContextAtomically(t *testing.T) {
	s, _ := newTestStore(t)
	node, _, err := s.CreateNode("fixture", "linux")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.Exec(`UPDATE graph_ref_gate SET enabled=1`); err != nil {
		t.Fatal(err)
	}
	if err = s.loadGraphRefs(); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"archived", "active"} {
		if err = s.GraphLifecycle("repo.create", protocol.GraphLifecycleRequest{Name: name}, "owner", false); err != nil {
			t.Fatal(err)
		}
	}
	if err = s.GraphLifecycle("agent.create", protocol.GraphLifecycleRequest{Name: "former"}, "owner", false); err != nil {
		t.Fatal(err)
	}
	existing, err := s.UpsertSession(node, protocol.SessionInfo{Harness: "pi", NativeID: "existing", Repo: protocol.RepoInfo{Project: "active"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.Exec(`UPDATE sessions SET agent_id='former',online=0 WHERE id=?`, existing); err != nil {
		t.Fatal(err)
	}
	if err = s.GraphLifecycle("agent.tombstone", protocol.GraphLifecycleRequest{Ref: &protocol.GraphRef{Kind: "agent", ID: "former"}}, "owner", false); err != nil {
		t.Fatal(err)
	}
	if err = s.GraphLifecycle("repo.archive", protocol.GraphLifecycleRequest{Ref: &protocol.GraphRef{Kind: "repo", ID: "archived"}}, "owner", false); err != nil {
		t.Fatal(err)
	}
	for _, in := range []protocol.SessionInfo{
		{Harness: "pi", NativeID: "new", Repo: protocol.RepoInfo{Project: "archived"}},
		{Harness: "pi", NativeID: "existing", Repo: protocol.RepoInfo{Project: "active"}},
	} {
		if _, err = s.UpsertSession(node, in); err == nil || !strings.Contains(err.Error(), "inactive") {
			t.Errorf("upsert %s: %v", in.NativeID, err)
		}
	}
	var count, online int
	if err = s.db.QueryRow(`SELECT COUNT(*) FROM sessions WHERE native_id='new'`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("partial new session: %d, %v", count, err)
	}
	if err = s.db.QueryRow(`SELECT online FROM sessions WHERE id=?`, existing).Scan(&online); err != nil || online != 0 {
		t.Fatalf("existing session online %d: %v", online, err)
	}
	_, err = s.SyncSessions(node, []protocol.SessionInfo{{Harness: "pi", NativeID: "good", Repo: protocol.RepoInfo{Project: "active"}}, {Harness: "pi", NativeID: "bad", Repo: protocol.RepoInfo{Project: "archived"}}})
	if err == nil || !strings.Contains(err.Error(), "inactive") {
		t.Fatalf("sync: %v", err)
	}
	if err = s.db.QueryRow(`SELECT COUNT(*) FROM sessions WHERE native_id IN ('good','bad')`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("partial sync: %d, %v", count, err)
	}
}

func TestSessionLifecycleUsesPersistedCutoverGate(t *testing.T) {
	s, _ := newTestStore(t)
	node, _, err := s.CreateNode("fixture", "linux")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.Exec(`INSERT INTO agents(id,display_name,created_at,updated_at,tombstone) VALUES('former','former',0,0,1);
 INSERT INTO repos(id,display_name,created_at,updated_at) VALUES('repo','repo',0,0)`); err != nil {
		t.Fatal(err)
	}
	id, err := s.UpsertSession(node, protocol.SessionInfo{Harness: "pi", NativeID: "session", Repo: protocol.RepoInfo{Project: "repo"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.Exec(`UPDATE sessions SET agent_id='former',online=0 WHERE id=?; UPDATE graph_ref_gate SET enabled=1`, id); err != nil {
		t.Fatal(err)
	}
	// The database commit can precede publishing the in-memory gate.
	if s.graphRefsOn.Load() {
		t.Fatal("unexpected cached gate")
	}
	if _, err = s.UpsertSession(node, protocol.SessionInfo{Harness: "pi", NativeID: "session", Repo: protocol.RepoInfo{Project: "repo"}}); err == nil || !strings.Contains(err.Error(), "inactive") {
		t.Fatalf("persisted cutover ignored: %v", err)
	}
	var online int
	if err = s.db.QueryRow(`SELECT online FROM sessions WHERE id=?`, id).Scan(&online); err != nil || online != 0 {
		t.Fatalf("session online %d: %v", online, err)
	}
}
