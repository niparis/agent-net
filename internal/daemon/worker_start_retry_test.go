package daemon

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"github.com/niparis/agent-net/internal/protocol"
)

// gatewayHook wraps chosen frames of the daemon's gateway link. fn returns
// handled=true to answer the frame itself; false passes it on to the gateway.
func gatewayHook(typ string, fn func(ctx context.Context, n int32) (json.RawMessage, bool, error), calls *atomic.Int32) func(*Config) {
	return func(c *Config) {
		c.testGatewayCall = func(ctx context.Context, t string, params any) (json.RawMessage, bool, error) {
			if t != typ {
				return nil, false, nil
			}
			return fn(ctx, calls.Add(1))
		}
	}
}

// noOpenStarts waits until the gateway has no start left open.
func noOpenStarts(t *testing.T, g *testGateway) {
	t.Helper()
	eventually(t, "no start left open at the gateway", func() bool {
		n, err := g.gw.Store().OpenStarts()
		return err == nil && n == 0
	})
}

func startThroughGateway(t *testing.T, w *workerNode, pi *localClient) protocol.WorkerStartResult {
	t.Helper()
	r := startFrom(t, pi, protocol.WorkerStartRequest{Model: "fake/m1", Project: "shop", Harness: "opencode", Task: "do work", Isolation: protocol.IsolationWorktree})
	if r.SessionID == "" {
		t.Fatalf("start: %+v", r)
	}
	return r
}

func hasTask(t *testing.T, w *workerNode, session, owner string) bool {
	t.Helper()
	// The gateway does not know the session until the worker registers.
	raw, err := w.cli.call("messages", protocol.QueryMessages{SessionID: session, Limit: 50})
	if err != nil {
		return false
	}
	var r struct {
		Messages []protocol.Message `json:"messages"`
	}
	json.Unmarshal(raw, &r)
	for _, m := range r.Messages {
		if m.Type == protocol.TypeRequest && m.From == owner && strings.Contains(m.Body, "do work") {
			return true
		}
	}
	return false
}

func notStartedNotices(t *testing.T, w *workerNode, owner string) []protocol.Message {
	t.Helper()
	var out []protocol.Message
	for _, m := range w.messagesOf(t, owner) {
		if strings.Contains(m.Body, "status: not started") {
			out = append(out, m)
		}
	}
	return out
}

// The task send of a started worker is one call with a long deadline, not the
// 20 s of the link. A gateway that answers late does not end a running worker.
func TestQueuedStartTaskSendHasALongDeadline(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	var calls atomic.Int32
	var wait atomic.Int64
	var deadline atomic.Int64
	w := startWorkerNode(t, g, shortSet, gatewayHook("worker.queued_start", func(ctx context.Context, n int32) (json.RawMessage, bool, error) {
		wait.Store(int64(callWait(ctx)))
		if dl, ok := ctx.Deadline(); ok {
			deadline.Store(int64(time.Until(dl)))
		}
		time.Sleep(1500 * time.Millisecond) // a slow gateway
		return nil, false, nil
	}, &calls))
	repo := newRepo(t, "shop")
	w.register(t, repo)
	pi, owner := helloPi(t, w.node, "owner", repo, true)
	r := startThroughGateway(t, w, pi)
	eventually(t, "the worker has its task", func() bool { return hasTask(t, w, r.SessionID, owner) })
	if d := time.Duration(wait.Load()); d <= 20*time.Second || d < queuedStartWait {
		t.Errorf("the call waits %v, want at least %v", d, queuedStartWait)
	}
	if d := time.Duration(deadline.Load()); d < queuedStartWait-30*time.Second {
		t.Errorf("the call deadline is %v away, want about %v", d, queuedStartWait)
	}
	if n := calls.Load(); n != 1 {
		t.Errorf("worker.queued_start calls = %d, want 1", n)
	}
	if ns := notStartedNotices(t, w, owner); len(ns) != 0 {
		t.Errorf("a not-started notice for a worker that started:\n%s", ns[0].Body)
	}
	if w.d.workerBySession(r.SessionID) == nil {
		t.Error("the worker was stopped")
	}
	noOpenStarts(t, g)
}

// A refusal, or a gateway that does not answer, ends the start: one call, one
// notice, a start_failed record.
func TestQueuedStartTaskSendFailureEndsTheStart(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		want string
	}{
		{"refusal", RemoteError("session is not a worker of this node with that owner"), "not a worker of this node"},
		{"unreachable", errGatewayTimeout, "did not answer"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			g := startGateway(t, t.TempDir(), "")
			var calls atomic.Int32
			w := startWorkerNode(t, g, shortSet, gatewayHook("worker.queued_start", func(ctx context.Context, n int32) (json.RawMessage, bool, error) {
				return nil, true, tc.err
			}, &calls))
			repo := newRepo(t, "shop")
			w.register(t, repo)
			pi, owner := helloPi(t, w.node, "owner", repo, true)
			r := startThroughGateway(t, w, pi)
			eventually(t, "one not-started notice", func() bool {
				ns := notStartedNotices(t, w, owner)
				return len(ns) == 1 && strings.Contains(ns[0].Body, "its task could not be sent") && strings.Contains(ns[0].Body, tc.want)
			})
			if n := calls.Load(); n != 1 {
				t.Errorf("worker.queued_start calls = %d, want 1", n)
			}
			noOpenStarts(t, g)
			eventually(t, "a start_failed record", func() bool {
				rows, _ := w.d.st.workers()
				for _, row := range rows {
					if row.SessionID == r.SessionID && row.EndedReason == protocol.StopStartFails {
						return true
					}
				}
				return false
			})
		})
	}
}

// The task send belongs to the worker: when the worker is stopped, the call is
// cancelled and the start is ended.
func TestQueuedStartTaskSendStopsWhenTheWorkerEnds(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	var calls atomic.Int32
	cancelled := make(chan struct{})
	w := startWorkerNode(t, g, shortSet, gatewayHook("worker.queued_start", func(ctx context.Context, n int32) (json.RawMessage, bool, error) {
		<-ctx.Done()
		close(cancelled)
		return nil, true, ctx.Err()
	}, &calls))
	repo := newRepo(t, "shop")
	w.register(t, repo)
	pi, owner := helloPi(t, w.node, "owner", repo, true)
	r := startThroughGateway(t, w, pi)
	eventually(t, "the task send is waiting", func() bool { return calls.Load() == 1 })
	wk := w.d.workerBySession(r.SessionID)
	if wk == nil {
		t.Fatal("no worker")
	}
	w.d.endWorker(wk, protocol.StopRequested)
	select {
	case <-cancelled:
	case <-time.After(2 * time.Second):
		t.Fatal("the task send went on after the worker ended")
	}
	// The worker never got its task, and the gateway must not keep the start
	// open: the owner hears once that it did not start.
	eventually(t, "one not-started notice", func() bool {
		ns := notStartedNotices(t, w, owner)
		return len(ns) == 1 && strings.Contains(ns[0].Body, "ended before its task was sent")
	})
	noOpenStarts(t, g)
}

// The not-started report is tried again while the gateway does not answer, and
// not again after a refusal.
func TestNotStartedReportIsRetried(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	var sendCalls, reports atomic.Int32
	hook := gatewayHook("worker.queued_start", func(ctx context.Context, n int32) (json.RawMessage, bool, error) {
		return nil, true, RemoteError("refused")
	}, &sendCalls)
	report := gatewayHook("worker.not_started", func(ctx context.Context, n int32) (json.RawMessage, bool, error) {
		if n <= 2 {
			return nil, true, errGatewayTimeout
		}
		return nil, false, nil
	}, &reports)
	both := func(c *Config) {
		hook(c)
		first := c.testGatewayCall
		report(c)
		second := c.testGatewayCall
		c.testGatewayCall = func(ctx context.Context, typ string, params any) (json.RawMessage, bool, error) {
			if typ == "worker.queued_start" {
				return first(ctx, typ, params)
			}
			return second(ctx, typ, params)
		}
	}
	w := startWorkerNode(t, g, shortSet, both)
	repo := newRepo(t, "shop")
	w.register(t, repo)
	pi, owner := helloPi(t, w.node, "owner", repo, true)
	startThroughGateway(t, w, pi)
	eventually(t, "the notice after two failed reports", func() bool { return len(notStartedNotices(t, w, owner)) == 1 })
	if n := reports.Load(); n != 3 {
		t.Errorf("worker.not_started calls = %d, want 3", n)
	}
}

// A task send that waits for a slow gateway does not hold up the queue: the
// next start is admitted while the first one still waits. The worker is
// already counted as running, so admission is unchanged.
func TestQueuedStartTaskSendDoesNotBlockTheQueue(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	fl := &fakeLoad{l: cpuBusy()}
	hold := make(chan struct{})
	var first atomic.Bool
	w := startWorkerNode(t, g, queueSet, func(c *Config) {
		withLoad(fl)(c)
		c.testGatewayCall = func(ctx context.Context, typ string, params any) (json.RawMessage, bool, error) {
			if typ == "worker.queued_start" && first.CompareAndSwap(false, true) {
				select {
				case <-hold:
				case <-ctx.Done():
				}
			}
			return nil, false, nil
		}
	})
	repo := newRepo(t, "shop")
	w.register(t, repo)
	pi, piID := helloPi(t, w.node, "caller", "/work/caller", true)
	for _, task := range []string{"first task", "second task"} {
		if r := startFrom(t, pi, protocol.WorkerStartRequest{Model: "fake/m1", Project: "shop", Harness: "opencode", Task: task}); r.Status != "queued" {
			t.Fatalf("%+v", r)
		}
	}
	fl.set(roomyLoad())
	eventually(t, "both starts became workers while the first task send waits", func() bool { return len(w.logs.lines("worker.dequeued")) == 2 })
	close(hold)
	eventually(t, "both workers were announced", func() bool {
		n := 0
		for _, m := range w.messagesOf(t, piID) {
			if strings.Contains(m.Body, "status: started") {
				n++
			}
		}
		return n == 2
	})
}

// A launch the machine refuses is ended at once: the caller got the refusal,
// and the gateway keeps no start open.
func TestRefusedLaunchLeavesNoStartOpen(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	w := startWorkerNode(t, g, shortSet, nil)
	repo := newRepo(t, "shop")
	w.register(t, repo)
	pi, _ := helloPi(t, w.node, "caller", repo, true)
	// The machine refuses a task that, with the project rules, does not fit in
	// one message. The gateway and the caller's own check pass it: they do not
	// see the rules.
	if err := os.MkdirAll(filepath.Join(repo, ".agent-net"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, ".agent-net", "worker.md"), []byte(strings.Repeat("rule\n", 3000)), 0o644); err != nil {
		t.Fatal(err)
	}
	task := strings.Repeat("x", protocol.MaxBodyBytes-len(protocol.WorkerTaskBody("", "/x", "b"))-100)
	_, err := pi.call("worker_start", protocol.WorkerStartRequest{Model: "fake/m1", Project: "shop", Harness: "opencode", Task: task, Isolation: protocol.IsolationNone})
	if err == nil {
		t.Fatal("the launch was not refused")
	}
	// It was the machine that refused, after the gateway tracked the start.
	if len(w.logs.lines("worker.refused")) == 0 {
		t.Fatalf("the machine did not refuse the launch: %v", err)
	}
	noOpenStarts(t, g)
}

// A gateway storage error on the report reaches the daemon over the real link
// marked as the gateway's own fault, and the report is tried again until it
// is stored. A refusal is not tried again (TestQueuedStartTaskSendFailureEndsTheStart).
func TestNotStartedReportRetriesAGatewayStorageError(t *testing.T) {
	dir := t.TempDir()
	g := startGateway(t, dir, "")
	db, err := sql.Open("sqlite", "file:"+filepath.Join(dir, "gateway.db")+"?_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	var sendCalls, reports atomic.Int32
	refuse := gatewayHook("worker.queued_start", func(ctx context.Context, n int32) (json.RawMessage, bool, error) {
		return nil, true, RemoteError("refused")
	}, &sendCalls)
	count := gatewayHook("worker.not_started", func(ctx context.Context, n int32) (json.RawMessage, bool, error) {
		if n == 3 { // the storage works again
			if _, err := db.Exec(`DROP TRIGGER fail_notice`); err != nil {
				t.Error(err)
			}
		}
		return nil, false, nil
	}, &reports)
	w := startWorkerNode(t, g, shortSet, func(c *Config) {
		refuse(c)
		first := c.testGatewayCall
		count(c)
		second := c.testGatewayCall
		c.testGatewayCall = func(ctx context.Context, typ string, params any) (json.RawMessage, bool, error) {
			if typ == "worker.queued_start" {
				return first(ctx, typ, params)
			}
			return second(ctx, typ, params)
		}
	})
	if _, err := db.Exec(`CREATE TRIGGER fail_notice BEFORE INSERT ON messages WHEN NEW.sender='agent-net'
		BEGIN SELECT RAISE(ABORT, 'disk full'); END`); err != nil {
		t.Fatal(err)
	}
	repo := newRepo(t, "shop")
	w.register(t, repo)
	pi, owner := helloPi(t, w.node, "owner", repo, true)
	startThroughGateway(t, w, pi)
	eventually(t, "the notice once the storage works", func() bool { return len(notStartedNotices(t, w, owner)) == 1 })
	if n := reports.Load(); n != 3 {
		t.Errorf("worker.not_started calls = %d, want 3 (two storage errors, one success)", n)
	}
	noOpenStarts(t, g)
}
