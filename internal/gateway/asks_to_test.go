package gateway

import (
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

// forgeRig makes team forge with lead agent "other" and one member repo
// "demo". It returns the asker (a session of repo demo) and the lead session.
func forgeRig(t *testing.T) (s *Store, asker, lead string) {
	t.Helper()
	s = teamStore(t)
	if err := s.db.QueryRow(`SELECT id FROM sessions WHERE repo_id='demo'`).Scan(&asker); err != nil {
		t.Fatal(err)
	}
	if err := s.db.QueryRow(`SELECT id FROM sessions WHERE repo_id='other'`).Scan(&lead); err != nil {
		t.Fatal(err)
	}
	agent := protocol.GraphRef{Kind: "agent", ID: "other"}
	teamWrite(t, s, protocol.TeamWrite{Action: "create", ID: "forge", Name: "forge", Owner: &agent})
	teamWrite(t, s, protocol.TeamWrite{Action: "membership", ID: "forge", Member: &protocol.GraphRef{Kind: "repo", ID: "demo"}})
	return s, asker, lead
}

func TestTeamAsksToDefaultsToOwnerAndTheOwnerAndCTOSetIt(t *testing.T) {
	s, _, _ := forgeRig(t)
	teams, err := s.Teams()
	if err != nil || len(teams) != 1 || teams[0].AsksTo != protocol.AsksToOwner {
		t.Fatalf("default: %+v %v", teams, err)
	}
	if err := s.WriteTeam(protocol.TeamWrite{Action: "asks_to", ID: "forge", AsksTo: "nobody"}, "owner"); err == nil {
		t.Fatal("invalid asks_to accepted")
	}
	teamWrite(t, s, protocol.TeamWrite{Action: "asks_to", ID: "forge", AsksTo: protocol.AsksToLead})
	teams, _ = s.Teams()
	if teams[0].AsksTo != protocol.AsksToLead {
		t.Fatalf("owner write: %+v", teams)
	}
	// The CTO sets it through the team charter, with no proposal, and can undo it.
	if err := s.SetLevelCharter(protocol.CharterSetRequest{Level: "team", Node: "forge", Attr: "asks_to", Value: "owner"}, "cto", false); err != nil {
		t.Fatal(err)
	}
	teams, _ = s.Teams()
	if teams[0].AsksTo != protocol.AsksToOwner {
		t.Fatalf("cto write: %+v", teams)
	}
	changes, err := s.CTOGraphHistory(0)
	if err != nil || len(changes) != 1 {
		t.Fatalf("graph history: %+v %v", changes, err)
	}
	if err := s.GraphUndo(changes[0].ID, "owner"); err != nil {
		t.Fatal(err)
	}
	teams, _ = s.Teams()
	if teams[0].AsksTo != protocol.AsksToLead {
		t.Fatalf("undo: %+v", teams)
	}
}

// Team rules reach every member, also one with its own repo rules.
func TestRulesUnionAcrossLevels(t *testing.T) {
	s := teamStore(t)
	teamWrite(t, s, protocol.TeamWrite{Action: "create", ID: "forge", Name: "forge"})
	teamWrite(t, s, protocol.TeamWrite{Action: "membership", ID: "forge", Member: &protocol.GraphRef{Kind: "repo", ID: "demo"}})
	for _, q := range []protocol.CharterSetRequest{
		{Level: "team", Node: "forge", Attr: "rules", Value: "Merge only on green CI; Ask the lead first"},
		{Level: "repo", Node: "demo", Attr: "rules", Value: "Use plain English\nAsk the lead first"},
		{Level: "agent", Node: "demo", Attr: "rules", Value: "Keep commits small"},
	} {
		if err := s.SetLevelCharter(q, "owner", false); err != nil {
			t.Fatal(err)
		}
	}
	c, err := s.EffectiveCharter("demo", "demo", "")
	if err != nil {
		t.Fatal(err)
	}
	want := "Merge only on green CI; Ask the lead first; Use plain English; Keep commits small"
	if c.Rules != want {
		t.Fatalf("rules = %q, want %q", c.Rules, want)
	}
	if c.Sources == nil || len((*c.Sources)["rules"]) != 3 {
		t.Fatalf("rule sources: %+v", c.Sources)
	}
	// A repo with no rules of its own still gets the team rules.
	other, err := s.EffectiveCharter("other", "", "forge")
	if err != nil || !strings.Contains(other.Rules, "Merge only on green CI") {
		t.Fatalf("team context: %+v %v", other, err)
	}
}
