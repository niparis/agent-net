package tui

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

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
func TestProposalExpiredHidden(t *testing.T) {
	p := proposalFixture()
	s := &proposalSource{fakeSource: fakeSource{snap: fakeSnapshot()}, proposals: []protocol.CTOProposal{p}}
	m := New(s)
	m.usage.at = m.now()
	updated, _ := m.update(m.fetchCmd()())
	m = updated.(Model)
	if len(m.proposals) != 1 || m.openProposals() != 1 {
		t.Fatal("open proposal not fetched")
	}
	s.proposals[0].Status = "expired"
	s.snap.ProposalCount = 0
	m.proposalsAt = time.Time{}
	updated, _ = m.update(m.fetchCmd()())
	m = updated.(Model)
	if got := m.openProposals(); got != 0 {
		t.Fatalf("%d expired proposals still counted", got)
	}
}

func TestProjectRemoveProposalNamesMachine(t *testing.T) {
	for _, tc := range []struct{ tool, action, args, machine string }{
		{"project_change", "remove", `{"name":"garden","node_id":"n_mac"}`, "n_mac"},
		{"project_remove", "", `{"path":"/work/garden","on":"n_mac"}`, "n_mac"},
		{"project_change", "remove", `{"name":"garden"}`, "here"},
	} {
		p := protocol.CTOProposal{Tool: tc.tool, Action: tc.action, NodeID: "n_mac", Args: json.RawMessage(tc.args)}
		if got := proposalSummary(p); !strings.Contains(got, "garden") || !strings.Contains(got, " @ "+tc.machine) {
			t.Errorf("summary: %s", got)
		}
	}
}
