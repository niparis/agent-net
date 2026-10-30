package gateway

import (
	"path/filepath"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

func TestGraphUndoEdgeAndNode(t *testing.T) {
	s, err := OpenStore(filepath.Join(t.TempDir(), "graph.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.AddArchitect("arch", "owner"); err != nil {
		t.Fatal(err)
	}
	if err := s.AddArchitect("other", "owner"); err != nil {
		t.Fatal(err)
	}
	e := protocol.Edge{From: "arch", To: "other", Kind: "depends"}
	if err := s.SetEdge(e, "owner"); err != nil {
		t.Fatal(err)
	}
	h, err := s.EdgeHistory("arch", 10)
	if err != nil || len(h) != 1 {
		t.Fatalf("history: %+v %v", h, err)
	}
	if err := s.GraphUndo(0, "owner"); err != nil {
		t.Fatal(err)
	}
	g, err := s.Graph()
	if err != nil || len(g.Edges) != 0 {
		t.Fatalf("undo edge: %+v %v", g, err)
	}
	if err := s.GraphUndo(h[0].ID, "owner"); err == nil {
		t.Fatal("stale undo succeeded")
	}
}

func TestGraphUndoRollsBackWhenHistoryFails(t *testing.T) {
	s, err := OpenStore(filepath.Join(t.TempDir(), "graph.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.AddArchitect("arch", "owner"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetCharter("arch", "purpose", "original", "owner"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`CREATE TRIGGER fail_history BEFORE INSERT ON graph_history BEGIN SELECT RAISE(ABORT, 'test failure'); END`); err != nil {
		t.Fatal(err)
	}
	if err := s.GraphUndo(0, "owner"); err == nil {
		t.Fatal("undo unexpectedly succeeded")
	}
	g, err := s.Graph()
	if err != nil || len(g.Nodes) != 1 || g.Nodes[0].Charter.Purpose != "original" {
		t.Fatalf("undo was not rolled back: %+v %v", g, err)
	}
}
