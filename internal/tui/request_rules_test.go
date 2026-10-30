package tui

import (
	"context"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

type ruleSource struct {
	fakeSource
	edges []protocol.Edge
}

func (s *ruleSource) SetRequestRule(_ context.Context, e protocol.Edge) error {
	s.edges = append(s.edges, e)
	return nil
}
func TestTrustKeyCyclesWildcardRule(t *testing.T) {
	for _, tc := range []struct{ current, next string }{{"normal", "ask"}, {"ask", "auto"}, {"auto", "normal"}} {
		src := &ruleSource{fakeSource: fakeSource{snap: fakeSnapshot()}}
		m := New(src)
		m.snap = src.snap
		m.selected = "s_0000000000000001"
		a, _ := m.selectedAgent()
		node := a.Repo.Project
		if node == "" {
			node = a.Profile.Project
		}
		if tc.current != "normal" {
			m.snap.Edges = []protocol.Edge{{From: "*", To: node, Kind: "depends", Rule: tc.current}}
		}
		_, cmd := press(m, keyT())
		if cmd == nil {
			t.Fatalf("%s: no action", tc.current)
		}
		cmd()
		if len(src.edges) != 1 || src.edges[0].Rule != tc.next || src.edges[0].From != "*" || src.edges[0].To != node {
			t.Fatalf("%s: %#v", tc.current, src.edges)
		}
	}
}
func TestTrustBadge(t *testing.T) {
	m := newModel(120, 30)
	a := m.snap.Agents[0]
	node := a.Repo.Project
	if node == "" {
		node = a.Profile.Project
	}
	m.snap.Edges = []protocol.Edge{{From: "*", To: node, Rule: "ask"}, {From: "other", To: node, Rule: "auto"}}
	if got := m.requestRuleBadge(a); got != "ask +1" {
		t.Fatalf("badge %q", got)
	}
}
