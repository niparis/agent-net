package gateway

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

func TestEdgeKindsMetadata(t *testing.T) {
	s, err := OpenStore(seededStorePath(t, "edge-kinds.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	for _, n := range []string{"a", "b"} {
		if err := s.AddArchitect(n, "owner"); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.SetEdge(protocol.Edge{From: "a", To: "b", Kind: "depends", Rule: "normal"}, "owner"); err != nil {
		t.Fatal(err)
	}
	var source, target string
	if err := s.db.QueryRow(`SELECT source_kind,target_kind FROM graph_edges WHERE source='a' AND target='b'`).Scan(&source, &target); err != nil {
		t.Fatal(err)
	}
	if source != "repo" || target != "repo" {
		t.Fatalf("kinds %q %q", source, target)
	}
	if err := s.db.QueryRow(`SELECT source_kind,target_kind FROM graph_history WHERE type='edge' ORDER BY id DESC LIMIT 1`).Scan(&source, &target); err != nil {
		t.Fatal(err)
	}
	if source != "repo" || target != "repo" {
		t.Fatalf("history kinds %q %q", source, target)
	}
}

// Return a freshly opened store to the schema just before typed edge metadata.
func removeEdgeKindsForTest(t *testing.T, s *Store) {
	t.Helper()
	if _, err := s.db.Exec(`DROP TRIGGER edge_kinds_insert; DROP TRIGGER edge_history_kinds_insert; DROP TRIGGER merger_removed; DROP TRIGGER merger_replaced;`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(mergerLifecycleMigration()); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`ALTER TABLE graph_edges DROP COLUMN source_kind; ALTER TABLE graph_edges DROP COLUMN target_kind; ALTER TABLE graph_history DROP COLUMN source_kind; ALTER TABLE graph_history DROP COLUMN target_kind; DELETE FROM schema_version WHERE version>=?`, migrationVersion(t, "Typed edge metadata")); err != nil {
		t.Fatal(err)
	}
}

func TestEdgeKindsMigrationPreservesRouting(t *testing.T) {
	r := newDeliveryRig(t)
	if err := r.s.AddArchitect("advisor", "owner"); err != nil {
		t.Fatal(err)
	}
	for _, e := range []protocol.Edge{
		{From: "demo", To: "other", Kind: "depends", Rule: "normal"},
		{From: "advisor", To: "demo", Kind: "governs", Mode: "advisor", Rule: "normal"},
	} {
		if _, err := r.s.db.Exec(`INSERT INTO graph_edges(source,target,kind,mode,rule) VALUES(?,?,?,?,?)`, e.From, e.To, e.Kind, e.Mode, e.Rule); err != nil {
			t.Fatal(err)
		}
	}
	if err := r.s.SetMerger(protocol.MergerSetRequest{Project: "demo", NodeID: r.a, SessionID: r.sonnet, Configure: true}, "owner"); err != nil {
		t.Fatal(err)
	}
	if err := r.s.SetDeliveryOwner(protocol.DeliveryOwnerSetRequest{Project: "demo", Step: "test", Node: "advisor", Expect: "@merger"}, "owner"); err != nil {
		t.Fatal(err)
	}
	open := r.submit(r.a, r.workerSubmit("feature", commitA)).Change.Change.ID
	merged := r.submit(r.a, r.workerSubmit("merged", commitB)).Change.Change.ID
	var acceptOwner string
	if err := r.s.db.QueryRow(`SELECT owner_node FROM delivery_step_tasks WHERE change_id=? AND step='accept' AND after_merge=0`, open).Scan(&acceptOwner); err != nil || acceptOwner != "/caller" {
		t.Fatalf("accept task owner = %q, want /caller, err=%v", acceptOwner, err)
	}
	var preMergeTests int
	if err := r.s.db.QueryRow(`SELECT COUNT(*) FROM delivery_step_tasks WHERE change_id=? AND step='test' AND after_merge=0`, open).Scan(&preMergeTests); err != nil {
		t.Fatal(err)
	}
	if preMergeTests != 0 {
		t.Fatalf("pre-merge test tasks before accept = %d", preMergeTests)
	}
	r.mustReport(r.a, protocol.StepReportRequest{SessionID: r.caller, ChangeID: open, StepRef: protocol.StepRef{Step: protocol.StepAccept}, Result: protocol.StepResultPass, Commit: commitA})
	if _, err := r.s.db.Exec(`UPDATE delivery_changes SET state='merged',merged_commit=? WHERE id=?`, commitMerged, merged); err != nil {
		t.Fatal(err)
	}
	removeEdgeKindsForTest(t, r.s)
	if _, err := r.s.db.Exec(`DELETE FROM delivery_step_tasks`); err != nil {
		t.Fatal(err)
	}
	if err := r.s.backfillDeliveryTasks(); err != nil {
		t.Fatal(err)
	}
	beforeTasks := repoAgentTaskSnapshot(t, r.s)
	foundOpen, foundMerged, foundNode := false, false, false
	for _, task := range beforeTasks {
		if task[0] == open && task[2] == protocol.StepTest && task[4] == "0" {
			foundOpen = true
		}
		if task[0] == merged && task[2] == protocol.StepTest && task[4] == "1" {
			foundMerged = true
		}
		if task[0] == open && task[2] == protocol.StepTest && task[4] == "0" && task[5] == "advisor" {
			foundNode = true
		}
	}
	if !foundOpen || !foundMerged || !foundNode {
		t.Fatalf("missing routed tasks: %v", beforeTasks)
	}
	before := repoAgentLegacySnapshot(t, r.s, r.caller, r.sonnet, open, merged)
	if _, err := r.s.db.Exec(`DELETE FROM delivery_step_tasks`); err != nil {
		t.Fatal(err)
	}
	prepareOldSessionIdentity(t, r.s)
	path := r.s.path
	if err := r.s.Close(); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		s, err := OpenStore(path)
		if err != nil {
			t.Fatal(err)
		}
		s.now = func() time.Time { return time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC) }
		if got := repoAgentTaskSnapshot(t, s); !reflect.DeepEqual(beforeTasks, got) {
			t.Fatalf("tasks before %v after %v", beforeTasks, got)
		}
		if got := repoAgentLegacySnapshot(t, s, r.caller, r.sonnet, open, merged); !reflect.DeepEqual(before, got) {
			t.Fatalf("legacy reads changed: before %v after %v", before, got)
		}
		rows, err := s.db.Query(`SELECT source,target,kind,source_kind,target_kind FROM graph_edges`)
		if err != nil {
			t.Fatal(err)
		}
		for rows.Next() {
			var from, to, kind, sk, tk string
			if err := rows.Scan(&from, &to, &kind, &sk, &tk); err != nil {
				t.Fatal(err)
			}
			wantS, wantT := "agent", "repo"
			if kind == "depends" {
				wantS = "repo"
			}
			if kind == "governs" {
				wantT = "agent"
			}
			if strings.HasPrefix(from, "/delivery/") {
				wantS = "special"
			}
			if sk != wantS || tk != wantT {
				t.Fatalf("edge %s %s %s: %s %s", from, to, kind, sk, tk)
			}
		}
		if err := rows.Close(); err != nil {
			t.Fatal(err)
		}
		var merger string
		if err := s.db.QueryRow(`SELECT session_id FROM project_mergers WHERE project='demo'`).Scan(&merger); err != nil || merger != r.sonnet {
			t.Fatalf("merger %s: %v", merger, err)
		}
		if i == 1 {
			var bad int
			if err := s.db.QueryRow(`SELECT count(*) FROM graph_history WHERE type='edge' AND (source_kind='' OR target_kind='')`).Scan(&bad); err != nil || bad != 0 {
				t.Fatalf("edge history missing kinds: %d %v", bad, err)
			}
			if _, err := s.db.Exec(`DELETE FROM project_mergers WHERE project='demo'`); err != nil {
				t.Fatal(err)
			}
			var count int
			if err := s.db.QueryRow(`SELECT count(*) FROM graph_edges WHERE source='/delivery/caller' AND target='demo' AND source_kind='special' AND target_kind='repo'`).Scan(&count); err != nil || count == 0 {
				t.Fatalf("merger trigger edge: %d %v", count, err)
			}
		}
		if err := s.Close(); err != nil {
			t.Fatal(err)
		}
	}
}
