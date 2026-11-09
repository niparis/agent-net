package gateway

import (
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

// leadMergeGrantLine is the sentence a member of a team with lead_merges=on
// reads in its place. The lead is the session of repo other in forgeRig.
const leadMergeGrantLine = "Merges: your lead other may merge this team's work on the owner's grant; hand a merge you cannot do to the lead."

func TestTeamLeadMergesDefaultsOffAndTheCTOGrantsIt(t *testing.T) {
	s, _, _ := forgeRig(t)
	teams, err := s.Teams()
	if err != nil || len(teams) != 1 || teams[0].LeadMerges != protocol.LeadMergesOff {
		t.Fatalf("default: %+v %v", teams, err)
	}
	if err := s.SetLevelCharter(protocol.CharterSetRequest{Level: "team", Node: "forge", Attr: "lead_merges", Value: "maybe"}, "cto", false); err == nil {
		t.Fatal("invalid lead_merges accepted")
	}
	// The CTO grants it through the team charter, with no proposal.
	if err := s.SetLevelCharter(protocol.CharterSetRequest{Level: "team", Node: "forge", Attr: "lead_merges", Value: "on"}, "cto", false); err != nil {
		t.Fatal(err)
	}
	teams, _ = s.Teams()
	if teams[0].LeadMerges != protocol.LeadMergesOn {
		t.Fatalf("cto grant: %+v", teams)
	}
	// An empty value means off again.
	if err := s.SetLevelCharter(protocol.CharterSetRequest{Level: "team", Node: "forge", Attr: "lead_merges", Value: ""}, "cto", false); err != nil {
		t.Fatal(err)
	}
	teams, _ = s.Teams()
	if teams[0].LeadMerges != protocol.LeadMergesOff {
		t.Fatalf("cto clear: %+v", teams)
	}
}

func TestNodeSettingsGetListsTheTeamLeadMergeGrant(t *testing.T) {
	s, _, _ := forgeRig(t)
	res, err := s.NodeSettings("forge")
	if err != nil || res.LeadMerges != protocol.LeadMergesOff {
		t.Fatalf("default node settings: %+v %v", res, err)
	}
	if err := s.SetLevelCharter(protocol.CharterSetRequest{Level: "team", Node: "forge", Attr: "lead_merges", Value: "on"}, "cto", false); err != nil {
		t.Fatal(err)
	}
	res, err = s.NodeSettings("forge")
	if err != nil || res.LeadMerges != protocol.LeadMergesOn {
		t.Fatalf("granted node settings: %+v %v", res, err)
	}
}

func TestMemberSeesTheLeadMergeGrantInItsPlace(t *testing.T) {
	s, asker, lead := forgeRig(t)
	place, err := s.placeText(asker)
	if err != nil || strings.Contains(place, "owner's grant") {
		t.Fatalf("grant off: member place %q, %v", place, err)
	}
	if err := s.SetLevelCharter(protocol.CharterSetRequest{Level: "team", Node: "forge", Attr: "lead_merges", Value: "on"}, "cto", false); err != nil {
		t.Fatal(err)
	}
	place, err = s.placeText(asker)
	if err != nil || !strings.Contains(place, leadMergeGrantLine) {
		t.Fatalf("grant on: member place %q, %v; want the grant line", place, err)
	}
	// The lead does not read the grant about itself.
	leadPlace, err := s.placeText(lead)
	if err != nil || strings.Contains(leadPlace, "owner's grant") {
		t.Fatalf("lead place %q, %v", leadPlace, err)
	}
}

func TestTeamLeadForCarriesTheGrantWhetherOrNotTheTeamAsksTheLead(t *testing.T) {
	s, asker, lead := forgeRig(t)
	res, err := s.TeamLeadFor(asker)
	if err != nil || res != (protocol.TeamLeadResult{}) {
		t.Fatalf("no grant and asks_to owner: %+v %v; want nothing", res, err)
	}
	if err := s.SetLevelCharter(protocol.CharterSetRequest{Level: "team", Node: "forge", Attr: "lead_merges", Value: "on"}, "cto", false); err != nil {
		t.Fatal(err)
	}
	res, err = s.TeamLeadFor(asker)
	want := protocol.TeamLeadResult{Team: "forge", Lead: "other", LeadSession: lead, LeadMerges: true}
	if err != nil || res != want {
		t.Fatalf("grant with asks_to owner: %+v %v; want %+v", res, err, want)
	}
	teamWrite(t, s, protocol.TeamWrite{Action: "asks_to", ID: "forge", AsksTo: protocol.AsksToLead})
	if err := s.SetLevelCharter(protocol.CharterSetRequest{Level: "team", Node: "forge", Attr: "lead_merges", Value: "off"}, "cto", false); err != nil {
		t.Fatal(err)
	}
	res, err = s.TeamLeadFor(asker)
	want = protocol.TeamLeadResult{Team: "forge", Lead: "other", LeadSession: lead, AsksToLead: true}
	if err != nil || res != want {
		t.Fatalf("asks_to lead without grant: %+v %v; want %+v", res, err, want)
	}
}
