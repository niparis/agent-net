package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

func TestGraphLifecycleGateAuthorityAndUndo(t *testing.T) {
	s, _ := newTestStore(t)
	node, _, err := s.CreateNode("fixture", "linux")
	if err != nil {
		t.Fatal(err)
	}
	owner, err := s.UpsertSession(node, protocol.SessionInfo{Harness: protocol.OwnerHarness, NativeID: protocol.OwnerNativeID, Delivery: "owner"})
	if err != nil {
		t.Fatal(err)
	}
	peer, err := s.UpsertSession(node, protocol.SessionInfo{Harness: "pi", NativeID: "peer"})
	if err != nil {
		t.Fatal(err)
	}
	g := &Gateway{store: s}
	nc := &nodeConn{id: node}
	nc.graphRefs.Store(true)
	call := func(typ string, args map[string]any) error {
		t.Helper()
		b, _ := json.Marshal(args)
		_, err := g.handle(context.Background(), nc, typ, b)
		return err
	}
	for _, typ := range []string{"repo.create", "repo.archive", "agent.create", "agent.tombstone"} {
		err := call(typ, map[string]any{"owner": owner, "name": "test", "ref": map[string]string{"kind": strings.Split(typ, ".")[0], "id": "test"}})
		if err == nil || !strings.Contains(err.Error(), "cutover") {
			t.Errorf("%s before cutover: %v", typ, err)
		}
	}
	if _, err = s.db.Exec(`UPDATE graph_ref_gate SET enabled=1`); err != nil {
		t.Fatal(err)
	}
	if err = s.loadGraphRefs(); err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"repo", "agent"} {
		name := "new-" + kind
		if err = call(kind+".create", map[string]any{"name": name, "owner": peer}); err == nil {
			t.Fatalf("%s peer create", kind)
		}
		if err = call(kind+".create", map[string]any{"name": name, "owner": owner, "by": "CTO (owner window)"}); err == nil {
			t.Fatalf("%s CTO direct create", kind)
		}
		if err = call(kind+".create", map[string]any{"name": name, "owner": owner}); err != nil {
			t.Fatalf("%s create: %v", kind, err)
		}
		if err = call(kind+".create", map[string]any{"name": name, "owner": owner}); err == nil {
			t.Fatalf("%s duplicate", kind)
		}
		var createID int64
		if err = s.db.QueryRow(`SELECT MAX(id) FROM graph_history`).Scan(&createID); err != nil {
			t.Fatal(err)
		}
		action := "archive"
		if kind == "agent" {
			action = "tombstone"
		}
		ref := map[string]string{"kind": kind, "id": name}
		if err = call(kind+"."+action, map[string]any{"ref": ref, "owner": peer}); err == nil {
			t.Fatalf("%s peer", action)
		}
		if err = call(kind+"."+action, map[string]any{"ref": ref, "owner": owner, "by": "cto"}); err == nil {
			t.Fatalf("%s CTO direct", action)
		}
		if err = call(kind+"."+action, map[string]any{"ref": ref, "owner": owner}); err != nil {
			t.Fatalf("%s: %v", action, err)
		}
		var archiveID int64
		if err = s.db.QueryRow(`SELECT MAX(id) FROM graph_history`).Scan(&archiveID); err != nil {
			t.Fatal(err)
		}
		if err = s.GraphUndo(archiveID, "owner"); err != nil {
			t.Fatalf("undo %s: %v", action, err)
		}
		if err = s.GraphUndo(createID, "owner"); err != nil {
			t.Fatalf("undo create %s: %v", kind, err)
		}
		var undoID int64
		s.db.QueryRow(`SELECT MAX(id) FROM graph_history`).Scan(&undoID)
		if err = s.GraphUndo(undoID, "owner"); err != nil {
			t.Fatalf("undo undo create %s: %v", kind, err)
		}
	}
}

func TestGraphLifecycleReferences(t *testing.T) {
	s, _ := newTestStore(t)
	session, node := graphSessions(t, s, "demo")
	owner, err := s.UpsertSession(node, protocol.SessionInfo{Harness: protocol.OwnerHarness, NativeID: protocol.OwnerNativeID, Delivery: "owner"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.Exec(`UPDATE graph_ref_gate SET enabled=1`); err != nil {
		t.Fatal(err)
	}
	if err = s.loadGraphRefs(); err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.Exec(`INSERT INTO graph_edges(source,target,kind,mode,rule,source_kind,target_kind) VALUES('/delivery/caller','demo','reviews','','normal','special','repo')`); err != nil {
		t.Fatal(err)
	}
	if _, err = s.AddProjects(node, protocol.ProjectAdd{Projects: []protocol.ProjectEntry{{Name: "demo", Path: "/sensitive/path"}}}); err != nil {
		t.Fatal(err)
	}
	g := &Gateway{store: s}
	nc := &nodeConn{id: node}
	nc.graphRefs.Store(true)
	for _, tc := range []struct {
		kind, action string
		want         []string
	}{{"repo", "archive", []string{"session", session["demo"], "reviews"}}, {"agent", "tombstone", []string{"repo", "demo"}}} {
		b, _ := json.Marshal(map[string]any{"owner": owner, "ref": map[string]string{"kind": tc.kind, "id": "demo"}})
		_, err = g.handle(context.Background(), nc, tc.kind+"."+tc.action, b)
		if err == nil {
			t.Fatalf("%s accepted references", tc.action)
		}
		if strings.Contains(err.Error(), "/sensitive/path") {
			t.Fatalf("checkout path exposed: %v", err)
		}
		for _, want := range tc.want {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("%s missing %q: %v", tc.action, want, err)
			}
		}
	}
}

func TestGraphLifecycleMigrationDuplicates(t *testing.T) {
	s, _ := newTestStore(t)
	_, err := s.db.Exec(`DROP TRIGGER IF EXISTS repos_unique_name_insert; DROP TRIGGER IF EXISTS repos_unique_name_update;
 DROP TRIGGER IF EXISTS agents_unique_name_insert; DROP TRIGGER IF EXISTS agents_unique_name_update;
 DROP TRIGGER repo_live_owner_insert; DROP TRIGGER repo_live_owner_update; DROP TRIGGER agent_owned_tombstone;
 DROP TRIGGER IF EXISTS session_lifecycle_insert; DROP TRIGGER IF EXISTS session_lifecycle_update;
 DROP TABLE archived_repos; DELETE FROM schema_version WHERE version>=121;
 INSERT INTO agents(id,display_name,created_at,updated_at) VALUES('a1','Duplicate',0,0),('a2','duplicate',0,0);
 INSERT INTO repos(id,display_name,created_at,updated_at) VALUES('r1','Shared',0,0),('r2','shared',0,0)`)
	if err != nil {
		t.Fatal(err)
	}
	// An interrupted replay can leave old versions of the name triggers behind.
	if _, err = s.db.Exec(`CREATE TRIGGER repos_unique_name_insert BEFORE INSERT ON repos BEGIN SELECT RAISE(ABORT, 'old repo trigger'); END;
 CREATE TRIGGER agents_unique_name_insert BEFORE INSERT ON agents BEGIN SELECT RAISE(ABORT, 'old agent trigger'); END;`); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if i > 0 {
			if _, err = s.db.Exec(`DELETE FROM schema_version WHERE version>=121`); err != nil {
				t.Fatal(err)
			}
		}
		if err = migrateOldFixture(t, s); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = s.db.Exec(`UPDATE graph_ref_gate SET enabled=1`); err != nil {
		t.Fatal(err)
	}
	if err = s.loadGraphRefs(); err != nil {
		t.Fatal(err)
	}
	g, err := s.GraphForRef(nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(g.Warnings) != 2 {
		t.Fatalf("duplicate warnings: %v", g.Warnings)
	}
	for _, kind := range []string{"repo", "agent"} {
		name := "Shared"
		if kind == "agent" {
			name = "DUPLICATE"
		}
		if err = s.GraphLifecycle(kind+".create", protocol.GraphLifecycleRequest{Name: name}, "owner", false); err == nil {
			t.Fatalf("%s duplicate accepted", kind)
		}
	}
	// Automatic/legacy writes may introduce collisions, which Graph reports.
	if _, err = s.db.Exec(`INSERT INTO repos(id,display_name,created_at,updated_at) VALUES('r3','SHARED',0,0); INSERT INTO agents(id,display_name,created_at,updated_at) VALUES('a3','DUPLICATE',0,0)`); err != nil {
		t.Fatalf("legacy duplicate insert: %v", err)
	}
	for _, kind := range []string{"repo", "agent"} {
		if err = s.GraphLifecycle(kind+".create", protocol.GraphLifecycleRequest{Name: "namesake"}, "owner", false); err != nil {
			t.Fatalf("namesake %s: %v", kind, err)
		}
	}
}

func TestGraphLifecycleProposalAndRetainedData(t *testing.T) {
	s, _ := newTestStore(t)
	node, _, err := s.CreateNode("fixture", "linux")
	if err != nil {
		t.Fatal(err)
	}
	owner, err := s.UpsertSession(node, protocol.SessionInfo{Harness: protocol.OwnerHarness, NativeID: protocol.OwnerNativeID, Delivery: "owner"})
	if err != nil {
		t.Fatal(err)
	}
	nc := &nodeConn{id: node}
	nc.graphRefs.Store(true)
	g := &Gateway{store: s}
	ctx := context.Background()
	if _, err = s.db.Exec(`UPDATE graph_ref_gate SET enabled=1`); err != nil {
		t.Fatal(err)
	}
	if err = s.loadGraphRefs(); err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"repo", "agent"} {
		create := kind + "_create"
		name := "proposal-" + kind
		args := map[string]any{"name": name}
		req, _ := json.Marshal(map[string]any{"tool": create, "args": args, "owner": owner})
		if _, err = g.validateCTOProposal(ctx, nc, req); err != nil {
			t.Fatal(err)
		}
		class, err := s.CTOGraphClass(create, []byte(`{}`))
		if err != nil || class != "rule" {
			t.Fatalf("classification %s %v", class, err)
		}
		payload, _ := json.Marshal(map[string]any{"name": name, "owner": owner, "by": "owner (CTO proposal p_1)"})
		if _, err = g.handle(ctx, nc, kind+".create", payload); err != nil {
			t.Fatal(err)
		}
		ref := protocol.GraphRef{Kind: kind, ID: name}
		if err = s.SetRefCharter(ref, "purpose", "keep this", "owner", false); err != nil {
			t.Fatalf("charter: %v", err)
		}
		action := "archive"
		if kind == "agent" {
			action = "tombstone"
		}
		raw, _ := json.Marshal(map[string]any{"ref": ref, "owner": owner, "by": "owner (CTO proposal p_2)"})
		if _, err = g.handle(ctx, nc, kind+"."+action, raw); err != nil {
			t.Fatal(err)
		}
		graph, err := s.GraphForRef(&ref)
		if err != nil || len(graph.Nodes) != 0 {
			t.Fatalf("inactive graph: %+v %v", graph, err)
		}
		h, err := s.RefCharterHistory(ref, 20)
		if err != nil || len(h) < 3 {
			t.Fatalf("history %+v %v", h, err)
		}
		class, err = s.CTOGraphClass("graph_undo", json.RawMessage(`{"id":0}`))
		if err != nil || class != "rule" {
			t.Fatalf("undo class %s %v", class, err)
		}
		if err = s.GraphUndo(h[0].ID, "CTO (owner window)"); err == nil {
			t.Fatal("CTO window undid lifecycle")
		}
		if err = s.GraphUndo(h[0].ID, "owner (CTO proposal p_3)"); err != nil {
			t.Fatal(err)
		}
		graph, err = s.GraphForRef(&ref)
		if err != nil || len(graph.Nodes) != 1 || graph.Nodes[0].Charter.Purpose != "keep this" {
			t.Fatalf("restored graph: %+v %v", graph, err)
		}
	}
}

func TestGraphLifecycleEdgeUndoCannotAttachInactiveIdentity(t *testing.T) {
	s, _ := newTestStore(t)
	if _, err := s.db.Exec(`UPDATE graph_ref_gate SET enabled=1`); err != nil {
		t.Fatal(err)
	}
	if err := s.loadGraphRefs(); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"first", "second"} {
		if err := s.GraphLifecycle("repo.create", protocol.GraphLifecycleRequest{Name: name}, "owner", false); err != nil {
			t.Fatal(err)
		}
	}
	edge := protocol.Edge{From: "first", To: "second", Kind: "depends", FromRef: &protocol.GraphRef{Kind: "repo", ID: "first"}, ToRef: &protocol.GraphRef{Kind: "repo", ID: "second"}}
	if err := s.SetEdge(edge, "owner"); err != nil {
		t.Fatal(err)
	}
	if err := s.RemoveEdge(edge, "owner"); err != nil {
		t.Fatal(err)
	}
	var removed int64
	if err := s.db.QueryRow(`SELECT MAX(id) FROM graph_history`).Scan(&removed); err != nil {
		t.Fatal(err)
	}
	if err := s.GraphLifecycle("repo.archive", protocol.GraphLifecycleRequest{Ref: &protocol.GraphRef{Kind: "repo", ID: "second"}}, "owner", false); err != nil {
		t.Fatal(err)
	}
	if err := s.GraphUndo(removed, "owner"); err == nil || !strings.Contains(err.Error(), "inactive") {
		t.Fatalf("undo restored edge to archived repo: %v", err)
	}
	edge.FromRef = nil
	edge.ToRef = nil
	if err := s.SetEdge(edge, "owner"); err == nil || !strings.Contains(err.Error(), "inactive") {
		t.Fatalf("legacy edge to archived repo: %v", err)
	}
}

func TestGraphLifecycleRepoNamesakeOfLegacyArchitect(t *testing.T) {
	s, _ := newTestStore(t)
	if err := s.AddArchitect("advisor", "owner"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetCharter("advisor", "purpose", "actor only", "owner"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`INSERT INTO agent_charter SELECT node,attr,value,updated_at,updated_by FROM graph_charter WHERE node='advisor'; UPDATE graph_ref_gate SET enabled=1`); err != nil {
		t.Fatal(err)
	}
	if err := s.loadGraphRefs(); err != nil {
		t.Fatal(err)
	}
	if err := s.GraphLifecycle("repo.create", protocol.GraphLifecycleRequest{Name: "advisor"}, "owner", false); err != nil {
		t.Fatal(err)
	}
	graph, err := s.GraphForRef(nil)
	if err != nil {
		t.Fatal(err)
	}
	var repo, agent *protocol.Node
	for i := range graph.Nodes {
		n := &graph.Nodes[i]
		if n.Name == "advisor" {
			if n.Kind == "repo" {
				repo = n
			}
			if n.Kind == "agent" {
				agent = n
			}
		}
	}
	if repo == nil || agent == nil || repo.Charter.Purpose != "" || agent.Charter.Purpose != "actor only" {
		t.Fatalf("namesakes: %+v", graph.Nodes)
	}
	if err = s.SetRefCharter(*repo.Ref, "purpose", "repo only", "owner", false); err != nil {
		t.Fatal(err)
	}
	graph, err = s.GraphForRef(agent.Ref)
	if err != nil || len(graph.Nodes) != 1 || graph.Nodes[0].Charter.Purpose != "actor only" {
		t.Fatalf("agent changed: %+v %v", graph, err)
	}
}

func TestGraphLifecycleAutomaticRegistrationNames(t *testing.T) {
	for _, kind := range []string{"session", "project"} {
		t.Run(kind, func(t *testing.T) {
			s, _ := newTestStore(t)
			node, _, err := s.CreateNode("fixture", "linux")
			if err != nil {
				t.Fatal(err)
			}
			if _, err = s.db.Exec(`UPDATE graph_ref_gate SET enabled=1`); err != nil {
				t.Fatal(err)
			}
			if err = s.loadGraphRefs(); err != nil {
				t.Fatal(err)
			}
			// A Repo and an Agent are independently owner-created with lower-case names.
			for _, tc := range []struct{ action, name string }{{"repo.create", "shop"}, {"agent.create", "reviewer"}} {
				if err = s.GraphLifecycle(tc.action, protocol.GraphLifecycleRequest{Name: tc.name}, "owner", false); err != nil {
					t.Fatal(err)
				}
			}
			if kind == "session" {
				// Upsert uses both the insert and update triggers across repeated discovery.
				for i := 0; i < 2; i++ {
					_, err = s.UpsertSession(node, protocol.SessionInfo{Harness: "pi", NativeID: "worker", Repo: protocol.RepoInfo{Project: "Shop", Root: "/code/Shop"}})
					if err != nil {
						t.Fatalf("register session with namesake repo: %v", err)
					}
				}
			} else {
				// A new path and an update of an existing path both keep working.
				for _, name := range []string{"another", "Reviewer", "Reviewer"} {
					_, err = s.AddProjects(node, protocol.ProjectAdd{Projects: []protocol.ProjectEntry{{Name: name, Path: "/code/reviewer"}}})
					if err != nil {
						t.Fatalf("register project with namesake agent: %v", err)
					}
				}
			}
		})
	}
}

func TestGraphLifecycleAutomaticRegistrationTombstonedOwner(t *testing.T) {
	for _, tc := range []struct {
		kind         string
		existingRepo bool
	}{{"session", false}, {"session", true}, {"project", false}, {"project", true}} {
		t.Run(fmt.Sprintf("%s_existing_repo_%t", tc.kind, tc.existingRepo), func(t *testing.T) {
			s, _ := newTestStore(t)
			node, _, err := s.CreateNode("fixture", "linux")
			if err != nil {
				t.Fatal(err)
			}
			if _, err = s.db.Exec(`UPDATE graph_ref_gate SET enabled=1`); err != nil {
				t.Fatal(err)
			}
			if err = s.loadGraphRefs(); err != nil {
				t.Fatal(err)
			}
			if err = s.GraphLifecycle("agent.create", protocol.GraphLifecycleRequest{Name: "former"}, "owner", false); err != nil {
				t.Fatal(err)
			}
			if err = s.GraphLifecycle("agent.tombstone", protocol.GraphLifecycleRequest{Ref: &protocol.GraphRef{Kind: "agent", ID: "former"}}, "owner", false); err != nil {
				t.Fatal(err)
			}
			if tc.existingRepo {
				if err = s.GraphLifecycle("repo.create", protocol.GraphLifecycleRequest{Name: "former"}, "owner", false); err != nil {
					t.Fatal(err)
				}
			}
			for i := 0; i < 2; i++ {
				if tc.kind == "session" {
					_, err = s.UpsertSession(node, protocol.SessionInfo{Harness: "pi", NativeID: "worker", Repo: protocol.RepoInfo{Project: "former", Root: "/code/former"}})
				} else {
					_, err = s.AddProjects(node, protocol.ProjectAdd{Projects: []protocol.ProjectEntry{{Name: "former", Path: "/code/former"}}})
				}
				if err != nil {
					t.Fatalf("registration: %v", err)
				}
			}
			var owner *string
			var tombstone bool
			if err = s.db.QueryRow(`SELECT owner_agent_id FROM repos WHERE id='former'`).Scan(&owner); err != nil {
				t.Fatal(err)
			}
			if err = s.db.QueryRow(`SELECT tombstone FROM agents WHERE id='former'`).Scan(&tombstone); err != nil {
				t.Fatal(err)
			}
			if owner != nil || !tombstone {
				t.Fatalf("repo owner %v, tombstone %v", owner, tombstone)
			}
			if _, err = s.db.Exec(`INSERT INTO repos(id,display_name,created_at,updated_at,owner_agent_id) VALUES('bad','bad',0,0,'former')`); err == nil {
				t.Fatal("tombstoned agent assigned on insert")
			}
			if _, err = s.db.Exec(`UPDATE repos SET owner_agent_id='former' WHERE id='former'`); err == nil {
				t.Fatal("tombstoned agent assigned on update")
			}
		})
	}
}
