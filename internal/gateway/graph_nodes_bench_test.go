package gateway

import (
	"fmt"
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

// graphNodesFixture is orgReadFixture shaped like the live explicit set: 14
// repos with a charter (four of them projects with sessions, ten with only a
// charter) and two architects. The 50 projects of the fixture have no charter,
// so the session branch of the node read does most of the work, as it does live.
func graphNodesFixture(tb testing.TB) *Store {
	tb.Helper()
	s := orgReadFixture(tb)
	for a := 0; a < 4; a++ {
		if err := s.SetCharter(fmt.Sprintf("p%02d", a), "purpose", "keeps the fixture busy", "owner"); err != nil {
			tb.Fatal(err)
		}
	}
	node, _, err := s.CreateNode("bench", "linux")
	if err != nil {
		tb.Fatal(err)
	}
	for i := 0; i < 10; i++ {
		name := fmt.Sprintf("ghost%02d", i)
		id, err := s.UpsertSession(node, protocol.SessionInfo{Harness: "claude", NativeID: name, Cwd: "/w/" + name,
			Repo: protocol.RepoInfo{Root: "/w/" + name, Project: name}})
		if err != nil {
			tb.Fatal(err)
		}
		if err := s.SetCharter(name, "purpose", "outlives its folder", "owner"); err != nil {
			tb.Fatal(err)
		}
		for _, q := range []string{`DELETE FROM sessions WHERE id=?`, `DELETE FROM projects WHERE name='` + name + `'`} {
			if _, err := s.db.Exec(q, anyArgs(q, id)...); err != nil {
				tb.Fatal(err)
			}
		}
	}
	for i := 0; i < 2; i++ {
		if err := s.AddArchitect(fmt.Sprintf("architect%d", i), "owner"); err != nil {
			tb.Fatal(err)
		}
	}
	return s
}

// anyArgs gives the session id to the one statement that takes it.
func anyArgs(q, id string) []any {
	if strings.Contains(q, "?") {
		return []any{id}
	}
	return nil
}

// BenchmarkGraphNodesRead times the node list on its own: the read that moved
// off graph_nodes onto charter_values and agents.
func BenchmarkGraphNodesRead(b *testing.B) {
	s := graphNodesFixture(b)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := s.graphNodes(); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkGraphReadExplicit times the whole untyped graph read on the same
// fixture.
func BenchmarkGraphReadExplicit(b *testing.B) {
	s := graphNodesFixture(b)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := s.graphWith(nil); err != nil {
			b.Fatal(err)
		}
	}
}
