package daemon

import (
	"context"
	"encoding/json"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// slowQueuedStart makes the first n worker.queued_start calls fail the way a
// busy or restarting gateway does: no answer in time, or no link.
func slowQueuedStart(n int32, err error, calls *atomic.Int32) func(*Config) {
	return func(c *Config) {
		c.testGatewayCall = func(ctx context.Context, typ string, params any) (json.RawMessage, bool, error) {
			if typ != "worker.queued_start" {
				return nil, false, nil
			}
			if calls.Add(1) <= n {
				return nil, true, err
			}
			return nil, false, nil
		}
	}
}

// The worker started, but the gateway answered the task send slowly or was
// restarting. The send is tried again while the gateway link is up or
// reconnecting, and the worker stays alive and gets its task. It used to be
// stopped as start_failed after the first timeout.
func TestWorkerStartRetriesTheTaskSendWhenTheGatewayIsSlow(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
	}{
		{"timeout", errGatewayTimeout},
		{"reconnecting", errNotConnected},
		{"transient", transientRemote{RemoteError("the owner's machine did not send the task in time")}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			g := startGateway(t, t.TempDir(), "")
			var calls atomic.Int32
			w := startWorkerNode(t, g, shortSet, slowQueuedStart(2, tc.err, &calls))
			repo := newRepo(t, "shop")
			w.register(t, repo)
			_, owner := helloPi(t, w.node, "owner", repo, true)
			out, err := w.d.startWorker(context.Background(), protocol.WorkerLaunch{Project: "shop", Path: repo, Harness: "opencode", Model: "fake/m1", Owner: owner, Task: "do work", Isolation: protocol.IsolationWorktree})
			if err != nil || !out.Starting {
				t.Fatalf("start: %+v %v", out, err)
			}
			eventually(t, "the worker has its task", func() bool {
				// The gateway does not know the session until the worker registers.
				raw, err := w.cli.call("messages", protocol.QueryMessages{SessionID: out.SessionID, Limit: 50})
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
			})
			if n := calls.Load(); n != 3 {
				t.Errorf("worker.queued_start calls = %d, want 3 (two failures, one success)", n)
			}
			for _, m := range w.messagesOf(t, owner) {
				if strings.Contains(m.Body, "status: not started") {
					t.Errorf("a not-started notice for a worker that kept going:\n%s", m.Body)
				}
			}
			if w.d.workerBySession(out.SessionID) == nil {
				t.Error("the worker was stopped")
			}
			rows, err := w.d.st.workers()
			if err != nil {
				t.Fatal(err)
			}
			for _, row := range rows {
				if row.SessionID == out.SessionID && row.EndedReason != "" {
					t.Errorf("the worker record ended: %q", row.EndedReason)
				}
			}
		})
	}
}

// A gateway that stays unreachable ends the start within the bound, as before:
// one not-started notice and a start_failed record.
func TestWorkerStartGivesUpWhenTheGatewayStaysUnreachable(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	set := shortSet
	// The same bound covers the checkout and launch, so it is not tiny.
	set.BackgroundStartTimeout = 1500 * time.Millisecond
	var calls atomic.Int32
	w := startWorkerNode(t, g, set, slowQueuedStart(1<<20, errGatewayTimeout, &calls))
	repo := newRepo(t, "shop")
	w.register(t, repo)
	pi, owner := helloPi(t, w.node, "owner", repo, true)
	out := startFrom(t, pi, protocol.WorkerStartRequest{Model: "fake/m1", Project: "shop", Harness: "opencode", Task: "do work", Isolation: protocol.IsolationWorktree})
	if out.SessionID == "" {
		t.Fatalf("start: %+v", out)
	}
	eventually(t, "one not-started notice", func() bool {
		n := 0
		for _, m := range w.messagesOf(t, owner) {
			if strings.Contains(m.Body, "status: not started") && strings.Contains(m.Body, "its task could not be sent") {
				n++
			}
		}
		return n == 1
	})
	if n := calls.Load(); n < 2 {
		t.Errorf("worker.queued_start calls = %d, want a retry before giving up", n)
	}
	eventually(t, "a start_failed record", func() bool {
		rows, _ := w.d.st.workers()
		for _, row := range rows {
			if row.SessionID == out.SessionID && row.EndedReason == protocol.StopStartFails {
				return true
			}
		}
		return false
	})
}

// The retry may send the task a second time when the first call was only slow.
// The task message carries a key, so the gateway stores it once.
func TestQueuedTaskSendIsIdempotent(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	w := startWorkerNode(t, g, shortSet, nil)
	repo := newRepo(t, "shop")
	w.register(t, repo)
	_, owner := helloPi(t, w.node, "owner", repo, true)
	_, worker := helloPi(t, w.node, "worker", repo, true)
	p := protocol.WorkerQueuedStart{Owner: owner, SessionID: worker, Harness: "opencode", Project: "shop", Task: "once only"}
	for i := 0; i < 2; i++ {
		if err := w.d.sendQueuedTask(context.Background(), p); err != nil {
			t.Fatal(err)
		}
	}
	n := 0
	for _, m := range w.messagesOf(t, worker) {
		if strings.Contains(m.Body, "once only") {
			n++
		}
	}
	if n != 1 {
		t.Errorf("task messages = %d, want 1", n)
	}
}

// A refusal from the gateway is final: no retry, one notice.
func TestWorkerStartDoesNotRetryARefusal(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	var calls atomic.Int32
	w := startWorkerNode(t, g, shortSet, slowQueuedStart(1<<20, RemoteError("session is not a worker of this node with that owner"), &calls))
	repo := newRepo(t, "shop")
	w.register(t, repo)
	pi, owner := helloPi(t, w.node, "owner", repo, true)
	out := startFrom(t, pi, protocol.WorkerStartRequest{Model: "fake/m1", Project: "shop", Harness: "opencode", Task: "do work", Isolation: protocol.IsolationWorktree})
	_ = out
	eventually(t, "one not-started notice", func() bool {
		n := 0
		for _, m := range w.messagesOf(t, owner) {
			if strings.Contains(m.Body, "status: not started") {
				n++
			}
		}
		return n == 1
	})
	if n := calls.Load(); n != 1 {
		t.Errorf("worker.queued_start calls = %d, want 1 (a refusal is final)", n)
	}
}

// The retry belongs to the worker. When the worker is stopped, no more calls
// are made, and no not-started notice follows: the worker's own end tells the
// owner.
func TestWorkerStartRetryStopsWhenTheWorkerEnds(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	set := shortSet
	set.BackgroundStartTimeout = time.Minute
	var calls atomic.Int32
	w := startWorkerNode(t, g, set, slowQueuedStart(1<<20, errGatewayTimeout, &calls))
	repo := newRepo(t, "shop")
	w.register(t, repo)
	pi, owner := helloPi(t, w.node, "owner", repo, true)
	out := startFrom(t, pi, protocol.WorkerStartRequest{Model: "fake/m1", Project: "shop", Harness: "opencode", Task: "do work", Isolation: protocol.IsolationWorktree})
	eventually(t, "the task send is being retried", func() bool { return calls.Load() >= 3 })
	wk := w.d.workerBySession(out.SessionID)
	if wk == nil {
		t.Fatal("no worker")
	}
	w.d.endWorker(wk, protocol.StopRequested)
	time.Sleep(100 * time.Millisecond) // a call that was in flight may still count
	after := calls.Load()
	time.Sleep(400 * time.Millisecond)
	if n := calls.Load(); n != after {
		t.Errorf("worker.queued_start went on after the worker ended: %d then %d", after, n)
	}
	for _, m := range w.messagesOf(t, owner) {
		if strings.Contains(m.Body, "status: not started") {
			t.Errorf("a not-started notice for a worker that was stopped:\n%s", m.Body)
		}
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
