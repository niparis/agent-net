package gateway

import (
	"encoding/json"
	"github.com/niparis/agent-net/internal/protocol"
	"strings"
	"testing"
	"time"
)

func TestCharterLevelsMigrationReplay(t *testing.T) {
	s, _ := newTestStore(t)
	attrs := map[string]string{"purpose": "purpose", "maturity": "mature - daily use", "accepts": "fixes", "refuses": "secrets", "feature_requests": "ideas", "escalate": "owner", "consumers": "clients", "needs": "database", "notes": "careful"}
	for k, v := range attrs {
		if _, err := s.db.Exec(`INSERT INTO graph_charter VALUES('example',?,?,1,'owner')`, k, v); err != nil {
			t.Fatal(err)
		}
	}
	// Rewind to just before the charter migration, as recovery of an interrupted upgrade does.
	rewind := migrationVersion(t, "Charter levels and enum cutover") - 1
	if _, err := s.db.Exec(`DELETE FROM schema_version WHERE version>?`, rewind); err != nil {
		t.Fatal(err)
	}
	if err := s.migrate(); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		var maturity, note, rules string
		if err := s.db.QueryRow(`SELECT value FROM graph_charter WHERE node='example' AND attr='maturity'`).Scan(&maturity); err != nil {
			t.Fatal(err)
		}
		if err := s.db.QueryRow(`SELECT value FROM graph_charter WHERE node='example' AND attr='maturity_note'`).Scan(&note); err != nil {
			t.Fatal(err)
		}
		if err := s.db.QueryRow(`SELECT value FROM graph_charter WHERE node='example' AND attr='rules'`).Scan(&rules); err != nil {
			t.Fatal(err)
		}
		if maturity != "mature" || note != "daily use" || rules != "Consumers:\nclients\n\nNeeds:\ndatabase\n\nNotes:\ncareful" {
			t.Fatalf("migration: %q %q %q", maturity, note, rules)
		}
		var n int
		if err := s.db.QueryRow(`SELECT count(*) FROM graph_charter WHERE attr IN ('consumers','needs','notes')`).Scan(&n); err != nil || n != 0 {
			t.Fatalf("dropped: %d %v", n, err)
		}
		if _, err := s.db.Exec(`DELETE FROM schema_version WHERE version>?`, rewind); err != nil {
			t.Fatal(err)
		}
		if err := s.migrate(); err != nil {
			t.Fatal(err)
		}
	}
	for _, attr := range []string{"consumers", "needs", "notes"} {
		if err := s.SetCharter("example", attr, "x", "owner"); err == nil {
			t.Fatalf("accepted dropped %s", attr)
		}
	}
}

func TestEffectiveCharterLevelsAndSources(t *testing.T) {
	s := teamStore(t)
	teamWrite(t, s, protocol.TeamWrite{Action: "create", ID: "alpha", Name: "Alpha", Purpose: "Team purpose"})
	teamWrite(t, s, protocol.TeamWrite{Action: "membership", ID: "alpha", Member: &protocol.GraphRef{Kind: "repo", ID: "demo"}})
	for _, q := range []protocol.CharterSetRequest{
		{Level: "global", Attr: "rules", Value: "global taste"},
		{Level: "global", Attr: "refuses", Value: "secrets"},
		{Level: "team", Node: "alpha", Attr: "refuses", Value: "delete backups"},
		{Level: "repo", Node: "demo", Attr: "refuses", Value: "production"},
		{Level: "agent", Node: "demo", Attr: "refuses", Value: "secrets"},
		{Level: "agent", Node: "demo", Attr: "accepts", Value: "fixes"},
	} {
		if err := s.SetLevelCharter(q, "owner", false); err != nil {
			t.Fatal(err)
		}
	}
	c, err := s.EffectiveCharter("demo", "demo", "")
	if err != nil {
		t.Fatal(err)
	}
	if c.Purpose != "Team purpose" || c.Escalate != "ask the team lead" || c.Rules != "global taste" || c.Accepts != "fixes" || c.Refuses != "secrets\ndelete backups\nproduction" {
		t.Fatalf("effective %+v", c)
	}
	b, _ := json.Marshal(c)
	if !strings.Contains(string(b), `"sources"`) || c.Sources == nil || len((*c.Sources)["refuses"]) != 4 || (*c.Sources)["accepts"][0].Level != "agent" {
		t.Fatalf("sources %s", b)
	}
	empty, err := s.EffectiveCharter("", "", "")
	if err != nil || empty.Escalate != "" {
		t.Fatalf("empty %+v %v", empty, err)
	}
}

func TestCharterLevelUndoAndValidation(t *testing.T) {
	s := teamStore(t)
	q := protocol.CharterSetRequest{Level: "global", Attr: "rules", Value: "plain text"}
	if err := s.SetLevelCharter(q, "owner", true); err != nil {
		t.Fatal(err)
	}
	c, err := s.EffectiveCharter("", "", "")
	if err != nil || c.Rules != "" {
		t.Fatalf("validation wrote %+v %v", c, err)
	}
	if err = s.SetLevelCharter(q, "owner", false); err != nil {
		t.Fatal(err)
	}
	if err = s.GraphUndo(0, "owner"); err != nil {
		t.Fatal(err)
	}
	c, err = s.EffectiveCharter("", "", "")
	if err != nil || c.Rules != "" {
		t.Fatalf("undo %+v %v", c, err)
	}
}

func TestTeamCharterAction(t *testing.T) {
	s := teamStore(t)
	teamWrite(t, s, protocol.TeamWrite{Action: "create", ID: "alpha", Name: "Alpha"})
	q, err := protocol.ResolveTeamAction("team_charter", json.RawMessage(`{"team":"Alpha","attr":"refuses","value":"delete backups"}`), s)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.WriteTeam(q, "owner"); err != nil {
		t.Fatal(err)
	}
	c, err := s.EffectiveCharter("", "", "alpha")
	if err != nil || c.Refuses != "delete backups" {
		t.Fatalf("%+v %v", c, err)
	}
}

func TestCharterAgentSafetyAcrossTeamContexts(t *testing.T) {
	s := teamStore(t)
	for _, id := range []string{"repo-team", "agent-team"} {
		teamWrite(t, s, protocol.TeamWrite{Action: "create", ID: id, Name: id})
	}
	teamWrite(t, s, protocol.TeamWrite{Action: "membership", ID: "repo-team", Member: &protocol.GraphRef{Kind: "repo", ID: "demo"}})
	teamWrite(t, s, protocol.TeamWrite{Action: "membership", ID: "agent-team", Member: &protocol.GraphRef{Kind: "agent", ID: "demo"}})
	for _, id := range []string{"repo-team", "agent-team"} {
		if err := s.SetLevelCharter(protocol.CharterSetRequest{Level: "team", Node: id, Attr: "refuses", Value: id + " safety"}, "owner", false); err != nil {
			t.Fatal(err)
		}
	}
	c, err := s.EffectiveCharter("demo", "demo", "")
	if err != nil || !strings.Contains(c.Refuses, "repo-team safety") || !strings.Contains(c.Refuses, "agent-team safety") {
		t.Fatalf("safety lost: %+v %v", c, err)
	}
}

func TestCharterExplicitLevelFields(t *testing.T) {
	s := teamStore(t)
	for _, q := range []protocol.CharterSetRequest{
		{Level: "agent", Node: "demo", Attr: "maturity", Value: "mature"},
		{Level: "agent", Node: "demo", Attr: "purpose", Value: "identity"},
		{Level: "repo", Node: "demo", Attr: "accepts", Value: "fixes"},
		{Level: "global", Attr: "maturity", Value: "mature"},
	} {
		if err := s.SetLevelCharter(q, "owner", false); err == nil {
			t.Fatalf("accepted %+v", q)
		}
	}
}

func TestSessionReadCharterSources(t *testing.T) {
	sources := protocol.CharterSources{"rules": {{Level: "global", Value: "plain text"}}}
	a := protocol.AgentView{Charter: protocol.AgentCharter{Effective: &protocol.Charter{Rules: "plain text", Sources: &sources}}}
	row := sessionRow(a, true, time.Now(), func(s string) string { return s })
	if row.Charter == nil || row.Charter.Rules != "plain text" || row.Charter.Sources == nil {
		t.Fatalf("lost inherited charter: %+v", row.Charter)
	}
}
