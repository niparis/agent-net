package daemon

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

// startLeftover starts a process for a stop to find, and reaps it when it
// ends, so an ended process does not linger as a zombie. done is closed then.
func startLeftover(t *testing.T, dir string, env []string, name string, args ...string) (pid int, done chan struct{}) {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir, cmd.Env = dir, append(os.Environ(), env...)
	stdin, err := cmd.StdinPipe() // an open input keeps a fake agy waiting, as a view does
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	done = make(chan struct{})
	go func() { cmd.Wait(); close(done) }()
	t.Cleanup(func() {
		stdin.Close()
		syscall.Kill(cmd.Process.Pid, syscall.SIGKILL)
		<-done
	})
	return cmd.Process.Pid, done
}

func closed(ch chan struct{}) bool {
	select {
	case <-ch:
		return true
	default:
		return false
	}
}

// startFakeHerdr starts a fake herdr server whose arguments are args and, in a
// pane under it, a fake agy view of native. It returns the view's process ID
// and a channel closed when the server exits. The server is a herdr by name,
// so the daemon finds it the way it finds the real one up a process tree.
func startFakeHerdr(t *testing.T, args []string, native string) (view int, serverDone chan struct{}) {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(t.TempDir(), "herdr")
	if err := os.Symlink(exe, bin); err != nil {
		t.Fatal(err)
	}
	records := t.TempDir()
	cmd := exec.Command(bin, append([]string{"-test.run", "^TestFakeHerdrServerProcess$", "--"}, args...)...)
	cmd.Env = append(os.Environ(), "FAKE_HERDR_SERVER=1", "FAKE_HERDR_NATIVE="+native, "FAKE_AGY_RECORDS="+records)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	serverDone = make(chan struct{})
	go func() { cmd.Wait(); close(serverDone) }()
	var recs []fakeAgyRecord
	eventually(t, "the view of the conversation runs", func() bool { recs = agyRecords(records); return len(recs) == 1 })
	view = recs[0].PID
	t.Cleanup(func() {
		syscall.Kill(view, syscall.SIGKILL)
		syscall.Kill(cmd.Process.Pid, syscall.SIGKILL)
		<-serverDone
	})
	return view, serverDone
}

// TestFakeHerdrServerProcess runs only as the fake herdr server that
// startFakeHerdr starts: it starts the fake agy view of FAKE_HERDR_NATIVE and
// stays alive as its parent until the view ends, as a server with a pane does.
func TestFakeHerdrServerProcess(t *testing.T) {
	if os.Getenv("FAKE_HERDR_SERVER") != "1" {
		return
	}
	exe, err := os.Executable()
	if err != nil {
		os.Exit(1)
	}
	view := exec.Command(exe, "-test.run", "^TestAgyProcess$", "--", "--conversation", os.Getenv("FAKE_HERDR_NATIVE"))
	view.Env = append(os.Environ(), "FAKE_AGY_PROCESS=1")
	stdin, err := view.StdinPipe() // an open input keeps the view waiting, as a view does
	if err != nil {
		os.Exit(1)
	}
	if err := view.Start(); err != nil {
		os.Exit(1)
	}
	view.Wait()
	stdin.Close()
}

// The daemon starts agent-net's own herdr server as `herdr --session agent-net
// server`, and herdr picks the socket, so those arguments carry none. A stop
// must still end a view in a pane of that server. A view under the owner's
// default session, or another named session, is left alone.
func TestStopEndsAViewUnderAgentNetsHerdrServer(t *testing.T) {
	const native = "0f7c6b9a-2d3e-4f50-8a91-b2c3d4e5f601"
	for _, tc := range []struct {
		name string
		args []string
		ends bool
	}{
		{"agent-net's session", []string{"--session", agentNetHerdrSession, "server"}, true},
		{"the default session", []string{"server"}, false},
		{"another session", []string{"--session", "work", "server"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st, err := openState(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { st.close() })
			d := &Daemon{st: st, workers: map[string]*worker{}, cfg: Config{Workers: shortSet}}
			row := workerRow{ID: "w_1", Harness: "agy", Owner: "s_owner", NativeID: native, SessionID: "s_worker",
				EndedReason: protocol.StopHandback, Resumable: true, Reported: true}
			if err := st.saveWorker(row); err != nil {
				t.Fatal(err)
			}
			view, serverDone := startFakeHerdr(t, tc.args, native)

			if err := d.stopWorkerSession(protocol.WorkerKill{Session: "s_worker", Requester: "s_owner"}); err != nil {
				t.Fatalf("stop: %v", err)
			}
			if tc.ends {
				eventually(t, "the view in agent-net's herdr ends", func() bool { return !alive(view) })
				return
			}
			if !alive(view) {
				t.Fatal("the stop ended a view under another herdr session")
			}
			if closed(serverDone) {
				t.Fatal("the stop ended a herdr server that is not agent-net's")
			}
		})
	}
}

// The M2Max case: an agy worker has ended, so the daemon no longer runs it,
// but a view of its conversation (agy --conversation <id>, from a herdr pane)
// and a program in its worktree are still running. The stop answers ok, ends
// both, leaves an unrelated process alone, and a later message does not
// start the worker again.
func TestStopOfAnEndedAgyWorkerEndsItsViewAndIsFinal(t *testing.T) {
	t.Parallel()
	w, dir, _ := startAgyNode(t, shortSet)
	res := w.mustStart(t, protocol.WorkerStartRequest{Project: "shop", Harness: "agy", Model: "gemini-3.8-flash-low", Task: "FAKE:handback", Isolation: protocol.IsolationWorktree})
	w.waitNotice(t, 1)
	eventually(t, "agy ends after its hand-back", func() bool { return w.d.workerCount() == 0 })
	native := agyRecords(dir)[0].Native

	viewRecords := t.TempDir()
	_, view := startLeftover(t, t.TempDir(), []string{"FAKE_AGY_PROCESS=1", "FAKE_AGY_RECORDS=" + viewRecords},
		os.Args[0], "-test.run", "^TestAgyProcess$", "--", "--conversation", native)
	eventually(t, "the view of the conversation runs", func() bool { return len(agyRecords(viewRecords)) == 1 })
	_, inWorktree := startLeftover(t, res.Worktree, nil, "sleep", "300")
	_, unrelated := startLeftover(t, t.TempDir(), nil, "sleep", "300")

	if _, err := w.cli.call(protocol.OpWorkerStop, protocol.WorkerStopRequest{Session: res.SessionID}); err != nil {
		t.Fatalf("stop of the ended worker: %v", err)
	}
	eventually(t, "the view and the worktree program end", func() bool { return closed(view) && closed(inWorktree) })
	if closed(unrelated) {
		t.Fatal("the stop ended a process it cannot tie to the worker")
	}
	_, err := w.cli.call(protocol.OpSend, protocol.OwnerSendRequest{To: res.SessionID, Body: "FAKE:handback again"})
	if err == nil || !strings.Contains(err.Error(), "was stopped") {
		t.Fatalf("message to the stopped worker: %v", err)
	}
	if n := len(agyRecords(dir)); n != 1 || w.d.workerCount() != 0 {
		t.Fatalf("the stopped worker started again: %d agy runs, %d workers", n, w.d.workerCount())
	}
}

// An ended agy worker leaves the daemon's registry, so its session is no
// longer reported online with the pid of a process that is gone.
func TestEndedAgyWorkerIsNoLongerReported(t *testing.T) {
	t.Parallel()
	w, dir, _ := startAgyNode(t, shortSet)
	res := w.mustStart(t, protocol.WorkerStartRequest{Project: "shop", Harness: "agy", Model: "gemini-3.8-flash-low", Task: "FAKE:handback", Isolation: protocol.IsolationWorktree})
	w.waitNotice(t, 1)
	eventually(t, "agy ends after its hand-back", func() bool { return w.d.workerCount() == 0 })
	native := agyRecords(dir)[0].Native
	eventually(t, "the session leaves the registry", func() bool { return w.d.SessionID("agy", native) == "" })
	eventually(t, "the gateway shows the worker offline", func() bool {
		for _, a := range w.agents(t, true) {
			if a.SessionID == res.SessionID {
				return !a.Online
			}
		}
		return true
	})
}

// A continue and a message restart of the same ended worker never both run:
// while one starts a new run, the other is refused, and once the continue
// has started, a message to the old worker does not start it again.
func TestContinueAndMessageRestartNeverRunTwoCopies(t *testing.T) {
	t.Parallel()
	g := startGateway(t, t.TempDir(), "")
	w := startWorkerNode(t, g, shortSet, nil)
	w.register(t, newRepo(t, "shop"))
	old := w.mustStart(t, protocol.WorkerStartRequest{Project: "shop", Harness: "opencode", Model: "fake/m1",
		Task: "FAKE:edit FAKE:handback add work.txt", Isolation: protocol.IsolationWorktree})
	w.workerStopped(t, g, protocol.StopHandback)
	row, ok, err := w.d.st.workerBySession(old.SessionID)
	if err != nil || !ok {
		t.Fatalf("row: %v %v", ok, err)
	}

	// A continue is starting the new worker: a message restart is refused.
	release, err := w.d.claimRelaunch(row.ID, old.SessionID, noRestartContinued)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.d.resumeWorkerSession(t.Context(), old.SessionID, row.Owner); err == nil || !strings.Contains(err.Error(), "being continued") {
		t.Fatalf("restart during a continue: %v", err)
	}
	release()

	if _, err := w.startDefault(protocol.WorkerStartRequest{Continue: old.SessionID, Task: "FAKE:ignore"}); err != nil {
		t.Fatal(err)
	}
	if _, err := w.d.resumeWorkerSession(t.Context(), old.SessionID, row.Owner); err == nil || !strings.Contains(err.Error(), "was continued") {
		t.Fatalf("restart after a continue: %v", err)
	}
	if n := w.d.workerCount(); n != 1 {
		t.Fatalf("%d workers run, want only the continued one", n)
	}
}

// A stop of a session the daemon no longer runs, without a gateway: it
// answers ok, ends the processes it can tie to the session (the native ID in
// the command line, or a folder inside the worktree), leaves a folder that
// only shares the prefix alone, drops a queued restart, and the worker cannot
// start again.
func TestStopOfAnUntrackedSessionEndsItsProcessesAndIsFinal(t *testing.T) {
	st, err := openState(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.close() })
	d := &Daemon{st: st, workers: map[string]*worker{}, cfg: Config{Workers: shortSet}}
	worktree, _ := filepath.EvalSymlinks(t.TempDir())
	inside := filepath.Join(worktree, "sub")
	if err := os.Mkdir(inside, 0o700); err != nil {
		t.Fatal(err)
	}
	row := workerRow{ID: "w_1", Harness: "agy", Owner: "s_owner", NativeID: "conv-1", SessionID: "s_worker", PID: 99999,
		Worktree: worktree, EndedReason: protocol.StopHandback, Resumable: true, Reported: true}
	if err := st.saveWorker(row); err != nil {
		t.Fatal(err)
	}
	if err := st.addQueued(queuedStart{ID: "q_1", ResumeSession: "s_worker", Owner: "s_owner", Project: "shop", Harness: "agy"}); err != nil {
		t.Fatal(err)
	}
	viewRecords := t.TempDir()
	_, view := startLeftover(t, t.TempDir(), []string{"FAKE_AGY_PROCESS=1", "FAKE_AGY_RECORDS=" + viewRecords},
		os.Args[0], "-test.run", "^TestAgyProcess$", "--", "--conversation", "conv-1")
	eventually(t, "the view of the conversation runs", func() bool { return len(agyRecords(viewRecords)) == 1 })
	_, inWorktree := startLeftover(t, inside, nil, "sleep", "300")
	_, prefix := startLeftover(t, mkdirT(t, worktree+"-other"), nil, "sleep", "300")

	if err := d.stopWorkerSession(protocol.WorkerKill{Session: "s_worker", Requester: "s_owner"}); err != nil {
		t.Fatalf("stop: %v", err)
	}
	eventually(t, "the view and the worktree program end", func() bool { return closed(view) && closed(inWorktree) })
	if closed(prefix) {
		t.Fatal("the stop ended a process in another folder")
	}
	if n, _ := st.queueLen(); n != 0 {
		t.Fatalf("%d queued restarts left", n)
	}
	got, _, _ := st.workerBySession("s_worker")
	if why := d.resumeRefusal(got); !strings.Contains(why, "was stopped") {
		t.Fatalf("restart refusal after the stop: %q", why)
	}
}

func mkdirT(t *testing.T, dir string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	return dir
}

// A stop the worker's machine did not get yet reaches the caller as a
// pending stop, with the gateway's note, not as a plain "stopped".
func TestPendingStopAnswerReachesTheCaller(t *testing.T) {
	raw := []byte(`{"stopped":false,"pending":true,"note":"machine mac did not answer, so the stop is pending"}`)
	got := workerStopResult("s_1", raw)
	if !got.Pending || !strings.Contains(got.Note, "pending") || !got.Stopped {
		t.Fatalf("stop answer = %+v", got)
	}
}

// A worker whose model this machine cannot run moves its task to the next
// model. When a stop came while the gateway answered, the task does not go
// on: no start is queued, and the caller is told the worker was stopped.
func TestStoppedWorkerDoesNotMoveToTheNextModel(t *testing.T) {
	st, err := openState(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.close() })
	var reasons []string
	d := &Daemon{st: st, workers: map[string]*worker{}, cfg: Config{testGatewayCall: func(_ context.Context, typ string, params any) (json.RawMessage, bool, error) {
		if p, ok := params.(protocol.WorkerNotStarted); ok && typ == "worker.not_started" {
			reasons = append(reasons, p.Reason)
		}
		return json.RawMessage(`{}`), true, nil
	}}}
	if err := st.markNoRestart("s_worker", noRestartStopped); err != nil {
		t.Fatal(err)
	}
	e := queuedStart{ID: "q_next", Owner: "s_owner", Project: "shop", Harness: "opencode", Task: "the task"}
	if d.queueNextModelStart(t.Context(), "s_worker", "fake/m1", e) {
		t.Fatal("the stopped worker's task moved to the next model")
	}
	if n, _ := st.queueLen(); n != 0 {
		t.Fatalf("%d starts queued", n)
	}
	if len(reasons) != 1 || !strings.Contains(reasons[0], "was stopped") {
		t.Fatalf("not-started reports: %q", reasons)
	}
}
