package gateway

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

func scopeSet(node, kind, over, note string) protocol.ScopeSetRequest {
	return protocol.ScopeSetRequest{Node: node, Kind: kind, Over: over, Note: note}
}

func TestScopeSetRefusesUnknownKind(t *testing.T) {
	s := teamStore(t)
	_, err := s.SetScope(scopeSet("demo", "approve", "all", ""), "owner", false)
	if err == nil || !strings.Contains(err.Error(), "unknown scope kind") {
		t.Fatalf("unknown kind: %v", err)
	}
}

func TestScopeSetRefusesBadOver(t *testing.T) {
	s := teamStore(t)
	teamWrite(t, s, protocol.TeamWrite{Action: "create", ID: "alpha", Name: "Alpha"})
	for _, over := range []string{"", "everything", "repo:", "repo: demo", "machine:demo", "team:nope", "repo:nope", "agent:nope"} {
		if _, err := s.SetScope(scopeSet("demo", protocol.ScopeMerge, over, ""), "owner", false); err == nil {
			t.Errorf("over %q accepted", over)
		}
	}
	if _, err := s.SetScope(scopeSet("nope", protocol.ScopeMerge, "all", ""), "owner", false); err == nil {
		t.Error("unknown node accepted")
	}
	for _, over := range []string{"all", "team:alpha", "repo:demo", "agent:other"} {
		if _, err := s.SetScope(scopeSet("demo", protocol.ScopeMerge, over, ""), "owner", false); err != nil {
			t.Errorf("over %q: %v", over, err)
		}
	}
}

func TestScopeWritesNeedOwner(t *testing.T) {
	s := teamStore(t)
	for _, actor := range []string{"CTO (minor)", "agent"} {
		if _, err := s.SetScope(scopeSet("demo", protocol.ScopeReview, "repo:demo", ""), actor, false); err == nil {
			t.Errorf("%s set a scope", actor)
		}
	}
}

func TestScopeSetAndRemoveReturnRows(t *testing.T) {
	s := teamStore(t)
	row, err := s.SetScope(scopeSet("demo", protocol.ScopeReview, "repo:other", " reviews its consumer "), "cto", false)
	want := protocol.Scope{Node: "demo", Kind: protocol.ScopeReview, Over: "repo:other", Note: "reviews its consumer", SetBy: "cto", SetAt: s.now().UnixNano()}
	if err != nil || row != want {
		t.Fatalf("set: %+v %v", row, err)
	}
	stored, err := s.NodeScopes("demo")
	if err != nil || len(stored) != 1 || stored[0] != want {
		t.Fatalf("stored: %+v %v", stored, err)
	}
	removed, err := s.RemoveScope(scopeSet("demo", protocol.ScopeReview, "repo:other", ""), "owner", false)
	if err != nil || removed != want {
		t.Fatalf("remove: %+v %v", removed, err)
	}
	if _, err := s.RemoveScope(scopeSet("demo", protocol.ScopeReview, "repo:other", ""), "owner", false); err == nil {
		t.Fatal("second remove accepted")
	}
	left, err := s.NodeScopes("demo")
	if err != nil || len(left) != 0 {
		t.Fatalf("after remove: %+v %v", left, err)
	}
	var history int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM node_scopes_history WHERE node='demo'`).Scan(&history); err != nil || history != 2 {
		t.Fatalf("history rows: %d %v", history, err)
	}
}

func TestScopeGraphUndo(t *testing.T) {
	s := teamStore(t)
	scope := scopeSet("demo", protocol.ScopeDeploy, "repo:other", "first")
	if _, err := s.SetScope(scope, "owner", false); err != nil {
		t.Fatal(err)
	}
	scope.Note = "second"
	if _, err := s.SetScope(scope, "owner", false); err != nil {
		t.Fatal(err)
	}
	notes := func() string {
		rows, err := s.NodeScopes("demo")
		if err != nil {
			t.Fatal(err)
		}
		out := []string{}
		for _, r := range rows {
			out = append(out, r.Note)
		}
		return strings.Join(out, ",")
	}
	if err := s.GraphUndo(0, "owner"); err != nil || notes() != "first" {
		t.Fatalf("undo note change: %q %v", notes(), err)
	}
	if _, err := s.RemoveScope(scope, "owner", false); err != nil {
		t.Fatal(err)
	}
	if err := s.GraphUndo(0, "owner"); err != nil || notes() != "first" {
		t.Fatalf("undo remove: %q %v", notes(), err)
	}
	var stale int64
	if err := s.db.QueryRow(`SELECT id FROM graph_history WHERE type='scope' AND node='demo' AND new_value LIKE '%second%'`).Scan(&stale); err != nil {
		t.Fatal(err)
	}
	if err := s.GraphUndo(stale, "owner"); err == nil {
		t.Fatal("stale undo accepted")
	}
}

func TestNodeScopesMigrationSeedsTodaysGrants(t *testing.T) {
	s, _ := newTestStore(t)
	ids, node := graphSessions(t, s, "demo", "other", "app", "chief-of-staff", "wbgpoc-lead", "homelab")
	if _, err := s.db.Exec(`UPDATE graph_ref_gate SET enabled=1 WHERE id=1`); err != nil {
		t.Fatal(err)
	}
	if err := s.loadGraphRefs(); err != nil {
		t.Fatal(err)
	}
	lead := protocol.GraphRef{Kind: "agent", ID: "wbgpoc-lead"}
	teamWrite(t, s, protocol.TeamWrite{Action: "create", ID: "wbgpoc", Name: "wbgpoc", Owner: &lead})
	deployRule := SettingsChange{Key: "delivery.deploy_rule", Level: "repo", Scope: "other", Value: json.RawMessage(`"owner"`)}
	if _, err := s.WriteSettings([]SettingsChange{deployRule}, "owner", "fixture"); err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		fmt.Sprintf(`INSERT INTO owner_settings(key,value,updated_at) VALUES('chief_session','%s',0)`, ids["chief-of-staff"]),
		`INSERT INTO standing_orders(id,text,saved_at) VALUES('so_f960d5fd38dcd366','wbgpoc-lead merges its team''s repos',0)`,
		fmt.Sprintf(`INSERT INTO project_mergers(project,node_id,session_id) VALUES('demo','%s','%s')`, node, ids["other"]),
		`INSERT INTO graph_edges(source,target,kind,mode,rule) VALUES('/delivery/merger','demo','releases','','normal')`,
		`INSERT INTO graph_edges(source,target,kind,mode,rule) VALUES('/delivery/merger','demo','merges','','normal')`,
		`INSERT INTO graph_edges(source,target,kind,mode,rule) VALUES('/delivery/caller','demo','accepts','','normal')`,
		`INSERT INTO graph_edges(source,target,kind,mode,rule) VALUES('/delivery/github','other','reviews','','normal')`,
		`INSERT INTO graph_edges(source,target,kind,mode,rule) VALUES('homelab','app','deploys','','auto')`,
		`INSERT INTO graph_edges(source,target,kind,mode,rule) VALUES('homelab','other','deploys','','auto')`,
		`INSERT INTO graph_edges(source,target,kind,mode,rule) VALUES('app','demo','depends','','normal')`,
	} {
		if _, err := s.db.Exec(q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	rerun := func(clear bool) {
		t.Helper()
		if clear {
			if _, err := s.db.Exec(`DELETE FROM node_scopes; DELETE FROM owner_settings WHERE key='node_scopes_seeded'`); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := s.db.Exec(`DELETE FROM schema_version WHERE version>=?`, migrationVersion(t, "node scopes seed")); err != nil {
			t.Fatal(err)
		}
		if err := s.migrate(); err != nil {
			t.Fatal(err)
		}
	}
	got := func() string {
		t.Helper()
		all, err := s.AllScopes()
		if err != nil {
			t.Fatal(err)
		}
		lines := []string{}
		for _, r := range all {
			lines = append(lines, fmt.Sprintf("%s %s %s | %s | %s", r.Node, r.Kind, r.Over, r.Note, r.SetBy))
		}
		return strings.Join(lines, "\n")
	}
	rerun(true)
	want := strings.Join([]string{
		"chief-of-staff asks all | sort, route, chase, close; never approve or execute | migration",
		"cto admin all |  | migration",
		"cto permissions all | on owner words | migration",
		"demo review repo:demo | from accepts edge, owner @caller | migration",
		"homelab deploy repo:app | from deploys edge; deploy_rule brief | migration",
		"other merge repo:demo | holds the merger of demo | migration",
		"other release repo:demo | from delivery owner @merger | migration",
		"wbgpoc-lead merge team:wbgpoc | from owner rule so_f960d5fd38dcd366 | migration",
	}, "\n")
	if g := got(); g != want {
		t.Fatalf("seeded scopes:\n%s\nwant:\n%s", g, want)
	}
	if _, err := s.RemoveScope(scopeSet("cto", protocol.ScopeAdmin, "all", ""), "owner", false); err != nil {
		t.Fatal(err)
	}
	rerun(false)
	if strings.Contains(got(), "cto admin all") {
		t.Fatal("seed ran twice and put back a removed scope")
	}
}
