package daemon

import (
	"context"
	"log"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// startWorkerNodeWithDetect is startWorkerNode with a different inventory:
// the tests use it to give a harness a model list.
func startWorkerNodeWithDetect(t *testing.T, g *testGateway, set WorkerSettings, detect func(context.Context) []protocol.HarnessInfo, cfg func(*Config)) *workerNode {
	t.Helper()
	logs := &logBuf{}
	n := startNode(t, g, nodeOpts{exe: "/x/opencode", cmdline: "pi", detect: detect, interval: time.Hour,
		logger: log.New(logs, "", 0), cfg: func(c *Config) {
			c.Workers = set
			if cfg != nil {
				cfg(c)
			}
		}})
	t.Cleanup(func() { killAllWorkers(n.d) })
	w := &workerNode{node: n, cli: cliClient(t, n), logs: logs}
	eventually(t, "the gateway has the inventory and the owner session", func() bool {
		return inventoryOf(t, w.cli, n) && w.ownerID() != ""
	})
	return w
}

func inventoryWithModels(name string, models []string) func(context.Context) []protocol.HarnessInfo {
	return func(ctx context.Context) []protocol.HarnessInfo {
		list := fakeInventory(ctx)
		for i := range list {
			if list[i].Name == name {
				list[i].Models = models
			}
		}
		return list
	}
}

func startCodexNodeWithModels(t *testing.T, g *testGateway, set WorkerSettings, models []string) *workerNode {
	t.Helper()
	t.Setenv(envFakeCodex, "1")
	t.Setenv("CODEX_HOME", t.TempDir())
	return startWorkerNodeWithDetect(t, g, set, inventoryWithModels("codex", models),
		func(c *Config) { c.HarnessPaths = map[string]string{"codex": os.Args[0]} })
}

// A loose model name is resolved for Codex too, and the start result says so.
func TestCodexLooseModelNameIsResolved(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	w := startCodexNodeWithModels(t, g, shortSet, []string{"gpt-6-luna", "gpt-6.1-luna"})
	repo := newRepo(t, "shop")
	w.register(t, repo)
	res := w.mustStart(t, protocol.WorkerStartRequest{Project: "shop", Harness: "codex", Model: "luna 6", Task: "say ok"})
	if res.Model != "gpt-6-luna" || res.ModelNote != "luna 6 -> gpt-6-luna" {
		t.Fatalf("start result: %+v", res)
	}
}

// A Codex name that matches nothing is refused before the worker starts, with
// the closest names and the machine in the message. Nothing is launched.
func TestCodexNoModelMatchRefusesWithClosest(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	w := startCodexNodeWithModels(t, g, shortSet, []string{"gpt-6-luna", "gpt-6.1-luna", "gpt-6.1"})
	repo := newRepo(t, "shop")
	w.register(t, repo)
	_, err := w.start(protocol.WorkerStartRequest{Project: "shop", Harness: "codex", Model: "luna 9", Task: "x"})
	if err == nil {
		t.Fatal("want a refusal")
	}
	for _, want := range []string{`no codex model matches "luna 9" on ` + w.cfg.Hostname, "closest:", "gpt-6-luna", "gpt-6.1-luna"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q lacks %q", err, want)
		}
	}
	if _, statErr := os.Stat(repo + "/fake-codex.json"); statErr == nil {
		t.Error("Codex was started")
	}
}

// A model that recently finished a worker successfully wins the tie when a
// loose name matches several models.
func TestCodexRecentSuccessBreaksTheTie(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	w := startCodexNodeWithModels(t, g, shortSet, []string{"gpt-6-luna", "gpt-6.1-luna"})
	repo := newRepo(t, "shop")
	w.register(t, repo)
	first := w.mustStart(t, protocol.WorkerStartRequest{Project: "shop", Harness: "codex", Model: "gpt-6.1-luna", Task: "say ok"})
	w.waitNotice(t, 1)
	eventually(t, "the gateway remembers the successful model", func() bool {
		ok, _ := g.gw.Store().SucceededModels(w.d.NodeID(), "codex")
		_, hit := ok["gpt-6.1-luna"]
		return hit
	})
	_ = first
	res := w.mustStart(t, protocol.WorkerStartRequest{Project: "shop", Harness: "codex", Model: "luna 6", Task: "say ok"})
	if res.Model != "gpt-6.1-luna" || res.ModelNote != "luna 6 -> gpt-6.1-luna" {
		t.Fatalf("after a success: %+v", res)
	}
}

// A stop on request is not the model's fault: the run is neither a success nor
// a failure, and a later loose name does not rank the model last or say that it
// failed a worker.
func TestStopOnRequestIsNotAModelFailure(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	w := startWorkerNodeWithDetect(t, g, shortSet, inventoryWithModels("opencode", []string{"fake/m1", "fake/m2"}), nil)
	repo := newRepo(t, "shop")
	w.register(t, repo)
	// A success on fake/m1 makes it the best match for the loose name "m", so
	// a failure on it would visibly push it behind fake/m2.
	w.mustStart(t, protocol.WorkerStartRequest{Project: "shop", Harness: "opencode", Model: "fake/m1", Task: "FAKE:handback done"})
	w.workerStopped(t, g, protocol.StopHandback)
	w.waitNotice(t, 1)
	res := w.mustStart(t, protocol.WorkerStartRequest{Project: "shop", Harness: "opencode", Model: "fake/m1", Task: "FAKE:ignore first"})
	w.cli.mustCall("worker_stop", protocol.WorkerStopRequest{Session: res.SessionID})
	w.workerStopped(t, g, protocol.StopRequested)
	var track *protocol.TrackRecord
	eventually(t, "the gateway has the stopped run", func() bool {
		tr, ok, err := g.gw.Store().TrackRecord(w.d.NodeID(), "opencode", "fake/m1", 30*24*time.Hour)
		if err != nil || !ok {
			return false
		}
		track = tr
		return true
	})
	if track.Failed != 0 || track.Complete != 1 || track.Stopped != 1 {
		t.Fatalf("a stop on request was counted as an outcome: %+v", track)
	}
	if failed, err := g.gw.Store().FailedModels(w.d.NodeID(), "opencode"); err != nil || len(failed) != 0 {
		t.Errorf("a stop on request demoted the model: %v %v", failed, err)
	}
	next := w.mustStart(t, protocol.WorkerStartRequest{Project: "shop", Harness: "opencode", Model: "m", Task: "FAKE:ignore second"})
	if next.Model != "fake/m1" {
		t.Errorf("a stopped model was not chosen again: %+v", next)
	}
	if strings.Contains(next.ModelNote, "failed a worker") || strings.Contains(next.ModelNote, "ranks last") {
		t.Errorf("the start note treats a stop as a model failure: %q", next.ModelNote)
	}
	if next.Track == nil || next.Track.Stopped != 1 || next.Track.Failed != 0 || next.Track.Complete != 1 ||
		strings.Contains(next.Track.Line(), "failed") || !strings.Contains(next.Track.Line(), "1 stopped") {
		t.Errorf("the start track line treats a stop as a failure: %+v", next.Track)
	}
}

// A start that failed does not hold the gap: the next start goes at once.
func TestFailedStartDoesNotHoldTheGap(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	set := queueSet
	set.StartGap = 5 * time.Second
	w := startWorkerNode(t, g, set, withLoad(&fakeLoad{l: roomyLoad()}))
	repo := newRepo(t, "shop")
	w.register(t, repo)

	t.Setenv("FAKE_OC_EXIT", "1")
	if _, err := w.start(protocol.WorkerStartRequest{Project: "shop", Harness: "opencode", Task: "x"}); err == nil {
		t.Fatal("the first start should fail")
	}
	os.Unsetenv("FAKE_OC_EXIT")
	res := w.mustStart(t, protocol.WorkerStartRequest{Project: "shop", Harness: "opencode", Task: "x"})
	if res.Status != "started" || res.QueueID != "" {
		t.Fatalf("the next start was held by the failed one: %+v", res)
	}
}

// A worker that ended within the first 30 seconds does not hold the gap.
func TestShortWorkerDoesNotHoldTheGap(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	set := queueSet
	set.StartGap = 5 * time.Second
	w := startWorkerNode(t, g, set, withLoad(&fakeLoad{l: roomyLoad()}))
	repo := newRepo(t, "shop")
	w.register(t, repo)
	first := w.mustStart(t, protocol.WorkerStartRequest{Project: "shop", Harness: "opencode", Task: "x"})
	w.cli.mustCall("worker_stop", protocol.WorkerStopRequest{Session: first.SessionID})
	eventually(t, "the short worker ended", func() bool { return w.d.workerCount() == 0 })
	res := w.mustStart(t, protocol.WorkerStartRequest{Project: "shop", Harness: "opencode", Task: "x"})
	if res.Status != "started" || res.QueueID != "" {
		t.Fatalf("the next start was held by the short worker: %+v", res)
	}
}
