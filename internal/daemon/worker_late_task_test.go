package daemon

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

func TestQueuedWorkerLateTaskAfterTimeout(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	entered := make(chan protocol.MsgSend, 1)
	release := make(chan struct{})
	ownerNode := startWorkerNode(t, g, shortSet, func(c *Config) {
		c.testGatewayCall = func(ctx context.Context, typ string, params any) (json.RawMessage, bool, error) {
			if p, ok := params.(protocol.MsgSend); typ == "msg.send" && ok && strings.HasPrefix(p.Body, "[agent-net worker brief]") {
				entered <- p
				<-release
			}
			return nil, false, nil
		}
	})
	starter := startWorkerNode(t, g, shortSet, func(c *Config) { c.testQueuedTaskTimeout = 500 * time.Millisecond })
	repo := newRepo(t, "late")
	starter.register(t, repo)
	_, ownerID := helloPi(t, ownerNode.node, "caller", repo, true)
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		starter.d.startQueued(context.Background(), queuedStart{Owner: ownerID, Project: "late", Path: repo, Harness: "opencode", Model: "fake/m1", Task: "late task canary", Isolation: protocol.IsolationNone, QueuedAt: time.Now()})
	}()
	var task protocol.MsgSend
	select {
	case task = <-entered:
	case <-time.After(15 * time.Second):
		close(release)
		t.Fatal("task send did not begin")
	}
	if !task.WorkerTask || task.WorkerTaskRef == "" {
		close(release)
		t.Fatalf("queued brief missing marker or relay reference: %+v", task)
	}
	select {
	case <-finished:
	case <-time.After(15 * time.Second):
		close(release)
		t.Fatal("start did not stop after timeout")
	}
	// startQueued hands the task send to its own goroutine, so the stop comes
	// after it returns. The owner's notice needs a start the gateway tracks;
	// the queue tests check it.
	deadline := time.Now().Add(15 * time.Second)
	for {
		stopped, _ := g.gw.Store().Events("worker.stopped")
		notStarted, _ := g.gw.Store().Events("worker.not_started")
		if len(stopped) > 0 && len(notStarted) > 0 {
			break
		}
		if time.Now().After(deadline) {
			close(release)
			t.Fatalf("before the task was released: stopped=%d not_started=%d", len(stopped), len(notStarted))
		}
		time.Sleep(20 * time.Millisecond)
	}
	close(release)
	eventually(t, "late task refused", func() bool {
		return len(g.logs.lines("msg.refused", "to="+task.Recipient)) > 0
	})
	for _, m := range ownerNode.messagesOf(t, ownerID) {
		if m.To == task.Recipient && strings.Contains(m.Body, "late task canary") {
			t.Fatalf("late task stored: %+v", m)
		}
	}
	if lines := g.logs.lines("worker.resume_failed", task.Recipient); len(lines) != 0 {
		t.Fatalf("gateway attempted to resume ended worker: %v", lines)
	}
	if starter.d.workerBySession(task.Recipient) != nil {
		t.Fatal("ended worker resumed")
	}
}

func TestQueuedWorkerTaskArrivesOnRunningWorker(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	ownerNode := startWorkerNode(t, g, shortSet, nil)
	starter := startWorkerNode(t, g, shortSet, nil)
	repo := newRepo(t, "running")
	starter.register(t, repo)
	_, ownerID := helloPi(t, ownerNode.node, "caller", repo, true)
	starter.d.startQueued(context.Background(), queuedStart{Owner: ownerID, Project: "running", Path: repo, Harness: "opencode", Model: "fake/m1", Task: "running task canary", Isolation: protocol.IsolationNone, QueuedAt: time.Now()})
	eventually(t, "running worker receives task", func() bool {
		for _, m := range ownerNode.messagesOf(t, ownerID) {
			if m.From == ownerID && m.Type == protocol.TypeRequest && strings.Contains(m.Body, "running task canary") {
				return protocol.IsWorkerTask(m) && m.WorkerTask != nil && starter.d.workerBySession(m.To) != nil
			}
		}
		return false
	})
}
