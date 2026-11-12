package gateway

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

func TestGraphRefsGateAndLegacyCompatibility(t *testing.T) {
	s, _ := newTestStore(t)
	_, node := graphSessions(t, s, "demo")
	g := &Gateway{store: s}
	nc := &nodeConn{id: node}
	if _, err := g.handle(context.Background(), nc, "hello", []byte(`{"version":"v0.4.144","hostname":"test"}`)); err != nil {
		t.Fatal(err)
	}
	if b, err := g.graphRefsBlockers(context.Background(), s.db, false); err != nil || len(b) == 0 {
		t.Fatal("old daemon passed gate")
	}
	if _, err := g.handleGraphRead([]byte(`{"project":"demo"}`)); err != nil {
		t.Fatal(err)
	}
	if _, err := g.handle(context.Background(), nc, "hello", []byte(`{"version":"v0.4.145","hostname":"test","graph_refs":true}`)); err != nil {
		t.Fatal(err)
	}
	if b, err := g.graphRefsBlockers(context.Background(), s.db, false); err != nil || len(b) != 0 {
		t.Fatalf("blockers %v: %v", b, err)
	}
	offline, _, err := s.CreateNode("offline", "linux")
	if err != nil {
		t.Fatal(err)
	}
	if b, err := g.graphRefsBlockers(context.Background(), s.db, false); err != nil || len(b) == 0 {
		t.Fatal("unreported offline node passed gate")
	}
	if _, err := s.db.Exec(`UPDATE nodes SET removed=1 WHERE id=?`, offline); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`UPDATE graph_ref_gate SET enabled=1 WHERE id=1`); err != nil {
		t.Fatal(err)
	}
	if err := s.loadGraphRefs(); err != nil {
		t.Fatal(err)
	}
	if _, err := g.handle(context.Background(), nc, "hello", []byte(`{"version":"v0.4.144","hostname":"test"}`)); err != nil {
		t.Fatalf("old hello after cutover: %v", err)
	}
	if _, err := g.handle(context.Background(), nc, "session.sync", []byte(`{}`)); err != nil {
		t.Fatalf("old session sync after cutover: %v", err)
	}
	for _, frame := range []string{"graph.get", "graph.read", "query.graph", "team.write", "charter.set", "edge.set", "node.add", "repo.create", "agent.tombstone", "cto.graph_history"} {
		if err := s.graphFrameAllowed(nc, frame); err == nil {
			t.Errorf("old connection allowed %s", frame)
		}
	}
}

func TestGraphRefsReadAndCTOWrite(t *testing.T) {
	s, _ := newTestStore(t)
	_, node := graphSessions(t, s, "demo")
	if err := s.SetNodeGraphRefs(node, "v0.4.145", true); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`UPDATE graph_ref_gate SET enabled=1 WHERE id=1`); err != nil {
		t.Fatal(err)
	}
	if err := s.loadGraphRefs(); err != nil {
		t.Fatal(err)
	}
	repo := protocol.GraphRef{Kind: "repo", ID: "demo"}
	agent := protocol.GraphRef{Kind: "agent", ID: "demo"}
	if err := s.SetRefCharter(agent, "purpose", "Actor purpose", "owner", false); err != nil {
		t.Fatal(err)
	}
	if err := s.SetRefCharter(repo, "purpose", "Repo purpose", "owner", false); err != nil {
		t.Fatal(err)
	}
	if err := s.SetRefCharter(agent, "refuses", "deploy", "CTO (minor)", false); err == nil {
		t.Fatal("agent authority bypass")
	}
	graph, err := s.GraphForRef(&agent)
	if err != nil || len(graph.Nodes) != 1 || graph.Nodes[0].Charter.Purpose != "Actor purpose" {
		t.Fatalf("agent: %+v %v", graph, err)
	}
	graph, err = s.GraphForRef(&repo)
	if err != nil || len(graph.Nodes) != 1 || graph.Nodes[0].Charter.Purpose != "Repo purpose" {
		t.Fatalf("repo: %+v %v", graph, err)
	}
	if err = s.SetRefCharter(agent, "rules", "context", "cto", false); err != nil {
		t.Fatal(err)
	}
	history, err := s.CTOGraphHistory(0)
	if err != nil || len(history) != 1 || history[0].Ref == nil || *history[0].Ref != agent {
		t.Fatalf("typed CTO history: %+v %v", history, err)
	}
	class, err := s.CTOGraphClass("charter_set", json.RawMessage(`{"ref":{"kind":"agent","id":"demo"},"attr":"purpose","value":"change"}`))
	if err != nil || class != "minor" {
		t.Fatalf("class: %s %v", class, err)
	}
	if _, err := s.GraphForRef(&protocol.GraphRef{Kind: "bogus", ID: "demo"}); err == nil {
		t.Fatal("invalid kind accepted")
	}
}

func TestGraphRefsAgentHistoryAndUndo(t *testing.T) {
	s, _ := newTestStore(t)
	graphSessions(t, s, "demo")
	if _, err := s.db.Exec(`UPDATE graph_ref_gate SET enabled=1 WHERE id=1`); err != nil {
		t.Fatal(err)
	}
	if err := s.loadGraphRefs(); err != nil {
		t.Fatal(err)
	}
	ref := protocol.GraphRef{Kind: "agent", ID: "demo"}
	if err := s.SetRefCharter(ref, "purpose", "actor", "owner", false); err != nil {
		t.Fatal(err)
	}
	h, err := s.RefCharterHistory(ref, 5)
	if err != nil || len(h) != 1 || h[0].Ref == nil || *h[0].Ref != ref {
		t.Fatalf("history: %+v %v", h, err)
	}
	if err = s.GraphUndo(h[0].ID, "CTO (minor)"); err == nil {
		t.Fatal("minor undo changed agent")
	}
	if err = s.GraphUndo(h[0].ID, "owner"); err != nil {
		t.Fatal(err)
	}
	got, err := s.GraphForRef(&ref)
	if err != nil || got.Nodes[0].Charter.Purpose != "" {
		t.Fatalf("undo: %+v %v", got, err)
	}
}

func TestGraphRefsAutomaticCutoverBacksUpAndMigratesCharter(t *testing.T) {
	g, err := New(Options{DataDir: seededDataDir(t)})
	if err != nil {
		t.Fatal(err)
	}
	defer g.Close()
	s := g.store
	node, _, err := s.CreateNode("fixture", "linux")
	if err != nil {
		t.Fatal(err)
	}
	if err = s.AddArchitect("advisor", "owner"); err != nil {
		t.Fatal(err)
	}
	if err = s.SetCharter("advisor", "purpose", "Legacy actor", "owner"); err != nil {
		t.Fatal(err)
	}
	if _, err = g.handle(context.Background(), &nodeConn{id: node}, "hello", []byte(`{"version":"v0.4.145","hostname":"fixture","graph_refs":true}`)); err != nil {
		t.Fatal(err)
	}
	waitGraphCutoverOn(t, g)
	on, err := s.graphRefsEnabled()
	if err != nil || !on {
		t.Fatalf("gate: %v %v", on, err)
	}
	backup := graphCutoverBackup(t, g)
	snapshot, err := OpenStore(filepath.Join(backup, "gateway.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer snapshot.Close()
	on, err = snapshot.graphRefsEnabled()
	if err != nil || on {
		t.Fatalf("backup is not pre-cutover: %v %v", on, err)
	}
	actorRef := protocol.GraphRef{Kind: "agent", ID: "advisor"}
	history, err := s.RefCharterHistory(actorRef, 5)
	if err != nil || len(history) != 1 {
		t.Fatalf("legacy actor history: %+v %v", history, err)
	}
	if err = s.GraphUndo(history[0].ID, "owner"); err != nil {
		t.Fatal(err)
	}
	actorGraph, err := s.GraphForRef(&actorRef)
	if err != nil || actorGraph.Nodes[0].Charter.Purpose != "" {
		t.Fatalf("legacy actor undo: %+v %v", actorGraph, err)
	}
	if err = s.SetCharter("advisor", "purpose", "Adapted actor", "owner"); err != nil {
		t.Fatal(err)
	}
	actorGraph, err = s.GraphForRef(&actorRef)
	if err != nil || actorGraph.Nodes[0].Charter.Purpose != "Adapted actor" {
		t.Fatalf("legacy actor write: %+v %v", actorGraph, err)
	}
	fresh := &nodeConn{id: node}
	if err = s.graphFrameAllowed(fresh, "graph.get"); err == nil {
		t.Fatal("new connection inherited old hello authority")
	}
}

func TestGraphRefsTypedEdgesKeepRoutingKeys(t *testing.T) {
	s, _ := newTestStore(t)
	ids, _ := graphSessions(t, s, "demo", "other")
	e := protocol.Edge{From: "demo", To: "other", Kind: "depends", Rule: "ask", FromRef: &protocol.GraphRef{Kind: "repo", ID: "demo"}, ToRef: &protocol.GraphRef{Kind: "repo", ID: "other"}}
	if err := s.SetEdge(e, "owner"); err == nil {
		t.Fatal("typed write passed before cutover")
	}
	if _, err := s.db.Exec(`UPDATE graph_ref_gate SET enabled=1 WHERE id=1`); err != nil {
		t.Fatal(err)
	}
	if err := s.loadGraphRefs(); err != nil {
		t.Fatal(err)
	}
	if err := s.SetEdge(e, "owner"); err != nil {
		t.Fatal(err)
	}
	rule, err := s.RequestRule(ids["demo"], ids["other"])
	if err != nil || rule != "ask" {
		t.Fatalf("routing changed: %q %v", rule, err)
	}
	e.FromRef.Kind = "agent"
	if err = s.SetEdge(e, "owner"); err == nil {
		t.Fatal("mismatched endpoint accepted")
	}
}

func TestGraphRefsCachedGate(t *testing.T) {
	s, _ := newTestStore(t)
	if _, err := s.db.Exec(`UPDATE graph_ref_gate SET enabled=1 WHERE id=1`); err != nil {
		t.Fatal(err)
	}
	reopened, err := OpenStore(s.path)
	if err != nil {
		t.Fatal(err)
	}
	if err = reopened.Close(); err != nil {
		t.Fatal(err)
	}
	enabled, err := reopened.graphRefsEnabled()
	if err != nil || !enabled {
		t.Fatalf("startup cache: %v %v", enabled, err)
	}
	nc := &nodeConn{}
	if err = reopened.graphFrameAllowed(nc, "graph.get"); err == nil {
		t.Fatal("unqualified frame allowed")
	}
	nc.graphRefs.Store(true)
	if err = reopened.graphFrameAllowed(nc, "graph.get"); err != nil {
		t.Fatal(err)
	}
}

func TestGraphRefsMigrationReplayIdempotent(t *testing.T) {
	s, _ := newTestStore(t)
	graphSessions(t, s, "demo")
	if err := s.SetAgentCharter("demo", "purpose", "Actor purpose", "owner"); err != nil {
		t.Fatal(err)
	}
	version := migrationVersion(t, "Typed Graph capability reports")
	for i := 0; i < 2; i++ {
		if _, err := s.db.Exec(`DELETE FROM schema_version WHERE version>=?`, version); err != nil {
			t.Fatal(err)
		}
		if err := migrateOldFixture(t, s); err != nil {
			t.Fatal(err)
		}
		var count int
		if err := s.db.QueryRow(`SELECT COUNT(*) FROM graph_history WHERE type='agent_charter' AND node='demo'`).Scan(&count); err != nil || count != 1 {
			t.Fatalf("replay %d: %d rows, %v", i, count, err)
		}
	}
}
