package daemon

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// A queued start waits for the next daemon when Stop has begun. The v0.5.146
// install on research took a start off the queue while the old daemon was on
// its way out, launched it, and the launch failed because the plugin could not
// register before the daemon exited (2026-11-26 05:09Z).
func TestStoppingDaemonLeavesItsQueuedStartsForTheNextOne(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	fl := &fakeLoad{l: cpuBusy()}
	set := shortSet
	set.StartGap = -1
	set.QueueTick = time.Hour // the test runs the queue pass itself
	w := startWorkerNode(t, g, set, withLoad(fl))
	repo := newRepo(t, "shop")
	w.register(t, repo)
	pi, _ := helloPi(t, w.node, "caller", "/work/caller", true)
	r := startFrom(t, pi, protocol.WorkerStartRequest{Model: "fake/m1", Project: "shop", Harness: "opencode", Task: "started after the upgrade"})
	if r.Status != "queued" {
		t.Fatalf("%+v", r)
	}
	fl.set(roomyLoad())

	// Stop has begun: this is the first thing it records, before it cancels
	// anything.
	w.d.bgMu.Lock()
	w.d.bgClosed = true
	w.d.bgMu.Unlock()
	w.d.processQueue(context.Background())

	if n, err := w.d.st.queueLen(); err != nil || n != 1 {
		t.Fatalf("queue length %d (%v) after a queue pass while stopping, want the start still queued", n, err)
	}
	if got := w.d.workerCount(); got != 0 {
		t.Fatalf("the stopping daemon launched %d worker(s)", got)
	}
	if lines := w.logs.lines("worker.dequeued"); len(lines) != 0 {
		t.Fatalf("the stopping daemon dequeued a start: %v", lines)
	}

	// The daemon really stops, and the next one starts what it left.
	w.d.Stop()
	st, err := openState(w.cfg.StateDir)
	if err != nil {
		t.Fatal(err)
	}
	queued, err := st.queue()
	st.close()
	if err != nil || len(queued) != 1 || queued[0].ID != r.QueueID {
		t.Fatalf("queue after the stop: %+v (%v), want %s", queued, err, r.QueueID)
	}
	w.cfg.Workers.QueueTick = 25 * time.Millisecond
	w.node.startAgain()
	eventually(t, "the next daemon starts the queued start", func() bool {
		return len(w.logs.lines("worker.dequeued", "queue="+r.QueueID)) == 1
	})
}

// A stop that interrupts a launch after it began is reported as a restart, not
// as a missing plugin: the harness error a stop causes reads as an install
// problem and sends the caller to run setup on a machine that is fine.
func TestStopDuringALaunchIsReportedAsARestartNotAMissingPlugin(t *testing.T) {
	t.Setenv("GORACE", "atexit_sleep_ms=0")
	t.Setenv("FAKE_OC_NOPLUGIN", "1")
	g := startGateway(t, t.TempDir(), "")
	fl := &fakeLoad{l: cpuBusy()}
	set := queueSet
	set.QueueTick = 20 * time.Millisecond
	w := startWorkerNode(t, g, set, withLoad(fl))
	repo := newRepo(t, "shop")
	w.register(t, repo)
	pi, _ := helloPi(t, w.node, "caller", "/work/caller", true)
	r := startFrom(t, pi, protocol.WorkerStartRequest{Model: "fake/m1", Project: "shop", Harness: "opencode", Task: "interrupted task"})
	if r.Status != "queued" {
		t.Fatalf("%+v", r)
	}
	// Room appears and the launch begins. It then waits for the plugin, which
	// this fake never registers, so the daemon is inside the launch when the
	// stop starts.
	fl.set(roomyLoad())
	eventually(t, "the launch reached the plugin wait", func() bool {
		return fakeOpenCodeLocation(repo) != ""
	})
	w.d.Stop()

	st, err := openState(w.cfg.StateDir)
	if err != nil {
		t.Fatal(err)
	}
	rows, err := st.readyStartOutcomes()
	st.close()
	if err != nil {
		t.Fatal(err)
	}
	reason := ""
	for _, row := range rows {
		if row.Key != notStartedOutcomeKey(r.QueueID) {
			continue
		}
		var p protocol.WorkerNotStarted
		if err := json.Unmarshal([]byte(row.Payload), &p); err != nil {
			t.Fatal(err)
		}
		reason = p.Reason
	}
	if reason == "" {
		t.Fatalf("no not-started report for %s", r.QueueID)
	}
	if !strings.Contains(reason, "daemon restart") {
		t.Errorf("reason does not say a restart interrupted the start: %q", reason)
	}
	for _, wrong := range []string{"plugin did not register", "live opencode model lookup"} {
		if strings.Contains(reason, wrong) {
			t.Errorf("reason sends the caller to reinstall: %q", reason)
		}
	}
}

// A stop that cuts the task send of a worker that already launched is reported
// the same way. The worker had no task, so it is stopped, and the caller is
// told to start again rather than that the gateway failed.
func TestStopAfterLaunchIsReportedAsARestart(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	fl := &fakeLoad{l: cpuBusy()}
	set := queueSet
	w := startWorkerNode(t, g, set, func(c *Config) {
		c.LoadProbe = fl.get
		c.testGatewayCall = func(ctx context.Context, typ string, params any) (json.RawMessage, bool, error) {
			if typ == "worker.queued_start" {
				<-ctx.Done()
				return nil, true, ctx.Err()
			}
			return nil, false, nil
		}
	})
	repo := newRepo(t, "shop")
	w.register(t, repo)
	pi, _ := helloPi(t, w.node, "caller", "/work/caller", true)
	r := startFrom(t, pi, protocol.WorkerStartRequest{Model: "fake/m1", Project: "shop", Harness: "opencode", Task: "interrupted after launch"})
	if r.Status != "queued" {
		t.Fatalf("%+v", r)
	}
	fl.set(roomyLoad())
	eventually(t, "the worker launched and its task send waits", func() bool {
		return len(w.logs.lines("worker.started")) == 1
	})
	w.d.Stop()

	st, err := openState(w.cfg.StateDir)
	if err != nil {
		t.Fatal(err)
	}
	rows, err := st.readyStartOutcomes()
	st.close()
	if err != nil {
		t.Fatal(err)
	}
	reason := ""
	for _, row := range rows {
		if row.Key != notStartedOutcomeKey(r.QueueID) {
			continue
		}
		var p protocol.WorkerNotStarted
		if err := json.Unmarshal([]byte(row.Payload), &p); err != nil {
			t.Fatal(err)
		}
		reason = p.Reason
	}
	if reason != stopInterruptedStartNote {
		t.Errorf("reason %q, want %q", reason, stopInterruptedStartNote)
	}
}

// A stop that begins after the head left the queue but before anything
// launched puts the start back in its place, so the next daemon starts it in
// the turn it had.
func TestStopBetweenTakeAndLaunchPutsTheStartBack(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	fl := &fakeLoad{l: cpuBusy()}
	set := shortSet
	set.StartGap = -1
	set.QueueTick = time.Hour
	w := startWorkerNode(t, g, set, withLoad(fl))
	repo := newRepo(t, "shop")
	w.register(t, repo)
	pi, _ := helloPi(t, w.node, "caller", "/work/caller", true)
	first := startFrom(t, pi, protocol.WorkerStartRequest{Model: "fake/m1", Project: "shop", Harness: "opencode", Task: "first"})
	second := startFrom(t, pi, protocol.WorkerStartRequest{Model: "fake/m1", Project: "shop", Harness: "opencode", Task: "second"})
	if first.Status != "queued" || second.Status != "queued" {
		t.Fatalf("%+v %+v", first, second)
	}
	fl.set(roomyLoad())
	entries, err := w.d.st.queue()
	if err != nil || len(entries) != 2 {
		t.Fatalf("queue %+v (%v)", entries, err)
	}
	if !w.d.takeQueuedHead(entries[0]) {
		t.Fatal("the head did not leave the queue with room")
	}
	// Stop begins here, before the launch.
	w.d.bgMu.Lock()
	w.d.bgClosed = true
	w.d.bgMu.Unlock()
	w.d.startQueued(context.Background(), entries[0])

	queued, err := w.d.st.queue()
	if err != nil || len(queued) != 2 || queued[0].ID != first.QueueID || queued[1].ID != second.QueueID {
		t.Fatalf("queue %+v (%v), want %s then %s", queued, err, first.QueueID, second.QueueID)
	}
	if got := w.d.workerCount(); got != 0 {
		t.Fatalf("the stopping daemon launched %d worker(s)", got)
	}
}

// fakeOpenCodeLocation is the project folder the fake OpenCode recorded when
// the daemon loaded it, which happens right before the plugin wait.
func fakeOpenCodeLocation(dir string) string {
	raw, err := os.ReadFile(filepath.Join(dir, "fake-opencode.json"))
	if err != nil {
		return ""
	}
	var rec fakeOCRecord
	if json.Unmarshal(raw, &rec) != nil {
		return ""
	}
	return rec.LocationDir
}
