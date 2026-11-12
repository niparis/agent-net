package daemon

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// The hand-back returns as soon as it is recorded. The project's check runs
// after the reply, and the notice carries its result when it ends.

// asyncCheck is a worker with a controllable check: the script logs "start"
// when it begins, waits until the test creates the release file, then logs
// "end".
type asyncCheck struct {
	g       *testGateway
	w       *workerNode
	res     protocol.WorkerStartResult
	runs    string // one line per script event
	release string
	pid     string // the pid of the check script's shell
}

func (a *asyncCheck) log(t *testing.T) []string {
	t.Helper()
	raw, _ := os.ReadFile(a.runs)
	return strings.Fields(string(raw))
}

func (a *asyncCheck) open(t *testing.T) {
	t.Helper()
	if err := os.WriteFile(a.release, nil, 0o600); err != nil {
		t.Fatal(err)
	}
}

// edit leaves a new file in the worker's worktree, so the next hand-back
// commits a new head.
func (a *asyncCheck) edit(t *testing.T, name string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(a.res.Worktree, name), []byte(name+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
}

func (a *asyncCheck) handback(t *testing.T) *protocol.WorkerHandbackResult {
	t.Helper()
	out, err := a.w.d.handback(context.Background(), a.res.SessionID, protocol.WorkerHandbackRequest{Report: "done"})
	if err != nil {
		t.Fatalf("handback: %v", err)
	}
	return out
}

func startAsyncCheck(t *testing.T) *asyncCheck {
	t.Helper()
	return startAsyncCheckWith(t, autoContinueOff())
}

// startAsyncCheckWith is startAsyncCheck with the worker's auto-continue
// choice; nil keeps the project's setting.
func startAsyncCheckWith(t *testing.T, autoContinue *bool) *asyncCheck {
	t.Helper()
	g := startGateway(t, t.TempDir(), "")
	w := startWorkerNode(t, g, queueSet, withLoad(&fakeLoad{l: roomyLoad()}))
	repo := newRepo(t, "shop")
	w.register(t, repo)
	dir := t.TempDir()
	a := &asyncCheck{g: g, w: w, runs: filepath.Join(dir, "runs"), release: filepath.Join(dir, "release"), pid: filepath.Join(dir, "pid")}
	command := handbackCheckScript(t, fmt.Sprintf("echo $$ > %q\n"+
		"echo start >> %q\n"+
		"while [ ! -f %q ]; do sleep 0.05; done\n"+
		"echo end >> %q\n", a.pid, a.runs, a.release, a.runs))
	setHandbackCheck(t, g, "shop", command, "")
	eventually(t, "the daemon has the hand-back check", func() bool {
		cmd, _ := w.d.handbackCheckSettings("shop")
		return cmd == command
	})
	a.res = w.mustStart(t, protocol.WorkerStartRequest{Project: "shop", Harness: "opencode", Model: "fake/m1",
		Task: "FAKE:ignore", Isolation: protocol.IsolationWorktree, AutoContinue: autoContinue})
	a.edit(t, "first.txt")
	return a
}

func (a *asyncCheck) waitStarts(t *testing.T, n int) {
	t.Helper()
	eventually(t, fmt.Sprintf("%d check runs started", n), func() bool {
		starts := 0
		for _, line := range a.log(t) {
			if line == "start" {
				starts++
			}
		}
		return starts >= n
	})
}

// The reply comes while the check still runs and no notice has gone out. The
// notice follows when the check ends, with its result.
func TestHandbackReturnsBeforeSlowCheckEnds(t *testing.T) {
	a := startAsyncCheck(t)
	out := a.handback(t)
	if !out.Checking {
		t.Fatalf("the reply does not say the check runs: %+v", out)
	}
	if !strings.Contains(out.Note, "check runs now") {
		t.Errorf("reply note: %q", out.Note)
	}
	a.waitStarts(t, 1)
	if n := len(a.w.notices(t)); n != 0 {
		t.Fatalf("the notice went out before the check ended (%d)", n)
	}
	a.open(t)
	n := a.w.waitNotice(t, 1)[0]
	if !strings.Contains(n.Body, "hand-back check: passed in ") {
		t.Fatalf("notice:\n%s", n.Body)
	}
}

// A second hand-back on the same head does not start a second check.
func TestHandbackSameHeadStartsOneCheck(t *testing.T) {
	a := startAsyncCheck(t)
	a.handback(t)
	a.waitStarts(t, 1)
	again := a.handback(t)
	if !again.Checking || !strings.Contains(again.Note, "already handed back; check running") {
		t.Fatalf("second reply: %+v", again)
	}
	a.open(t)
	a.w.waitNotice(t, 1)
	if got := strings.Join(a.log(t), ","); got != "start,end" {
		t.Fatalf("check runs: %s, want one", got)
	}
	if n := len(a.w.notices(t)); n != 1 {
		t.Fatalf("%d notices, want 1", n)
	}
}

// A hand-back on a new head stops the running check and checks the new head.
// The stopped check sends no notice.
func TestHandbackNewHeadReplacesRunningCheck(t *testing.T) {
	a := startAsyncCheck(t)
	a.handback(t)
	a.waitStarts(t, 1)
	a.edit(t, "second.txt")
	a.handback(t)
	a.waitStarts(t, 2)
	if n := len(a.w.notices(t)); n != 0 {
		t.Fatalf("the stopped check sent a notice (%d)", n)
	}
	a.open(t)
	n := a.w.waitNotice(t, 1)[0]
	if !strings.Contains(n.Body, "hand-back check: passed in ") {
		t.Fatalf("notice:\n%s", n.Body)
	}
	if got := strings.Join(a.log(t), ","); got != "start,start,end" {
		t.Fatalf("check runs: %s, want the first stopped and the second finished", got)
	}
	if n := len(a.w.notices(t)); n != 1 {
		t.Fatalf("%d notices, want 1", n)
	}
}

// A daemon that stops while a hand-back check runs (an upgrade or a restart)
// returns at once. It kills the check's process group, keeps the worker
// running, and records the check as interrupted on the change and in the
// notice. Before, Stop waited for the check, which can run for its whole
// timeout, and systemd killed the daemon after 90 seconds.
func TestDaemonStopInterruptsRunningHandbackCheck(t *testing.T) {
	a := startAsyncCheck(t)
	a.handback(t)
	a.waitStarts(t, 1)
	raw, err := os.ReadFile(a.pid)
	if err != nil {
		t.Fatal(err)
	}
	checkPID, err := strconv.Atoi(strings.TrimSpace(string(raw)))
	if err != nil {
		t.Fatalf("check pid %q: %v", raw, err)
	}
	workerPID := 0
	a.w.d.wmu.Lock()
	for _, w := range a.w.d.workers {
		if w.sessionID == a.res.SessionID {
			workerPID = w.pid
		}
	}
	a.w.d.wmu.Unlock()
	if workerPID <= 0 {
		t.Fatal("no worker process")
	}

	stopped := make(chan struct{})
	go func() { a.w.d.Stop(); close(stopped) }()
	select {
	case <-stopped:
	case <-time.After(scaled(5 * time.Second)):
		stacks := make([]byte, 1<<20)
		stacks = stacks[:runtime.Stack(stacks, true)]
		a.open(t) // let the check end, so the test cleanup can stop the daemon
		<-stopped
		t.Fatalf("Stop waited for the running hand-back check; goroutines:\n%s", stacks)
	}

	eventually(t, "the check's process is gone", func() bool {
		return syscall.Kill(checkPID, 0) != nil
	})
	if err := syscall.Kill(workerPID, 0); err != nil {
		t.Fatalf("the worker process did not survive the daemon stop: %v", err)
	}

	a.w.node.restart()
	a.w.cli = cliClient(t, a.w.node)
	n := a.w.waitNotice(t, 1)[0]
	if !strings.Contains(n.Body, "hand-back check: could not run: interrupted") {
		t.Fatalf("notice:\n%s", n.Body)
	}
	check := changeView(t, a.g, noticeChangeID(t, n.Body)).HandbackCheck
	if check == nil || check.Status != protocol.HandbackCheckCouldNotRun || !strings.Contains(check.Detail, "daemon stopped") {
		t.Fatalf("change hand-back check: %+v", check)
	}
}
