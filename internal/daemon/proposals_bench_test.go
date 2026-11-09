package daemon

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

// seedProposals fills the state like a CTO host that has run for a few months:
// most proposals are closed, with the result of the call they ran, and each
// has the two delivery lines the CTO adds. A few are open.
func seedProposals(tb testing.TB, s *state, n, open int, now time.Time) {
	tb.Helper()
	tx, err := s.db.Begin()
	if err != nil {
		tb.Fatal(err)
	}
	defer tx.Rollback()
	args := `{"project":"agent-net","text":"` + strings.Repeat("start a worker for this ", 16) + `"}`
	result := `{"started":true,"detail":"` + strings.Repeat("worker started on the machine ", 60) + `"}`
	for i := 0; i < n; i++ {
		status, res, at := "applied", result, now.Add(-time.Duration(n-i)*10*time.Minute)
		switch {
		case i >= n-open:
			status, res, at = "open", "", now.Add(-time.Duration(n-i)*time.Minute)
		case i%7 == 0:
			status, res = "declined", ""
		case i%11 == 0:
			status, res = "expired", ""
		}
		// Each open call is different: the open calls are unique.
		r, err := tx.Exec(`INSERT INTO cto_proposals(tool,action,args,reason,created_at,status,note,result) VALUES(?,?,?,?,?,?,?,?)`,
			"cto_start", "set", fmt.Sprintf(`{"n":%d,%s`, i, args[1:]), "the owner asked for it", at.UnixNano(), status, "", res)
		if err != nil {
			tb.Fatal(err)
		}
		id, _ := r.LastInsertId()
		for _, sender := range []string{"s_alice", "s_bob"} {
			if _, err := tx.Exec(`INSERT INTO cto_proposal_messages(proposal_id,sender,at) VALUES(?,?,?)`, id, sender, at.UnixNano()); err != nil {
				tb.Fatal(err)
			}
		}
	}
	if err := tx.Commit(); err != nil {
		tb.Fatal(err)
	}
}

func benchProposals(b *testing.B, n int) (proposalRunner, time.Time) {
	b.Helper()
	s, err := openState(b.TempDir())
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { s.close() })
	now := time.Unix(1_800_000_000, 0)
	seedProposals(b, s, n, 6, now)
	return proposalRunner{state: s, now: func() time.Time { return now }}, now
}

// BenchmarkProposalList is the read behind proposal_list: every proposal the
// CTO host holds, with the delivery lines of each.
func BenchmarkProposalList(b *testing.B) {
	for _, n := range []int{300, 3000} {
		b.Run(fmt.Sprintf("proposals=%d", n), func(b *testing.B) {
			p, _ := benchProposals(b, n)
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				items, err := p.list()
				if err != nil || len(items) != n {
					b.Fatalf("%d items: %v", len(items), err)
				}
			}
		})
	}
}
