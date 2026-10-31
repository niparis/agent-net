package gateway

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

func entry(name, path string) protocol.ProjectEntry {
	return protocol.ProjectEntry{Name: name, Path: path, Remote: "https://example.com/" + name + ".git", DefaultBranch: "main"}
}

func TestAddProjectsAddsAndReportsExisting(t *testing.T) {
	s, c, lc := newLoggedStore(t)
	n, _, _ := s.CreateNode("mac", "darwin")
	res, err := s.AddProjects(n, protocol.ProjectAdd{Projects: []protocol.ProjectEntry{entry("a", "/c/a"), entry("b", "/c/b"), entry("a", "/c/a")},
		AddedBySession: "s_1", AddedByHarness: "claude"})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Added) != 2 || len(res.Existing) != 0 {
		t.Fatalf("first add: %+v", res)
	}
	c.t = c.t.Add(time.Hour)
	changed := entry("a", "/c/a")
	changed.Remote, changed.DefaultBranch = "git@example.com:o/a.git", "trunk"
	res, err = s.AddProjects(n, protocol.ProjectAdd{Projects: []protocol.ProjectEntry{changed, entry("c", "/c/c")}, AddedByHarness: "cli"})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Added) != 1 || res.Added[0].Path != "/c/c" || len(res.Existing) != 1 || res.Existing[0].Path != "/c/a" {
		t.Fatalf("second add: %+v", res)
	}
	ps, _ := s.Projects("a")
	if len(ps) != 1 {
		t.Fatalf("projects: %+v", ps)
	}
	p := ps[0]
	// Re-registering refreshes the checked data and time, and keeps who added it and when.
	if p.Remote != "git@example.com:o/a.git" || p.DefaultBranch != "trunk" || p.AddedBy != "claude s_1" ||
		p.AddedAt != c.t.Add(-time.Hour).Unix() || p.CheckedAt != c.t.Unix() || p.Source != protocol.SourceRegistered {
		t.Fatalf("project: %+v", p)
	}
	// One event and one log line per project, in key=value form.
	evs, _ := s.Events("project.added")
	if !reflect.DeepEqual(evs, []string{"name=a path=/c/a", "name=b path=/c/b", "name=c path=/c/c"}) {
		t.Fatalf("events: %v", evs)
	}
	lines := lc.with("event=project.added")
	if len(lines) != 3 || !strings.Contains(lines[0], "node="+n) || !strings.Contains(lines[0], "host=mac") ||
		!strings.Contains(lines[0], "session=s_1") || !strings.Contains(lines[0], "harness=claude") ||
		!strings.Contains(lines[0], "name=a") || !strings.Contains(lines[0], "path=/c/a") {
		t.Fatalf("log lines: %q", lines)
	}
}

func TestAddProjectsDryRunChangesNothing(t *testing.T) {
	s, _, lc := newLoggedStore(t)
	n, _, _ := s.CreateNode("mac", "darwin")
	s.AddProjects(n, protocol.ProjectAdd{Projects: []protocol.ProjectEntry{entry("a", "/c/a")}})
	res, err := s.AddProjects(n, protocol.ProjectAdd{DryRun: true, Projects: []protocol.ProjectEntry{entry("a", "/c/a"), entry("b", "/c/b")}})
	if err != nil || len(res.Added) != 1 || res.Added[0].Name != "b" || len(res.Existing) != 1 {
		t.Fatalf("%v %+v", err, res)
	}
	if ps, _ := s.Projects(""); len(ps) != 1 {
		t.Fatalf("a dry run stored something: %+v", ps)
	}
	if len(lc.with("event=project.added")) != 1 {
		t.Fatalf("a dry run logged: %q", lc.all())
	}
}

func TestAddProjectsRefusesBadInput(t *testing.T) {
	s, _ := newTestStore(t)
	n, _, _ := s.CreateNode("mac", "darwin")
	many := make([]protocol.ProjectEntry, protocol.ProjectMaxPerCall+1)
	for i := range many {
		many[i] = entry("p", "/c/p"+strings.Repeat("x", i+1))
	}
	cases := map[string]protocol.ProjectAdd{
		"none":          {},
		"relative path": {Projects: []protocol.ProjectEntry{entry("a", "c/a")}},
		"unclean path":  {Projects: []protocol.ProjectEntry{entry("a", "/c/../a")}},
		"control char":  {Projects: []protocol.ProjectEntry{{Name: "a\nb", Path: "/c/a"}}},
		"huge field":    {Projects: []protocol.ProjectEntry{{Name: "a", Path: "/c/a", Remote: strings.Repeat("x", protocol.ProjectMaxFieldLen+1)}}},
		"too many":      {Projects: many},
	}
	for name, in := range cases {
		if _, err := s.AddProjects(n, in); err == nil {
			t.Errorf("%s: want an error", name)
		} else if _, ok := err.(Rejection); !ok {
			t.Errorf("%s: want a Rejection, got %T", name, err)
		}
	}
	if ps, _ := s.Projects(""); len(ps) != 0 {
		t.Fatalf("a refused call stored something: %+v", ps)
	}
}

func TestAddProjectsStripsCredentialsAndDefaultsName(t *testing.T) {
	s, _ := newTestStore(t)
	n, _, _ := s.CreateNode("mac", "darwin")
	_, err := s.AddProjects(n, protocol.ProjectAdd{Projects: []protocol.ProjectEntry{
		{Path: "/c/a", Remote: "https://bob:hunter2@example.com/o/a.git"}}})
	if err != nil {
		t.Fatal(err)
	}
	ps, _ := s.Projects("")
	if len(ps) != 1 || ps[0].Name != "a" || ps[0].Remote != "https://example.com/o/a.git" {
		t.Fatalf("%+v", ps)
	}
}

// The same folder on two machines is two projects.
func TestProjectsAreKeyedByMachineAndPath(t *testing.T) {
	s, _ := newTestStore(t)
	n1, _, _ := s.CreateNode("mac", "darwin")
	n2, _, _ := s.CreateNode("box", "linux")
	s.AddProjects(n1, protocol.ProjectAdd{Projects: []protocol.ProjectEntry{entry("a", "/c/a")}})
	res, err := s.AddProjects(n2, protocol.ProjectAdd{Projects: []protocol.ProjectEntry{entry("a", "/c/a")}})
	if err != nil || len(res.Added) != 1 {
		t.Fatalf("%v %+v", err, res)
	}
	ps, _ := s.Projects("")
	if len(ps) != 2 || ps[0].Hostname != "box" || ps[1].Hostname != "mac" {
		t.Fatalf("%+v", ps)
	}
}

func TestProjectsMergeRegisteredAndSeen(t *testing.T) {
	s, _ := newTestStore(t)
	n1, _, _ := s.CreateNode("mac", "darwin")
	n2, _, _ := s.CreateNode("box", "linux")
	repo := protocol.RepoInfo{Root: "/c/both", Project: "both", Remote: "https://example.com/both.git"}
	s.UpsertSession(n1, protocol.SessionInfo{Harness: "claude", NativeID: "a", Cwd: "/c/both/web", Repo: repo})
	s.UpsertSession(n1, protocol.SessionInfo{Harness: "pi", NativeID: "b", Cwd: "/c/seen",
		Repo: protocol.RepoInfo{Root: "/c/seen", Project: "seen"}})
	s.AddProjects(n1, protocol.ProjectAdd{Projects: []protocol.ProjectEntry{
		{Name: "both", Path: "/c/both", DefaultBranch: "main"}, entry("only", "/c/only")}})
	// A session on another machine in a folder that is registered only on mac.
	s.UpsertSession(n2, protocol.SessionInfo{Harness: "pi", NativeID: "c", Cwd: "/c/only",
		Repo: protocol.RepoInfo{Root: "/c/only", Project: "only"}})

	ps, err := s.Projects("")
	if err != nil {
		t.Fatal(err)
	}
	type row struct{ name, host, source string }
	var got []row
	for _, p := range ps {
		got = append(got, row{p.Name, p.Hostname, p.Source})
	}
	want := []row{{"both", "mac", "both"}, {"only", "box", "seen"}, {"only", "mac", "registered"}, {"seen", "mac", "seen"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got  %+v\nwant %+v", got, want)
	}
	both := ps[0]
	if both.Sessions != 1 || both.Online != 1 || both.Remote != "https://example.com/both.git" ||
		both.DefaultBranch != "main" || both.AddedBy != "" || both.CheckedAt == 0 || len(both.Harnesses) != 1 {
		t.Fatalf("merged row: %+v", both)
	}
	reg := ps[2]
	if reg.Sessions != 0 || reg.LastSeen != 0 || reg.Harnesses == nil || reg.Worktrees == nil {
		t.Fatalf("registered-only row: %+v", reg)
	}
	if one, _ := s.Projects("ONLY"); len(one) != 2 {
		t.Fatalf("name filter: %+v", one)
	}
}

// A registered project stays when its sessions are pruned.
func TestRegisteredProjectOutlivesSessions(t *testing.T) {
	s, c := newTestStore(t)
	n, _, _ := s.CreateNode("mac", "darwin")
	s.UpsertSession(n, protocol.SessionInfo{Harness: "pi", NativeID: "b", Cwd: "/c/p", Repo: protocol.RepoInfo{Root: "/c/p", Project: "p"}})
	s.AddProjects(n, protocol.ProjectAdd{Projects: []protocol.ProjectEntry{entry("p", "/c/p")}})
	s.SyncSessions(n, nil) // offline
	c.t = c.t.Add(48 * time.Hour)
	if _, err := s.Prune(Retention{Sessions: time.Hour}); err != nil {
		t.Fatal(err)
	}
	ps, _ := s.Projects("")
	if len(ps) != 1 || ps[0].Source != protocol.SourceRegistered {
		t.Fatalf("%+v", ps)
	}
}

func TestRemoveProject(t *testing.T) {
	s, _, lc := newLoggedStore(t)
	n1, _, _ := s.CreateNode("mac", "darwin")
	n2, _, _ := s.CreateNode("box", "linux")
	s.AddProjects(n1, protocol.ProjectAdd{Projects: []protocol.ProjectEntry{entry("a", "/c/a"), entry("dup", "/c/x/dup"), entry("dup", "/c/y/dup")}})
	s.AddProjects(n2, protocol.ProjectAdd{Projects: []protocol.ProjectEntry{entry("a", "/c/a")}})

	// A name that fits two projects is refused, and the paths are listed.
	_, err := s.RemoveProject(n1, "DUP", "", n1)
	if err == nil || !strings.Contains(err.Error(), "/c/x/dup") || !strings.Contains(err.Error(), "/c/y/dup") {
		t.Fatalf("ambiguous name: %v", err)
	}
	if _, err := s.RemoveProject(n1, "nope", "", n1); err == nil || !strings.Contains(err.Error(), `no registered project "nope" on mac`) {
		t.Fatalf("missing: %v", err)
	}
	if _, err := s.RemoveProject(n1, "", "", n1); err == nil {
		t.Fatal("no name or path must be refused")
	}
	// By name, only on the given machine.
	e, err := s.RemoveProject(n1, "a", "", n1)
	if err != nil || e.Path != "/c/a" {
		t.Fatalf("%v %+v", err, e)
	}
	if ps, _ := s.Projects("a"); len(ps) != 1 || ps[0].Hostname != "box" {
		t.Fatalf("the other machine's project must stay: %+v", ps)
	}
	// By path.
	if _, err := s.RemoveProject(n1, "", "/c/y/dup", n1); err != nil {
		t.Fatal(err)
	}
	evs, _ := s.Events("project.removed")
	if len(evs) != 2 || evs[0] != "name=a path=/c/a node="+n1 {
		t.Fatalf("events: %v", evs)
	}
	if len(lc.with("event=project.removed", "name=a", "path=/c/a", "host=mac")) != 1 {
		t.Fatalf("log: %q", lc.all())
	}
}

func TestMigration5AddsProjectsToAnOlderDatabase(t *testing.T) {
	path := filepath.Join(t.TempDir(), "gw.db")
	// A database as version 0.3.5 left it: migrations 1 to 4 applied.
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	db.Exec(`CREATE TABLE schema_version (version INTEGER NOT NULL)`)
	for i := 0; i < 4; i++ {
		if _, err := db.Exec(migrations[i]); err != nil {
			t.Fatal(err)
		}
		db.Exec(`INSERT INTO schema_version (version) VALUES (?)`, i+1)
	}
	db.Exec(`INSERT INTO nodes (id, hostname, os, token_hash, created_at) VALUES ('n_1','old','linux','h',1)`)
	db.Close()
	s, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if v, _ := s.SchemaVersion(); v != len(migrations) || len(migrations) < 5 {
		t.Fatalf("schema version %d of %d, want at least 5", v, len(migrations))
	}
	if res, err := s.AddProjects("n_1", protocol.ProjectAdd{Projects: []protocol.ProjectEntry{entry("a", "/c/a")}}); err != nil || len(res.Added) != 1 {
		t.Fatalf("%v %+v", err, res)
	}
}

func TestGraphIncludesProjectLocationsAndLiveSessions(t *testing.T) {
	s, _ := newTestStore(t)
	n, _, _ := s.CreateNode("box", "linux")
	if _, err := s.AddProjects(n, protocol.ProjectAdd{Projects: []protocol.ProjectEntry{entry("cogforge", "/src/cogforge")}}); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 6; i++ {
		_, err := s.UpsertSession(n, protocol.SessionInfo{Harness: "pi", NativeID: fmt.Sprint(i), Name: "builder", Status: "busy", Cwd: "/src/cogforge", Repo: protocol.RepoInfo{Root: "/src/cogforge", Project: "cogforge"}})
		if err != nil {
			t.Fatal(err)
		}
	}
	g, err := s.visibleGraph(false)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(g)
	var v struct {
		Nodes []struct {
			Name         string                                       `json:"name"`
			Locations    []struct{ Machine, Path string }             `json:"locations"`
			Sessions     []struct{ ID, Name, Machine, Status string } `json:"sessions"`
			SessionCount int                                          `json:"session_count"`
		} `json:"nodes"`
	}
	if err := json.Unmarshal(b, &v); err != nil {
		t.Fatal(err)
	}
	for _, node := range v.Nodes {
		if node.Name == "cogforge" {
			if len(node.Locations) != 1 || node.Locations[0].Machine != "box" || node.Locations[0].Path != "/src/cogforge" || len(node.Sessions) != 5 || node.SessionCount != 6 || node.Sessions[0].ID == "" || node.Sessions[0].Machine != "box" {
				t.Fatalf("node: %+v", node)
			}
			return
		}
	}
	t.Fatalf("missing project: %s", b)
}
