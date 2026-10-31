package gateway

import (
	"database/sql"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

// A session's name is its project name: the base name of the main working
// tree folder. A harness title is not the name any more; it is kept apart for
// the TASK column (decision 0024).
func TestSessionNameIsTheProjectName(t *testing.T) {
	s, _ := newTestStore(t)
	node, _, err := s.CreateNode("box", "linux")
	if err != nil {
		t.Fatal(err)
	}
	id, err := s.UpsertSession(node, protocol.SessionInfo{
		Harness: "claude", NativeID: "n1", Name: "tenebrae-53", Cwd: "/code/agent-a76c",
		Repo: protocol.RepoInfo{Root: "/code/a2a-ocs", Worktree: "/code/agent-a76c", Project: "a2a-ocs",
			Remote: "git@github.com:niparis/a2a-ocs.git"},
	})
	if err != nil {
		t.Fatal(err)
	}
	a := agentOf(t, s, id)
	if a.Profile.Name != "a2a-ocs" {
		t.Fatalf("name %q, want the project name", a.Profile.Name)
	}
	if a.Title != "tenebrae-53" {
		t.Fatalf("title %q, want the harness name", a.Title)
	}
}

// A daemon from before the repository fields sends no project. The folder is
// then the name, which for a linked worktree on an old daemon is the only
// thing it knows.
func TestSessionNameFallsBackToTheFolder(t *testing.T) {
	s, _ := newTestStore(t)
	node, _, _ := s.CreateNode("box", "linux")
	id, err := s.UpsertSession(node, protocol.SessionInfo{
		Harness: "pi", NativeID: "n1", Name: "assistant", Cwd: "/home/me/code/a2a-ocs/",
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := agentOf(t, s, id).Profile.Name; got != "a2a-ocs" {
		t.Fatalf("name %q, want the folder name", got)
	}

	// Not in git: the folder name is the name too.
	id, err = s.UpsertSession(node, protocol.SessionInfo{Harness: "pi", NativeID: "n2", Cwd: "/home/me/notes"})
	if err != nil {
		t.Fatal(err)
	}
	if got := agentOf(t, s, id).Profile.Name; got != "notes" {
		t.Fatalf("name %q, want notes", got)
	}
}

// A main working tree with no remote still gives its folder name, because the
// daemon reports the main tree as the project (decision 0003).
func TestSessionNameIgnoresTheRemote(t *testing.T) {
	s, _ := newTestStore(t)
	node, _, _ := s.CreateNode("box", "linux")
	id, err := s.UpsertSession(node, protocol.SessionInfo{
		Harness: "claude", NativeID: "n1", Cwd: "/code/agent-a76c",
		Repo: protocol.RepoInfo{Root: "/code/shop", Worktree: "/code/agent-a76c", Project: "shop"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := agentOf(t, s, id).Profile.Name; got != "shop" {
		t.Fatalf("name %q, want shop", got)
	}
}

// Two live sessions with the same name: the first keeps it, the next gets the
// lowest free number, and a later report does not move it.
func TestDuplicateNamesGetTheLowestFreeNumber(t *testing.T) {
	s, _ := newTestStore(t)
	node, _, _ := s.CreateNode("box", "linux")
	add := func(native string) string {
		t.Helper()
		id, err := s.UpsertSession(node, protocol.SessionInfo{Harness: "claude", NativeID: native, Cwd: "/code/shop",
			Repo: protocol.RepoInfo{Root: "/code/shop", Project: "shop"}})
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	a := add("n1")
	b := add("n2")
	c := add("n3")
	if got := agentOf(t, s, a).Profile.Name; got != "shop" {
		t.Fatalf("first name %q", got)
	}
	if got := agentOf(t, s, b).Profile.Name; got != "shop 2" {
		t.Fatalf("second name %q", got)
	}
	if got := agentOf(t, s, c).Profile.Name; got != "shop 3" {
		t.Fatalf("third name %q", got)
	}
	// A report does not renumber a live session.
	if _, err := s.UpsertSession(node, protocol.SessionInfo{Harness: "claude", NativeID: "n2", Cwd: "/code/shop",
		Repo: protocol.RepoInfo{Root: "/code/shop", Project: "shop"}}); err != nil {
		t.Fatal(err)
	}
	if got := agentOf(t, s, b).Profile.Name; got != "shop 2" {
		t.Fatalf("name moved after a report: %q", got)
	}
	// Once the first is offline, its number is free again.
	if err := s.SetSessionOffline(node, a); err != nil {
		t.Fatal(err)
	}
	d := add("n4")
	if got := agentOf(t, s, d).Profile.Name; got != "shop" {
		t.Fatalf("name after the first went offline: %q", got)
	}
}

// A worker is "<project> worker <n>", with n the lowest free number of the
// live workers of that project.
func TestWorkerNamesAreNumberedPerProject(t *testing.T) {
	s, _ := newTestStore(t)
	node, _, _ := s.CreateNode("box", "linux")
	add := func(native string, worker *protocol.WorkerInfo) string {
		t.Helper()
		id, err := s.UpsertSession(node, protocol.SessionInfo{Harness: "opencode", NativeID: native, Cwd: "/code/shop",
			Repo: protocol.RepoInfo{Root: "/code/shop", Project: "shop"}, Worker: worker})
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	w1 := add("w1", &protocol.WorkerInfo{ID: "w_1"})
	w2 := add("w2", &protocol.WorkerInfo{ID: "w_2"})
	plain := add("p1", nil)
	if got := agentOf(t, s, w1).Profile.Name; got != "shop worker 1" {
		t.Fatalf("first worker %q", got)
	}
	if got := agentOf(t, s, w2).Profile.Name; got != "shop worker 2" {
		t.Fatalf("second worker %q", got)
	}
	if got := agentOf(t, s, plain).Profile.Name; got != "shop" {
		t.Fatalf("normal session %q", got)
	}

	// Another project numbers its own workers from one.
	id, err := s.UpsertSession(node, protocol.SessionInfo{Harness: "opencode", NativeID: "x1", Cwd: "/code/web",
		Repo: protocol.RepoInfo{Root: "/code/web", Project: "web"}, Worker: &protocol.WorkerInfo{ID: "w_3"}})
	if err != nil {
		t.Fatal(err)
	}
	if got := agentOf(t, s, id).Profile.Name; got != "web worker 1" {
		t.Fatalf("worker of another project %q", got)
	}
}

// A harness title is kept for the TASK column, not shown as the name.
func TestHarnessTitleIsNotTheName(t *testing.T) {
	s, _ := newTestStore(t)
	node, _, _ := s.CreateNode("box", "linux")
	id, err := s.UpsertSession(node, protocol.SessionInfo{
		Harness: "codex", NativeID: "t1", Name: "Plan portal apps and project de", Cwd: "/code/a2a-ocs",
		Repo: protocol.RepoInfo{Root: "/code/a2a-ocs", Project: "a2a-ocs"},
	})
	if err != nil {
		t.Fatal(err)
	}
	a := agentOf(t, s, id)
	if a.Profile.Name != "a2a-ocs" {
		t.Fatalf("name %q", a.Profile.Name)
	}
	if a.Title != "Plan portal apps and project de" {
		t.Fatalf("title %q", a.Title)
	}
}

// An announce from an older client still carries a name. The gateway ignores
// it: only the owner renames a session (decision 0033). The harness title
// stays apart.
func TestAnnounceIgnoresAnOlderClientsName(t *testing.T) {
	s, _ := newTestStore(t)
	node, _, _ := s.CreateNode("box", "linux")
	in := protocol.SessionInfo{
		Harness: "claude", NativeID: "n1", Name: "tenebrae-53", Cwd: "/code/shop",
		Repo: protocol.RepoInfo{Root: "/code/shop", Project: "shop"},
	}
	id, err := s.UpsertSession(node, in)
	if err != nil {
		t.Fatal(err)
	}
	// The frame an older daemon sends, name and all.
	var old protocol.ProfileAnnounce
	payload := `{"session_id":"` + id + `","name":"x","task":"second"}`
	if err := json.Unmarshal([]byte(payload), &old); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Announce(node, old); err != nil {
		t.Fatal(err)
	}
	a := agentOf(t, s, id)
	if a.Profile.Name != "shop" {
		t.Fatalf("an announce renamed the session to %q", a.Profile.Name)
	}
	if a.Profile.Task != "second" {
		t.Fatalf("task %q", a.Profile.Task)
	}
	if a.Title != "tenebrae-53" {
		t.Fatalf("title %q", a.Title)
	}
	// A later report and another session in the same project keep the name.
	if _, err := s.UpsertSession(node, in); err != nil {
		t.Fatal(err)
	}
	if _, err := s.UpsertSession(node, protocol.SessionInfo{Harness: "claude", NativeID: "n2", Cwd: "/code/shop",
		Repo: protocol.RepoInfo{Root: "/code/shop", Project: "shop"}}); err != nil {
		t.Fatal(err)
	}
	if got := agentOf(t, s, id).Profile.Name; got != "shop" {
		t.Fatalf("name after a report %q", got)
	}
}

// An older gateway stored the name an agent announced. The migration clears
// those names, so every session shows its automatic project name again at its
// next report (decision 0033).
func TestMigrationClearsAnnounceNames(t *testing.T) {
	path := filepath.Join(t.TempDir(), "gw.db")
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	db.Exec(`CREATE TABLE schema_version (version INTEGER NOT NULL)`)
	clear := -1
	for i, m := range migrations {
		if strings.Contains(m, "UPDATE profiles SET name") {
			clear = i
			break
		}
	}
	if clear < 0 {
		t.Fatal("no migration clears announce names")
	}
	for i := 0; i < clear; i++ {
		if _, err := db.Exec(migrations[i]); err != nil {
			t.Fatal(err)
		}
		db.Exec(`INSERT INTO schema_version (version) VALUES (?)`, i+1)
	}
	db.Exec(`INSERT INTO nodes (id, hostname, os, token_hash, created_at) VALUES ('n_1','old','linux','h',1)`)
	db.Exec(`INSERT INTO sessions (id, node_id, harness, native_id, name, cwd, last_seen, online) VALUES ('s_1','n_1','claude','x','shop','/code/shop',5,1)`)
	db.Exec(`INSERT INTO profiles (session_id, name, function, project, capabilities, task, updated_at) VALUES ('s_1','reviewer','','','[]','t',1)`)
	db.Close()

	s, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	a := agentOf(t, s, "s_1")
	if a.Profile.Name != "shop" {
		t.Fatalf("the announced name survived the migration: %q", a.Profile.Name)
	}
	if a.Profile.Task != "t" {
		t.Fatalf("the migration erased the task: %+v", a.Profile)
	}
	// A later report keeps the automatic name.
	if _, err := s.UpsertSession("n_1", protocol.SessionInfo{Harness: "claude", NativeID: "x", Cwd: "/code/shop"}); err != nil {
		t.Fatal(err)
	}
	if got := agentOf(t, s, "s_1").Profile.Name; got != "shop" {
		t.Fatalf("name after a report: %q", got)
	}
}

// The owner pseudo-session keeps the name its daemon gives it.
func TestOwnerNameIsKept(t *testing.T) {
	s, _ := newTestStore(t)
	node, _, _ := s.CreateNode("box", "linux")
	id, err := s.UpsertSession(node, protocol.SessionInfo{Harness: protocol.OwnerHarness, NativeID: protocol.OwnerNativeID,
		Name: protocol.OwnerName("box")})
	if err != nil {
		t.Fatal(err)
	}
	if got := agentOf(t, s, id).Profile.Name; got != protocol.OwnerName("box") {
		t.Fatalf("owner name %q", got)
	}
}
