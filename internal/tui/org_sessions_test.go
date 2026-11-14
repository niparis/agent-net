package tui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/muesli/termenv"

	"github.com/niparis/agent-net/internal/protocol"
)

const (
	leadSessionID  = "s_0000000000000301"
	childSessionID = "s_0000000000000302"
	chiefSessionID = "s_0000000000000303"
)

// orgFixture adds a lead session of the "strategy" Agent and a session of the
// "research" Agent that reports to it (decision 0160).
func orgFixture() Snapshot {
	snap := fakeSnapshot()
	snap.Agents = append(snap.Agents,
		agent(leadSessionID, "n_mac", "Mac", "claude", "strategy lead", func(a *protocol.AgentView) {
			a.AgentID = "org-strategy"
			a.RepoLeadExplicit = true
		}),
		agent(childSessionID, "n_mac", "Mac", "claude", "research", func(a *protocol.AgentView) {
			a.AgentID = "org-research"
			a.AgentName = "research"
			a.ReportsTo = "org-strategy"
		}))
	return snap
}

// A session whose Agent reports to another Agent hangs under that Agent's
// session, after the lead row, the way a merger hangs under its project agent.
func TestReportingSessionNestsUnderItsLead(t *testing.T) {
	m := treeModel(120, 34, orgFixture())
	order := ids(m)
	at := indexOfID(order, leadSessionID)
	if at < 0 || at+1 >= len(order) || order[at+1] != childSessionID {
		t.Fatalf("research is not under the strategy lead: %v", order)
	}
	marks := map[string]treeMark{}
	for _, g := range m.groups() {
		for id, mk := range g.marks {
			marks[id] = mk
		}
	}
	if marks[childSessionID].prefix != "└─ " {
		t.Errorf("child prefix %q, want the last child glyph", marks[childSessionID].prefix)
	}
}

// A reporting session whose Agent has no shown session stays at the top
// level, with no tree mark.
func TestReportingSessionWithoutShownLeadStaysTopLevel(t *testing.T) {
	snap := fakeSnapshot()
	snap.Agents = append(snap.Agents, agent(childSessionID, "n_mac", "Mac", "claude", "research", func(a *protocol.AgentView) {
		a.AgentID = "org-research"
		a.ReportsTo = "org-absent"
	}))
	m := treeModel(120, 34, snap)
	for _, g := range m.groups() {
		for id, mk := range g.marks {
			if id == childSessionID && mk.prefix != "" {
				t.Errorf("session with no shown lead got mark %+v", mk)
			}
		}
	}
	if indexOfID(ids(m), childSessionID) < 0 {
		t.Fatal("session with no shown lead is not a row")
	}
}

// When an Agent has more than one shown session, the session its pinned lead
// is wins the nesting, even when another session of the Agent is online and
// has a lower ID.
func TestPinnedLeadIsTheNestingParent(t *testing.T) {
	const pinnedID, onlineID = "s_0000000000000311", "s_0000000000000310"
	snap := orgFixture()
	for i := range snap.Agents {
		if snap.Agents[i].SessionID == leadSessionID {
			snap.Agents[i].RepoLeadExplicit = false
		}
	}
	snap.Agents = append(snap.Agents,
		agent(onlineID, "n_mac", "Mac", "claude", "strategy spare", func(a *protocol.AgentView) {
			a.AgentID = "org-strategy"
		}),
		agent(pinnedID, "n_mac", "Mac", "claude", "strategy pinned", func(a *protocol.AgentView) {
			a.AgentID = "org-strategy"
			a.Online = false
			a.TeamLead = true
		}))
	m := treeModel(120, 34, snap)
	shown := map[string]protocol.AgentView{}
	for _, a := range m.snap.Agents {
		shown[a.SessionID] = a
	}
	child := shown[childSessionID]
	if got := m.reportParent(child, shown); got != pinnedID {
		t.Fatalf("parent %s, want the pinned lead %s", got, pinnedID)
	}
}

// A chief of staff is the first row of its band, whatever its ID or activity.
func TestChiefOfStaffIsFirstInItsBand(t *testing.T) {
	snap := fakeSnapshot()
	snap.Agents = append(snap.Agents, agent("s_0000000000000399", "n_mac", "Mac", "claude", "chief of staff", func(a *protocol.AgentView) {
		a.AgentID = "chief-of-staff"
		a.LastActive = t0.Add(-30 * time.Minute).Unix()
	}))
	m := treeModel(120, 34, snap)
	const chief = "s_0000000000000399"
	m.chief = protocol.ChiefStatus{SessionID: chief, Online: true}
	m.chiefLoaded = true
	for _, g := range m.groups() {
		for i, a := range g.agents {
			if a.SessionID == chief && i != 0 {
				t.Fatalf("chief is row %d of its group, want 0: %v", i, agentIDs(g.agents))
			}
		}
	}
}

func TestOrgTreeGolden(t *testing.T) {
	for _, size := range []struct{ w, h int }{{80, 24}, {120, 34}, {160, 40}} {
		snap := orgFixture()
		for i := range snap.Agents {
			if snap.Agents[i].SessionID == leadSessionID {
				// The lead's Agent has a pinned lead that is gone, so its row
				// is in the warning colour.
				snap.Agents[i].AgentLeadSession = "s_0000000000000398"
			}
		}
		snap.Agents = append(snap.Agents, agent(chiefSessionID, "n_mac", "Mac", "claude", "chief of staff", func(a *protocol.AgentView) {
			a.AgentID = "chief-of-staff"
		}))
		m := quietFixtures(treeModel(size.w, size.h, snap))
		m.chief = protocol.ChiefStatus{SessionID: chiefSessionID, Online: true}
		m.chiefLoaded = true
		golden(t, fmt.Sprintf("tree_org_%dx%d", size.w, size.h), m.testView())
	}
}

// The selected session's detail pane shows its Agent's delegated authority.
func TestOrgDetailGolden(t *testing.T) {
	for _, size := range []struct{ w, h int }{{80, 24}, {120, 34}, {160, 40}} {
		snap := orgFixture()
		for i := range snap.Agents {
			if snap.Agents[i].SessionID == childSessionID {
				snap.Agents[i].Scopes = []protocol.Scope{
					{Node: "org-research", Kind: protocol.ScopeReview, Over: protocol.ScopeOverAll},
					{Node: "org-research", Kind: protocol.ScopeMerge, Over: "team:forge"},
				}
			}
		}
		m := quietFixtures(treeModel(size.w, size.h, snap))
		m.expand = true
		golden(t, fmt.Sprintf("tree_org_detail_%dx%d", size.w, size.h), selectID(m, childSessionID).testView())
	}
}

// A session whose Agent's pinned lead session is gone shows its name in the
// warning colour, so the owner sees the lead needs a new one.
func TestGoneLeadSessionWarnsItsAgentRows(t *testing.T) {
	const goneLead = "s_0000000000000399"
	old := testColorProfile
	defer setTestColorProfile(old)
	setTestColorProfile(termenv.TrueColor)
	snap := orgFixture()
	for i := range snap.Agents {
		if snap.Agents[i].SessionID == leadSessionID {
			snap.Agents[i].AgentLeadSession = goneLead
			snap.Agents[i].RepoLeadExplicit = false
		}
	}
	m := treeModel(120, 34, snap)
	var lead protocol.AgentView
	for _, a := range m.snap.Agents {
		if a.SessionID == leadSessionID {
			lead = a
		}
	}
	if got, want := m.sessionNameCell(lead, treeMark{}, 30), sBad.Render(cell("strategy lead", 30)); got != want {
		t.Errorf("gone lead row %q, want the warning colour on its name", got)
	}
	healthy := lead
	healthy.AgentLeadSession = ""
	if got := m.sessionNameCell(healthy, treeMark{}, 30); strings.Contains(got, sBad.Render(cell("strategy lead", 30))) {
		t.Errorf("row with no gone lead is in the warning colour: %q", got)
	}
}
