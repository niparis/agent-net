package gateway

import (
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

// A Repo whose repo_leads row names no lead has no Repo lead in the tree, so
// routing and the graph agree on that instead of the graph naming the owner.
func TestClearedRepoLeadIsNotTheRepoOwner(t *testing.T) {
	s := teamStore(t)
	var node, outsider string
	if err := s.db.QueryRow(`SELECT node_id FROM sessions WHERE repo_id='demo'`).Scan(&node); err != nil {
		t.Fatal(err)
	}
	if err := s.db.QueryRow(`SELECT id FROM sessions WHERE repo_id='other'`).Scan(&outsider); err != nil {
		t.Fatal(err)
	}
	helper, err := s.UpsertSession(node, protocol.SessionInfo{Harness: "pi", NativeID: "helper", Repo: protocol.RepoInfo{Project: "demo"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.Exec(`UPDATE sessions SET agent_id='helper' WHERE id=?`, helper); err != nil {
		t.Fatal(err)
	}
	if err = s.SetLead(protocol.LeadSetRequest{Ref: protocol.GraphRef{Kind: "repo", ID: "demo"}}, "owner", false); err != nil {
		t.Fatal(err)
	}
	m, _, route, err := s.StoreLeadMessage(protocol.MsgSend{Sender: outsider, Recipient: helper, Body: "hello"})
	if err != nil {
		t.Fatal(err)
	}
	if m.To != helper || !strings.Contains(route, "No lead is set") {
		t.Fatalf("request went to %s (%q), want the helper with no lead", m.To, route)
	}
	g, err := s.GraphForRefWithPlaces(&protocol.GraphRef{Kind: "repo", ID: "demo"}, true)
	if err != nil {
		t.Fatal(err)
	}
	repo := graphNodeByRef(t, g, protocol.GraphRef{Kind: "repo", ID: "demo"})
	if repo.Place == nil {
		t.Fatal("repo node has no place")
	}
	if repo.Place.RepoLead != "" {
		t.Fatalf("the graph names %q as the repo lead, but routing says no lead", repo.Place.RepoLead)
	}
}

// The place text names the parent the tree stores. A team lead under the CTO
// is told so.
func TestPlaceTextNamesTheTreeParent(t *testing.T) {
	r := newPlaceRig(t)
	if err := r.s.SetEdge(reportsEdge("other", "cto"), "owner"); err != nil {
		t.Fatal(err)
	}
	text, err := r.s.placeText(r.lead)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(text, "You report to the CTO.") {
		t.Fatalf("place text does not name the tree parent:\n%s", text)
	}
}

// Authority follows the direct lead only. An agent higher in the tree is not
// above an agent below another lead, even when the tree links them.
func TestAuthorityStaysWithTheDirectLead(t *testing.T) {
	s := teamStore(t)
	var lead, node, other string
	if err := s.db.QueryRow(`SELECT id,node_id FROM sessions WHERE repo_id='demo'`).Scan(&lead, &node); err != nil {
		t.Fatal(err)
	}
	if err := s.db.QueryRow(`SELECT id FROM sessions WHERE repo_id='other'`).Scan(&other); err != nil {
		t.Fatal(err)
	}
	if err := s.AddArchitect("helper", "owner"); err != nil {
		t.Fatal(err)
	}
	helper, err := s.UpsertSession(node, protocol.SessionInfo{Harness: "pi", NativeID: "helper", Repo: protocol.RepoInfo{Project: "demo"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.Exec(`UPDATE sessions SET agent_id='helper' WHERE id=?`, helper); err != nil {
		t.Fatal(err)
	}
	if err = s.GraphLifecycle("agent.create", protocol.GraphLifecycleRequest{Name: "chief"}, "owner", false); err != nil {
		t.Fatal(err)
	}
	chief, err := s.UpsertSession(node, protocol.SessionInfo{Harness: "claude", NativeID: "chief", Repo: protocol.RepoInfo{Project: "other"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.Exec(`UPDATE sessions SET agent_id='chief' WHERE id=?`, chief); err != nil {
		t.Fatal(err)
	}
	for _, e := range []protocol.Edge{
		reportsEdge("helper", "demo"),
		reportsEdge("demo", "other"),
		reportsEdge("other", "chief"),
	} {
		if err := s.SetEdge(e, "owner"); err != nil {
			t.Fatal(err)
		}
	}
	if m, route := sendFlag(t, s, lead, helper, "write the tests", true); !m.IsOrder {
		t.Fatalf("the direct repo lead was not above its agent: %q", route)
	}
	for name, from := range map[string]string{"the lead two levels up": other, "the chief three levels up": chief} {
		m, route := sendFlag(t, s, from, helper, "do this now", true)
		if m.IsOrder || !strings.Contains(route, "not above") {
			t.Fatalf("%s was above the helper: order=%v route %q", name, m.IsOrder, route)
		}
	}
}
