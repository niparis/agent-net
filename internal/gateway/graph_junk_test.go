package gateway

import (
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
 DELETE FROM schema_version WHERE version=(SELECT MAX(version) FROM schema_version)`)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.migrate(); err != nil {
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
