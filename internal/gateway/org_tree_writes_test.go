package gateway

import (
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

// agentSession makes an extra session of a named Agent in a Repo, the way the
// owner assigns a lasting session to another Agent.
func agentSession(t *testing.T, s *Store, project, agent string) string {
	t.Helper()
	var node string
	if err := s.db.QueryRow(`SELECT node_id FROM sessions LIMIT 1`).Scan(&node); err != nil {
		t.Fatal(err)
	}
	if err := s.GraphLifecycle("agent.create", protocol.GraphLifecycleRequest{Name: agent}, "owner", false); err != nil {
		t.Fatal(err)
	}
	id, err := s.UpsertSession(node, protocol.SessionInfo{Harness: "claude", NativeID: "writes-" + agent + "-" + project,
		Repo: protocol.RepoInfo{Root: "/w/" + project, Project: project}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.Exec(`UPDATE sessions SET agent_id=? WHERE id=?`, agent, id); err != nil {
		t.Fatal(err)
	}
	return id
}

func sessionID(t *testing.T, s *Store, native string) string {
	t.Helper()
	var id string
	if err := s.db.QueryRow(`SELECT id FROM sessions WHERE native_id=?`, native).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

func leadSession(t *testing.T, s *Store, ref protocol.GraphRef, session string) {
	t.Helper()
	if err := s.SetLead(protocol.LeadSetRequest{Ref: ref, Lead: &protocol.GraphRef{Kind: "session", ID: session}}, "owner", false); err != nil {
		t.Fatal(err)
	}
}

func agentLeadSession(t *testing.T, s *Store, agent string) string {
	t.Helper()
	var session string
	if err := s.db.QueryRow(`SELECT COALESCE(lead_session,'') FROM agents WHERE id=?`, agent).Scan(&session); err != nil {
		t.Fatal(err)
	}
	return session
}

// A repo lead change moves every Agent that works in the repo, and moves the
// lead session copy with it.
func TestRepoLeadChangeMovesRepoAgents(t *testing.T) {
	s := teamStore(t)
	demoSession := sessionID(t, s, "n-demo")
	leadSessionID := agentSession(t, s, "demo", "lead2")
	agentSession(t, s, "demo", "visitor")
	leadSession(t, s, protocol.GraphRef{Kind: "repo", ID: "demo"}, demoSession)
	if got := reportsTo(t, s, "visitor"); got != "demo" {
		t.Fatalf("before the change visitor reports to %q, want demo", got)
	}
	leadSession(t, s, protocol.GraphRef{Kind: "repo", ID: "demo"}, leadSessionID)
	if got := reportsTo(t, s, "visitor"); got != "lead2" {
		t.Fatalf("visitor reports to %q after the repo lead changed, want lead2", got)
	}
	if got := agentLeadSession(t, s, "lead2"); got != leadSessionID {
		t.Fatalf("lead2 lead_session=%q, want %q", got, leadSessionID)
	}
	if got := agentLeadSession(t, s, "demo"); got != "" {
		t.Fatalf("the old repo lead keeps lead_session %q", got)
	}
}

// A team lead change moves the team's Agent members to the new lead.
func TestTeamLeadChangeMovesMembers(t *testing.T) {
	s := teamStore(t)
	demoSession := sessionID(t, s, "n-demo")
	otherSession := sessionID(t, s, "n-other")
	if err := s.GraphLifecycle("agent.create", protocol.GraphLifecycleRequest{Name: "helper"}, "owner", false); err != nil {
		t.Fatal(err)
	}
	teamWrite(t, s, protocol.TeamWrite{Action: "create", ID: "alpha", Name: "Alpha", Owner: &protocol.GraphRef{Kind: "agent", ID: "demo"}})
	teamWrite(t, s, protocol.TeamWrite{Action: "membership", ID: "alpha", Member: &protocol.GraphRef{Kind: "agent", ID: "helper"}})
	if got := reportsTo(t, s, "helper"); got != "demo" {
		t.Fatalf("helper reports to %q, want demo", got)
	}
	leadSession(t, s, protocol.GraphRef{Kind: "team", ID: "alpha"}, demoSession)
	leadSession(t, s, protocol.GraphRef{Kind: "team", ID: "alpha"}, otherSession)
	if got := reportsTo(t, s, "helper"); got != "other" {
		t.Fatalf("helper reports to %q after the team lead changed, want other", got)
	}
	if got := agentLeadSession(t, s, "other"); got != otherSession {
		t.Fatalf("other lead_session=%q, want %q", got, otherSession)
	}
	if got := agentLeadSession(t, s, "demo"); got != "" {
		t.Fatalf("the old team lead keeps lead_session %q", got)
	}
}

// A Team member add and remove moves that member's parent.
func TestTeamMemberChangeMovesTheMember(t *testing.T) {
	s := teamStore(t)
	if err := s.GraphLifecycle("agent.create", protocol.GraphLifecycleRequest{Name: "helper"}, "owner", false); err != nil {
		t.Fatal(err)
	}
	teamWrite(t, s, protocol.TeamWrite{Action: "create", ID: "alpha", Name: "Alpha", Owner: &protocol.GraphRef{Kind: "agent", ID: "demo"}})
	teamWrite(t, s, protocol.TeamWrite{Action: "membership", ID: "alpha", Member: &protocol.GraphRef{Kind: "agent", ID: "helper"}})
	if got := reportsTo(t, s, "helper"); got != "demo" {
		t.Fatalf("helper reports to %q after it joined, want demo", got)
	}
	teamWrite(t, s, protocol.TeamWrite{Action: "membership", ID: "", ExpectTeam: "alpha", Member: &protocol.GraphRef{Kind: "agent", ID: "helper"}})
	if got := reportsTo(t, s, "helper"); got != "" {
		t.Fatalf("helper still reports to %q after it left", got)
	}
}

// A repo owner change, here the architect delete trigger clearing the owner,
// moves the Agents that work in the repo.
func TestRepoOwnerChangeMovesRepoAgents(t *testing.T) {
	s, _ := newTestStore(t)
	node, _, err := s.CreateNode("shop-node", "linux")
	if err != nil {
		t.Fatal(err)
	}
	if err = s.AddArchitect("advisor", "owner"); err != nil {
		t.Fatal(err)
	}
	if err = s.AddArchitect("visitor", "owner"); err != nil {
		t.Fatal(err)
	}
	if _, err = s.AddProjects(node, protocol.ProjectAdd{Projects: []protocol.ProjectEntry{{Name: "shop", Path: "/w/shop"}}}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.Exec(`UPDATE repos SET owner_agent_id='advisor' WHERE id='shop'`); err != nil {
		t.Fatal(err)
	}
	session, err := s.UpsertSession(node, protocol.SessionInfo{Harness: "claude", NativeID: "visit",
		Repo: protocol.RepoInfo{Root: "/w/shop", Project: "shop"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.Exec(`UPDATE sessions SET agent_id='visitor' WHERE id=?`, session); err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.Exec(`INSERT INTO graph_edges(source,target,kind,mode,rule) VALUES('visitor','advisor','reports_to','','normal')`); err != nil {
		t.Fatal(err)
	}
	if err = s.RemoveArchitect("advisor", "owner"); err != nil {
		t.Fatal(err)
	}
	if got := reportsTo(t, s, "visitor"); got != "" {
		t.Fatalf("visitor still reports to %q after the repo owner went", got)
	}
}

// A restored merger gets its role and edge back, as a started merger has them.
func TestMergerRestoreRebindsItsAgent(t *testing.T) {
	s, merger := mergerRig(t)
	var node string
	if err := s.db.QueryRow(`SELECT node_id FROM sessions WHERE id=?`, merger).Scan(&node); err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		`DELETE FROM graph_edges WHERE source='demo-merger' AND kind='reports_to'`,
		`UPDATE agents SET role='' WHERE id='demo-merger'`,
		`UPDATE sessions SET agent_id='demo' WHERE id='` + merger + `'`,
	} {
		if _, err := s.db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.RestoreMergerRegistrations("demo", []protocol.MergerSetRequest{{Project: "demo", NodeID: node, SessionID: merger}}); err != nil {
		t.Fatal(err)
	}
	var role, agent string
	if err := s.db.QueryRow(`SELECT a.role,s.agent_id FROM sessions s JOIN agents a ON a.id=s.agent_id WHERE s.id=?`, merger).Scan(&role, &agent); err != nil {
		t.Fatal(err)
	}
	if role != "merger" || agent != "demo-merger" {
		t.Fatalf("restored merger role=%q agent=%q", role, agent)
	}
	if got := reportsTo(t, s, "demo-merger"); got != "demo" {
		t.Fatalf("restored merger reports to %q, want demo", got)
	}
}

// The build and the per-write update share one rule: an explicit repo_leads
// row wins over repos.owner_agent_id.
func TestOrgTreeBuildPrefersRepoLead(t *testing.T) {
	s := teamStore(t)
	agentSession(t, s, "demo", "visitor")
	if _, err := s.db.Exec(`INSERT INTO repo_leads(repo_id,agent_id) VALUES('demo','other')`); err != nil {
		t.Fatal(err)
	}
	rewindOrgTree(t, s)
	if got := reportsTo(t, s, "visitor"); got != "other" {
		t.Fatalf("visitor reports to %q, want the explicit repo lead other", got)
	}
}

// A lead undo is a lead write: the tree follows it back.
func TestLeadUndoMovesTheTree(t *testing.T) {
	s := teamStore(t)
	teamWrite(t, s, protocol.TeamWrite{Action: "create", ID: "alpha", Name: "Alpha"})
	if err := s.SetLead(protocol.LeadSetRequest{Ref: protocol.GraphRef{Kind: "team", ID: "alpha"}, Lead: &protocol.GraphRef{Kind: "agent", ID: "demo"}}, "cto", false); err != nil {
		t.Fatal(err)
	}
	if got := reportsTo(t, s, "demo"); got != "cto" {
		t.Fatalf("team lead reports to %q, want cto", got)
	}
	history, err := s.CTOGraphHistory(0)
	if err != nil || len(history) == 0 {
		t.Fatalf("history %+v %v", history, err)
	}
	if err = s.GraphUndo(history[0].ID, "owner"); err != nil {
		t.Fatal(err)
	}
	if got := reportsTo(t, s, "demo"); got != "" {
		t.Fatalf("after the undo the former team lead still reports to %q", got)
	}
}
