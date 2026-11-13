package gateway

import (
	"fmt"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

// orgReadFixture is a store about the size of the live gateway: 50 Agents
// with 12 sessions each (600, half of them offline), every Agent under a
// parent in the org tree, a lead session on each Agent, and two scopes on
// every fifth Agent.
func orgReadFixture(tb testing.TB) *Store {
	tb.Helper()
	s := teamStore(tb)
	var projects []string
	for a := 0; a < 50; a++ {
		projects = append(projects, fmt.Sprintf("p%02d", a))
	}
	leads, node := graphSessions(tb, s, projects...)
	for a, project := range projects {
		if _, err := s.db.Exec(`UPDATE agents SET lead_session=? WHERE id=?`, leads[project], project); err != nil {
			tb.Fatal(err)
		}
		for i := 1; i < 12; i++ {
			id, err := s.UpsertSession(node, protocol.SessionInfo{Harness: "claude", NativeID: fmt.Sprintf("%s-%d", project, i), Cwd: "/w/" + project,
				Repo: protocol.RepoInfo{Root: "/w/" + project, Project: project}})
			if err != nil {
				tb.Fatal(err)
			}
			if i%2 == 1 {
				if _, err := s.db.Exec(`UPDATE sessions SET online=0 WHERE id=?`, id); err != nil {
					tb.Fatal(err)
				}
			}
		}
		parent := protocol.OrgRoot
		if a > 0 {
			parent = projects[(a-1)/5]
		}
		if err := s.SetEdge(reportsEdge(project, parent), "owner"); err != nil {
			tb.Fatal(err)
		}
		if a%5 == 0 {
			for _, q := range []protocol.ScopeSetRequest{
				{Node: project, Kind: protocol.ScopeMerge, Over: "repo:demo"},
				{Node: project, Kind: protocol.ScopeReview, Over: protocol.ScopeOverAll},
			} {
				if _, err := s.SetScope(q, "owner", false); err != nil {
					tb.Fatal(err)
				}
			}
		}
	}
	return s
}

// BenchmarkGraphReadOrgTree times the whole typed graph read that graph.get
// and agent_graph_read use, on the fixture above.
func BenchmarkGraphReadOrgTree(b *testing.B) {
	s := orgReadFixture(b)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := s.GraphForRef(nil); err != nil {
			b.Fatal(err)
		}
	}
}
