package gateway

import (
	"database/sql"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

// The one placement rule of decision 0193, case by case, on a graph with the
// CTO above a team lead above a project lead, a member Agent and a merger
// Agent that lead nothing, and Agents with no live lead session.
func TestSessionParentRule(t *testing.T) {
	g := placeGraph{
		reportsTo: map[string]string{
			"team-lead": "cto", "proj-lead": "team-lead", "proj-agent": "proj-lead", "proj-merger": "proj-lead",
			"orphan-lead": "gone", "chief": protocol.OrgRoot, "idle-lead": "team-lead",
			"helper": protocol.OrgRoot, "stray": "", "late-agent": "idle-lead",
		},
		lead: map[string]string{"cto": "s_cto", "team-lead": "s_team", "proj-lead": "s_proj", "orphan-lead": "s_orphan", "chief": "s_chief",
			// A stale pin on an Agent that leads nothing is not a lead session.
			"proj-agent": "s_m1"},
		leads:       map[string]bool{"team-lead": true, "proj-lead": true, "orphan-lead": true, "chief": true, "idle-lead": true},
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
		{"other session of a leading Agent under its lead session", placeSession{id: "s_proj2", agent: "proj-lead"}, "s_proj", ""},
		{"leading Agent without a live lead session: drawn under the project lead", placeSession{id: "s_i", agent: "idle-lead", project: "proj"}, "s_proj", "no lead"},
		{"member session 1 under the lead it reports to", placeSession{id: "s_m1", agent: "proj-agent", project: "proj"}, "s_proj", ""},
		{"member session 2 beside its peer, not under it", placeSession{id: "s_m2", agent: "proj-agent", project: "proj"}, "s_proj", ""},
		{"member session 3 beside its peers", placeSession{id: "s_m3", agent: "proj-agent", project: "proj"}, "s_proj", ""},
		{"merger session under the lead it reports to", placeSession{id: "s_merge", agent: "proj-merger", project: "proj"}, "s_proj", ""},
		{"Agent that leads nothing and reports to the owner is a root", placeSession{id: "s_h", agent: "helper"}, "", ""},
		{"Agent that leads nothing and reports to no one: drawn under the project lead", placeSession{id: "s_s", agent: "stray", project: "proj"}, "s_proj", "no reports_to"},
		{"Agent that leads nothing above a lead with no live session: drawn under the project lead", placeSession{id: "s_l", agent: "late-agent", project: "proj"}, "s_proj", "no lead"},
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

// Rule 5: a leading Agent with no lead pin and no live session draws its most
// recently active session as its lead, without writing anything; once a
// session is live and pinned, the pin places the tree.
func TestAllOfflineLeadingAgentDrawsAsOneRow(t *testing.T) {
	s, ids := orgReadRig(t)
	node := ""
	if err := s.db.QueryRow(`SELECT node_id FROM sessions WHERE id=?`, ids["lead"]).Scan(&node); err != nil {
		t.Fatal(err)
	}
	id, err := s.UpsertSession(node, protocol.SessionInfo{Harness: "claude", NativeID: "lead-3", Repo: protocol.RepoInfo{Root: "/w/lead", Project: "lead"}})
	if err != nil {
		t.Fatal(err)
	}
	ids["lead-3"] = id
	// lead-2 was active last, by last_active for one and last_seen for another.
	for _, q := range []struct {
		sql  string
		args []any
	}{
		{`UPDATE sessions SET agent_id='lead', online=0, last_active=CASE id WHEN ? THEN 300 ELSE 100 END,
			last_seen=CASE id WHEN ? THEN 200 ELSE 100 END WHERE id IN (?,?,?)`,
			[]any{ids["lead-2"], ids["lead-3"], ids["lead"], ids["lead-2"], ids["lead-3"]}},
		{`UPDATE agents SET lead_session=NULL WHERE id='lead'`, nil},
	} {
		if _, err := s.db.Exec(q.sql, q.args...); err != nil {
			t.Fatalf("%s: %v", q.sql, err)
		}
	}
	placed := func() map[string][2]string {
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
	want := func(got map[string][2]string, lead string) {
		t.Helper()
		if got[lead][1] != "" {
			t.Fatalf("drawn lead %s has marker %q (all: %v)", lead, got[lead][1], got)
		}
		for _, session := range []string{"lead", "lead-2", "lead-3", "member"} {
			if ids[session] == lead {
				continue
			}
			if got[ids[session]] != [2]string{lead, ""} {
				t.Fatalf("session %s placed %v, want under %s (all: %v)", session, got[ids[session]], lead, got)
			}
		}
	}
	want(placed(), ids["lead-2"])
	var pin sql.NullString
	if err := s.db.QueryRow(`SELECT lead_session FROM agents WHERE id='lead'`).Scan(&pin); err != nil || pin.Valid {
		t.Fatalf("drawing pinned a lead: %v %v", pin, err)
	}
	if _, err := s.db.Exec(`UPDATE sessions SET online=1 WHERE id=?`, ids["lead-3"]); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`UPDATE agents SET lead_session=? WHERE id='lead'`, ids["lead-3"]); err != nil {
		t.Fatal(err)
	}
	want(placed(), ids["lead-3"])
}
