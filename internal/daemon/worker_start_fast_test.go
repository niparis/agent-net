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

func countingJudge(calls *atomic.Int32, wait func(ctx context.Context)) func(*Config) {
	return func(c *Config) {
		c.testAssess = func(ctx context.Context, project, task string) *protocol.WorkerAssessment {
			calls.Add(1)
			if wait != nil {
				wait(ctx)
			}
			return &protocol.WorkerAssessment{RunID: protocol.NewID("k"), Label: "normal", PickerLabel: "normal", AssessedAt: time.Now().Unix()}
		}
	}
}

// A start for a model whose provider has no allowance left is refused at once,
// with the reason and the picker's own choice, and the judge never runs.
func TestWorkerStartExhaustedProviderFailsBeforeTheJudge(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	var judged atomic.Int32
	w := startWorkerNode(t, g, shortSet, countingJudge(&judged, nil))
	repo := newRepo(t, "shop")
	w.register(t, repo)
	setJudgeScores(t, w, map[string]float64{"fake/m1": 30})
	pi, _ := helloPi(t, w.node, "caller", repo, true)
	if err := g.gw.Store().RecordQuotaBlock(w.cfg.NodeID, protocol.QuotaBlock{Provider: "claude", Until: time.Now().Add(48 * time.Hour)}, "weekly quota reached"); err != nil {
		t.Fatal(err)
	}
	began := time.Now()
	_, err := pi.call("worker_start", protocol.WorkerStartRequest{Project: "shop", Harness: "claude", Model: "claude-opus-5-5", Task: "do work", Isolation: protocol.IsolationNone})
	if err == nil || !strings.Contains(err.Error(), "claude quota used up until") || !strings.Contains(err.Error(), "; try opencode fake/m1") {
		t.Fatalf("start of a model with no allowance: %v", err)
	}
	if took := time.Since(began); took > 2*time.Second {
		t.Fatalf("the refusal took %s", took)
	}
	if n := judged.Load(); n != 0 {
		t.Fatalf("the judge ran %d times before the refusal", n)
	}
	noOpenStarts(t, g)
}

// A judge that takes 30 s does not hold the caller: the start is accepted
// within the budget, and the worker starts when the judge is done, with a
// notice to the caller.
func TestWorkerStartAcceptedWhileTheJudgeIsSlow(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	var judged atomic.Int32
	release := make(chan struct{})
	w := startWorkerNode(t, g, shortSet, func(c *Config) {
		countingJudge(&judged, func(ctx context.Context) {
			select {
			case <-release: // stands for a judge that takes 30 s
			case <-ctx.Done():
			}
		})(c)
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
	pi, owner := helloPi(t, w.node, "caller", repo, true)
	began := time.Now()
	res := startFrom(t, pi, protocol.WorkerStartRequest{Project: "shop", Harness: "opencode", Model: "fake/m1", Task: "do work"})
	if took := time.Since(began); took > 3*time.Second {
		t.Fatalf("the answer took %s with the judge blocked", took)
	}
	if res.Status != "accepted" || res.StartID == "" || res.SessionID != "" || judged.Load() != 1 {
		t.Fatalf("answer %+v, judge runs %d", res, judged.Load())
	}
	close(release)
	eventually(t, "the started notice names the accepted start", func() bool {
		for _, m := range w.messagesOf(t, owner) {
			if strings.Contains(m.Body, "status: started") && strings.Contains(m.Body, "start: "+res.StartID) {
				return true
			}
		}
		return false
	})
	eventually(t, "the worker got its task", func() bool {
		for _, m := range w.messagesOf(t, owner) {
			if m.Type == protocol.TypeRequest && strings.Contains(m.Body, "do work") {
				return true
			}
		}
		return false
	})
}

// A start that was accepted and then failed is reported to the caller.
func TestWorkerStartAcceptedThenFailedSendsNotice(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	var judged, started atomic.Int32
	release := make(chan struct{})
	w := startWorkerNode(t, g, shortSet, func(c *Config) {
		countingJudge(&judged, func(ctx context.Context) {
			select {
			case <-release:
			case <-ctx.Done():
			}
		})(c)
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
		t.Fatalf("answer %+v", res)
	}
	close(release)
	defer func() {
		if t.Failed() {
			t.Log(w.logs.String())
		}
	}()
	eventually(t, "the not started notice", func() bool {
		for _, m := range w.messagesOf(t, owner) {
			if strings.Contains(m.Body, "status: not started") && strings.Contains(m.Body, "the disk is full") && strings.Contains(m.Body, "start: "+res.StartID) {
				return true
			}
		}
		return false
	})
}

// The slow log line of worker_start says how long each part took.
func TestWorkerStartSlowLogHasPhases(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	var judged atomic.Int32
	w := startWorkerNode(t, g, shortSet, countingJudge(&judged, func(ctx context.Context) {
		select {
		case <-time.After(400 * time.Millisecond):
		case <-ctx.Done():
		}
	}))
	repo := newRepo(t, "shop")
	w.register(t, repo)
	pi, _ := helloPi(t, w.node, "caller", repo, true)
	res := startFrom(t, pi, protocol.WorkerStartRequest{Project: "shop", Harness: "opencode", Model: "fake/m1", Task: "do work"})
	if res.Status != "started" {
		t.Fatalf("answer %+v", res)
	}
	var line string
	eventually(t, "the slow line of worker_start", func() bool {
		for _, l := range strings.Split(w.logs.String(), "\n") {
			if strings.Contains(l, "event=slow method=worker_start ") {
				line = l
				return true
			}
		}
		return false
	})
	for _, want := range []string{"phases=validate=", " precheck=", " judge=4", " gateway=", "(picker=", " launch=", " task="} {
		if !strings.Contains(line, want) {
			t.Fatalf("slow line lacks %q: %s", want, line)
		}
	}
}
