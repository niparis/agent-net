package gateway

import (
	"database/sql"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/niparis/agent-net/internal/protocol"
)

func sessionWithDescription(t *testing.T, s *Store, desc string) (node, id string) {
	t.Helper()
	node, _, err := s.CreateNode("box", "linux")
	if err != nil {
		t.Fatal(err)
	}
	id, err = s.UpsertSession(node, protocol.SessionInfo{Harness: "claude", NativeID: "n1", Name: "harness-name", Cwd: "/w/p",
		Repo: protocol.RepoInfo{Root: "/w/p", Project: "p", Description: desc}})
	if err != nil {
		t.Fatal(err)
	}
	return node, id
}

func agentOf(t *testing.T, s *Store, id string) protocol.AgentView {
	t.Helper()
	a, ok, err := s.Agent(id)
	if err != nil || !ok {
		t.Fatalf("agent %s: %v %v", id, ok, err)
	}
	return a
}

// FUNCTION is the description from the project's AGENTS.md that the daemon
// sent. An announce, however it is worded, does not change it.
func TestFunctionIsTheDescriptionAndAnnounceCannotOverrideIt(t *testing.T) {
	s, _ := newTestStore(t)
	node, id := sessionWithDescription(t, s, "A gateway for agents")
	if got := agentOf(t, s, id).Profile.Function; got != "A gateway for agents" {
		t.Fatalf("function %q", got)
	}
	prof, err := s.Announce(node, protocol.ProfileAnnounce{SessionID: id, Function: "I do repository maintenance", Task: "fixing tests"})
	if err != nil {
		t.Fatal(err)
	}
	a := agentOf(t, s, id)
	if a.Profile.Function != "A gateway for agents" || prof.Function != "A gateway for agents" {
		t.Fatalf("announce overrode the function: %q / %q", a.Profile.Function, prof.Function)
	}
	if a.Profile.Task != "fixing tests" {
		t.Fatalf("task %q", a.Profile.Task)
	}
	// The description follows the file: the next session update changes it.
	if _, err := s.UpsertSession(node, protocol.SessionInfo{Harness: "claude", NativeID: "n1", Name: "harness-name", Cwd: "/w/p",
		Repo: protocol.RepoInfo{Root: "/w/p", Project: "p", Description: "New words"}}); err != nil {
		t.Fatal(err)
	}
	if got := agentOf(t, s, id).Profile.Function; got != "New words" {
		t.Fatalf("function after update %q", got)
	}
	// And it is searchable.
	if as, _ := s.Agents(protocol.QueryAgents{Text: "new words"}); len(as) != 1 {
		t.Fatalf("text search over the function found %d", len(as))
	}
}

func TestNoDescriptionMeansNoFunction(t *testing.T) {
	s, _ := newTestStore(t)
	node, id := sessionWithDescription(t, s, "")
	if _, err := s.Announce(node, protocol.ProfileAnnounce{SessionID: id, Function: "model text", Task: "t"}); err != nil {
		t.Fatal(err)
	}
	if got := agentOf(t, s, id).Profile.Function; got != "" {
		t.Fatalf("the agent's own text became the function: %q", got)
	}
	// An old announce in the database does not show either.
	if _, err := s.db.Exec(`UPDATE profiles SET function = 'stale model text' WHERE session_id = ?`, id); err != nil {
		t.Fatal(err)
	}
	if got := agentOf(t, s, id).Profile.Function; got != "" {
		t.Fatalf("stored model text shows: %q", got)
	}
}

// An announce keeps what is there. It never changes the session's name.
func TestAnnounceKeepsWhatIsThere(t *testing.T) {
	s, _ := newTestStore(t)
	node, id := sessionWithDescription(t, s, "d")
	if _, err := s.Announce(node, protocol.ProfileAnnounce{SessionID: id, Task: "first task"}); err != nil {
		t.Fatal(err)
	}
	if a := agentOf(t, s, id); a.Profile.Name != "p" {
		t.Fatalf("announce renamed the session to %q", a.Profile.Name)
	}
	if _, err := s.Announce(node, protocol.ProfileAnnounce{SessionID: id, Project: "p9", Capabilities: []string{"go"}, Task: "second"}); err != nil {
		t.Fatal(err)
	}
	if a := agentOf(t, s, id); a.Profile.Name != "p" || a.Profile.Project != "p9" || a.Profile.Task != "second" {
		t.Fatalf("explicit fields: %+v", a.Profile)
	}
	if _, err := s.Announce(node, protocol.ProfileAnnounce{SessionID: id, Task: "third"}); err != nil {
		t.Fatal(err)
	}
	a := agentOf(t, s, id)
	if a.Profile.Name != "p" || a.Profile.Project != "p9" || a.Profile.Task != "third" || strings.Join(a.Profile.Capabilities, ",") != "go" {
		t.Fatalf("a later announce erased fields: %+v", a.Profile)
	}
	// Capabilities can still be cleared with an empty list.
	if _, err := s.Announce(node, protocol.ProfileAnnounce{SessionID: id, Task: "fourth", Capabilities: []string{}}); err != nil {
		t.Fatal(err)
	}
	if a := agentOf(t, s, id); len(a.Profile.Capabilities) != 0 {
		t.Fatalf("capabilities: %v", a.Profile.Capabilities)
	}
	// A session of another node cannot be announced.
	other, _, _ := s.CreateNode("other", "linux")
	if _, err := s.Announce(other, protocol.ProfileAnnounce{SessionID: id, Task: "x"}); err == nil {
		t.Fatal("announce for a session of another node was accepted")
	}
}

func TestDescriptionIsCleanedAndCut(t *testing.T) {
	s, _ := newTestStore(t)
	long := strings.Repeat("é", 300)
	_, id := sessionWithDescription(t, s, "  a\x00b\nc "+long)
	got := agentOf(t, s, id).Profile.Function
	if utf8.RuneCountInString(got) != protocol.MaxDescriptionLen || strings.ContainsAny(got, "\x00\n") || !strings.HasPrefix(got, "abc") {
		t.Fatalf("got %d chars: %q", utf8.RuneCountInString(got), got)
	}
}

// A daemon that does not know the field sends none; the gateway shows no
// function. A new daemon's field is omitted when empty, so an older gateway
// sees the frame it always saw.
func TestDescriptionWireCompatibility(t *testing.T) {
	var old protocol.SessionInfo
	if err := json.Unmarshal([]byte(`{"harness":"claude","native_id":"x","repo":{"root":"/r","project":"r"}}`), &old); err != nil {
		t.Fatal(err)
	}
	if old.Repo.Description != "" {
		t.Fatalf("got %q", old.Repo.Description)
	}
	b, _ := json.Marshal(protocol.RepoInfo{Root: "/r"})
	if strings.Contains(string(b), "description") {
		t.Fatalf("empty description is on the wire: %s", b)
	}
	b, _ = json.Marshal(protocol.RepoInfo{Root: "/r", Description: "d"})
	if !strings.Contains(string(b), `"description":"d"`) {
		t.Fatalf("description missing: %s", b)
	}
	// A gateway ignores announce fields it does not know.
	var p protocol.ProfileAnnounce
	if err := json.Unmarshal([]byte(`{"session_id":"s","name":"","function":"x","task":"t","extra":1}`), &p); err != nil || p.Task != "t" {
		t.Fatalf("%+v %v", p, err)
	}
}

func TestMigration8AddsDescription(t *testing.T) {
	path := filepath.Join(t.TempDir(), "gw.db")
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	// A database as version 0.3.10 left it: migrations 1 to 6 applied.
	db.Exec(`CREATE TABLE schema_version (version INTEGER NOT NULL)`)
	for i := 0; i < 6; i++ {
		if _, err := db.Exec(migrations[i]); err != nil {
			t.Fatal(err)
		}
		db.Exec(`INSERT INTO schema_version (version) VALUES (?)`, i+1)
	}
	db.Exec(`INSERT INTO nodes (id, hostname, os, token_hash, created_at) VALUES ('n_1','old','linux','h',1)`)
	db.Exec(`INSERT INTO sessions (id, node_id, harness, native_id, name, cwd, last_seen, online) VALUES ('s_1','n_1','claude','x','n','/w/p',5,0)`)
	db.Exec(`INSERT INTO profiles (session_id, name, function, task, updated_at) VALUES ('s_1','n','model text','t',1)`)
	db.Close()

	s, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if v, _ := s.SchemaVersion(); v != len(migrations) || len(migrations) < 8 {
		t.Fatalf("schema version %d of %d", v, len(migrations))
	}
	a := agentOf(t, s, "s_1")
	if a.Profile.Function != "" || a.Profile.Task != "t" || a.Profile.Name != "n" {
		t.Fatalf("profile after migration: %+v", a.Profile)
	}
}
