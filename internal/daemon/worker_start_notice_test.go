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

// A continue of an ended worker answers within the budget like a new start. On
// 2026-11-19 a slow continue held the caller past its tool timeout, though the
// start went on; the caller never got a start id and could not tell if it must
// ask again.
func TestWorkerContinueAcceptedWhileTheGatewayIsSlow(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	var calls atomic.Int32
	release := make(chan struct{})
	w := startWorkerNode(t, g, shortSet, func(c *Config) {
		c.testStartBudget = 300 * time.Millisecond
		gatewayHook("worker.start", func(ctx context.Context, n int32) (json.RawMessage, bool, error) {
			if n > 1 { // the first call is the worker's own start
				select {
				case <-release:
				case <-ctx.Done():
				}
			}
			return nil, false, nil
		}, &calls)(c)
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
	// Under -race this first start can take longer than the test budget, and
	// then the answer is "accepted" with no session id yet. This is the only
	// worker, so find its session in the daemon's own records.
	startFrom(t, pi, protocol.WorkerStartRequest{Project: "shop", Harness: "opencode", Model: "fake/m1", Task: "FAKE:handback first"})
	var firstSession string
	eventually(t, "the worker ended", func() bool {
		rows, err := w.d.st.workers()
		if err != nil || len(rows) != 1 {
			return false
		}
		row := rows[0]
		firstSession = row.SessionID
		return w.d.workerCount() == 0 && row.SessionID != "" && row.EndedReason != "" && row.Reported
	})

	began := time.Now()
	var res protocol.WorkerStartResult
	mustJSON(t, pi.mustCall("worker_start", protocol.WorkerStartRequest{Continue: firstSession, Task: "do more"}), &res)
	if took := time.Since(began); took > 3*time.Second {
		t.Fatalf("the answer took %s with the gateway blocked", took)
	}
	if res.Status != "accepted" || res.StartID == "" {
		t.Fatalf("answer %+v", res)
	}
	close(release)
	eventually(t, "the started notice names the accepted continue", func() bool {
		for _, m := range w.messagesOf(t, owner) {
			if strings.Contains(m.Body, "start: "+res.StartID) && (strings.Contains(m.Body, "status: started") || strings.Contains(m.Body, "status: starting")) {
				return true
			}
		}
		return false
	})
}

// The notice of an accepted start that is still being set up reaches the
// caller without a refusal. It used to come from the new worker's session,
// which the gateway does not know yet, so the gateway logged "sender session
// not on this node" for every such start.
func TestAcceptedStartNoticeIsNotRefused(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	var judged atomic.Int32
	release := make(chan struct{})
	w := startWorkerNode(t, g, shortSet, func(c *Config) {
		countingJudge(&judged, func(ctx context.Context) {
			select {
			case <-release:
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
	res := startFrom(t, pi, protocol.WorkerStartRequest{Project: "shop", Harness: "opencode", Model: "fake/m1", Task: "do work", Isolation: protocol.IsolationWorktree})
	if res.Status != "accepted" {
		t.Fatalf("answer %+v", res)
	}
	close(release)
	defer func() {
		if t.Failed() {
			t.Log(w.logs.String(), g.logs.String())
		}
	}()
	eventually(t, "the started notice", func() bool {
		for _, m := range w.messagesOf(t, owner) {
			if strings.Contains(m.Body, "start: "+res.StartID) && strings.Contains(m.Body, "status: start") {
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
	if strings.Contains(g.logs.String(), "msg.refused") || strings.Contains(w.logs.String(), "accepted start notice") {
		t.Fatalf("a start notice was refused:\ngateway:\n%s\ndaemon:\n%s", g.logs.String(), w.logs.String())
	}
}
