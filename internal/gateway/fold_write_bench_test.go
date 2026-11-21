package gateway

import (
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// Run the write-path timings of the side-table fold with
//
//	AGENT_NET_BENCH_FOLD=1 go test ./internal/gateway -run '^TestBenchFoldWritePaths$' -v -count=1
//
// It prints ns/op for the writes that touch messages and delivery_revisions.
// The same file runs on the schema before and after the fold: it only uses
// gateway operations, never the folded tables. It is skipped otherwise, so the
// suite does not time anything.
func TestBenchFoldWritePaths(t *testing.T) {
	if os.Getenv("AGENT_NET_BENCH_FOLD") == "" {
		t.Skip("set AGENT_NET_BENCH_FOLD=1 to time the write paths")
	}
	body := strings.Repeat("a message body line of about fifty characters\n", 70)
	files := []protocol.FileEntry{
		{ID: "f1", Name: "one.txt", Size: 90, SHA256: filesTestSum},
		{ID: "f2", Name: "two.txt", Size: 91, SHA256: filesTestSum},
	}
	report := func(name string, res testing.BenchmarkResult) {
		t.Logf("BENCH %-42s %8d ns/op (%d ops)", name, res.NsPerOp(), res.N)
	}

	// A message row is rewritten by every state change, so its size matters.
	// The store holds a few thousand messages of about 3 KB first, so the
	// timing is on a table that has pages to split, like the live one.
	msgStore := func(withFiles bool) func(b *testing.B) {
		return func(b *testing.B) {
			s, clk := newTestStore(t)
			peer, to := twoSessions(t, s)
			// The owner is exempt from the inbox limit, so the run can write
			// thousands of messages to one session.
			from, err := s.UpsertSession(mustNode(t, s, peer), protocol.SessionInfo{Harness: protocol.OwnerHarness, NativeID: protocol.OwnerNativeID})
			if err != nil {
				t.Fatal(err)
			}
			for i := 0; i < 3000; i++ {
				clk.advance(5 * time.Second) // stay under the per-session rate limit
				if _, _, err := s.StoreMessage(protocol.MsgSend{Sender: from, Recipient: to, Body: body, IdempotencyKey: fmt.Sprintf("seed-%d", i)}); err != nil {
					t.Fatalf("bench: %v", err)
				}
			}
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				clk.advance(5 * time.Second)
				send := protocol.MsgSend{Sender: from, Recipient: to, Body: body, IdempotencyKey: fmt.Sprintf("bench-%d", i)}
				if withFiles {
					send.Files = files
				}
				if _, _, err := s.StoreMessage(send); err != nil {
					t.Fatalf("bench: %v", err)
				}
			}
		}
	}
	report("messages insert, no files", testing.Benchmark(msgStore(false)))
	report("messages insert, 2 files", testing.Benchmark(msgStore(true)))

	// One submit writes the revision row and then the diff stat, the signals
	// and the hand-back check of that revision. A resubmit with a new head
	// does it again on a new revision of the same change.
	submit := func(stat string, resubmit bool) func(b *testing.B) {
		return func(b *testing.B) {
			s, _ := newTestStore(t)
			r := deliveryRigOn(t, s)
			signals := []protocol.QualitySignal{{Code: "tests_added", Strength: "strong", Evidence: "adds a test for the new behaviour"}}
			check := &protocol.HandbackCheck{Status: "passed", ExitCode: 0, DurationMS: 1200, OutputTail: strings.Repeat("ok  \tpackage\t0.5s\n", 20)}
			do := func(branch string, n int) {
				q := r.workerSubmit(branch, fmt.Sprintf("%040x", n))
				q.DiffStat, q.Signals, q.HandbackCheck = stat, signals, check
				if _, err := deliveryCall[protocol.ChangeSubmitResult](r, r.a, protocol.FrameChangeSubmit, q); err != nil {
					t.Fatalf("bench: %v", err)
				}
			}
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				branch := fmt.Sprintf("feature/bench-%d", i)
				do(branch, i*2+1)
				if resubmit {
					do(branch, i*2+2)
				}
			}
		}
	}
	line := " internal/gateway/some_long_file_name.go | 120 ++++++++++++++++++++++++-----\n"
	typical := strings.Repeat(line, 40)
	largest := strings.Repeat(line, protocol.MaxDiffStatBytes/len(line))
	report("change submit, 3 KB diff stat", testing.Benchmark(submit(typical, false)))
	report("change submit, 8 KB diff stat", testing.Benchmark(submit(largest, false)))
	report("change submit then resubmit, 3 KB", testing.Benchmark(submit(typical, true)))
}
