package tui

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/charmbracelet/x/ansi"
	"github.com/niparis/agent-net/internal/protocol"
)

func TestLeadAndRepoLimitDisplay(t *testing.T) {
	m := Model{}
	a := protocol.AgentView{RepoLead: true, RepoLeadExplicit: true, Online: true, RepoMaxAgents: 3, RepoAgentCount: 4, RepoOverMax: true}
	if got := ansi.Strip(m.nameText(a, treeMark{}, 30)); !strings.Contains(got, "★ ") {
		t.Fatalf("lead mark missing: %q", got)
	}

	a.Repo.Project = "zeta"
	b := protocol.AgentView{RepoMaxAgents: 3, RepoAgentCount: 3}
	b.Repo.Project = "alpha"
	m.snap.Agents = []protocol.AgentView{b, a, a}
	if got := ansi.Strip(m.repoLimitsLine(80)); got != "agents per repo: zeta 4/3 over max · alpha 3/3" {
		t.Fatalf("limit line: %q", got)
	}
	if got := ansi.Strip(m.groupLine(group{host: "host", agents: []protocol.AgentView{a}}, 80)); strings.Contains(got, "4/3") {
		t.Fatalf("limit on machine: %q", got)
	}
	if got := ansi.Strip(m.repoLimitsLine(32)); !strings.HasSuffix(got, "…") || !strings.Contains(got, "zeta 4/3") {
		t.Fatalf("truncated limits: %q", got)
	}
}

func TestOwnerDefaultLeadHasNoMarkOrWidthCost(t *testing.T) {
	m := newModel(120, 34)
	for i := range m.snap.Agents {
		m.snap.Agents[i].RepoLead = true
		m.snap.Agents[i].RepoLeadExplicit = false
	}
	got := m.testView()
	if strings.Contains(got, "★") {
		t.Fatalf("owner-default leads should not be marked:\n%s", got)
	}
	golden(t, "leads_owner_default_120x34", got)
}

func TestStartMaxPromptKeys(t *testing.T) {
	m, base := workerModel(120, 34)
	src := &settingsValuesTestSource{fakeSource: base}
	m.src = src
	base.sessionStart = protocol.StartedSession{Status: "started"}
	change := &protocol.SettingsChange{Key: "max_agents", Level: "repo", Scope: "garden", Value: []byte("4"), Expect: []byte("3")}
	m, _ = m.onSessionStarted(sessionStartedMsg{req: protocol.SessionStartRequest{Project: "garden"}, res: protocol.StartedSession{Status: "failed", Error: "garden already has 3 agents (max 3). Stop one, or raise the max to 4.", SuggestedChange: change}})
	if m.sessionPending == nil || !strings.Contains(m.testView(), "y raise the max to 4 and start · esc cancel") {
		t.Fatal("missing max prompt")
	}
	cancelled, cmd := press(m, tea.KeyPressMsg{Code: tea.KeyEscape})
	if cancelled.sessionPending != nil || cmd != nil {
		t.Fatal("escape did not cancel")
	}
	_, cmd = press(m, tea.KeyPressMsg{Code: 'y', Text: "y"})
	if cmd == nil {
		t.Fatal("y did not raise max")
	}
	cmd()
	if len(src.write.Changes) != 1 || src.write.Changes[0].Scope != "garden" || len(base.sessionStarts) != 1 {
		t.Fatalf("write %+v starts %+v", src.write, base.sessionStarts)
	}
}

func TestTeamLeadPicker(t *testing.T) {
	m, base := workerModel(120, 34)
	src := &leadTestSource{fakeSource: base}
	m.src = src
	team := protocol.GraphRef{Kind: "team", ID: "team1"}
	agent := protocol.GraphRef{Kind: "agent", ID: "agent1"}
	m.mp.full = protocol.GraphView{Nodes: []protocol.Node{{Name: "A", Ref: &agent}}, Edges: []protocol.Edge{{Kind: "member_of", FromRef: &agent, ToRef: &team}}}
	m = m.startLeadPicker(team, "Team")
	m, _ = press(m, tea.KeyPressMsg{Code: tea.KeyDown})
	_, cmd := press(m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("selection did not save")
	}
	cmd()
	if src.lead.Lead == nil || *src.lead.Lead != agent || src.lead.Ref != team {
		t.Fatalf("wrong lead: %+v", src.lead)
	}
}

type leadTestSource struct {
	*fakeSource
	lead protocol.LeadSetRequest
}

func (s *leadTestSource) Call(_ context.Context, method string, arg any) (json.RawMessage, error) {
	if method == protocol.OpLeadSet {
		s.lead = arg.(protocol.LeadSetRequest)
	}
	return json.RawMessage(`{}`), nil
}

func TestLeadLimitGoldens(t *testing.T) {
	for _, size := range []struct{ w, h int }{{80, 24}, {120, 34}, {160, 40}} {
		m, base := workerModel(size.w, size.h)
		m.src = &settingsValuesTestSource{fakeSource: base}
		for i := range m.snap.Agents {
			if m.snap.Agents[i].SessionID == "s_0000000000000002" {
				m.snap.Agents[i].RepoLead = true
				m.snap.Agents[i].RepoLeadExplicit = true
				m.snap.Agents[i].RepoMaxAgents = 3
				m.snap.Agents[i].RepoAgentCount = 3
			}
			if m.snap.Agents[i].SessionID == "s_0000000000000001" {
				m.snap.Agents[i].RepoMaxAgents = 3
				m.snap.Agents[i].RepoAgentCount = 4
				m.snap.Agents[i].RepoOverMax = true
			}
		}
		golden(t, fmt.Sprintf("leads_sessions_%dx%d", size.w, size.h), m.testView())
		change := &protocol.SettingsChange{Key: "max_agents", Level: "repo", Scope: "garden", Value: []byte("4"), Expect: []byte("3")}
		m, _ = m.onSessionStarted(sessionStartedMsg{req: protocol.SessionStartRequest{Project: "garden"}, res: protocol.StartedSession{Status: "failed", Error: "garden already has 3 agents (max 3). Stop one, or raise the max to 4.", SuggestedChange: change}})
		golden(t, fmt.Sprintf("leads_refused_%dx%d", size.w, size.h), m.testView())
	}
}

func TestLeadLiveSession(t *testing.T) {
	m := Model{snap: Snapshot{Agents: []protocol.AgentView{{AgentID: "a", Online: true}}}}
	if !m.leadLive(protocol.AgentView{AgentID: "a", RepoLead: true}) {
		t.Fatal("another live session should keep the lead bright")
	}
	if m.leadLive(protocol.AgentView{AgentID: "b", RepoLead: true}) {
		t.Fatal("lead without a live session should be dim")
	}
}

func TestLeadPickerAndRepoCardGoldens(t *testing.T) {
	for _, size := range []struct{ w, h int }{{80, 24}, {120, 34}, {160, 40}} {
		v := teamGraphFixture()
		v.Nodes[2].MaxAgents = 3
		v.Nodes[2].AgentCount = 4
		v.Nodes[2].OverMax = true
		v.Nodes[2].Owner = teamRef("agent", "api-architect")
		m := mapModel(size.w, size.h, v)
		m.applyMap(mapMsg{view: v, at: t0})
		m.tab = graphTab
		m.mp.selected = "cogforge"
		m.mapOn = true
		golden(t, fmt.Sprintf("leads_repo_%dx%d", size.w, size.h), m.testView())
		m = m.startLeadPicker(protocol.GraphRef{Kind: "repo", ID: "cogforge"}, "cogforge")
		golden(t, fmt.Sprintf("leads_picker_%dx%d", size.w, size.h), m.testView())
	}
}

func TestRepoLimitCardLabel(t *testing.T) {
	if got := repoLimitCardText(4, 3); got != "agents 4/3 · over max" {
		t.Fatal(got)
	}
	if got := repoLimitCardText(3, 3); got != "agents 3/3" {
		t.Fatal(got)
	}
}

func TestRepoLimitsTruncateAt80Golden(t *testing.T) {
	m, _ := workerModel(80, 24)
	for i := range m.snap.Agents {
		if i < 4 {
			m.snap.Agents[i].RepoMaxAgents = 3
			m.snap.Agents[i].RepoAgentCount = 3
			m.snap.Agents[i].Repo.Project = fmt.Sprintf("project-with-long-name-%d", i)
		}
	}
	m.snap.Agents[3].RepoAgentCount = 4
	golden(t, "leads_limits_truncated_80x24", m.testView())
}
