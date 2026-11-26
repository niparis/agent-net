package gateway

import (
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

// The one placement rule of decision 0193, case by case, on a graph with the
// CTO above a team lead above a project lead, and an Agent with no lead
// session.
func TestSessionParentRule(t *testing.T) {
	g := placeGraph{
		reportsTo: map[string]string{
			"team-lead": "cto", "proj-lead": "team-lead", "proj-agent": "proj-lead",
			"orphan-lead": "gone", "chief": protocol.OrgRoot,
		},
		lead:        map[string]string{"cto": "s_cto", "team-lead": "s_team", "proj-lead": "s_proj", "orphan-lead": "s_orphan", "chief": "s_chief"},
		projectLead: map[string]string{"proj": "s_proj"},
	}
	for _, c := range []struct {
		name             string
		s                placeSession
		parent, noParent string
	}{
		{"worker under the session that started it", placeSession{id: "s_w", agent: "proj-agent", worker: "s_caller"}, "s_caller", ""},
		{"owner, CTO and bots are roots", placeSession{id: "s_cto", agent: "cto", root: true}, "", ""},
		{"lead session under the lead session of the Agent above", placeSession{id: "s_proj", agent: "proj-lead"}, "s_team", ""},
		{"lead session whose Agent reports to the CTO sits under the CTO", placeSession{id: "s_team", agent: "team-lead"}, "s_cto", ""},
		{"lead session whose Agent reports to the owner is a root", placeSession{id: "s_chief", agent: "chief"}, "", ""},
		{"lead session whose Agent above has no lead session is a root", placeSession{id: "s_orphan", agent: "orphan-lead"}, "", ""},
		{"other session under its Agent's lead session", placeSession{id: "s_proj2", agent: "proj-lead"}, "s_proj", ""},
		{"Agent without a lead session: drawn under the project lead", placeSession{id: "s_m", agent: "proj-agent", project: "proj"}, "s_proj", "no lead"},
		{"no Agent: drawn under the project lead", placeSession{id: "s_x", project: "proj"}, "s_proj", "no Agent"},
		{"no Agent and no project lead: a root with the marker", placeSession{id: "s_y", project: "other"}, "", "no Agent"},
	} {
		parent, noParent := sessionParent(c.s, g)
		if parent != c.parent || noParent != c.noParent {
			t.Errorf("%s: got (%q, %q), want (%q, %q)", c.name, parent, noParent, c.parent, c.noParent)
		}
	}
}

// The session list carries the computed parent, and only a live lead session
// counts: a replaced one leaves its Agent's other sessions unplaced.
func TestAgentListCarriesTheSessionParent(t *testing.T) {
	s, ids := orgReadRig(t)
	parents := func() map[string][2]string {
		agents, err := s.Agents(protocol.QueryAgents{})
		if err != nil {
			t.Fatal(err)
		}
		out := map[string][2]string{}
		for _, a := range agents {
			out[a.SessionID] = [2]string{a.Parent, a.NoParent}
		}
		return out
	}
	got := parents()
	for session, want := range map[string][2]string{
		ids["lead"]:     {"", ""},
		ids["lead-2"]:   {ids["lead"], ""},
		ids["member"]:   {ids["lead"], ""},
		ids["member-2"]: {ids["member"], ""},
	} {
		if got[session] != want {
			t.Fatalf("session %s placed %v, want %v (all: %v)", session, got[session], want, got)
		}
	}
	if _, err := s.db.Exec(`UPDATE sessions SET replaced_by='s_gone' WHERE id=?`, ids["member"]); err != nil {
		t.Fatal(err)
	}
	if got := parents()[ids["member-2"]]; got[1] != "no lead" {
		t.Fatalf("member-2 with a replaced lead session placed %v, want the no lead marker", got)
	}
}
