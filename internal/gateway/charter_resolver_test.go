package gateway

import (
	"reflect"
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

// The Agents list resolves every row's charter in memory. It must match a
// single EffectiveCharter call for the same row, with a Repo in a Team other
// than its Agent's own Team, and with a row that belongs to no Team.
func TestAgentListChartersMatchEffectiveCharter(t *testing.T) {
	s := teamStore(t)
	for _, q := range []protocol.TeamWrite{
		{Action: "create", ID: "alpha", Name: "Alpha", Purpose: "Alpha purpose"},
		{Action: "create", ID: "beta", Name: "Beta"},
	} {
		teamWrite(t, s, q)
	}
	teamWrite(t, s, protocol.TeamWrite{Action: "membership", ID: "alpha", Member: &protocol.GraphRef{Kind: "repo", ID: "demo"}})
	teamWrite(t, s, protocol.TeamWrite{Action: "membership", ID: "beta", Member: &protocol.GraphRef{Kind: "agent", ID: "demo"}})
	for _, q := range []protocol.CharterSetRequest{
		{Level: "global", Attr: "rules", Value: "global taste"},
		{Level: "global", Attr: "refuses", Value: "global secrets"},
		{Level: "team", Node: "alpha", Attr: "refuses", Value: "no alpha secrets"},
		{Level: "team", Node: "beta", Attr: "refuses", Value: "no beta secrets"},
		{Level: "repo", Node: "demo", Attr: "refuses", Value: "production"},
		{Level: "agent", Node: "demo", Attr: "refuses", Value: "secrets"},
		{Level: "agent", Node: "demo", Attr: "accepts", Value: "fixes"},
	} {
		if err := s.SetLevelCharter(q, "owner", false); err != nil {
			t.Fatal(err)
		}
	}
	// Pin the demo session's Agent so both its own Team and its Repo Team apply.
	if _, err := s.db.Exec(`UPDATE sessions SET agent_id='demo' WHERE repo_id='demo'`); err != nil {
		t.Fatal(err)
	}
	agents, err := s.Agents(protocol.QueryAgents{})
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, a := range agents {
		project := projectOfSession(a)
		want, err := s.EffectiveCharter(project, a.AgentID, "")
		if err != nil {
			t.Fatalf("%s: %v", a.SessionID, err)
		}
		if a.Charter.Effective == nil {
			t.Fatalf("%s: no effective charter", a.SessionID)
		}
		if !reflect.DeepEqual(*a.Charter.Effective, want) {
			t.Fatalf("%s (%q): batch %+v, single %+v", a.SessionID, project, *a.Charter.Effective, want)
		}
		seen[project] = true
	}
	if !seen["demo"] || !seen["other"] {
		t.Fatalf("fixture projects missing: %v", seen)
	}
	demo, err := s.EffectiveCharter("demo", "demo", "")
	if err != nil {
		t.Fatal(err)
	}
	items := map[string]bool{}
	for _, item := range protocol.SplitCharterList(demo.Refuses) {
		items[item] = true
	}
	for _, refusal := range []string{"global secrets", "no alpha secrets", "no beta secrets", "production", "secrets"} {
		if !items[refusal] {
			t.Fatalf("demo refuses missing %q: %q", refusal, demo.Refuses)
		}
	}
	if demo.Purpose != "Alpha purpose" || demo.Rules != "global taste" || demo.Accepts != "fixes" {
		t.Fatalf("demo charter: %+v", demo)
	}
}

// A team and a repo each list several refusals. Inheriting joins them into one
// list, so every reader must see the items separately: the resolver, the graph
// read and the per-turn text.
func TestInheritedRefusesBecomeSeparateItems(t *testing.T) {
	s := teamStore(t)
	teamWrite(t, s, protocol.TeamWrite{Action: "create", ID: "forge", Name: "Forge"})
	teamWrite(t, s, protocol.TeamWrite{Action: "membership", ID: "forge", Member: &protocol.GraphRef{Kind: "repo", ID: "demo"}})
	for _, q := range []protocol.CharterSetRequest{
		{Level: "team", Node: "forge", Attr: "refuses", Value: "a; b"},
		{Level: "repo", Node: "demo", Attr: "refuses", Value: "c; d"},
	} {
		if err := s.SetLevelCharter(q, "owner", false); err != nil {
			t.Fatal(err)
		}
	}
	c, err := s.EffectiveCharter("demo", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if c.Refuses != "a; b; c; d" {
		t.Fatalf("effective refuses = %q, want %q", c.Refuses, "a; b; c; d")
	}
	g, err := s.Graph()
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, n := range g.Nodes {
		if n.Name != "demo" {
			continue
		}
		found = true
		if n.Charter.Refuses != "a; b; c; d" {
			t.Fatalf("graph refuses = %q", n.Charter.Refuses)
		}
		text := protocol.NodeContext(g, n.Name)
		if !strings.Contains(text, "refuses: a; b; c; d\n") {
			t.Fatalf("turn text refuses = %q", text)
		}
	}
	if !found {
		t.Fatalf("graph has no demo node: %+v", g.Nodes)
	}
}

// The same refusal from two levels is one item in the effective charter.
func TestInheritedRefusesDropDuplicateItems(t *testing.T) {
	s := teamStore(t)
	teamWrite(t, s, protocol.TeamWrite{Action: "create", ID: "forge", Name: "Forge"})
	teamWrite(t, s, protocol.TeamWrite{Action: "membership", ID: "forge", Member: &protocol.GraphRef{Kind: "repo", ID: "demo"}})
	for _, q := range []protocol.CharterSetRequest{
		{Level: "team", Node: "forge", Attr: "refuses", Value: "a; shared"},
		{Level: "repo", Node: "demo", Attr: "refuses", Value: "shared; d"},
	} {
		if err := s.SetLevelCharter(q, "owner", false); err != nil {
			t.Fatal(err)
		}
	}
	c, err := s.EffectiveCharter("demo", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if c.Refuses != "a; shared; d" {
		t.Fatalf("effective refuses = %q, want %q", c.Refuses, "a; shared; d")
	}
}
