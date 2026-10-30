package gateway

import (
	"testing"

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
