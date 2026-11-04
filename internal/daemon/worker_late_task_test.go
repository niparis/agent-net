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
	select {
	case <-finished:
	case <-time.After(15 * time.Second):
		close(release)
		t.Fatal("start did not stop after timeout")
	}
	eventRows, err := g.gw.Store().Events("worker.stopped")
	if err != nil || len(eventRows) == 0 {
		close(release)
		t.Fatalf("worker did not stop: %v %v", eventRows, err)
	}
	events, err := g.gw.Store().Events("worker.not_started")
	if err != nil || len(events) == 0 {
		close(release)
		t.Fatalf("missing not-started event: %v %v", events, err)
	}
	notice := false
	for _, m := range ownerNode.messagesOf(t, ownerID) {
		if m.To == ownerID && strings.Contains(m.Body, "status: not started") {
			notice = true
		}
	}
	if !notice {
		close(release)
		t.Fatal("not-started notice was not stored before the task was released")
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
				return starter.d.workerBySession(m.To) != nil
			}
		}
		return false
	})
}
