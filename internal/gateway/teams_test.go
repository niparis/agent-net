package gateway

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
	"github.com/niparis/agent-net/internal/settings"
)

func teamStore(t *testing.T) *Store {
	t.Helper()
	s, _ := newTestStore(t)
	graphSessions(t, s, "demo", "other")
	if _, err := s.db.Exec(`UPDATE graph_ref_gate SET enabled=1 WHERE id=1`); err != nil {
		t.Fatal(err)
	}
	if err := s.loadGraphRefs(); err != nil {
		t.Fatal(err)
	}
	return s
}
func teamWrite(t *testing.T, s *Store, q protocol.TeamWrite) {
	t.Helper()
	if err := s.WriteTeam(q, "owner"); err != nil {
		t.Fatal(err)
	}
}
func TestTeamRecordsHistoryAndArchive(t *testing.T) {
	s := teamStore(t)
	owner := protocol.GraphRef{Kind: "agent", ID: "demo"}
	teamWrite(t, s, protocol.TeamWrite{Action: "create", ID: "alpha", Name: "Alpha", Purpose: "Shared work", Owner: &owner})
	teamWrite(t, s, protocol.TeamWrite{Action: "rename", ID: "alpha", Name: "Renamed", ExpectName: "Alpha"})
	if err := s.WriteTeam(protocol.TeamWrite{Action: "rename", ID: "alpha", Name: "Stale", ExpectName: "Alpha"}, "owner"); err == nil {
		t.Fatal("stale rename accepted")
	}
	teamWrite(t, s, protocol.TeamWrite{Action: "archive", ID: "alpha"})
	teams, err := s.Teams()
	if err != nil || len(teams) != 1 || teams[0].Active || teams[0].Name != "Renamed" || teams[0].Owner == nil || *teams[0].Owner != owner {
		t.Fatalf("teams: %+v %v", teams, err)
	}
	h, err := s.TeamHistory("alpha", 100)
	if err != nil || len(h) != 3 || h[0].Action != "archive" || h[2].Action != "create" || h[0].By != "owner" {
		t.Fatalf("history: %+v %v", h, err)
	}
	if err := s.WriteTeam(protocol.TeamWrite{Action: "create", ID: "alpha", Name: "Reuse"}, "owner"); err == nil {
		t.Fatal("archived identity reused")
	}
	g, err := s.GraphForRef(&protocol.GraphRef{Kind: "team", ID: "alpha"})
	if err != nil || len(g.Nodes) != 1 || g.Nodes[0].Active == nil || *g.Nodes[0].Active {
		t.Fatalf("archived read: %+v %v", g, err)
	}
}
func TestTeamPurposeRecordsHistoryAndUndo(t *testing.T) {
	s := teamStore(t)
	teamWrite(t, s, protocol.TeamWrite{Action: "create", ID: "alpha", Name: "Alpha", Purpose: "Old purpose"})
	if err := s.WriteTeam(protocol.TeamWrite{Action: "purpose", ID: "alpha", Purpose: "New purpose"}, "cto"); err != nil {
		t.Fatal(err)
	}
	teams, err := s.Teams()
	if err != nil || len(teams) != 1 || teams[0].Purpose != "New purpose" {
		t.Fatalf("purpose write: %+v %v", teams, err)
	}
	history, err := s.TeamHistory("alpha", 100)
	if err != nil || len(history) != 2 || history[0].Action != "purpose" {
		t.Fatalf("team history: %+v %v", history, err)
	}
	changes, err := s.CTOGraphHistory(0)
	if err != nil || len(changes) != 1 {
		t.Fatalf("graph history: %+v %v", changes, err)
	}
	if err := s.GraphUndo(changes[0].ID, "owner"); err != nil {
		t.Fatal(err)
	}
	teams, err = s.Teams()
	if err != nil || len(teams) != 1 || teams[0].Purpose != "Old purpose" {
		t.Fatalf("purpose undo: %+v %v", teams, err)
	}
}

func TestTeamMembershipKindsAndExpectedValue(t *testing.T) {
	s := teamStore(t)
	for _, id := range []string{"alpha", "beta"} {
		teamWrite(t, s, protocol.TeamWrite{Action: "create", ID: id, Name: id})
	}
	repo := protocol.GraphRef{Kind: "repo", ID: "demo"}
	agent := protocol.GraphRef{Kind: "agent", ID: "demo"}
	for _, member := range []protocol.GraphRef{repo, agent} {
		teamWrite(t, s, protocol.TeamWrite{Action: "membership", ID: "alpha", Member: &member})
	}
	if err := s.WriteTeam(protocol.TeamWrite{Action: "membership", ID: "beta", Member: &repo}, "owner"); err == nil {
		t.Fatal("second active team accepted without expected old value")
	}
	for _, ref := range []protocol.GraphRef{{Kind: "team", ID: "alpha"}, {Kind: "special", ID: "*"}, {Kind: "agent", ID: "missing"}, {Kind: "repo", ID: "missing"}} {
		if err := s.WriteTeam(protocol.TeamWrite{Action: "membership", ID: "beta", Member: &ref}, "owner"); err == nil {
			t.Fatalf("invalid member accepted: %+v", ref)
		}
	}
	teamWrite(t, s, protocol.TeamWrite{Action: "membership", ID: "beta", Member: &repo, ExpectTeam: "alpha"})
	if err := s.WriteTeam(protocol.TeamWrite{Action: "archive", ID: "alpha"}, "owner"); err == nil {
		t.Fatal("archive with member accepted")
	}
	teamWrite(t, s, protocol.TeamWrite{Action: "membership", Member: &agent, ExpectTeam: "alpha"})
	teamWrite(t, s, protocol.TeamWrite{Action: "archive", ID: "alpha"})
	if err := s.WriteTeam(protocol.TeamWrite{Action: "membership", ID: "alpha", Member: &repo, ExpectTeam: "beta"}, "owner"); err == nil {
		t.Fatal("inactive team accepted")
	}
	g, err := s.GraphForRef(&repo)
	if err != nil || len(g.Edges) != 1 || g.Edges[0].ToRef.ID != "beta" {
		t.Fatalf("move rollback: %+v %v", g, err)
	}
	h, err := s.TeamHistory("alpha", 100)
	if err != nil || len(h) != 6 {
		t.Fatalf("move/removal history: %+v %v", h, err)
	}
}
func TestTeamGateAndOwnerValidation(t *testing.T) {
	s, _ := newTestStore(t)
	q := protocol.TeamWrite{Action: "create", ID: "alpha", Name: "Alpha"}
	if err := s.WriteTeam(q, "owner"); err == nil || !strings.Contains(err.Error(), "cutover") {
		t.Fatalf("gate: %v", err)
	}
	if _, err := s.db.Exec(`UPDATE graph_ref_gate SET enabled=1 WHERE id=1`); err != nil {
		t.Fatal(err)
	}
	if err := s.loadGraphRefs(); err != nil {
		t.Fatal(err)
	}
	for _, actor := range []string{"", "CTO (minor)", "peer"} {
		if err := s.WriteTeam(q, actor); err == nil {
			t.Fatalf("actor %q accepted", actor)
		}
	}
	badOwner := protocol.GraphRef{Kind: "repo", ID: "demo"}
	q.Owner = &badOwner
	if err := s.WriteTeam(q, "owner"); err == nil {
		t.Fatal("repo owner accepted")
	}
	q.Owner = &protocol.GraphRef{Kind: "agent", ID: "missing"}
	if err := s.WriteTeam(q, "owner"); err == nil {
		t.Fatal("missing owner accepted")
	}
	teams, err := s.Teams()
	if err != nil || len(teams) != 0 {
		t.Fatalf("failed write persisted: %+v %v", teams, err)
	}
}
func TestTeamGraphLegacyParityAndMigrationReplay(t *testing.T) {
	s := teamStore(t)
	before, err := s.Graph()
	if err != nil {
		t.Fatal(err)
	}
	// No teams must leave every legacy node, setting, edge and source untouched.
	v := migrationVersion(t, "Team records and typed membership")
	if v != 126 {
		t.Fatalf("migration %d, want 126", v)
	}
	for i := 0; i < 2; i++ {
		if _, err := s.db.Exec(`DELETE FROM schema_version WHERE version>=?`, v); err != nil {
			t.Fatal(err)
		}
		if err := migrateOldFixture(t, s); err != nil {
			t.Fatal(err)
		}
	}
	after, err := s.Graph()
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatalf("no-team parity changed: %v", err)
	}
	teamWrite(t, s, protocol.TeamWrite{Action: "create", ID: "only-team", Name: "Group", Purpose: "Shared work"})
	member := protocol.GraphRef{Kind: "agent", ID: "demo"}
	teamWrite(t, s, protocol.TeamWrite{Action: "membership", ID: "only-team", Member: &member})
	for _, project := range []string{"", "demo", "only-team"} {
		legacy, err := s.GraphFor(project)
		if err != nil {
			t.Fatal(err)
		}
		for _, n := range legacy.Nodes {
			if n.Kind == "team" || n.Name == "only-team" {
				t.Fatalf("legacy node union leaked team: %+v", legacy)
			}
		}
		for _, e := range legacy.Edges {
			if e.Kind == "member_of" {
				t.Fatal("legacy edge leaked membership")
			}
		}
	}
	after, err = s.Graph()
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatalf("team data changed legacy projection: %+v %v", after, err)
	}
	ref := protocol.GraphRef{Kind: "team", ID: "only-team"}
	g, err := s.GraphForRef(&ref)
	if err != nil || len(g.Nodes) != 2 || len(g.Edges) != 1 || g.Edges[0].FromRef.Kind != "agent" {
		t.Fatalf("typed graph: %+v %v", g, err)
	}
	for i := 0; i < 2; i++ {
		if _, err := s.db.Exec(`DELETE FROM schema_version WHERE version>=?`, v); err != nil {
			t.Fatal(err)
		}
		if err := migrateOldFixture(t, s); err != nil {
			t.Fatal(err)
		}
	}
	h, err := s.TeamHistory("only-team", 100)
	if err != nil || len(h) != 2 {
		t.Fatalf("replay history: %+v %v", h, err)
	}
}
func TestTeamGatewayOwnerAndRead(t *testing.T) {
	s := teamStore(t)
	g := &Gateway{store: s}
	_, node := graphSessions(t, s, "reader")
	owner, err := s.UpsertSession(node, protocol.SessionInfo{Harness: protocol.OwnerHarness, NativeID: protocol.OwnerNativeID, Delivery: "owner"})
	if err != nil {
		t.Fatal(err)
	}
	nc := &nodeConn{id: node}
	nc.graphRefs.Store(true)
	for _, id := range []string{"", "fake", owner} {
		raw, _ := json.Marshal(map[string]any{"action": "create", "team_id": "alpha", "name": "Alpha", "owner_session": id})
		_, err := g.handle(context.Background(), nc, "team.write", raw)
		if (id == owner) != (err == nil) {
			t.Fatalf("owner %q: %v", id, err)
		}
	}
	got, err := g.handleGraphRead([]byte(`{"ref":{"kind":"team","id":"alpha"}}`))
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(got)
	if !strings.Contains(string(raw), `"kind":"team"`) {
		t.Fatalf("team absent: %s", raw)
	}
}
func TestTeamOwnerCannotDisappear(t *testing.T) {
	s := teamStore(t)
	owner := protocol.GraphRef{Kind: "agent", ID: "demo"}
	teamWrite(t, s, protocol.TeamWrite{Action: "create", ID: "alpha", Name: "Alpha", Owner: &owner})
	for _, query := range []string{`DELETE FROM agents WHERE id='demo'`, `UPDATE agents SET tombstone=1 WHERE id='demo'`} {
		if _, err := s.db.Exec(query); err == nil {
			t.Fatal("team owner disappeared")
		}
	}
}

func TestTeamNoInheritanceParity(t *testing.T) {
	for _, populated := range []bool{false, true} {
		t.Run(map[bool]string{false: "defaults", true: "explicit"}[populated], func(t *testing.T) {
			s := teamStore(t)
			ids, _ := graphSessions(t, s, "consumer", "provider")
			if populated {
				if _, err := s.WriteSettings([]SettingsChange{{Key: "delivery.merge_authority", Level: "network", Scope: "network", Value: settingValue(`"delegated"`)}, {Key: "delivery.tests_before_merge", Level: "repo", Scope: "demo", Value: settingValue(`"suite"`)}}, "owner", "fixture"); err != nil {
					t.Fatal(err)
				}
				if err := s.SetEdge(protocol.Edge{From: "consumer", To: "provider", Kind: "depends", Rule: "ask"}, "owner"); err != nil {
					t.Fatal(err)
				}
			}
			capture := func() map[string]settings.Effective {
				snap, err := s.SettingsSnapshot()
				if err != nil {
					t.Fatal(err)
				}
				out := map[string]settings.Effective{}
				for _, d := range settings.Standard().Definitions {
					if strings.HasPrefix(d.Key, "delivery.") {
						for _, repo := range []string{"", "demo", "other"} {
							e, err := snap.Resolve(d.Key, settings.Where{Repo: repo})
							if err != nil {
								t.Fatal(err)
							}
							out[d.Key+"/"+repo] = e
						}
					}
				}
				return out
			}
			before := capture()
			rule, err := s.RequestRule(ids["consumer"], ids["provider"])
			if err != nil {
				t.Fatal(err)
			}
			relation, err := s.RelationFor(ids["consumer"], ids["provider"])
			if err != nil {
				t.Fatal(err)
			}
			teamWrite(t, s, protocol.TeamWrite{Action: "create", ID: "alpha", Name: "Alpha"})
			repo := protocol.GraphRef{Kind: "repo", ID: "demo"}
			teamWrite(t, s, protocol.TeamWrite{Action: "membership", ID: "alpha", Member: &repo})
			if after := capture(); !reflect.DeepEqual(before, after) {
				t.Fatalf("Team changed delivery values or sources: before=%+v after=%+v", before, after)
			}
			gotRule, err := s.RequestRule(ids["consumer"], ids["provider"])
			if err != nil || gotRule != rule {
				t.Fatalf("request parity: %q %v", gotRule, err)
			}
			gotRelation, err := s.RelationFor(ids["consumer"], ids["provider"])
			if err != nil || gotRelation != relation {
				t.Fatalf("relation parity: %q %v", gotRelation, err)
			}
		})
	}
}

func TestTeamStandaloneAgentAndTombstoneValidation(t *testing.T) {
	s := teamStore(t)
	if err := s.AddArchitect("advisor", "owner"); err != nil {
		t.Fatal(err)
	}
	ref := protocol.GraphRef{Kind: "agent", ID: "advisor"}
	teamWrite(t, s, protocol.TeamWrite{Action: "create", ID: "alpha", Name: "Alpha", Owner: &ref})
	teamWrite(t, s, protocol.TeamWrite{Action: "membership", ID: "alpha", Member: &ref})
	g, err := s.GraphForRef(&protocol.GraphRef{Kind: "team", ID: "alpha"})
	if err != nil || len(g.Edges) != 1 || len(g.Nodes) != 2 {
		t.Fatalf("standalone Agent: %+v %v", g, err)
	}
	if err := s.AddArchitect("retired", "owner"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`UPDATE agents SET tombstone=1 WHERE id='retired'`); err != nil {
		t.Fatal(err)
	}
	dead := protocol.GraphRef{Kind: "agent", ID: "retired"}
	for _, q := range []protocol.TeamWrite{{Action: "create", ID: "beta", Name: "Beta", Owner: &dead}, {Action: "membership", ID: "alpha", Member: &dead}} {
		if err := s.WriteTeam(q, "owner"); err == nil {
			t.Fatal("tombstoned Agent accepted")
		}
	}
}
