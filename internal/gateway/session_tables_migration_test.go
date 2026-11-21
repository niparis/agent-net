package gateway

import (
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// sessionTablesSeed writes rows into every table the session tables rework
// drops, with the layout they had before it.
func sessionTablesSeed(t *testing.T, path string) {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	const at = 1795000000000000000
	for _, q := range []string{
		`INSERT INTO nodes(id,hostname,os,token_hash,created_at) VALUES('n_1','box','linux','h',1)`,
		`INSERT INTO sessions(id,node_id,harness,native_id,name,cwd,last_seen,last_active,online,status,repo_project,repo_root,created_at)
		 VALUES('s_named','n_1','claude','na','shop','/code/shop',1795000000000000000,1795000000000000000,1,'idle','shop','/code/shop',1),
		       ('s_loose','n_1','pi','nb','notes','/tmp/x',1795000000000000000,1795000000000000000,1,'idle','','',1),
		       ('s_split','n_1','codex','nc','web','/code/web',1795000000000000000,1795000000000000000,1,'idle','web','/code/web',1)`,
		`INSERT INTO profiles(session_id,name,function,project,capabilities,task,updated_at,expertise,task_started)
		 VALUES('s_named','reviewer','','','["go","sql"]','review the fold',1795000000000000000,'gateway schema',1794999000000000000),
		       ('s_loose','','','notes','[]','write notes',1795000000000000000,'',1795000000000000000),
		       ('s_split','','','other','[]','',1795000000000000000,'',0)`,
		`INSERT INTO session_checks(session_id,check_active,check_waiting,check_more,check_open,check_msgs,check_day,check_day_count,check_task_hash,check_announced_at,check_fingerprint)
		 VALUES('s_named',11,1,2,3,4,'2026-11-21',5,'hash',12,'print')`,
		`INSERT INTO session_wakes(session_id,wake_day,wake_day_count,wake_streak,wake_open,wake_msgs,wake_failed_at,wake_failed_reason,wake_attempted_at)
		 VALUES('s_named','2026-11-21',2,1,3,4,13,'refused',14)`,
		`INSERT INTO repo_start_reservations(node_id,ref,repo_id,harness,native_id,expires_at) VALUES('n_1','r1','shop','claude','',1)`,
		`INSERT INTO session_agent_starts(node_id,harness,native_id,agent_id,ref,expires_at) VALUES('n_1','claude','','a1','r2',1)`,
		`INSERT INTO session_agent_start_failures(node_id,ref,harness,native_id,owner_session,project,item_id,reason) VALUES('n_1','r3','claude','','s_o','shop','i1','expired')`,
		`INSERT INTO check_answer_counts(day,kind,count) VALUES('2026-11-21','done',3)`,
	} {
		if _, err := db.Exec(q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
}

// sessionTablesGet is what agent_session_get answers for the seeded sessions.
func sessionTablesGet(t *testing.T, s *Store) string {
	t.Helper()
	s.now = func() time.Time { return time.Unix(0, 1795000600000000000) }
	var out []protocol.SessionGetResult
	for _, id := range []string{"s_named", "s_loose"} {
		got, err := s.GetSession(protocol.SessionGetRequest{Session: id})
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, got)
	}
	b, _ := json.MarshalIndent(out, "", "  ")
	return string(b) + "\n"
}

// The golden is what agent_session_get answered for the seeded sessions before
// the rework, written by the code of the base commit.
const sessionTablesGolden = "testdata/session_tables_get.golden.json"

// sessionDroppedTables are the tables the session tables rework dropped.
var sessionDroppedTables = []string{"profiles", "session_checks", "session_wakes", "repo_start_reservations", "session_agent_starts", "session_agent_start_failures", "check_answer_counts"}

// The session tables rework folds seven tables into sessions. This seeds an
// older database with a row in each, migrates it, and checks that the read
// tools answer as before and that each fact landed in its column.
func TestSessionTablesMigrationKeepsWhatToolsShow(t *testing.T) {
	path := filepath.Join(t.TempDir(), "old.db")
	old := migrateOnly(t, path, migrationVersion(t, sessionTablesMarker)-1)
	old.Close()
	sessionTablesSeed(t, path)
	s, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	want, err := os.ReadFile(sessionTablesGolden)
	if err != nil {
		t.Fatal(err)
	}
	if got := sessionTablesGet(t, s); got != string(want) {
		t.Errorf("agent_session_get changed:\n%s\nwant:\n%s", got, want)
	}
	for _, gone := range sessionDroppedTables {
		if n := sideFoldInt(t, s, `SELECT COUNT(*) FROM sqlite_master WHERE name=?`, gone); n != 0 {
			t.Errorf("%s is still there", gone)
		}
	}
	var name, task, expertise, caps, checkDay, fingerprint, wakeDay, wakeReason string
	var fixed, checkActive, checkWaiting, checkCount, wakeCount, wakeFailed, wakeAttempted, announced int64
	if err := s.db.QueryRow(`SELECT name,name_fixed,task,expertise,capabilities,announced_at,check_active,check_waiting,check_day,check_day_count,check_fingerprint,
		wake_day,wake_day_count,wake_failed_at,wake_failed_reason,wake_attempted_at FROM sessions WHERE id='s_named'`).Scan(
		&name, &fixed, &task, &expertise, &caps, &announced, &checkActive, &checkWaiting, &checkDay, &checkCount, &fingerprint,
		&wakeDay, &wakeCount, &wakeFailed, &wakeReason, &wakeAttempted); err != nil {
		t.Fatal(err)
	}
	if name != "reviewer" || fixed != 1 || task != "review the fold" || expertise != "gateway schema" || caps != `["go","sql"]` || announced != 1795000000000000000 ||
		checkActive != 11 || checkWaiting != 1 || checkDay != "2026-11-21" || checkCount != 5 || fingerprint != "print" ||
		wakeDay != "2026-11-21" || wakeCount != 2 || wakeFailed != 13 || wakeReason != "refused" || wakeAttempted != 14 {
		t.Errorf("s_named columns: %q %d %q %q %q %d / %d %d %q %d %q / %q %d %d %q %d", name, fixed, task, expertise, caps, announced,
			checkActive, checkWaiting, checkDay, checkCount, fingerprint, wakeDay, wakeCount, wakeFailed, wakeReason, wakeAttempted)
	}
	// One project per session: the detected repo wins over a different
	// announced one, which the read tools showed before.
	got, err := s.GetSession(protocol.SessionGetRequest{Session: "s_split"})
	if err != nil || got.Session.Project != "web" {
		t.Errorf("s_split project %q, %v", got.Session.Project, err)
	}
	// The next report keeps the owner's name.
	if _, err := s.UpsertSession("n_1", protocol.SessionInfo{Harness: "claude", NativeID: "na", Cwd: "/code/shop", Status: "idle", Repo: protocol.RepoInfo{Project: "shop", Root: "/code/shop"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.UpsertSession("n_1", protocol.SessionInfo{Harness: "pi", NativeID: "nb", Cwd: "/tmp/x", Status: "idle"}); err != nil {
		t.Fatal(err)
	}
	if a := agentOf(t, s, "s_named"); a.Profile.Name != "reviewer" {
		t.Errorf("a report renamed the session to %q", a.Profile.Name)
	}
	if a := agentOf(t, s, "s_loose"); a.Profile.Project != "notes" {
		t.Errorf("a report dropped the announced project: %+v", a.Profile)
	}
}

// The read tools show the detected repo first, then the announced project,
// as the TUI and the graph do.
func TestReadToolsShowTheDetectedRepoFirst(t *testing.T) {
	s, _ := newTestStore(t)
	node, _, err := s.CreateNode("box", "linux")
	if err != nil {
		t.Fatal(err)
	}
	loose, err := s.UpsertSession(node, protocol.SessionInfo{Harness: "pi", NativeID: "nb", Cwd: "/tmp/x", Status: "idle"})
	if err != nil {
		t.Fatal(err)
	}
	inRepo, err := s.UpsertSession(node, protocol.SessionInfo{Harness: "claude", NativeID: "na", Cwd: "/code/web", Status: "idle", Repo: protocol.RepoInfo{Project: "web", Root: "/code/web"}})
	if err != nil {
		t.Fatal(err)
	}
	for id, want := range map[string]string{loose: "notes", inRepo: "web"} {
		if _, err := s.Announce(node, protocol.ProfileAnnounce{SessionID: id, Project: "notes", Task: "t"}); err != nil {
			t.Fatal(err)
		}
		if got, err := s.GetSession(protocol.SessionGetRequest{Session: id}); err != nil || got.Session.Project != want {
			t.Errorf("%s project %q, want %q: %v", id, got.Session.Project, want, err)
		}
	}
}
