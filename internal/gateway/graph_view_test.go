package gateway

import (
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// graphTestStore creates a store with three projects, one architect and two
// edges, and returns it.
func graphTestStore(t *testing.T) *Store {
	t.Helper()
	s, err := OpenStore(filepath.Join(t.TempDir(), "graph.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	_, err = s.AddProjects("n_test", protocol.ProjectAdd{Projects: []protocol.ProjectEntry{
		{Name: "cogforge", Path: "/w/cogforge"},
		{Name: "tenebrae", Path: "/w/tenebrae"},
		{Name: "shop", Path: "/w/shop"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	graphSessions(t, s, "shop")
	if err := s.AddArchitect("trading-architect", "owner"); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct{ node, attr, value string }{
		{"trading-architect", "maturity", "mature - stable"},
		{"cogforge", "purpose", "Generic document API"},
		{"cogforge", "maturity", "early - prototype"},
		{"tenebrae", "maturity", "growing"},
	} {
		if err := s.SetCharter(c.node, c.attr, c.value, "owner"); err != nil {
			t.Fatal(err)
		}
	}
	for _, e := range []protocol.Edge{
		{From: "trading-architect", To: "cogforge", Kind: "governs", Mode: "owner", Rule: "normal"},
		{From: "tenebrae", To: "cogforge", Kind: "depends", Rule: "ask"},
	} {
		if err := s.SetEdge(e, "owner"); err != nil {
			t.Fatal(err)
		}
	}
	return s
}

func TestGraphViewReturnsNodesChartersEdgesAndHistory(t *testing.T) {
	s := graphTestStore(t)
	view, err := s.GraphView("", protocol.GraphHistoryPerNode)
	if err != nil {
		t.Fatal(err)
	}
	if len(view.Nodes) != 4 {
		t.Fatalf("%d nodes, want 4: %#v", len(view.Nodes), view.Nodes)
	}
	if len(view.Edges) != 2 {
		t.Fatalf("%d edges, want 2: %#v", len(view.Edges), view.Edges)
	}
	byName := map[string]protocol.Node{}
	for _, n := range view.Nodes {
		byName[n.Name] = n
	}
	if got := byName["cogforge"].Charter.Purpose; got != "Generic document API" {
		t.Fatalf("cogforge purpose %q", got)
	}
	if got := byName["cogforge"].Charter.Maturity; got != "early - prototype" {
		t.Fatalf("cogforge maturity %q", got)
	}
	if len(view.Changes) != 4 {
		t.Fatalf("%d changes, want 4: %#v", len(view.Changes), view.Changes)
	}
	if view.Changes[0].Attr != "maturity" || view.Changes[0].Node != "tenebrae" {
		t.Fatalf("changes are not newest first: %#v", view.Changes)
	}
}

func TestGraphViewKeepsTheLastChangesPerNode(t *testing.T) {
	s := graphTestStore(t)
	for i := 0; i < 7; i++ {
		if err := s.SetCharter("cogforge", "notes", fmt.Sprintf("note %d", i), "owner"); err != nil {
			t.Fatal(err)
		}
	}
	view, err := s.GraphView("", protocol.GraphHistoryPerNode)
	if err != nil {
		t.Fatal(err)
	}
	got := 0
	for _, c := range view.Changes {
		if c.Node == "cogforge" && c.Attr == "notes" {
			got++
		}
	}
	if want := protocol.GraphHistoryPerNode; got != want {
		t.Fatalf("%d notes changes for cogforge, want %d (the newest)", got, want)
	}
}

func TestGraphViewOfOneProjectIsItsNeighbourhood(t *testing.T) {
	s := graphTestStore(t)
	view, err := s.GraphView("cogforge", protocol.GraphHistoryPerNode)
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, n := range view.Nodes {
		names[n.Name] = true
	}
	for _, want := range []string{"cogforge", "trading-architect", "tenebrae"} {
		if !names[want] {
			t.Errorf("missing %q in %v", want, names)
		}
	}
	if names["shop"] {
		t.Error("shop has no edge with cogforge and must not be there")
	}
	if len(view.Edges) != 2 {
		t.Fatalf("%d edges, want the 2 that touch cogforge: %#v", len(view.Edges), view.Edges)
	}
	for _, c := range view.Changes {
		if c.Node == "shop" {
			t.Fatalf("a change of an unrelated node: %#v", c)
		}
	}
}

func TestGraphTalkCountsSevenDays(t *testing.T) {
	s := graphTestStore(t)
	now := s.now()
	for _, v := range []struct{ id, project, harness string }{{"a", "cogforge", "claude"}, {"b", "tenebrae", "claude"}, {"c", "shop", "claude"}, {"owner", "shop", "owner"}} {
		_, err := s.db.Exec(`INSERT INTO sessions (id,node_id,harness,native_id,repo_project,last_seen) VALUES (?,?,?,?,?,?)`, v.id, "n_test", v.harness, v.id, v.project, now.UnixNano())
		if err != nil {
			t.Fatal(err)
		}
	}
	for i, v := range []struct {
		from, to string
		age      time.Duration
	}{{"a", "b", time.Hour}, {"b", "a", time.Hour}, {"a", "c", time.Hour}, {"a", "a", time.Hour}, {"a", "owner", time.Hour}, {"a", "c", 8 * 24 * time.Hour}} {
		_, err := s.db.Exec(`INSERT INTO messages (id,conversation_id,sender,recipient,type,body,hop,idem_key,created_at,expires_at,state) VALUES (?,?,?,?,?,?,?,?,?,?,?)`, fmt.Sprint(i), "conv", v.from, v.to, "request", "", 0, fmt.Sprint(i), now.Add(-v.age).UnixNano(), now.UnixNano(), "stored")
		if err != nil {
			t.Fatal(err)
		}
	}
	talk, err := s.GraphTalkCounts()
	if err != nil {
		t.Fatal(err)
	}
	if len(talk) != 2 || talk[0].From != "cogforge" || talk[0].To != "shop" || talk[0].Count != 1 || talk[0].HasEdge || talk[1].Count != 2 || !talk[1].HasEdge {
		t.Fatalf("talk: %+v", talk)
	}
}
