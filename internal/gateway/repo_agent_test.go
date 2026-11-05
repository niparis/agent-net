package gateway

import (
	"reflect"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

func TestRepoAgentRecordsFollowLegacyCreation(t *testing.T) {
	s, _ := newTestStore(t)
	node, _, err := s.CreateNode("test", "linux")
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.AddProjects(node, protocol.ProjectAdd{Projects: []protocol.ProjectEntry{{Name: "demo", Path: "/code/demo"}}})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.AddArchitect("advisor", "owner"); err != nil {
		t.Fatal(err)
	}
	var owner, role string
	if err = s.db.QueryRow(`SELECT owner_agent_id FROM repos WHERE id='demo'`).Scan(&owner); err != nil || owner != "demo" {
		t.Fatalf("repo owner %q: %v", owner, err)
	}
	if err = s.db.QueryRow(`SELECT role FROM agents WHERE id='advisor' AND tombstone=0`).Scan(&role); err != nil || role != "architect" {
		t.Fatalf("architect role %q: %v", role, err)
	}
	if err = s.RemoveArchitect("advisor", "owner"); err != nil {
		t.Fatal(err)
	}
	var tombstone int
	if err = s.db.QueryRow(`SELECT tombstone FROM agents WHERE id='advisor'`).Scan(&tombstone); err != nil || tombstone != 1 {
		t.Fatalf("tombstone %d: %v", tombstone, err)
	}
}

func TestRepoAgentMigrationPreservesGraphAndDelivery(t *testing.T) {
	r := newDeliveryRig(t)
	if _, err := r.s.AddProjects(r.a, protocol.ProjectAdd{Projects: []protocol.ProjectEntry{{Name: "demo", Path: "/code/demo"}, {Name: "registered", Path: "/code/registered"}}}); err != nil {
		t.Fatal(err)
	}
	if _, err := r.s.UpsertSession(r.a, protocol.SessionInfo{Harness: "pi", NativeID: "session-only", Repo: protocol.RepoInfo{Project: "session-only", Root: "/code/session-only"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := r.s.db.Exec(`INSERT INTO graph_edges(source,target,kind,mode,rule) VALUES('*','edge-only','depends','','auto')`); err != nil {
		t.Fatal(err)
	}
	if err := r.s.AddArchitect("advisor", "owner"); err != nil {
		t.Fatal(err)
	}
	if err := r.s.SetMerger(protocol.MergerSetRequest{Project: "demo", NodeID: r.a, SessionID: r.sonnet, Configure: true}, "owner"); err != nil {
		t.Fatal(err)
	}
	openChange := r.submit(r.a, r.workerSubmit("feature", commitA)).Change.Change.ID
	mergedChange := r.submit(r.a, r.workerSubmit("merged", commitB)).Change.Change.ID
	if _, err := r.s.db.Exec(`UPDATE delivery_changes SET state='merged',merged_commit=? WHERE id=?`, commitMerged, mergedChange); err != nil {
		t.Fatal(err)
	}
	if err := r.s.SetCharter("demo", "purpose", "Build software", "owner"); err != nil {
		t.Fatal(err)
	}
	// Recreate the state immediately before this migration, keeping the legacy data.
	removeEdgeKindsForTest(t, r.s)
	_, err := r.s.db.Exec(`DROP TRIGGER repo_agent_project_insert; DROP TRIGGER repo_agent_project_update; DROP TRIGGER repo_agent_session_insert; DROP TRIGGER repo_agent_session_update; DROP TRIGGER repo_agent_architect_insert; DROP TRIGGER repo_agent_architect_delete; DROP TABLE repos; DROP TABLE agents; DELETE FROM schema_version WHERE version>=?`, migrationVersion(t, "Repo and agent records"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.s.db.Exec(`DELETE FROM delivery_step_tasks`); err != nil {
		t.Fatal(err)
	}
	if err := r.s.backfillDeliveryTasks(); err != nil {
		t.Fatal(err)
	}
	beforeTasks := repoAgentTaskSnapshot(t, r.s)
	seen := map[string]bool{}
	for _, task := range beforeTasks {
		seen[task[0]+":"+task[4]] = true
	}
	if !seen[openChange+":0"] || !seen[mergedChange+":1"] {
		t.Fatalf("expected open and merged backfill tasks, got %+v", beforeTasks)
	}
	before := repoAgentLegacySnapshot(t, r.s, r.caller, r.sonnet, openChange, mergedChange)
	if _, err := r.s.db.Exec(`DELETE FROM delivery_step_tasks`); err != nil {
		t.Fatal(err)
	}
	path := r.s.path
	if err = r.s.Close(); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		s, err := OpenStore(path)
		if err != nil {
			t.Fatal(err)
		}
		// Match the clock used for the legacy snapshot when graph visibility is evaluated.
		s.now = func() time.Time { return time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC) }
		if got := repoAgentTaskSnapshot(t, s); !reflect.DeepEqual(beforeTasks, got) {
			t.Fatalf("delivery backfill changed on open %d: before %+v after %+v", i, beforeTasks, got)
		}
		if got := repoAgentLegacySnapshot(t, s, r.caller, r.sonnet, openChange, mergedChange); !reflect.DeepEqual(before, got) {
			t.Fatalf("legacy graph or delivery reads changed on open %d: before %+v after %+v", i, before, got)
		}
		repos, agents, err := s.RepoAgents()
		if err != nil {
			t.Fatal(err)
		}
		if len(repos) != 4 || len(agents) != 5 {
			t.Fatalf("repos %+v agents %+v", repos, agents)
		}
		for _, repo := range repos {
			if repo.OwnerAgentID == nil || *repo.OwnerAgentID != repo.ID {
				t.Fatalf("owner: %+v", repo)
			}
		}
		if agents[0].ID != "advisor" || agents[0].Role != "architect" {
			t.Fatalf("architect: %+v", agents)
		}
		if err = s.Close(); err != nil {
			t.Fatal(err)
		}
	}
}

// Exclude creation time: backfill records the time of each run.
func repoAgentTaskSnapshot(t *testing.T, s *Store) [][7]string {
	t.Helper()
	rows, err := s.db.Query(`SELECT change_id,revision,step,for_step,after_merge,owner_node,completed FROM delivery_step_tasks ORDER BY change_id,revision,step,for_step,after_merge`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out [][7]string
	for rows.Next() {
		var v [7]string
		if err := rows.Scan(&v[0], &v[1], &v[2], &v[3], &v[4], &v[5], &v[6]); err != nil {
			t.Fatal(err)
		}
		out = append(out, v)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

func repoAgentLegacySnapshot(t *testing.T, s *Store, caller, other, openID, mergedID string) []any {
	t.Helper()
	var out []any
	for _, project := range []string{"", "demo", "advisor", "missing"} {
		graph, err := s.GraphFor(project)
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, graph)
		for _, all := range []bool{false, true} {
			view, err := s.graphView(project, 10, all)
			if err != nil {
				t.Fatal(err)
			}
			out = append(out, view)
		}
	}
	history, err := s.CharterHistory("demo", 100)
	if err != nil {
		t.Fatal(err)
	}
	out = append(out, history)
	ctoHistory, err := s.CTOGraphHistory(0)
	if err != nil {
		t.Fatal(err)
	}
	out = append(out, ctoHistory)
	last, err := s.CTOLastGraphID()
	if err != nil {
		t.Fatal(err)
	}
	out = append(out, last)
	relation, err := s.RelationFor(caller, other)
	if err != nil {
		t.Fatal(err)
	}
	out = append(out, relation)
	rule, err := s.RequestRule(caller, other)
	if err != nil {
		t.Fatal(err)
	}
	out = append(out, rule)
	for _, id := range []string{openID, mergedID} {
		change, err := s.GetChange(id)
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, change)
	}
	return out
}
