package gateway

import (
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

// A Repo lead whose Repo is in no Team reports to the CTO. Every reader of the
// computed parent says so, so the Agent appears under the CTO rather than at
// the top of the tree.
func TestSoloRepoLeadReportsToTheCTO(t *testing.T) {
	s := teamStore(t)
	demoSession := sessionID(t, s, "n-demo")
	if err := s.AddArchitect("helper", "owner"); err != nil {
		t.Fatal(err)
	}
	var node string
	if err := s.db.QueryRow(`SELECT node_id FROM sessions WHERE id=?`, demoSession).Scan(&node); err != nil {
		t.Fatal(err)
	}
	helper, err := s.UpsertSession(node, protocol.SessionInfo{Harness: "pi", NativeID: "helper", Repo: protocol.RepoInfo{Project: "demo"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.Exec(`UPDATE sessions SET agent_id='helper',name='Helper' WHERE id=?`, helper); err != nil {
		t.Fatal(err)
	}

	if got := reportsTo(t, s, "demo"); got != "cto" {
		t.Fatalf("the solo repo lead reports to %q, want cto", got)
	}
	agents, err := s.Agents(protocol.QueryAgents{})
	if err != nil {
		t.Fatal(err)
	}
	listed := map[string]string{}
	for _, a := range agents {
		listed[a.SessionID] = a.ReportsTo
	}
	if got := listed[demoSession]; got != "cto" {
		t.Fatalf("the session list says the solo repo lead reports to %q, want cto", got)
	}
	got, err := s.GetSession(protocol.SessionGetRequest{Session: demoSession})
	if err != nil {
		t.Fatal(err)
	}
	if got.Session.ReportsTo != "cto" {
		t.Fatalf("agent_session_get says the solo repo lead reports to %q, want cto", got.Session.ReportsTo)
	}
	g, err := s.GraphForRef(nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := agentNodes(g)["demo"].ReportsTo; got != "cto" {
		t.Fatalf("the typed graph says the solo repo lead reports to %q, want cto", got)
	}
	text, err := s.placeText(demoSession)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(text, "You report to the CTO.") {
		t.Fatalf("the place text does not name the CTO:\n%s", text)
	}
	placed, err := s.GraphForRefWithPlaces(&protocol.GraphRef{Kind: "agent", ID: "demo"}, true)
	if err != nil {
		t.Fatal(err)
	}
	if node := graphNodeByRef(t, placed, protocol.GraphRef{Kind: "agent", ID: "demo"}); node.Place == nil || node.Place.ReportsTo != "the CTO" {
		t.Fatalf("the graph place reports to %+v, want the CTO", node.Place)
	}
}

// The other tree rules still hold around a solo repo lead.
func TestSoloRepoLeadKeepsTheOtherTreeRules(t *testing.T) {
	t.Run("a repo in a team reports to its team lead", func(t *testing.T) {
		s := teamStore(t)
		teamWrite(t, s, protocol.TeamWrite{Action: "create", ID: "alpha", Name: "Alpha", Owner: &protocol.GraphRef{Kind: "agent", ID: "other"}})
		teamWrite(t, s, protocol.TeamWrite{Action: "membership", ID: "alpha", Member: &protocol.GraphRef{Kind: "repo", ID: "demo"}})
		if got := reportsTo(t, s, "demo"); got != "other" {
			t.Fatalf("the repo lead in a team reports to %q, want the team lead other", got)
		}
		if got := reportsTo(t, s, "other"); got != "cto" {
			t.Fatalf("the team lead reports to %q, want cto", got)
		}
	})

	t.Run("the chief reports to the owner", func(t *testing.T) {
		s := teamStore(t)
		ids, _ := graphSessions(t, s, "chief")
		if _, err := s.db.Exec(`INSERT OR REPLACE INTO gateway_state(key,value,updated_at) VALUES('chief_session',?,0)`, ids["chief"]); err != nil {
			t.Fatal(err)
		}
		if got := reportsTo(t, s, "chief"); got != protocol.OrgRoot {
			t.Fatalf("the chief reports to %q, want the owner", got)
		}
	})

	t.Run("a merger reports to its repo lead", func(t *testing.T) {
		s, _ := mergerRig(t)
		if got := reportsTo(t, s, "demo-merger"); got != "demo" {
			t.Fatalf("the merger reports to %q, want the repo lead demo", got)
		}
	})

	t.Run("an explicit move wins", func(t *testing.T) {
		s := teamStore(t)
		if err := s.SetEdge(reportsEdge("demo", "other"), "owner"); err != nil {
			t.Fatal(err)
		}
		if got := reportsTo(t, s, "demo"); got != "other" {
			t.Fatalf("an explicit move left the solo repo lead at %q, want other", got)
		}
	})

	t.Run("an archived repo does not place its former lead", func(t *testing.T) {
		s := teamStore(t)
		if _, err := s.db.Exec(`UPDATE repos SET archived=1 WHERE id='demo'`); err != nil {
			t.Fatal(err)
		}
		if got := reportsTo(t, s, "demo"); got != "" {
			t.Fatalf("an archived repo placed its lead at %q, want no parent", got)
		}
	})
}

// Leading a Repo outranks working in another Repo. An Agent that leads a solo
// Repo and also has a session in another Repo reports to the CTO, not to that
// other Repo's lead.
func TestSoloRepoLeadWhoWorksInAnotherRepoReportsToTheCTO(t *testing.T) {
	s := teamStore(t)
	var node string
	if err := s.db.QueryRow(`SELECT node_id FROM sessions WHERE repo_id='other'`).Scan(&node); err != nil {
		t.Fatal(err)
	}
	visiting, err := s.UpsertSession(node, protocol.SessionInfo{Harness: "claude", NativeID: "visiting", Repo: protocol.RepoInfo{Project: "other"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.Exec(`UPDATE sessions SET agent_id='demo' WHERE id=?`, visiting); err != nil {
		t.Fatal(err)
	}
	if got := reportsTo(t, s, "demo"); got != "cto" {
		t.Fatalf("the solo repo lead who works in another repo reports to %q, want cto", got)
	}
}

// The new parent is display and reporting only: messages to a member of a solo
// Repo route as before for every sender, and the CTO gains no order right.
func TestSoloRepoLeadRoutingAndOrderRightsAreUnchanged(t *testing.T) {
	s := teamStore(t)
	var lead, node string
	if err := s.db.QueryRow(`SELECT id,node_id FROM sessions WHERE repo_id='demo'`).Scan(&lead, &node); err != nil {
		t.Fatal(err)
	}
	if err := s.AddArchitect("helper", "owner"); err != nil {
		t.Fatal(err)
	}
	helper, err := s.UpsertSession(node, protocol.SessionInfo{Harness: "pi", NativeID: "helper", Repo: protocol.RepoInfo{Project: "demo"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.Exec(`UPDATE sessions SET agent_id='helper',name='Helper' WHERE id=?`, helper); err != nil {
		t.Fatal(err)
	}
	outsider := sessionID(t, s, "n-other")
	owner, err := s.UpsertSession(node, protocol.SessionInfo{Harness: "owner", NativeID: protocol.OwnerNativeID})
	if err != nil {
		t.Fatal(err)
	}
	cto, err := s.RegisterCTOSession(node)
	if err != nil {
		t.Fatal(err)
	}

	if _, ok, err := orderAbove(s.db, lead, helper, s.now()); err != nil || !ok {
		t.Fatalf("the repo lead is no longer above its member: ok=%v err=%v", ok, err)
	}
	for name, from := range map[string]string{"the CTO": cto, "the owner": owner} {
		if _, ok, err := orderAbove(s.db, from, lead, s.now()); err != nil || ok {
			t.Fatalf("%s gained an order right over a solo repo lead: ok=%v err=%v", name, ok, err)
		}
	}

	to := map[string]string{"repo lead": lead, "owner": owner, "cto": cto}
	for name, sender := range to {
		m, _, route, err := s.StoreLeadMessage(protocol.MsgSend{Sender: sender, Recipient: helper, Body: "hello"})
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if m.To != helper {
			t.Fatalf("a message from the %s went to %s (%q), want the member %s", name, m.To, route, helper)
		}
	}
	m, _, route, err := s.StoreLeadMessage(protocol.MsgSend{Sender: outsider, Recipient: helper, Body: "hello"})
	if err != nil {
		t.Fatal(err)
	}
	if m.To != lead || !strings.Contains(route, "Redirected") {
		t.Fatalf("an outsider's message went to %s (%q), want the repo lead %s", m.To, route, lead)
	}
}

// A second session of the CTO's Agent is not the CTO session. The reporting
// line from a solo Repo lead to the CTO gives no command right, so this
// sender's request to a member of the solo Repo still goes to that Repo's
// lead, as it did before the line existed.
func TestCTOAgentsOtherSessionRoutesToTheSoloRepoLead(t *testing.T) {
	s := teamStore(t)
	var lead, node string
	if err := s.db.QueryRow(`SELECT id,node_id FROM sessions WHERE repo_id='demo'`).Scan(&lead, &node); err != nil {
		t.Fatal(err)
	}
	if err := s.AddArchitect("helper", "owner"); err != nil {
		t.Fatal(err)
	}
	helper, err := s.UpsertSession(node, protocol.SessionInfo{Harness: "pi", NativeID: "helper", Repo: protocol.RepoInfo{Project: "demo"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.Exec(`UPDATE sessions SET agent_id='helper',name='Helper' WHERE id=?`, helper); err != nil {
		t.Fatal(err)
	}
	if _, err = s.RegisterCTOSession(node); err != nil {
		t.Fatal(err)
	}
	// A second session runs as the CTO's Agent, in another Repo. It is not
	// the CTO session, so the routing rules apply to it.
	other, err := s.UpsertSession(node, protocol.SessionInfo{Harness: "claude", NativeID: "cto-other", Repo: protocol.RepoInfo{Project: "other"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.Exec(`UPDATE sessions SET agent_id='cto',name='CTO helper' WHERE id=?`, other); err != nil {
		t.Fatal(err)
	}
	m, _, route, err := s.StoreLeadMessage(protocol.MsgSend{Sender: other, Recipient: helper, Body: "hello"})
	if err != nil {
		t.Fatal(err)
	}
	if m.To != lead || !strings.Contains(route, "Redirected") {
		t.Fatalf("the CTO Agent's other session went to %s (%q), want the repo lead %s", m.To, route, lead)
	}
}
