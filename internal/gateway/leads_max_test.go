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
		want := other
		if pin := agentLeadSession(t, s, "other"); pin != "" {
			want = protocol.GraphRef{Kind: "session", ID: pin}
		}
		g, err := s.GraphForRef(&ref)
		if err != nil || len(g.Nodes) != 1 || g.Nodes[0].Lead == nil || *g.Nodes[0].Lead != want {
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

func TestSessionLeadSetRepo(t *testing.T) {
	s := teamStore(t)
	var sid string
	if err := s.db.QueryRow(`SELECT id FROM sessions WHERE repo_id='demo'`).Scan(&sid); err != nil {
		t.Fatal(err)
	}
	ref := protocol.GraphRef{Kind: "repo", ID: "demo"}
	lead := protocol.GraphRef{Kind: "session", ID: sid}
	if err := s.SetLead(protocol.LeadSetRequest{Ref: ref, Lead: &lead}, "owner", false); err != nil {
		t.Fatalf("set session lead: %v", err)
	}
	g, err := s.GraphForRef(&ref)
	if err != nil || len(g.Nodes) != 1 || g.Nodes[0].Lead == nil || *g.Nodes[0].Lead != lead {
		t.Fatalf("lead: %+v %v", g, err)
	}
}

func TestSessionLeadRoutingAndViews(t *testing.T) {
	s := teamStore(t)
	var first, node, outsider string
	if err := s.db.QueryRow(`SELECT id,node_id FROM sessions WHERE repo_id='demo'`).Scan(&first, &node); err != nil {
		t.Fatal(err)
	}
	if err := s.db.QueryRow(`SELECT id FROM sessions WHERE repo_id='other'`).Scan(&outsider); err != nil {
		t.Fatal(err)
	}
	second, err := s.UpsertSession(node, protocol.SessionInfo{Harness: "pi", NativeID: "second-lead", Repo: protocol.RepoInfo{Project: "demo"}})
	if err != nil {
		t.Fatal(err)
	}
	lead := protocol.GraphRef{Kind: "session", ID: first}
	for _, kind := range []string{"repo", "team"} {
		if kind == "team" {
			teamWrite(t, s, protocol.TeamWrite{Action: "create", ID: "alpha", Name: "Alpha"})
		}
		id := "demo"
		if kind == "team" {
			id = "alpha"
		}
		if err := s.SetLead(protocol.LeadSetRequest{Ref: protocol.GraphRef{Kind: kind, ID: id}, Lead: &lead}, "owner", false); err != nil {
			t.Fatal(err)
		}
		got, err := s.ResolveLeadAddress(kind + ":" + id)
		if err != nil || got != first {
			t.Fatalf("%s lead: %q %v", kind, got, err)
		}
	}
	for _, addr := range []string{"repo:demo", "team:alpha"} {
		m, _, _, err := s.StoreLeadMessage(protocol.MsgSend{Sender: outsider, Recipient: first, LeadAddress: addr, Body: "addressed"})
		if err != nil || m.To != first {
			t.Fatalf("address %s: %+v %v", addr, m, err)
		}
		if _, _, _, err := s.StoreLeadMessage(protocol.MsgSend{Sender: outsider, Recipient: second, LeadAddress: addr, Body: "forged"}); err == nil {
			t.Fatalf("%s accepted alternate while lead online", addr)
		}
	}
	a, _, err := s.Agent(first)
	if err != nil || !a.RepoLead {
		t.Fatalf("lead view: %+v %v", a, err)
	}
	a, _, err = s.Agent(second)
	if err != nil || a.RepoLead {
		t.Fatalf("other view: %+v %v", a, err)
	}
	m, _, _, err := s.StoreLeadMessage(protocol.MsgSend{Sender: outsider, Recipient: second, Body: "hello"})
	if err != nil || m.To != first {
		t.Fatalf("redirect: %+v %v", m, err)
	}
	if _, err := s.db.Exec(`UPDATE sessions SET online=0 WHERE id=?`, first); err != nil {
		t.Fatal(err)
	}
	// The pinned lead session keeps the address when offline. Another live
	// session of its Agent is not the lead.
	for _, addr := range []string{"repo:demo", "team:alpha"} {
		got, err := s.ResolveLeadAddress(addr)
		if err != nil || got != first {
			t.Fatalf("offline pinned %s: %q %v", addr, got, err)
		}
	}
	m, _, _, err = s.StoreLeadMessage(protocol.MsgSend{Sender: outsider, Recipient: second, Body: "hello again"})
	if err != nil || m.To != first {
		t.Fatalf("offline pinned redirect: %+v %v", m, err)
	}
}
