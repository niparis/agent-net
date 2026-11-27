package gateway

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// Decision 0193 part 3: the self-check finds a break of each rule that
// slipped past the writes, logs it once, gives the agent-net lead one Desk
// item per cause, and resolves the item when the cause is gone. It changes
// no data of the tree.
func TestTreeSelfCheck(t *testing.T) {
	r := newTreeRig(t)
	var logs []string
	r.s.logf = func(event string, kv ...any) { logs = append(logs, formatLine(r.s.now(), event, kv...)) }
	r.start("an-lead", "agent-net")
	r.start("shop-lead", "shop")
	member := r.start("shop-member", "shop")
	dup := r.start("shop-dup", "shop")
	starter := r.start("shop-starter", "shop")
	orphan := r.worker("shop-orphan", "shop", starter)
	old := r.start("shop-old", "shop")
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := r.s.db.Exec(q, args...); err != nil {
			t.Fatal(err)
		}
	}
	// One break of each rule, written past the write-time checks.
	exec(`UPDATE agents SET lead_session=? WHERE id='shop-agent'`, member) // I1
	exec(`UPDATE sessions SET agent_id='shop' WHERE id=?`, dup)            // I2
	exec(`UPDATE sessions SET archived_at=1 WHERE id=?`, starter)          // I3
	exec(`UPDATE sessions SET agent_id='shop', online=0, last_seen=0, last_active=0, created_at=0, owner_used_at=0
		WHERE id=?`, old) // I4

	tree := r.tree()
	logs = nil
	found, err := r.s.checkTree()
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"I1 agent shop-agent", "I2 session " + dup, "I3 session " + orphan, "I4 session " + old}
	var got []string
	for _, v := range found {
		got = append(got, v.rule+" "+v.kind+" "+v.id)
	}
	if !slices.Equal(got, want) {
		t.Fatalf("violations %v, want %v", got, want)
	}
	if r.tree() != tree {
		t.Fatalf("the check changed the tree:\n%s\nwas:\n%s", r.tree(), tree)
	}
	wantLogs := []string{"event=tree.violation rule=I1 agent=shop-agent", "event=tree.violation rule=I2 session=" + dup,
		"event=tree.violation rule=I3 session=" + orphan, "event=tree.violation rule=I4 session=" + old}
	if len(logs) != len(wantLogs) {
		t.Fatalf("log lines:\n%s\nwant %d", strings.Join(logs, "\n"), len(wantLogs))
	}
	for i, w := range wantLogs {
		if !strings.Contains(logs[i], w) {
			t.Fatalf("log line %d = %q, want %q", i, logs[i], w)
		}
	}

	// Fixing the data clears every violation at the next pass.
	exec(`UPDATE agents SET lead_session=NULL WHERE id='shop-agent'`)
	exec(`UPDATE sessions SET agent_id='shop-agent' WHERE id=?`, dup)
	exec(`UPDATE sessions SET online=0, archived_at=1 WHERE id=?`, orphan)
	exec(`UPDATE sessions SET archived_at=1 WHERE id=?`, old)
	found, err = r.s.checkTree()
	if err != nil || len(found) != 0 {
		t.Fatalf("after the fix: %v %v", found, err)
	}
}

// BenchmarkTreeCheck times one pass of the four queries: on the part 1
// fixture (the scrubbed live gateway of 2026-11-26) and on a store of 1,000
// sessions.
func BenchmarkTreeCheck(b *testing.B) {
	b.Run("fixture", func(b *testing.B) {
		s, _ := newTestStore(b)
		fixture, err := os.ReadFile(filepath.Join("..", "tui", "testdata", "session_tree_0193.sql"))
		if err != nil {
			b.Fatal(err)
		}
		if _, err := s.db.Exec(string(fixture)); err != nil {
			b.Fatal(err)
		}
		benchTreeCheck(b, s)
	})
	b.Run("1000_sessions", func(b *testing.B) {
		s, _ := newTestStore(b)
		seedTreeCheckStore(b, s, 1000)
		benchTreeCheck(b, s)
	})
}

func benchTreeCheck(b *testing.B, s *Store) {
	b.Helper()
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM sessions`).Scan(&n); err != nil {
		b.Fatal(err)
	}
	b.ReportMetric(float64(n), "sessions")
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := s.treeViolations(s.now()); err != nil {
			b.Fatal(err)
		}
	}
}

// seedTreeCheckStore writes n sessions in 50 projects: per project a lead
// Agent with its live lead session, a member Agent, and the rest split
// between live members, workers and old offline sessions.
func seedTreeCheckStore(b *testing.B, s *Store, n int) {
	b.Helper()
	tx, err := s.db.Begin()
	if err != nil {
		b.Fatal(err)
	}
	defer tx.Rollback()
	exec := func(q string, args ...any) {
		if _, err := tx.Exec(q, args...); err != nil {
			b.Fatal(err)
		}
	}
	exec(`INSERT INTO nodes (id,hostname,os,token_hash,created_at,seen_at,version) VALUES ('n_b','host-b','linux','token-n_b',0,0,'v0.0.0')`)
	const projects = 50
	for p := 0; p < projects; p++ {
		repo := fmt.Sprintf("p%02d", p)
		exec(`INSERT INTO agents (id,display_name,role,created_at,updated_at,tombstone,lead_session) VALUES (?,?,'',0,0,0,?)`, repo, repo, repo+"-s0")
		exec(`INSERT INTO agents (id,display_name,role,created_at,updated_at,tombstone) VALUES (?,?,'',0,0,0)`, repo+"-agent", repo+"-agent")
		exec(`INSERT INTO repos (id,display_name,created_at,updated_at,archived,lead_agent_id,team_id,merger_session) VALUES (?,?,0,0,0,?,'','')`, repo, repo, repo)
	}
	for i := 0; i < n; i++ {
		p := i % projects
		repo := fmt.Sprintf("p%02d", p)
		id := fmt.Sprintf("%s-s%d", repo, i/projects)
		agent, worker, online, seen := repo+"-agent", "", 1, int64(1795600000000000000)
		switch {
		case i < projects:
			agent = repo
		case i%4 == 1:
			agent, worker = "", fmt.Sprintf(`{"id":"w_%d","owner":"%s-s0"}`, i, repo)
		case i%4 == 2:
			online, seen = 0, 0
		}
		exec(`INSERT INTO sessions (id,node_id,harness,native_id,name,cwd,status,last_seen,online,worker,created_at,last_active,agent_id,repo_id)
			VALUES (?,'n_b','claude',?,?,'/w','idle',?,?,?,0,0,NULLIF(?,''),?)`, id, "native-"+id, id, seen, online, worker, agent, repo)
	}
	if err := tx.Commit(); err != nil {
		b.Fatal(err)
	}
}
