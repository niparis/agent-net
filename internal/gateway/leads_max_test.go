package gateway

import (
	"encoding/json"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

func TestMaxAgentsSettingAndCount(t *testing.T) {
	s := teamStore(t)
	change := SettingsChange{Key: "max_agents", Level: "repo", Scope: "demo", Value: json.RawMessage(`1`)}
	if _, err := s.WriteSettings([]SettingsChange{change}, "owner", "limit"); err != nil {
		t.Fatal(err)
	}
	info, err := s.RepoAgentLimit("demo")
	if err != nil || info.MaxAgents != 1 || info.AgentCount != 1 || info.OverMax {
		t.Fatalf("limit: %+v %v", info, err)
	}
	_, node := graphSessions(t, s, "extra")
	_, err = s.UpsertSession(node, protocol.SessionInfo{Harness: "pi", NativeID: "terminal", Repo: protocol.RepoInfo{Project: "demo"}})
	if err != nil {
		t.Fatalf("terminal registration refused: %v", err)
	}
	info, err = s.RepoAgentLimit("demo")
	if err != nil || info.AgentCount != 2 || !info.OverMax {
		t.Fatalf("over max: %+v %v", info, err)
	}
	change.Value = json.RawMessage(`-1`)
	change.Expect = json.RawMessage(`1`)
	if _, err = s.WriteSettings([]SettingsChange{change}, "owner", "bad"); err == nil {
		t.Fatal("negative maximum accepted")
	}
}

func TestLeadSetRepoAndTeam(t *testing.T) {
	s := teamStore(t)
	repo := protocol.GraphRef{Kind: "repo", ID: "demo"}
	team := protocol.GraphRef{Kind: "team", ID: "alpha"}
	other := protocol.GraphRef{Kind: "agent", ID: "other"}
	teamWrite(t, s, protocol.TeamWrite{Action: "create", ID: "alpha", Name: "Alpha"})
	if err := s.SetLead(protocol.LeadSetRequest{Ref: repo, Lead: &other}, "owner", false); err == nil {
		t.Fatal("unrelated repo agent accepted")
	}
	if _, err := s.db.Exec(`UPDATE sessions SET agent_id='other' WHERE repo_id='demo'`); err != nil {
		t.Fatal(err)
	}
	for _, ref := range []protocol.GraphRef{repo, team} {
		if err := s.SetLead(protocol.LeadSetRequest{Ref: ref, Lead: &other}, "owner", false); err != nil {
			t.Fatalf("set %s: %v", ref.Kind, err)
		}
		g, err := s.GraphForRef(&ref)
		if err != nil || len(g.Nodes) != 1 || g.Nodes[0].Lead == nil || *g.Nodes[0].Lead != other {
			t.Fatalf("%s lead: %+v %v", ref.Kind, g, err)
		}
	}
	if err := s.SetLead(protocol.LeadSetRequest{Ref: repo}, "owner", false); err != nil {
		t.Fatal(err)
	}
	g, err := s.GraphForRef(&repo)
	if err != nil || g.Nodes[0].Lead != nil {
		t.Fatalf("clear lead: %+v %v", g, err)
	}
}

func TestLeadAndMaxSessionViews(t *testing.T) {
	s := teamStore(t)
	var sid string
	if err := s.db.QueryRow(`SELECT id FROM sessions WHERE repo_id='demo'`).Scan(&sid); err != nil {
		t.Fatal(err)
	}
	_, err := s.WriteSettings([]SettingsChange{{Key: "max_agents", Level: "repo", Scope: "demo", Value: json.RawMessage(`3`)}}, "owner", "limit")
	if err != nil {
		t.Fatal(err)
	}
	lead := protocol.GraphRef{Kind: "agent", ID: "demo"}
	teamWrite(t, s, protocol.TeamWrite{Action: "create", ID: "alpha", Name: "Alpha", Owner: &lead})
	repo := protocol.GraphRef{Kind: "repo", ID: "demo"}
	teamWrite(t, s, protocol.TeamWrite{Action: "membership", ID: "alpha", Member: &repo})
	a, ok, err := s.Agent(sid)
	if err != nil || !ok || !a.RepoLead || a.RepoLeadExplicit || !a.TeamLead || a.RepoMaxAgents != 3 || a.RepoAgentCount != 1 {
		t.Fatalf("session: %+v %v", a, err)
	}
	if err := s.SetLead(protocol.LeadSetRequest{Ref: repo, Lead: &lead}, "owner", false); err != nil {
		t.Fatal(err)
	}
	a, _, err = s.Agent(sid)
	if err != nil || !a.RepoLead || !a.RepoLeadExplicit {
		t.Fatalf("explicit repo lead: %+v %v", a, err)
	}
}

func TestLeadAgentCannotBeTombstoned(t *testing.T) {
	s := teamStore(t)
	if err := s.AddArchitect("helper", "owner"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`UPDATE sessions SET agent_id='helper' WHERE repo_id='demo'`); err != nil {
		t.Fatal(err)
	}
	lead := protocol.GraphRef{Kind: "agent", ID: "helper"}
	repo := protocol.GraphRef{Kind: "repo", ID: "demo"}
	if err := s.SetLead(protocol.LeadSetRequest{Ref: repo, Lead: &lead}, "owner", false); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`UPDATE agents SET tombstone=1 WHERE id='helper'`); err == nil {
		t.Fatal("tombstoned a repo lead")
	}
}
