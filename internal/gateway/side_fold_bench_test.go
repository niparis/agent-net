package gateway

import (
	"fmt"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// Write-path benchmarks for the parents that took in a side table (schema
// reduction, part 6). Each one calls a production function, so the same file
// measures the layout before the fold and after it. Fixtures are the size of
// the live gateway or larger. Run: go test ./internal/gateway -run '^$'
// -bench 'BenchmarkSideFold' -benchtime 2000x

// benchExec runs a statement on columns that exist in both layouts.
func benchExec(b *testing.B, s *Store, q string, args ...any) {
	b.Helper()
	if _, err := s.db.Exec(q, args...); err != nil {
		b.Fatal(err)
	}
}

func BenchmarkSideFoldModelMarks(b *testing.B) {
	s, _ := newTestStore(b)
	for i := 0; i < 200; i++ {
		model := fmt.Sprintf("model-%d", i)
		if err := s.RecordModelFailure("n1", "claude", model); err != nil {
			b.Fatal(err)
		}
		if err := s.RecordModelSuccess("n1", "claude", model); err != nil {
			b.Fatal(err)
		}
	}
	b.Run("RecordModelSuccess", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			if err := s.RecordModelSuccess("n1", "claude", fmt.Sprintf("model-%d", i%200)); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("RecordModelFailure", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			if err := s.RecordModelFailure("n1", "claude", fmt.Sprintf("model-%d", i%200), "401 unauthorized"); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("ReadMarks", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			if _, err := s.FailedModels("n1", "claude"); err != nil {
				b.Fatal(err)
			}
			if _, err := s.SucceededModels("n1", "claude"); err != nil {
				b.Fatal(err)
			}
			if _, err := s.ModelAuthBlocked("n1", "claude", "model-3"); err != nil {
				b.Fatal(err)
			}
		}
	})
}

func BenchmarkSideFoldSessionWakes(b *testing.B) {
	s, _ := newTestStore(b)
	for i := 0; i < 500; i++ {
		benchExec(b, s, `INSERT INTO sessions(id,node_id,harness,native_id,last_seen,wake_day,wake_day_count) VALUES(?,'n1','claude',?,1,?,1)`, fmt.Sprintf("s_%d", i), fmt.Sprintf("w_%d", i), "2026-11-20")
	}
	// The parent's own write, as the idle check and the loop make it.
	b.Run("ParentUpsert", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			benchExec(b, s, `UPDATE sessions SET wake_day_count=CASE WHEN wake_day=? THEN wake_day_count+1 ELSE 1 END, wake_day=? WHERE id=?`,
				"2026-11-20", "2026-11-20", fmt.Sprintf("s_%d", i%500))
		}
	})
	b.Run("ClaimWake", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			// A claim runs the same update whether the back-off passes or not.
			if _, err := s.claimWake(fmt.Sprintf("s_%d", i%500)); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("FailAndClearWake", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			session := fmt.Sprintf("s_%d", i%500)
			if _, err := s.failWake(session, "a1", "refused"); err != nil {
				b.Fatal(err)
			}
			if _, ok := s.wakeFailure(session); !ok {
				b.Fatal("failure not stored")
			}
			if err := s.clearWakeFailure(session); err != nil {
				b.Fatal(err)
			}
		}
	})
}

func BenchmarkSideFoldAutoDefect(b *testing.B) {
	// 217 defects on 100 or so workers on the live gateway; also ten times that.
	for _, size := range []int{217, 2170} {
		b.Run(fmt.Sprintf("%d", size), func(b *testing.B) {
			s, _ := newTestStore(b)
			node, _, err := s.CreateNode("host", "linux")
			if err != nil {
				b.Fatal(err)
			}
			var workers []string
			for w := 0; w < size/10; w++ {
				session, err := s.UpsertSession(node, protocol.SessionInfo{Harness: "claude", NativeID: fmt.Sprintf("w%d", w)})
				if err != nil {
					b.Fatal(err)
				}
				if err := s.RecordWorkerRun(protocol.WorkerRun{NodeID: node, SessionID: session, Harness: "claude", Model: "claude-opus-5-5",
					Outcome: protocol.OutcomeComplete, EndedAt: s.now().Unix(), Caller: "c", Difficulty: "normal"}); err != nil {
					b.Fatal(err)
				}
				workers = append(workers, session)
			}
			add := func(i int, worker string) {
				tx, err := s.db.Begin()
				if err != nil {
					b.Fatal(err)
				}
				if _, err := s.addAutoDefect(tx, autoDefect{changeID: fmt.Sprintf("ch_%d", i), kind: autoDefectVerify, revision: 1,
					worker: worker, severity: 3, note: "verify failed"}); err != nil {
					b.Fatal(err)
				}
				if err := tx.Commit(); err != nil {
					b.Fatal(err)
				}
			}
			for i := 0; i < size; i++ {
				add(-i-1, workers[i%len(workers)])
			}
			b.ResetTimer()
			b.Run("AddAutoDefect", func(b *testing.B) {
				for i := 0; i < b.N; i++ {
					add(i, workers[i%len(workers)])
				}
			})
			b.Run("PickerDefectRead", func(b *testing.B) {
				for i := 0; i < b.N; i++ {
					rows, err := s.db.Query(`SELECT session_id, COUNT(*), COALESCE(SUM(severity), 0) FROM worker_defects
						WHERE blame = 'worker' GROUP BY session_id`)
					if err != nil {
						b.Fatal(err)
					}
					rows.Close()
				}
			})
		})
	}
}

func BenchmarkSideFoldMemberBlocked(b *testing.B) {
	s, _ := newTestStore(b)
	for i := 0; i < 500; i++ {
		benchExec(b, s, `INSERT INTO member_blocked_notices(session_id,text_hash,at) VALUES(?,?,?)`, fmt.Sprintf("s_%d", i), "h", 1)
	}
	b.Run("NoticeUpsert", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			benchExec(b, s, `INSERT INTO member_blocked_notices(session_id,text_hash,at) VALUES(?,?,?)
				ON CONFLICT(session_id) DO UPDATE SET text_hash=excluded.text_hash,at=excluded.at`,
				fmt.Sprintf("s_%d", i%500), fmt.Sprintf("h%d", i), time.Now().UnixNano())
		}
	})
	b.Run("RecordTold", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			if err := s.recordMemberTold(s.db, []memberTold{{session: fmt.Sprintf("s_%d", i%500), key: fmt.Sprintf("k%d", i)}}); err != nil {
				b.Fatal(err)
			}
		}
	})
}

func BenchmarkSideFoldFriction(b *testing.B) {
	// 111 reports on the live gateway; also about twenty times that.
	for _, size := range []int{111, 2000} {
		b.Run(fmt.Sprintf("%d", size), func(b *testing.B) {
			s, _ := newTestStore(b)
			for i := 0; i < size; i++ {
				if _, err := s.ReportFriction(protocol.FrictionReportRequest{SessionID: "s_1", Project: fmt.Sprintf("p%d", i%9), Harness: "claude",
					Entries: []protocol.FrictionEntry{{Area: protocol.FrictionCI, Text: fmt.Sprintf("report %d", i)}}}); err != nil {
					b.Fatal(err)
				}
			}
			// Mark two projects twice, so the history is longer than one row per report.
			for round := 0; round < 2; round++ {
				if _, err := s.MarkFriction("cto", protocol.FrictionMarkRequest{Project: "p1", Status: protocol.FrictionRead}); err != nil {
					b.Fatal(err)
				}
				if _, err := s.MarkFriction("cto", protocol.FrictionMarkRequest{Project: "p2", Status: protocol.FrictionTriaged}); err != nil {
					b.Fatal(err)
				}
			}
			b.ResetTimer()
			b.Run("MarkOne", func(b *testing.B) {
				for i := 0; i < b.N; i++ {
					if _, err := s.MarkFriction("owner", protocol.FrictionMarkRequest{IDs: []int64{int64(i%size + 1)}, Status: protocol.FrictionRead}); err != nil {
						b.Fatal(err)
					}
				}
			})
			b.Run("FindPage", func(b *testing.B) {
				for i := 0; i < b.N; i++ {
					if _, err := s.FrictionFind(protocol.FrictionQuery{}); err != nil {
						b.Fatal(err)
					}
				}
			})
			b.Run("FindByStatus", func(b *testing.B) {
				for i := 0; i < b.N; i++ {
					if _, err := s.FrictionFind(protocol.FrictionQuery{Status: protocol.FrictionNew}); err != nil {
						b.Fatal(err)
					}
				}
			})
		})
	}
}
