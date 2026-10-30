package gateway

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

func TestCharterStore(t *testing.T) {
	s, err := OpenStore(filepath.Join(t.TempDir(), "graph.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.AddArchitect("architect", "owner"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetCharter("architect", "purpose", "hello\x00\nworld", "owner"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetCharter("architect", "maturity", "bad", "owner"); err == nil {
		t.Fatal("invalid maturity accepted")
	}
	if err := s.SetCharter("architect", "purpose", strings.Repeat("a", protocol.CharterValueMaxBytes+1), "owner"); err == nil {
		t.Fatal("oversize accepted")
	}
	if err := s.SetCharter("architect", "purpose", "", "owner"); err != nil {
		t.Fatal(err)
	}
	h, err := s.CharterHistory("architect", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(h) != 2 || h[0].OldValue != "hello\nworld" || h[0].NewValue != "" {
		t.Fatalf("history: %#v", h)
	}
	if err := s.GraphUndo(h[0].ID, "owner"); err != nil {
		t.Fatal(err)
	}
	g, err := s.Graph()
	if err != nil {
		t.Fatal(err)
	}
	if len(g.Nodes) != 1 || g.Nodes[0].Charter.Purpose != "hello\nworld" {
		t.Fatalf("graph: %#v", g)
	}
}

func TestGraphEdges(t *testing.T) {
	s, err := OpenStore(filepath.Join(t.TempDir(), "graph.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	for _, n := range []string{"a", "b"} {
		if err := s.AddArchitect(n, "owner"); err != nil {
			t.Fatal(err)
		}
	}
	e := protocol.Edge{From: "a", To: "b", Kind: "depends", Rule: "normal"}
	if err := s.SetEdge(e, "owner"); err != nil {
		t.Fatal(err)
	}
	if err := s.RemoveArchitect("a", "owner"); err == nil {
		t.Fatal("removed node with edge")
	}
	g, err := s.Graph()
	if err != nil || len(g.Edges) != 1 {
		t.Fatalf("graph: %#v, %v", g, err)
	}
	if err := s.RemoveEdge(e, "owner"); err != nil {
		t.Fatal(err)
	}
	if err := s.RemoveArchitect("a", "owner"); err != nil {
		t.Fatal(err)
	}
}
