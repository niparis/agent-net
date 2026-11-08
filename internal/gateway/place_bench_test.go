package gateway

import (
	"fmt"
	"testing"
)

// benchPlaceStore builds a 40-node typed graph: 40 agents, 40 repos, 4 teams,
// repo leads, held back text and live sessions for half the nodes.
func benchPlaceStore(b *testing.B) *Store {
	b.Helper()
	s, _ := newTestStore(b)
	if _, err := s.db.Exec(`UPDATE graph_ref_gate SET enabled=1 WHERE id=1`); err != nil {
		b.Fatal(err)
	}
	if err := s.loadGraphRefs(); err != nil {
		b.Fatal(err)
	}
	now := s.now().UnixNano()
	for i := 0; i < 40; i++ {
		agent, repo := fmt.Sprintf("agent-%02d", i), fmt.Sprintf("repo-%02d", i)
		if _, err := s.db.Exec(`INSERT INTO agents(id,display_name,role,created_at,updated_at) VALUES(?,?,?,?,?)`, agent, agent, "architect", now, now); err != nil {
			b.Fatal(err)
		}
		if _, err := s.db.Exec(`INSERT INTO repos(id,display_name,created_at,updated_at,owner_agent_id) VALUES(?,?,?,?,?)`, repo, repo, now, now, agent); err != nil {
			b.Fatal(err)
		}
		if _, err := s.db.Exec(`INSERT INTO repo_leads(repo_id,agent_id) VALUES(?,?)`, repo, agent); err != nil {
			b.Fatal(err)
		}
		if i%2 == 0 {
			if _, err := s.db.Exec(`INSERT INTO repo_held_back(repo_id,held_back) VALUES(?,?)`, repo, "force pushes"); err != nil {
				b.Fatal(err)
			}
			if _, err := s.db.Exec(`INSERT INTO sessions(id,node_id,harness,native_id,name,last_seen,online,agent_id,repo_id,archived_at,worker) VALUES(?,?,?,?,?,?,?,?,?,0,'')`,
				"session-"+repo, "node", "claude", "native-"+repo, agent, now, 1, agent, repo); err != nil {
				b.Fatal(err)
			}
		}
	}
	for t := 0; t < 4; t++ {
		id := fmt.Sprintf("team-%d", t)
		owner := fmt.Sprintf("agent-%02d", t)
		if _, err := s.db.Exec(`INSERT INTO teams(id,name,purpose,owner_agent_id,active,created_at,updated_at,authority_mode,held_back) VALUES(?,?,?,?,1,?,?,?,?)`,
			id, id, "bench team", owner, now, now, "charter_first", "deploy to production; schema changes"); err != nil {
			b.Fatal(err)
		}
		for i := t * 10; i < (t+1)*10; i++ {
			if _, err := s.db.Exec(`INSERT INTO team_members(member_kind,member_id,team_id) VALUES('repo',?,?)`, fmt.Sprintf("repo-%02d", i), id); err != nil {
				b.Fatal(err)
			}
			if _, err := s.db.Exec(`INSERT INTO team_members(member_kind,member_id,team_id) VALUES('agent',?,?)`, fmt.Sprintf("agent-%02d", i), id); err != nil {
				b.Fatal(err)
			}
		}
	}
	return s
}

// BenchmarkGraphViewPlaces40 measures the owner Graph read with place data on
// a 40 node graph.
func BenchmarkGraphViewPlaces40(b *testing.B) {
	s := benchPlaceStore(b)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := s.GraphForRefWithPlaces(nil, true); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkGraphViewNoPlaces40 is the same read without place data.
func BenchmarkGraphViewNoPlaces40(b *testing.B) {
	s := benchPlaceStore(b)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := s.GraphForRefWithPlaces(nil, false); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkPlaceIndex40 measures the one-pass place load on its own.
func BenchmarkPlaceIndex40(b *testing.B) {
	s := benchPlaceStore(b)
	repos, agents, err := s.RepoAgents()
	if err != nil {
		b.Fatal(err)
	}
	now := s.now()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := s.buildPlaceIndex(repos, agents, now); err != nil {
			b.Fatal(err)
		}
	}
}
