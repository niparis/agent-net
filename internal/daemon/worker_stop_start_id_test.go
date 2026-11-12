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

// blockedJudge makes the difficulty judge of every start wait until release is
// closed, so that the start is accepted and answered before it goes on.
func blockedJudge(judged *atomic.Int32, release chan struct{}) func(*Config) {
	return countingJudge(judged, func(ctx context.Context) {
		select {
		case <-release:
		case <-ctx.Done():
		}
	})
}

// startNoticeCount counts the notices to owner that carry status and start id.
func startNoticeCount(w *workerNode, t *testing.T, owner, status, startID string) int {
	n := 0
	for _, m := range w.messagesOf(t, owner) {
		if strings.Contains(m.Body, "status: "+status) && strings.Contains(m.Body, "start: "+startID) {
			n++
		}
	}
	return n
}

// An accepted start that waits for room is cancelled with the start id the
// caller got from worker_start. Until the start is queued, there is nothing to
// cancel yet, and the stop says so.
func TestStopByStartIDCancelsAQueuedStart(t *testing.T) {
	t.Parallel()
	g := startGateway(t, t.TempDir(), "")
	fl := &fakeLoad{l: cpuBusy()}
	var judged atomic.Int32
	release := make(chan struct{})
	w := startWorkerNode(t, g, queueSet, func(c *Config) {
		withLoad(fl)(c)
		blockedJudge(&judged, release)(c)
		c.testStartBudget = 300 * time.Millisecond
	})
	t.Cleanup(func() {
		select {
		case <-release:
		default:
			close(release)
		}
	})
	repo := newRepo(t, "shop")
	w.register(t, repo)
	pi, owner := helloPi(t, w.node, "caller", "/work/caller", true)
	res := startFrom(t, pi, protocol.WorkerStartRequest{Model: "fake/m1", Project: "shop", Harness: "opencode", Task: "x"})
	if res.Status != "accepted" || res.StartID == "" {
		t.Fatalf("start: %+v", res)
	}
	if _, err := pi.call("worker_stop", protocol.WorkerStopRequest{Session: res.StartID}); err == nil || !strings.Contains(err.Error(), "still") {
		t.Fatalf("a start that is still being judged: stop error %v", err)
	}
	close(release)
	eventually(t, "the queued notice for the start id", func() bool {
		return startNoticeCount(w, t, owner, "queued", res.StartID) == 1
	})
	if n, _ := w.d.st.queueLen(); n != 1 {
		t.Fatalf("queue length %d before the cancel", n)
	}
	if _, err := pi.call("worker_stop", protocol.WorkerStopRequest{Session: res.StartID}); err != nil {
		t.Fatalf("cancel by start id: %v", err)
	}
	if n, _ := w.d.st.queueLen(); n != 0 {
		t.Fatalf("queue length %d after the cancel", n)
	}
	noOpenStarts(t, g)
}

// A start that waited for room and then became a worker is stopped with the
// start id: the id stands for the worker once it has one.
func TestStopByStartIDStopsTheWorkerOfAQueuedStart(t *testing.T) {
	t.Parallel()
	g := startGateway(t, t.TempDir(), "")
	fl := &fakeLoad{l: cpuBusy()}
	var judged atomic.Int32
	release := make(chan struct{})
	w := startWorkerNode(t, g, queueSet, func(c *Config) {
		withLoad(fl)(c)
		blockedJudge(&judged, release)(c)
		c.testStartBudget = 300 * time.Millisecond
	})
	t.Cleanup(func() {
		select {
		case <-release:
		default:
			close(release)
		}
	})
	repo := newRepo(t, "shop")
	w.register(t, repo)
	pi, owner := helloPi(t, w.node, "caller", "/work/caller", true)
	res := startFrom(t, pi, protocol.WorkerStartRequest{Model: "fake/m1", Project: "shop", Harness: "opencode", Task: "x"})
	if res.Status != "accepted" || res.StartID == "" {
		t.Fatalf("start: %+v", res)
	}
	close(release)
	eventually(t, "the queued notice for the start id", func() bool {
		return startNoticeCount(w, t, owner, "queued", res.StartID) == 1
	})
	fl.set(roomyLoad())
	// The started notice comes after the task went to the worker, which is
	// when the start id comes to stand for the worker.
	eventually(t, "the started notice", func() bool {
		for _, m := range w.messagesOf(t, owner) {
			if strings.Contains(m.Body, "status: started") {
				return true
			}
		}
		return false
	})
	if _, err := pi.call("worker_stop", protocol.WorkerStopRequest{Session: res.StartID}); err != nil {
		t.Fatalf("stop the worker by start id: %v", err)
	}
	eventually(t, "the stopped worker leaves the list", func() bool { return len(w.workers(t)) == 0 })
}

// A start id this daemon never gave out gets an error that names the id and
// says what to use instead.
func TestStopByUnknownStartIDNamesWhatItIs(t *testing.T) {
	t.Parallel()
	g := startGateway(t, t.TempDir(), "")
	w := startWorkerNode(t, g, queueSet, nil)
	repo := newRepo(t, "shop")
	w.register(t, repo)
	pi, _ := helloPi(t, w.node, "caller", "/work/caller", true)
	const id = "a_0123456789abcdef"
	_, err := pi.call("worker_stop", protocol.WorkerStopRequest{Session: id})
	if err == nil || !strings.Contains(err.Error(), id) || !strings.Contains(err.Error(), "does not track") {
		t.Fatalf("stop by an unknown start id: %v", err)
	}
}

// A start that did not start has nothing to stop, and the error says so.
func TestStopByStartIDOfAStartThatFailedSaysSo(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	var judged, started atomic.Int32
	release := make(chan struct{})
	w := startWorkerNode(t, g, shortSet, func(c *Config) {
		blockedJudge(&judged, release)(c)
		c.testStartBudget = 300 * time.Millisecond
		gatewayHook("worker.start", func(ctx context.Context, n int32) (json.RawMessage, bool, error) {
			return nil, true, RemoteError("machine nas refused the worker: the disk is full")
		}, &started)(c)
	})
	t.Cleanup(func() {
		select {
		case <-release:
		default:
			close(release)
		}
	})
	repo := newRepo(t, "shop")
	w.register(t, repo)
	pi, owner := helloPi(t, w.node, "caller", repo, true)
	res := startFrom(t, pi, protocol.WorkerStartRequest{Project: "shop", Harness: "opencode", Model: "fake/m1", Task: "do work"})
	if res.Status != "accepted" || res.StartID == "" {
		t.Fatalf("start: %+v", res)
	}
	close(release)
	eventually(t, "the not started notice for the start id", func() bool {
		return startNoticeCount(w, t, owner, "not started", res.StartID) == 1
	})
	_, err := pi.call("worker_stop", protocol.WorkerStopRequest{Session: res.StartID})
	if err == nil || !strings.Contains(err.Error(), "did not start") {
		t.Fatalf("stop by the id of a start that failed: %v", err)
	}
}
