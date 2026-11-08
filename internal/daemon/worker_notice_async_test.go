package daemon

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

func TestWorkerBlockedEndNoticeDoesNotDelayIdleLimit(t *testing.T) {
	t.Parallel()
	g := startGateway(t, t.TempDir(), "")
	blocked, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	defer unblock()
	set := shortSet
	set.Idle, set.MaxAge = time.Hour, 24*time.Hour
	w := startWorkerNode(t, g, set, func(c *Config) {
		c.testGatewayCall = func(ctx context.Context, method string, params any) (json.RawMessage, bool, error) {
			if method == "msg.send" {
				p := params.(protocol.MsgSend)
				if strings.Contains(p.Body, "status: timed out") {
					select {
					case <-blocked:
					default:
						close(blocked)
						<-release
					}
				}
			}
			return nil, false, nil
		}
	})
	// Release before node cleanup, including on failure.
	t.Cleanup(unblock)
	repo := newRepo(t, "shop")
	w.register(t, repo)
	start := func() protocol.WorkerStartResult {
		return w.mustStart(t, protocol.WorkerStartRequest{Project: "shop", Harness: "opencode", Model: "fake/m1",
			Task: "FAKE:ignore hold the worker", Isolation: protocol.IsolationNone})
	}
	first, second := start(), start()
	expire := func(sid string) *worker {
		t.Helper()
		rec := w.d.workerBySession(sid)
		if rec == nil {
			t.Fatal("worker missing")
		}
		eventually(t, "worker task delivered", func() bool {
			w.d.wmu.Lock()
			defer w.d.wmu.Unlock()
			return rec.taskMsg != "" && rec.sendsInFlight == 0
		})
		w.d.wmu.Lock()
		rec.lastActive = time.Now().Add(-2 * time.Hour)
		w.d.wmu.Unlock()
		return rec
	}
	expire(first.SessionID)
	select {
	case <-blocked:
	case <-time.After(5 * time.Second):
		t.Fatal("first end notice did not reach blocked gateway")
	}
	rec := expire(second.SessionID)
	// The end decision is made by the worker loop on its tick, and that loop
	// can be late on a loaded host. The first send stays blocked for the whole
	// wait, so an end that queued behind it would still fail here.
	eventually(t, "another worker's idle limit ended it while the end queue was busy", func() bool {
		w.d.wmu.Lock()
		defer w.d.wmu.Unlock()
		return rec.stopping
	})
	unblock()
	notices := w.waitNotice(t, 2)
	if len(notices) != 2 || notices[0].From != first.SessionID || notices[1].From != second.SessionID {
		t.Fatalf("end notices duplicated or reordered: %+v", notices)
	}
}

func TestWorkerHandbackWIPDoesNotWaitBehindQueuedEndNotice(t *testing.T) {
	t.Parallel()
	g := startGateway(t, t.TempDir(), "")
	w := startWorkerNode(t, g, shortSet, nil)
	repo := newRepo(t, "shop")
	w.register(t, repo)
	res := w.mustStart(t, protocol.WorkerStartRequest{Project: "shop", Harness: "opencode", Model: "fake/m1",
		Task: "FAKE:ignore", Isolation: protocol.IsolationWorktree, AutoContinue: autoContinueOff()})
	if err := os.WriteFile(filepath.Join(res.Worktree, "left.txt"), []byte("left\n"), 0600); err != nil {
		t.Fatal(err)
	}
	blocked, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	t.Cleanup(unblock)
	w.d.queueWorkerEnd(func() { close(blocked); <-release })
	<-blocked
	finished := make(chan error, 1)
	go func() {
		_, err := w.d.handback(context.Background(), res.SessionID, protocol.WorkerHandbackRequest{Status: protocol.StatusBlocked, Report: "blocked"})
		finished <- err
	}()
	// The hand-back runs git and gateway calls, which slow down under host load,
	// so it gets the same wait as the other steps of a worker (20 s, scaled). A
	// hand-back that waits behind the blocked end still fails: it cannot return
	// before the release.
	select {
	case err := <-finished:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(scaled(20 * time.Second)):
		unblock()
		select {
		case <-finished:
		case <-time.After(scaled(20 * time.Second)):
			t.Fatal("hand-back did not return after releasing the end queue")
		}
		t.Fatal("hand-back WIP commit waited behind a blocked global end notice")
	}
	unblock()
}

// Stop must drain both admitted jobs and an end already in progress when it
// closes background admission. Offline sends must survive in outbox order.
func TestWorkerEndNoticesSurviveShutdown(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	d, err := New(Config{StateDir: dir, HomeDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	blocked, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	defer unblock()
	send := func(sid string) {
		w := &worker{sessionID: sid, owner: "owner", stopping: true}
		if _, err := d.sendNotice(context.Background(), w, protocol.WorkerNotice{Status: protocol.StatusFailed}, ""); err != nil {
			t.Errorf("send notice: %v", err)
		}
	}
	d.queueWorkerEnd(func() {
		close(blocked)
		<-release
		send("first")
	})
	<-blocked
	// Represent a worker-loop end that is already in progress when Stop starts.
	d.wg.Add(1)
	stopped := make(chan struct{})
	go func() { d.Stop(); close(stopped) }()
	eventually(t, "shutdown closes background admission", func() bool {
		d.bgMu.Lock()
		defer d.bgMu.Unlock()
		return d.bgClosed
	})
	lateDone := make(chan struct{})
	go func() {
		defer d.wg.Done()
		defer close(lateDone)
		d.queueWorkerEnd(func() { send("second") })
	}()
	select {
	case <-stopped:
		t.Fatal("shutdown lost a pending notice")
	default:
	}
	unblock()
	select {
	case <-stopped:
	case <-time.After(5 * time.Second):
		t.Fatal("shutdown did not drain end notices")
	}
	<-lateDone
	st, err := openState(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer st.close()
	queued, err := st.outbox()
	if err != nil {
		t.Fatal(err)
	}
	if len(queued) != 2 || queued[0].msg.Sender != "first" || queued[1].msg.Sender != "second" {
		t.Fatalf("shutdown outbox lost or reordered notices: %+v", queued)
	}
}
