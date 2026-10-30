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
