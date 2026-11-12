package tui

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/niparis/agent-net/internal/protocol"
)

type proposalSource struct {
	fakeSource
	answers   []protocol.CTOProposalAnswer
	proposals []protocol.CTOProposal
	graph     protocol.Graph
}

func (s *proposalSource) Call(_ context.Context, method string, arg any) (json.RawMessage, error) {
	switch method {
	case protocol.OpProposalList:
		return json.Marshal(s.proposals)
	case protocol.OpAsksList:
		return json.Marshal(protocol.AsksResult{Asks: openProposalAsks(s.proposals)})
	case protocol.OpProposalAnswer:
		s.answers = append(s.answers, arg.(protocol.CTOProposalAnswer))
		return json.RawMessage(`{}`), nil
	case protocol.OpGraphGet:
		return json.Marshal(s.graph)
	}
	return json.RawMessage(`{}`), nil
}
func proposalFixture() protocol.CTOProposal {
	return protocol.CTOProposal{ID: "p_1", NodeID: "n_mac", Tool: "graph_change", Action: "charter_set", Args: json.RawMessage(`{"node":"shop","attr":"purpose","value":"new purpose"}`), Reason: "This reflects the current work.", Status: "open", CreatedAt: time.Unix(1700000000, 0)}
}
func TestProposalKeysAndConfirm(t *testing.T) {
	for _, key := range []string{"a", "r"} {
		t.Run(key, func(t *testing.T) {
			p := proposalFixture()
			s := &proposalSource{proposals: []protocol.CTOProposal{p}}
			m := New(s)
			m.proposals = s.proposals
			m.asks = openProposalAsks(s.proposals)
			m.tab = inboxTab
			_, cmd := m.update(tea.KeyPressMsg{Text: key, Code: []rune(key)[0]})
			if cmd == nil {
				t.Fatal("no command")
			}
			cmd()
			want := key
			if key == "r" {
				want = "x"
			}
			if len(s.answers) != 1 || s.answers[0].ID != p.ID || s.answers[0].NodeID != p.NodeID || s.answers[0].Action != want {
				t.Fatalf("answers: %+v", s.answers)
			}
		})
	}
	p := proposalFixture()
	s := &proposalSource{proposals: []protocol.CTOProposal{p}}
	m := New(s)
	m.proposals = s.proposals
	m.asks = openProposalAsks(s.proposals)
	m.tab = inboxTab
	updated, cmd := m.update(tea.KeyPressMsg{Text: "A", Code: []rune("A")[0]})
	m = updated.(Model)
	if cmd != nil || len(s.answers) != 0 || !strings.Contains(ansi.Strip(m.testView()), "Approve all") {
		t.Fatal("missing confirmation or ran early")
	}
	_, cmd = m.update(tea.KeyPressMsg{Text: "y", Code: []rune("y")[0]})
	if cmd == nil {
		t.Fatal("no confirm command")
	}
	cmd()
	if len(s.answers) != 1 || s.answers[0].Action != "A" || len(s.answers[0].Confirm) != 1 || s.answers[0].Confirm[0].ID != p.ID || s.answers[0].Confirm[0].NodeID != p.NodeID {
		t.Fatalf("answers: %+v", s.answers)
	}
}
func TestProposalExpiredHidden(t *testing.T) {
	p := proposalFixture()
	s := &proposalSource{fakeSource: fakeSource{snap: fakeSnapshot()}, proposals: []protocol.CTOProposal{p}}
	m := New(s)
	m.usage.at = m.now()
	updated, _ := m.update(m.fetchCmd()())
	m = updated.(Model)
	m = keys(m, "7")
	if len(m.proposals) != 1 || !strings.Contains(m.testView(), "CTO proposal") {
		t.Fatal("open proposal not fetched")
	}
	s.proposals[0].Status = "expired"
	updated, _ = m.update(m.fetchCmd()())
	m = updated.(Model)
	if got := len(m.inboxRows()); got != 0 {
		t.Fatalf("expired proposal shown in %d rows", got)
	}
}

func TestProposalFreshDiffAndResult(t *testing.T) {
	p := proposalFixture()
	p.Messages = []protocol.CTOProposalMessage{{Sender: "peer", At: time.Unix(1700000000, 0)}}
	s := &proposalSource{proposals: []protocol.CTOProposal{p}, graph: protocol.Graph{Nodes: []protocol.Node{{Name: "shop", Charter: protocol.Charter{Purpose: "old value"}}}}}
	m := New(s)
	m.proposals = s.proposals
	m.asks = openProposalAsks(s.proposals)
	m.tab = inboxTab
	updated, cmd := m.update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = updated.(Model)
	if cmd == nil {
		t.Fatal("detail did not fetch")
	}
	updated, _ = m.update(cmd())
	m = updated.(Model)
	view := ansi.Strip(m.testView())
	if !strings.Contains(view, "- old value") || !strings.Contains(view, "+ new purpose") || !strings.Contains(view, "peer") || !strings.Contains(view, "CTO's words") {
		t.Fatalf("detail: %s", view)
	}
	s.graph.Nodes[0].Charter.Purpose = "changed value"
	updated, _ = m.update(tea.KeyPressMsg{Code: tea.KeyEsc})
	m = updated.(Model)
	updated, cmd = m.update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = updated.(Model)
	updated, _ = m.update(cmd())
	m = updated.(Model)
	if !strings.Contains(ansi.Strip(m.testView()), "- changed value") {
		t.Fatal("diff was not refreshed")
	}
	beforeLines := strings.Split(m.testView(), "\n")
	m.say("applied")
	afterLines := strings.Split(m.testView(), "\n")
	if len(afterLines) != len(beforeLines) || strings.Join(afterLines[:len(afterLines)-1], "\n") != strings.Join(beforeLines[:len(beforeLines)-1], "\n") {
		t.Fatal("result line moved the detail")
	}
	m.proposalDetail.proposal.Result = json.RawMessage(`{"answer":"full-result"}`)
	if !strings.Contains(m.testView(), "full-result") {
		t.Fatal("full result missing")
	}
}
func TestProposalGoldens(t *testing.T) {
	for _, w := range []int{80, 120} {
		m := newModel(w, 34)
		p := proposalFixture()
		p.Messages = []protocol.CTOProposalMessage{{Sender: "design agent", At: time.Date(2026, 1, 2, 15, 4, 0, 0, time.UTC)}, {Sender: "owner", At: time.Date(2026, 1, 2, 15, 5, 0, 0, time.UTC)}}
		m.proposals = []protocol.CTOProposal{p}
		m.tab = inboxTab
		m.selIdx = 0
		golden(t, fmt.Sprintf("proposal_row_%d", w), m.testView())
		m.proposalDetail = &proposalDetail{proposal: p, diff: []string{"- old purpose", "+ new purpose"}}
		golden(t, fmt.Sprintf("proposal_detail_%d", w), m.testView())
		m.proposalDetail = nil
		m.openProposalConfirm()
		golden(t, fmt.Sprintf("proposal_confirm_%d", w), m.testView())
	}
}

func TestProposalSessionStartGoldens(t *testing.T) {
	for _, w := range []int{80, 120} {
		m := newModel(w, 34)
		p := proposalFixture()
		p.Tool, p.Action = "session_change", "session_start"
		p.Args = json.RawMessage(`{"action":"session_start","project":"garden","node_id":"build-vm","harness":"claude","folder":"worktree","first_message":"Review the garden planner and report what needs fixing"}`)
		m.proposals = []protocol.CTOProposal{p}
		m.tab = inboxTab
		m.selIdx = 0
		golden(t, fmt.Sprintf("proposal_session_start_%d", w), m.testView())
	}
}

func TestProjectRemoveProposalNamesMachine(t *testing.T) {
	m, _ := inboxAllKindsModel(120, 34)
	for _, tc := range []struct{ tool, action, args, machine string }{
		{"project_change", "remove", `{"name":"garden","node_id":"n_mac"}`, "n_mac"},
		{"project_remove", "", `{"path":"/work/garden","on":"n_mac"}`, "n_mac"},
		{"project_change", "remove", `{"name":"garden"}`, "here"},
	} {
		p := protocol.CTOProposal{Tool: tc.tool, Action: tc.action, NodeID: "n_mac", Args: json.RawMessage(tc.args)}
		if got := proposalSummary(p); !strings.Contains(got, "garden") || !strings.Contains(got, " @ "+tc.machine) {
			t.Errorf("summary: %s", got)
		}
		if got := m.inboxProposalSummary(p); !strings.Contains(got, "garden") || !strings.Contains(got, "on Mac") {
			t.Errorf("inbox: %s", got)
		}
	}
}
