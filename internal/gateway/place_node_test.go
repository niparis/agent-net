package gateway

import (
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

// The Graph carries the same place the turn text builds: the team, its live
// lead, the mode, the repo lead and every held back item with its level.
func TestGraphNodePlaceMatchesTurnText(t *testing.T) {
	r := newPlaceRig(t)
	teamWrite(t, r.s, protocol.TeamWrite{Action: "mode", ID: "alpha", Mode: "absolute"})
	teamWrite(t, r.s, protocol.TeamWrite{Action: "held_back", ID: "alpha", HeldBack: "deploy to production"})
	teamWrite(t, r.s, protocol.TeamWrite{Action: "repo_held_back", ID: "demo", HeldBack: "force pushes"})

	agent, err := r.s.GraphForRefWithPlaces(&protocol.GraphRef{Kind: "agent", ID: "other"}, true)
	if err != nil {
		t.Fatal(err)
	}
	if plain, err := r.s.GraphForRef(&protocol.GraphRef{Kind: "agent", ID: "other"}); err != nil {
		t.Fatal(err)
	} else if got := graphNodeByRef(t, plain, protocol.GraphRef{Kind: "agent", ID: "other"}); got.Place != nil {
		t.Fatal("a graph read without Places paid for place data")
	}
	lead := graphNodeByRef(t, agent, protocol.GraphRef{Kind: "agent", ID: "other"})
	if lead.Place == nil {
		t.Fatal("agent node has no place")
	}
	if lead.Place.Team != "Alpha" || lead.Place.Mode != protocol.AuthorityAbsolute || lead.Place.TeamLead != "forge-lead" {
		t.Fatalf("agent place: %+v", *lead.Place)
	}
	if lead.Place.ReportsTo != "the Alpha lead" {
		t.Fatalf("agent reports to %q", lead.Place.ReportsTo)
	}

	repo, err := r.s.GraphForRefWithPlaces(&protocol.GraphRef{Kind: "repo", ID: "demo"}, true)
	if err != nil {
		t.Fatal(err)
	}
	member := graphNodeByRef(t, repo, protocol.GraphRef{Kind: "repo", ID: "demo"})
	if member.Place == nil {
		t.Fatal("repo node has no place")
	}
	p := *member.Place
	if p.Team != "Alpha" || p.Mode != protocol.AuthorityAbsolute || p.TeamLead != "forge-lead" || p.Repo != "demo" || p.RepoLead != "" {
		t.Fatalf("repo place: %+v", p)
	}
	want := []protocol.PlaceHeldBack{{Text: "deploy to production", From: "team"}, {Text: "force pushes", From: "repo"}}
	if len(p.HeldBack) != len(want) {
		t.Fatalf("held back: %+v", p.HeldBack)
	}
	for i := range want {
		if p.HeldBack[i] != want[i] {
			t.Fatalf("held back %d: %+v, want %+v", i, p.HeldBack[i], want[i])
		}
	}
	text, err := r.s.placeText(r.member)
	if err != nil || !strings.Contains(text, "Held back from your team: deploy to production; force pushes.") {
		t.Fatalf("turn text and card disagree: %q %v", text, err)
	}
}

func graphNodeByRef(t *testing.T, g protocol.Graph, ref protocol.GraphRef) protocol.Node {
	t.Helper()
	for _, n := range g.Nodes {
		if n.Ref != nil && *n.Ref == ref {
			return n
		}
	}
	t.Fatalf("no node %+v in %+v", ref, g.Nodes)
	return protocol.Node{}
}

// A lead pinned to a session with no title still names its Agent. The PLACE
// block must never fall back to the internal session ID.
func TestGraphPlaceTeamLeadNeverShowsSessionID(t *testing.T) {
	r := newPlaceRig(t)
	if _, err := r.s.db.Exec(`UPDATE sessions SET name='' WHERE id=?`, r.lead); err != nil {
		t.Fatal(err)
	}
	if _, err := r.s.db.Exec(`UPDATE agents SET lead_session=? WHERE id='other'`, r.lead); err != nil {
		t.Fatal(err)
	}
	team := protocol.GraphRef{Kind: "team", ID: "alpha"}
	g, err := r.s.GraphForRefWithPlaces(&team, true)
	if err != nil {
		t.Fatal(err)
	}
	node := graphNodeByRef(t, g, team)
	if node.Place == nil {
		t.Fatal("team node has no place")
	}
	if node.Place.TeamLead == r.lead {
		t.Fatalf("team lead is the session ID %q", r.lead)
	}
	if node.Place.TeamLead != "other" {
		t.Fatalf("team lead = %q, want the agent name %q", node.Place.TeamLead, "other")
	}
}
