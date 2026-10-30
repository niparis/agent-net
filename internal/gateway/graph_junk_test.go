package gateway

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

func TestGraphOmitsSessionsOutsideRepositories(t *testing.T) {
	s, _ := newTestStore(t)
	node, _, err := s.CreateNode("test", "linux")
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.UpsertSession(node, protocol.SessionInfo{Harness: "claude", NativeID: "scratch", Cwd: "/tmp/tmpabc123", Repo: protocol.RepoInfo{Project: "tmpabc123"}})
	if err != nil {
		t.Fatal(err)
	}
	g, err := s.Graph()
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range g.Nodes {
		if n.Name == "tmpabc123" {
			t.Fatalf("non-repository session created graph node: %+v", n)
		}
	}
}

func TestGraphProjectRoots(t *testing.T) {
	s, _ := newTestStore(t)
	node, _, err := s.CreateNode("test", "linux")
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name, root string
		want       bool
	}{
		{"temp-repo", "/tmp/repo", false},
		{"var-temp", "/var/folders/ab/repo", false},
		{"private-temp", "/private/var/folders/ab/repo", false},
		{"home", "/home/example", false},
		{"home-repo", "/home/example/repo", true},
	}
	for _, tc := range cases {
		_, err := s.UpsertSession(node, protocol.SessionInfo{Harness: "claude", NativeID: tc.name, Cwd: tc.root, Repo: protocol.RepoInfo{Root: tc.root, Project: tc.name}})
		if err != nil {
			t.Fatal(err)
		}
	}
	g, err := s.Graph()
	if err != nil {
		t.Fatal(err)
	}
	found := map[string]bool{}
	for _, n := range g.Nodes {
		found[n.Name] = true
	}
	for _, tc := range cases {
		if found[tc.name] != tc.want {
			t.Errorf("%s present=%v, want %v", tc.name, found[tc.name], tc.want)
		}
	}
}

func TestGraphViewHidesOldEmptyImplicitProjects(t *testing.T) {
	s, _ := newTestStore(t)
	ids, _ := graphSessions(t, s, "old", "live")
	_, err := s.db.Exec(`UPDATE sessions SET online=0,last_seen=? WHERE id=?`, s.now().Add(-8*24*time.Hour).UnixNano(), ids["old"])
	if err != nil {
		t.Fatal(err)
	}
	view, err := s.GraphView("", 5)
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range view.Nodes {
		if n.Name == "old" {
			t.Fatal("old empty project is visible")
		}
	}
	all, err := s.visibleGraph(true)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, n := range all.Nodes {
		if n.Name == "old" {
			found = true
		}
	}
	if !found {
		t.Fatal("all omitted old project")
	}
}

func TestGraphJunkMigrationPreservesManagedNodes(t *testing.T) {
	s, _ := newTestStore(t)
	_, err := s.db.Exec(`INSERT INTO graph_nodes VALUES ('tmpabc123','component',1),('keep-charter','component',1),('keep-edge','component',1),('keep-history','component',1);
 INSERT INTO graph_charter VALUES ('keep-charter','purpose','useful',1,'owner');
 INSERT INTO graph_edges VALUES ('keep-edge','other','depends','','normal');
 INSERT INTO graph_history(type,node,attr,old_value,new_value,at,actor) VALUES ('node','keep-history','','','component',1,'owner');
 `)
	if err != nil {
		t.Fatal(err)
	}
	migration := ""
	for _, candidate := range migrations {
		if strings.Contains(candidate, "DELETE FROM graph_nodes\nWHERE kind=") {
			migration = candidate
			break
		}
	}
	if migration == "" {
		t.Fatal("missing graph cleanup migration")
	}
	if _, err := s.db.Exec(migration); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name string
		want int
	}{{"tmpabc123", 0}, {"keep-charter", 1}, {"keep-edge", 1}, {"keep-history", 1}} {
		var count int
		if err := s.db.QueryRow(`SELECT count(*) FROM graph_nodes WHERE name=?`, tc.name).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != tc.want {
			t.Errorf("%s count=%d want=%d", tc.name, count, tc.want)
		}
	}
}

func TestGraphCustomTemporaryAndHomeRoots(t *testing.T) {
	t.Setenv("TMPDIR", "/scratch/custom")
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		root string
		want bool
	}{
		{"/scratch/custom/repo", false},
		{home, false},
		{filepath.Join(home, "repo"), true},
	} {
		if got := graphProjectRoot(tc.root); got != tc.want {
			t.Errorf("graphProjectRoot(%q) = %v, want %v", tc.root, got, tc.want)
		}
	}
}

func TestGraphUsesReportingMachineExclusions(t *testing.T) {
	s, _ := newTestStore(t)
	node, _, err := s.CreateNode("test", "linux")
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.UpsertSession(node, protocol.SessionInfo{Harness: "claude", NativeID: "remote", Repo: protocol.RepoInfo{Root: "/remote/scratch/repo", Project: "remote"}, GraphExcluded: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddProjects(node, protocol.ProjectAdd{Projects: []protocol.ProjectEntry{{Name: "remote-registered", Path: "/remote/scratch/registered", GraphExcluded: true}}}); err != nil {
		t.Fatal(err)
	}
	g, err := s.Graph()
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range g.Nodes {
		if n.Name == "remote-registered" {
			t.Fatal("reporting machine excluded registered root is visible")
		}
		if n.Name == "remote" {
			t.Fatal("reporting machine excluded root is visible")
		}
	}
}

func TestGraphUsesRemoteEligibilityInsteadOfGatewayEnvironment(t *testing.T) {
	s, _ := newTestStore(t)
	t.Setenv("TMPDIR", "/remote/repo")
	node, _, err := s.CreateNode("test", "linux")
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.UpsertSession(node, protocol.SessionInfo{Harness: "claude", NativeID: "remote-eligible", Repo: protocol.RepoInfo{Root: "/remote/repo", Project: "remote-eligible"}, GraphChecked: true})
	if err != nil {
		t.Fatal(err)
	}
	g, err := s.Graph()
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range g.Nodes {
		if n.Name == "remote-eligible" {
			return
		}
	}
	t.Fatal("gateway temporary directory hid an eligible remote repository")
}

func TestGraphKeepsManagedEdgeEndpointsWithoutEligibleRoots(t *testing.T) {
	s, _ := newTestStore(t)
	node, _, err := s.CreateNode("test", "linux")
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.UpsertSession(node, protocol.SessionInfo{Harness: "claude", NativeID: "scratch", Repo: protocol.RepoInfo{Project: "scratch"}})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.AddArchitect("architect", "owner"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetEdge(protocol.Edge{From: "architect", To: "scratch", Kind: "governs", Mode: "advisor"}, "owner"); err != nil {
		t.Fatal(err)
	}
	g, err := s.Graph()
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range g.Nodes {
		if n.Name == "scratch" {
			return
		}
	}
	t.Fatal("managed edge endpoint is missing")
}
