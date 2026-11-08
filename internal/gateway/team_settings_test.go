package gateway

import (
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

func TestTeamWriteCharterShapeSetsWorkModeAndAsksTo(t *testing.T) {
	s := teamStore(t)
	teamWrite(t, s, protocol.TeamWrite{Action: "create", ID: "forge", Name: "Forge", Purpose: "Shared work"})
	teamWrite(t, s, protocol.TeamWrite{Action: "membership", ID: "forge", Member: &protocol.GraphRef{Kind: "repo", ID: "demo"}})

	if err := s.WriteTeam(protocol.TeamWrite{Action: "charter", ID: "forge", Attr: "work_mode", Value: "workers_code"}, "owner"); err != nil {
		t.Fatal(err)
	}
	if err := s.WriteTeam(protocol.TeamWrite{Action: "charter", ID: "forge", Attr: "asks_to", Value: "lead"}, "owner"); err != nil {
		t.Fatal(err)
	}
	c, err := s.EffectiveCharter("demo", "", "")
	if err != nil || c.WorkMode != protocol.WorkModeWorkersCode {
		t.Fatalf("member work_mode = %q, %v; want workers_code", c.WorkMode, err)
	}
	teams, err := s.Teams()
	if err != nil || len(teams) != 1 || teams[0].AsksTo != protocol.AsksToLead {
		t.Fatalf("asks_to = %+v, %v; want lead", teams, err)
	}
	if err := s.WriteTeam(protocol.TeamWrite{Action: "charter", ID: "forge", Attr: "work_mode", Value: "always"}, "owner"); err == nil {
		t.Fatal("team charter accepted a bad work_mode")
	}
}
