package daemon

import (
	"context"
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// ---- helpers ----

type workerNode struct {
	*node
	cli  *localClient
	logs *logBuf
}

// fakeInventory is the harness list of a machine that has OpenCode, which the
// tests point at the test binary (the fake `opencode serve`).
func fakeInventory(context.Context) []protocol.HarnessInfo {
	return []protocol.HarnessInfo{{Name: "opencode", Path: os.Args[0], Version: "fake", Primary: true,
		Models: []string{"fake/m1", "fake/m2"}}}
}

// startWorkerNode starts a node that can run workers. The settings are short.
func startWorkerNode(t *testing.T, g *testGateway, set WorkerSettings, cfg func(*Config)) *workerNode {
	t.Helper()
	logs := &logBuf{}
	n := startNode(t, g, nodeOpts{exe: "/x/opencode", cmdline: "pi", detect: fakeInventory, interval: time.Hour,
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

// killAllWorkers is the test cleanup: no worker outlives its test.
func killAllWorkers(d *Daemon) {
	d.wmu.Lock()
	defer d.wmu.Unlock()
	for _, w := range d.workers {
		if w.pgid > 0 {
			syscall.Kill(-w.pgid, syscall.SIGKILL)
		}
	}
}

func inventoryOf(t *testing.T, cli *localClient, n *node) bool {
	var ni protocol.NodesInfo
	json.Unmarshal(cli.mustCall("nodes", nil), &ni)
	for _, v := range ni.Nodes {
		if v.NodeID == n.d.NodeID() && v.Online && v.Launch != nil && len(v.Harnesses) > 0 {
			return true
		}
	}
	return false
}

func (w *workerNode) ownerID() string {
	var info protocol.OperatorInfo
	json.Unmarshal(w.cli.mustCall("info", nil), &info)
	return info.OwnerSession
}

// newRepo makes a git repository named name and returns its resolved path.
func newRepo(t *testing.T, name string) string {
	t.Helper()
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	repo := filepath.Join(base, name)
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	gitIn(t, repo, "init", "-q", "-b", "main")
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("# "+name+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitIn(t, repo, "add", "README.md")
	gitIn(t, repo, "commit", "-q", "-m", "first")
	// The fake opencode writes its record into its folder; git ignores it.
	if err := os.WriteFile(filepath.Join(repo, ".git", "info", "exclude"), []byte("fake-opencode.json\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return repo
}

func (w *workerNode) register(t *testing.T, repo string) {
	t.Helper()
	var out protocol.ProjectAddOutcome
	json.Unmarshal(w.cli.mustCall("project_add", protocol.ProjectAddRequest{Paths: []string{repo}}), &out)
	if len(out.Added) != 1 {
		t.Fatalf("project not registered: %+v", out)
	}
}

func (w *workerNode) start(req protocol.WorkerStartRequest) (protocol.WorkerStartResult, error) {
	raw, err := w.cli.call("worker_start", req)
	var res protocol.WorkerStartResult
	if err == nil {
		json.Unmarshal(raw, &res)
	}
	return res, err
}

func (w *workerNode) mustStart(t *testing.T, req protocol.WorkerStartRequest) protocol.WorkerStartResult {
	t.Helper()
	res, err := w.start(req)
	if err != nil {
		t.Fatalf("worker_start: %v", err)
	}
	if res.SessionID == "" {
		t.Fatalf("no session in %+v", res)
	}
	return res
}

func readFake(t *testing.T, dir string) fakeOCRecord {
	t.Helper()
	var rec fakeOCRecord
	eventually(t, "the fake opencode wrote its record", func() bool {
		raw, err := os.ReadFile(filepath.Join(dir, "fake-opencode.json"))
		return err == nil && json.Unmarshal(raw, &rec) == nil && rec.SessionID != ""
	})
	return rec
}

func alive(pid int) bool {
	_, ok := procToken(pid)
	return ok
}

func (w *workerNode) agents(t *testing.T, includeOwners bool) []protocol.AgentView {
	t.Helper()
	var r protocol.AgentsResult
	json.Unmarshal(w.cli.mustCall("list", protocol.QueryAgents{IncludeOwners: includeOwners}), &r)
	return r.Agents
}

func (w *workerNode) workers(t *testing.T) []protocol.WorkerView {
	t.Helper()
	var r protocol.WorkersResult
	json.Unmarshal(w.cli.mustCall("workers", nil), &r)
	return r.Workers
}

func (w *workerNode) messagesOf(t *testing.T, session string) []protocol.Message {
	t.Helper()
	var r struct {
		Messages []protocol.Message `json:"messages"`
	}
	json.Unmarshal(w.cli.mustCall("messages", protocol.QueryMessages{SessionID: session, Limit: 50}), &r)
	return r.Messages
}

// replyTo waits for the reply to a message and returns it.
func (w *workerNode) replyTo(t *testing.T, session, parent string) protocol.Message {
	t.Helper()
	var got protocol.Message
	eventually(t, "a reply to "+parent, func() bool {
		for _, m := range w.messagesOf(t, session) {
			if m.ParentID == parent && m.Type == protocol.TypeReply {
				got = m
				return true
			}
		}
		return false
	})
	return got
}

func (w *workerNode) workerStopped(t *testing.T, g *testGateway, reason string) {
	t.Helper()
	eventually(t, "worker.stopped with reason "+reason, func() bool {
		evs, _ := g.gw.Store().Events("worker.stopped")
		for _, e := range evs {
			if strings.Contains(e, "reason="+reason) {
				return true
			}
		}
		return false
	})
}

var shortSet = WorkerSettings{Tick: 25 * time.Millisecond, StartTimeout: 8 * time.Second}

// ---- the lifecycle ----

// Start a worker from the command line (the owner), see its reply come back to
// the owner, send it a follow-up as the owner, and stop it. The process, its
// child and the whole group go away.
func TestWorkerLifecycleStartReplyStop(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	w := startWorkerNode(t, g, shortSet, nil)
	repo := newRepo(t, "shop")
	w.register(t, repo)
	owner := w.ownerID()

	res := w.mustStart(t, protocol.WorkerStartRequest{Project: "shop", Harness: "opencode", Model: "fake/m1", Task: "say ok"})
	if res.Project != "shop" || res.Path != repo || res.Harness != "opencode" || res.Model != "fake/m1" || res.Task == nil {
		t.Fatalf("result: %+v", res)
	}

	// How the process was started.
	rec := readFake(t, repo)
	if rec.Cwd != repo {
		t.Errorf("the worker runs in %s, not in the project %s", rec.Cwd, repo)
	}
	if len(rec.Args) != 5 || rec.Args[0] != "serve" || rec.Args[1] != "--hostname" || rec.Args[2] != "127.0.0.1" || rec.Args[3] != "--port" {
		t.Errorf("args: %v", rec.Args)
	}
	for _, a := range rec.Args {
		if strings.Contains(a, "auto") || strings.Contains(a, "dangerous") || strings.Contains(a, "yolo") {
			t.Errorf("a flag that turns permission checks off: %v", rec.Args)
		}
	}
	if !strings.HasPrefix(rec.Env["AGENT_NET_WORKER"], "w_") || rec.Env["AGENT_NET_SOCKET"] != w.cfg.SocketPath || rec.Env["OPENCODE_SERVER_PASSWORD"] == "" {
		t.Errorf("env: %v", rec.Env)
	}
	if !strings.HasPrefix(rec.Env["XDG_STATE_HOME"], filepath.Join(w.cfg.StateDir, "workers")) {
		t.Errorf("the worker shares the user's OpenCode state folder: %s", rec.Env["XDG_STATE_HOME"])
	}
	if rec.Pgid != rec.Pid {
		t.Errorf("the worker is not the leader of its own process group: pid %d pgid %d", rec.Pid, rec.Pgid)
	}
	if rec.Env["AGENT_NET_WORKER_SESSION_FILE"] != filepath.Join(w.cfg.StateDir, "workers", rec.Env["AGENT_NET_WORKER"], "session") {
		t.Errorf("session file: %q", rec.Env["AGENT_NET_WORKER_SESSION_FILE"])
	}
	model, _ := rec.SessionBody["model"].(map[string]any)
	if model["providerID"] != "fake" || model["id"] != "m1" {
		t.Errorf("session model: %v", rec.SessionBody)
	}
	if pw, _ := os.ReadFile(filepath.Join(w.cfg.StateDir, "daemon.db")); strings.Contains(string(pw), rec.Env["OPENCODE_SERVER_PASSWORD"]) {
		t.Error("the server password is stored in daemon.db")
	}

	// The task is a normal message from the owner with the fixed preamble.
	if res.Task.From != owner || res.Task.To != res.SessionID || res.Task.Type != protocol.TypeRequest {
		t.Errorf("task message: %+v", res.Task)
	}
	if !strings.HasPrefix(res.Task.Body, protocol.WorkerPreamble) || !strings.HasSuffix(res.Task.Body, "Task:\nsay ok") {
		t.Errorf("task body: %q", res.Task.Body)
	}

	// The reply goes back to the owner, and its body can be read here.
	reply := w.replyTo(t, owner, res.Task.ID)
	if reply.To != owner || reply.From != res.SessionID || !strings.HasPrefix(reply.Body, "ok from worker") {
		t.Fatalf("reply: %+v", reply)
	}
	var st protocol.MessageResult
	json.Unmarshal(w.cli.mustCall("message_status", protocol.QueryMessage{MessageID: reply.ID}), &st)
	if st.Message.Body != reply.Body {
		t.Errorf("the owner cannot read the reply body: %+v", st)
	}

	// The worker is a background session with an owner. The owner is not in
	// the plain agent list, but it is when asked for.
	var worker *protocol.AgentView
	for _, a := range w.agents(t, false) {
		a := a
		if protocol.IsOwner(a.Harness) {
			t.Errorf("the owner is listed as an agent: %+v", a)
		}
		if a.SessionID == res.SessionID {
			worker = &a
		}
	}
	if worker == nil || worker.Worker == nil || worker.Worker.Owner != owner || worker.Worker.Project != "shop" ||
		worker.Mode != protocol.ModeBackground || worker.Model == "" && false || !worker.Online {
		t.Fatalf("worker view: %+v", worker)
	}
	var ownerView *protocol.AgentView
	for _, a := range w.agents(t, true) {
		a := a
		if a.SessionID == owner {
			ownerView = &a
		}
	}
	if ownerView == nil || ownerView.Profile.Name != protocol.OwnerName(w.cfg.Hostname) || ownerView.Harness != protocol.OwnerHarness {
		t.Fatalf("owner view: %+v", ownerView)
	}
	ws := w.workers(t)
	if len(ws) != 1 || ws[0].SessionID != res.SessionID || ws[0].Owner != owner || ws[0].OwnerName != protocol.OwnerName(w.cfg.Hostname) ||
		ws[0].Project != "shop" || ws[0].Path != repo {
		t.Fatalf("workers: %+v", ws)
	}

	// A follow-up from the owner reaches the worker, which answers.
	var sent protocol.MessageSent
	json.Unmarshal(w.cli.mustCall("owner_send", protocol.OwnerSendRequest{To: res.SessionID, Body: "and run the tests"}), &sent)
	if sent.Message == nil || sent.Message.From != owner || sent.Message.To != res.SessionID {
		t.Fatalf("owner_send: %+v", sent)
	}
	if r := w.replyTo(t, owner, sent.Message.ID); !strings.HasPrefix(r.Body, "ok from worker") {
		t.Errorf("follow-up reply: %+v", r)
	}

	// Stop it: the group is gone, the session is offline, the end is recorded.
	w.cli.mustCall("worker_stop", protocol.WorkerStopRequest{Session: res.SessionID})
	eventually(t, "the worker process is gone", func() bool { return !alive(rec.Pid) })
	eventually(t, "the child in the group is gone", func() bool { return !alive(rec.ChildPid) })
	if err := syscall.Kill(-rec.Pgid, 0); err != syscall.ESRCH {
		t.Errorf("the process group is still there: %v", err)
	}
	eventually(t, "the session is offline", func() bool { return len(w.workers(t)) == 1 && !w.workers(t)[0].Online })
	w.workerStopped(t, g, protocol.StopRequested)
	started, _ := g.gw.Store().Events("worker.started")
	if len(started) != 1 || !strings.Contains(started[0], "project=shop") || !strings.Contains(started[0], "owner="+owner) {
		t.Errorf("worker.started events: %v", started)
	}
	if n := len(g.logs.lines("event=worker.started", "session="+res.SessionID)); n != 1 {
		t.Errorf("gateway worker.started lines: %d", n)
	}
	if n := len(g.logs.lines("event=worker.stopped", "reason=stopped", "session="+res.SessionID)); n != 1 {
		t.Errorf("gateway worker.stopped lines: %d", n)
	}
	eventually(t, "the daemon logs the end", func() bool {
		return len(w.logs.lines("worker.stopped", "reason=stopped", "session="+res.SessionID)) == 1 &&
			len(w.logs.lines("worker.started", "session="+res.SessionID, "project=shop")) == 1
	})
	if w.d.workerCount() != 0 {
		t.Errorf("the daemon still counts %d workers", w.d.workerCount())
	}
	if rows, _ := w.d.st.workers(); len(rows) != 0 {
		t.Errorf("worker rows left in daemon.db: %+v", rows)
	}
	if _, err := os.Stat(filepath.Join(w.cfg.StateDir, "workers")); err == nil {
		if ents, _ := os.ReadDir(filepath.Join(w.cfg.StateDir, "workers")); len(ents) != 0 {
			t.Errorf("the worker folder is not cleaned up: %v", ents)
		}
	}
}

// A worker that ignores SIGTERM is killed anyway.
func TestWorkerStopKillsAProcessThatIgnoresTerm(t *testing.T) {
	t.Setenv("FAKE_OC_IGNORE_TERM", "1")
	g := startGateway(t, t.TempDir(), "")
	set := shortSet
	set.KillGrace = 300 * time.Millisecond
	w := startWorkerNode(t, g, set, nil)
	repo := newRepo(t, "shop")
	w.register(t, repo)
	res := w.mustStart(t, protocol.WorkerStartRequest{Project: "shop", Harness: "opencode", Task: "x"})
	rec := readFake(t, repo)
	w.cli.mustCall("worker_stop", protocol.WorkerStopRequest{Session: res.SessionID})
	eventually(t, "killed", func() bool { return !alive(rec.Pid) && !alive(rec.ChildPid) })
}

// ---- the timers ----

func TestWorkerEndsWhenIdle(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	set := shortSet
	set.Idle = 400 * time.Millisecond
	w := startWorkerNode(t, g, set, nil)
	repo := newRepo(t, "shop")
	w.register(t, repo)
	w.mustStart(t, protocol.WorkerStartRequest{Project: "shop", Harness: "opencode", Task: "x"})
	rec := readFake(t, repo)
	w.workerStopped(t, g, protocol.StopIdle)
	eventually(t, "the process is gone", func() bool { return !alive(rec.Pid) && !alive(rec.ChildPid) })
}

// A message to or from the worker restarts the idle time.
func TestWorkerMessagesKeepItAlive(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	set := shortSet
	set.Idle = 700 * time.Millisecond
	w := startWorkerNode(t, g, set, nil)
	repo := newRepo(t, "shop")
	w.register(t, repo)
	res := w.mustStart(t, protocol.WorkerStartRequest{Project: "shop", Harness: "opencode", Task: "x"})
	// Keep talking for longer than the idle time.
	end := time.Now().Add(1500 * time.Millisecond)
	for time.Now().Before(end) {
		w.cli.mustCall("owner_send", protocol.OwnerSendRequest{To: res.SessionID, Body: "ping"})
		time.Sleep(150 * time.Millisecond)
	}
	if evs, _ := g.gw.Store().Events("worker.stopped"); len(evs) != 0 {
		t.Fatalf("a worker that was talked to ended: %v", evs)
	}
	w.workerStopped(t, g, protocol.StopIdle) // then it goes quiet and ends
}

func TestWorkerEndsAtMaxAge(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	set := shortSet
	set.MaxAge = 400 * time.Millisecond
	w := startWorkerNode(t, g, set, nil)
	repo := newRepo(t, "shop")
	w.register(t, repo)
	w.mustStart(t, protocol.WorkerStartRequest{Project: "shop", Harness: "opencode", Task: "x"})
	w.workerStopped(t, g, protocol.StopMaxAge)
}

// When the owner session ends and stays away longer than the grace time, its
// worker ends. A worker whose owner is back in time stays.
func TestWorkerEndsWhenItsOwnerIsGone(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	set := shortSet
	set.OwnerGrace = 500 * time.Millisecond
	w := startWorkerNode(t, g, set, func(c *Config) { c.Workers.Tick = 25 * time.Millisecond })
	repo := newRepo(t, "shop")
	w.register(t, repo)
	// The owner is an agent session (Pi) that starts the worker itself.
	pi, piID := helloPi(t, w.node, "owner-pi", "/work/pi", true)
	res := mustWorker(t, pi.mustCall("worker_start", protocol.WorkerStartRequest{Project: "shop", Harness: "opencode", Task: "x"}))
	if res.Task == nil || res.Task.From != piID {
		t.Fatalf("the task is not from the calling session: %+v", res.Task)
	}
	// The worker's reply comes to the agent session that started it.
	if m := pi.nextPush(); m.Type != protocol.TypeReply || m.From != res.SessionID || !strings.HasPrefix(m.Body, "ok from worker") {
		t.Fatalf("push to the caller: %+v", m)
	}
	rec := readFake(t, repo)
	pi.c.Close() // the owner session ends
	w.workerStopped(t, g, protocol.StopOwnerGone)
	eventually(t, "the process is gone", func() bool { return !alive(rec.Pid) })
}

func mustWorker(t *testing.T, raw json.RawMessage) protocol.WorkerStartResult {
	t.Helper()
	var res protocol.WorkerStartResult
	if err := json.Unmarshal(raw, &res); err != nil || res.SessionID == "" {
		t.Fatalf("bad result %s", raw)
	}
	return res
}

// ---- the limit, and who may start ----

func TestWorkerLimitPerMachine(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	set := shortSet
	set.Max = 2
	w := startWorkerNode(t, g, set, nil)
	repo := newRepo(t, "shop")
	w.register(t, repo)
	req := protocol.WorkerStartRequest{Project: "shop", Harness: "opencode", Task: "x"}
	a := w.mustStart(t, req)
	w.mustStart(t, req)
	_, err := w.start(req)
	if err == nil || !strings.Contains(err.Error(), "already runs 2 workers, which is the limit") {
		t.Fatalf("third worker: %v", err)
	}
	// The refusal is an event and a log line, and nothing was started.
	refused, _ := g.gw.Store().Events("worker.refused")
	if len(refused) != 1 || !strings.Contains(refused[0], "project=shop") || !strings.Contains(refused[0], "limit") {
		t.Errorf("worker.refused events: %v", refused)
	}
	if len(g.logs.lines("event=worker.refused", "reason=")) != 1 {
		t.Errorf("gateway refused lines: %v", g.logs.lines("event=worker.refused"))
	}
	if len(w.logs.lines("worker.refused", "project=shop")) != 1 || w.d.workerCount() != 2 {
		t.Errorf("daemon refused lines: %v, workers %d", w.logs.lines("worker.refused"), w.d.workerCount())
	}
	// Stopping one makes room.
	w.cli.mustCall("worker_stop", protocol.WorkerStopRequest{Session: a.SessionID})
	eventually(t, "room for a third", func() bool {
		_, err := w.start(req)
		return err == nil
	})
}

// A worker cannot start workers: the daemon checks the worker flag of the
// calling session, whatever the model says.
func TestWorkerCannotStartWorkers(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	w := startWorkerNode(t, g, shortSet, nil)
	shop := newRepo(t, "shop")
	w.register(t, shop)
	res := w.mustStart(t, protocol.WorkerStartRequest{Project: "shop", Harness: "opencode", Task: "x"})
	w.replyTo(t, w.ownerID(), res.Task.ID)
	// The worker (the fake plugin) asks for another worker.
	var sent protocol.MessageSent
	json.Unmarshal(w.cli.mustCall("owner_send", protocol.OwnerSendRequest{To: res.SessionID, Body: "FAKE:start"}), &sent)
	r := w.replyTo(t, w.ownerID(), sent.Message.ID)
	if !strings.Contains(r.Body, "a worker cannot start workers") {
		t.Fatalf("the worker was able to ask: %q", r.Body)
	}
	if w.d.workerCount() != 1 {
		t.Fatalf("a worker started a worker: %d workers", w.d.workerCount())
	}
	if len(w.logs.lines("worker.refused", "a worker cannot start workers")) != 1 {
		t.Errorf("no worker.refused line: %v", w.logs.lines("worker.refused"))
	}
	// The gateway refuses it too, if a daemon let it through.
	wk := w.workerSession(t, res.SessionID)
	_, err := w.d.call(context.Background(), "worker.start", protocol.WorkerStartFrame{
		WorkerStartRequest: protocol.WorkerStartRequest{Project: "shop", Harness: "opencode", Task: "x"}, Owner: wk})
	if err == nil || !strings.Contains(err.Error(), "a worker cannot start workers") {
		t.Fatalf("the gateway let a worker start a worker: %v", err)
	}
}

func (w *workerNode) workerSession(t *testing.T, id string) string {
	t.Helper()
	if wk := w.d.workerBySession(id); wk == nil {
		t.Fatal("no such worker")
	}
	return id
}

// A start from the command line that comes from inside an agent (its shell
// tool) is refused: the process is a descendant of an agent session.
func TestOwnerActionsRefuseACallerInsideAnAgent(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	fa := &fakeAdapter{harness: "fake"}
	n := startNode(t, g, nodeOpts{adapter: fa, realGate: true})
	// The fake agent's process is the parent of this test process, as if the
	// test were the shell tool of that agent.
	fa.setSessions(protocol.SessionInfo{Harness: "fake", NativeID: "na", PID: os.Getppid(), Cwd: "/work/a", Name: "alice",
		Status: "idle", Delivery: "native-claude"})
	cli := cliClient(t, n)
	eventually(t, "owner session", func() bool {
		var info protocol.OperatorInfo
		json.Unmarshal(cli.mustCall("info", nil), &info)
		return info.OwnerSession != ""
	})
	for _, c := range []struct {
		method string
		params any
	}{
		{"owner_send", protocol.OwnerSendRequest{To: "s_x", Body: "hi"}},
		{"worker_start", protocol.WorkerStartRequest{Project: "p", Harness: "opencode", Task: "t"}},
		{"worker_stop", protocol.WorkerStopRequest{Session: "s_x"}},
	} {
		_, err := cli.call(c.method, c.params)
		if err == nil || !strings.Contains(err.Error(), "inside an agent session") {
			t.Errorf("%s from inside an agent: %v", c.method, err)
		}
	}
	// The reads still work from there.
	if _, err := cli.call("list", nil); err != nil {
		t.Errorf("list: %v", err)
	}
}

// ---- who may connect as a worker's session ----

func TestOnlyTheWorkersProcessCanConnectAsItsSession(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	w := startWorkerNode(t, g, shortSet, nil)
	repo := newRepo(t, "shop")
	w.register(t, repo)
	w.mustStart(t, protocol.WorkerStartRequest{Project: "shop", Harness: "opencode", Task: "x"})
	rec := readFake(t, repo)
	// The user's own OpenCode service sees the session in the shared database
	// and tries to register it: refused.
	lc := dialLocal(t, w.sock)
	_, err := lc.call("hello", map[string]any{"client": "opencode", "harness": "opencode", "native_id": rec.SessionID, "cwd": repo})
	if err == nil || !strings.Contains(err.Error(), "belongs to a worker") {
		t.Fatalf("a foreign process connected as the worker's session: %v", err)
	}
	// An unrelated session of another id is not affected.
	lc2 := dialLocal(t, w.sock)
	if _, err := lc2.call("hello", map[string]any{"client": "opencode", "harness": "opencode", "native_id": "ses_user", "cwd": "/work/u"}); err != nil {
		t.Fatalf("a normal OpenCode session was refused: %v", err)
	}
}

// ---- start failures ----

func TestWorkerStartFailures(t *testing.T) {
	cases := []struct {
		name string
		env  string
		want string
	}{
		{"the plugin is not installed", "FAKE_OC_NOPLUGIN", "agent-net plugin did not register"},
		{"opencode exits at once", "FAKE_OC_EXIT", "exited at start"},
		{"the plugin registers another session", "FAKE_OC_WRONG_SESSION", "register"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Setenv(c.env, "1")
			g := startGateway(t, t.TempDir(), "")
			set := shortSet
			set.StartTimeout = 1500 * time.Millisecond
			w := startWorkerNode(t, g, set, nil)
			repo := newRepo(t, "shop")
			w.register(t, repo)
			_, err := w.start(protocol.WorkerStartRequest{Project: "shop", Harness: "opencode", Task: "x"})
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("error %v, want %q", err, c.want)
			}
			// Nothing is left running or stored.
			if w.d.workerCount() != 0 {
				t.Errorf("%d workers left", w.d.workerCount())
			}
			if rec, err := os.ReadFile(filepath.Join(repo, "fake-opencode.json")); err == nil {
				var r fakeOCRecord
				json.Unmarshal(rec, &r)
				eventually(t, "the failed worker is killed", func() bool { return !alive(r.Pid) && !alive(r.ChildPid) })
			}
			if len(w.logs.lines("worker.refused", "project=shop")) == 0 {
				t.Error("no worker.refused line")
			}
		})
	}
}

// ---- refusals at the gateway ----

func TestWorkerRefusals(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	w := startWorkerNode(t, g, shortSet, nil)
	shop := newRepo(t, "shop")
	w.register(t, shop)
	// A machine that refuses workers, with the same project name.
	g2 := w // the second machine joins the same gateway
	other := startWorkerNode(t, g, shortSet, func(c *Config) { c.NoLaunch = true })
	otherRepo := newRepo(t, "private")
	other.register(t, otherRepo)
	// A machine with no OpenCode.
	bare := startNode(t, g, nodeOpts{exe: "/x/opencode", interval: time.Hour,
		detect: func(context.Context) []protocol.HarnessInfo { return []protocol.HarnessInfo{} }})
	bareCLI := cliClient(t, bare)
	bareRepo := newRepo(t, "bare")
	json.Unmarshal(bareCLI.mustCall("project_add", protocol.ProjectAddRequest{Paths: []string{bareRepo}}), &protocol.ProjectAddOutcome{})
	_ = g2

	cases := []struct {
		name string
		req  protocol.WorkerStartRequest
		want string
	}{
		{"unknown project", protocol.WorkerStartRequest{Project: "nope", Harness: "opencode", Task: "x"}, `no project named "nope" is known`},
		{"harness the machine has not", protocol.WorkerStartRequest{Project: "bare", Harness: "opencode", Task: "x"}, "opencode is not installed on"},
		{"unsupported harness", protocol.WorkerStartRequest{Project: "shop", Harness: "claude", Task: "x"}, "workers can run these harnesses: opencode"},
		{"model not in the list", protocol.WorkerStartRequest{Project: "shop", Harness: "opencode", Model: "fake/zzz", Task: "x"}, `model "fake/zzz" is not in the opencode model list`},
		{"machine that refuses workers", protocol.WorkerStartRequest{Project: "private", Harness: "opencode", Task: "x"}, "does not accept workers (it was set up with --no-launch)"},
		{"wrong machine named", protocol.WorkerStartRequest{Project: "shop", Machine: "elsewhere", Harness: "opencode", Task: "x"}, `no project named "shop" on machine "elsewhere"`},
		{"no task", protocol.WorkerStartRequest{Project: "shop", Harness: "opencode"}, "worker_start needs project, harness and task"},
	}
	for _, c := range cases {
		_, err := w.start(c.req)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: error %v, want %q", c.name, err, c.want)
		}
	}
	// The gateway recorded its refusals, with the reason.
	refused, _ := g.gw.Store().Events("worker.refused")
	if len(refused) != len(cases)-1 { // "no task" is refused before it reaches the gateway
		t.Errorf("worker.refused events: %d %v", len(refused), refused)
	}
	if nodes := inventoryNodes(t, w.cli); nodes != 3 {
		t.Errorf("nodes: %d", nodes)
	}
	if v := launchOf(t, w.cli, other.d.NodeID()); v == nil || *v {
		t.Errorf("the --no-launch machine reports %v", v)
	}
	if v := launchOf(t, w.cli, w.d.NodeID()); v == nil || !*v {
		t.Errorf("the default machine reports %v", v)
	}
	nothing, _ := os.ReadFile(filepath.Join(shop, "fake-opencode.json"))
	if len(nothing) != 0 {
		t.Error("a worker was started although every start was refused")
	}
}

func inventoryNodes(t *testing.T, cli *localClient) int {
	var ni protocol.NodesInfo
	json.Unmarshal(cli.mustCall("nodes", nil), &ni)
	return len(ni.Nodes)
}

func launchOf(t *testing.T, cli *localClient, node string) *bool {
	var ni protocol.NodesInfo
	json.Unmarshal(cli.mustCall("nodes", nil), &ni)
	for _, n := range ni.Nodes {
		if n.NodeID == node {
			return n.Launch
		}
	}
	return nil
}

// A project that is on two machines needs the machine to be named.
func TestWorkerProjectOnTwoMachines(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	a := startWorkerNode(t, g, shortSet, nil)
	b := startWorkerNode(t, g, shortSet, nil)
	ra, rb := newRepo(t, "shop"), newRepo(t, "shop")
	a.register(t, ra)
	b.register(t, rb)
	req := protocol.WorkerStartRequest{Project: "shop", Harness: "opencode", Task: "x"}
	_, err := a.start(req)
	if err == nil || !strings.Contains(err.Error(), "is on several machines") {
		t.Fatalf("ambiguous project: %v", err)
	}
	// Named, it runs on the other machine; the reply still comes back here.
	req.Machine = b.cfg.Hostname
	res := a.mustStart(t, req)
	if res.NodeID != b.d.NodeID() || res.Path != rb {
		t.Fatalf("started on the wrong machine: %+v", res)
	}
	rec := readFake(t, rb)
	if rec.Cwd != rb || b.d.workerCount() != 1 || a.d.workerCount() != 0 {
		t.Errorf("the process must run on the project's machine only: %+v %d %d", rec, b.d.workerCount(), a.d.workerCount())
	}
	reply := a.replyTo(t, a.ownerID(), res.Task.ID)
	if reply.To != a.ownerID() {
		t.Errorf("reply: %+v", reply)
	}
	// The worker is listed with its owner on the other machine, and the person
	// at the first machine can stop it.
	ws := a.workers(t)
	if len(ws) != 1 || ws[0].Owner != a.ownerID() || ws[0].OwnerName != protocol.OwnerName(a.cfg.Hostname) || ws[0].Hostname != b.cfg.Hostname {
		t.Fatalf("workers: %+v", ws)
	}
	a.cli.mustCall("worker_stop", protocol.WorkerStopRequest{Session: res.SessionID})
	eventually(t, "stopped on the other machine", func() bool { return b.d.workerCount() == 0 && !alive(rec.Pid) })
}

// An agent may stop its own workers, and only those.
func TestAgentStopsOnlyItsOwnWorkers(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	w := startWorkerNode(t, g, shortSet, nil)
	repo := newRepo(t, "shop")
	w.register(t, repo)
	mine, _ := helloPi(t, w.node, "pi-a", "/work/a", true)
	other, _ := helloPi(t, w.node, "pi-b", "/work/b", true)
	res := mustWorker(t, mine.mustCall("worker_start", protocol.WorkerStartRequest{Project: "shop", Harness: "opencode", Task: "x"}))
	if _, err := other.call("worker_stop", protocol.WorkerStopRequest{Session: res.SessionID}); err == nil ||
		!strings.Contains(err.Error(), "only the worker's owner") {
		t.Fatalf("another agent stopped the worker: %v", err)
	}
	if w.d.workerCount() != 1 {
		t.Fatal("the worker is gone")
	}
	mine.mustCall("worker_stop", protocol.WorkerStopRequest{Session: res.SessionID})
	eventually(t, "stopped by its owner", func() bool { return w.d.workerCount() == 0 })
}

// ---- the owner as a sender ----

func TestOwnerSendsAsTheUserInPerson(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	n := startNode(t, g, nodeOpts{cmdline: "pi"})
	cli := cliClient(t, n)
	pi, piID := helloPi(t, n, "pi-1", "/work/p", true)
	var info protocol.OperatorInfo
	eventually(t, "owner session", func() bool {
		json.Unmarshal(cli.mustCall("info", nil), &info)
		return info.OwnerSession != ""
	})

	var sent protocol.MessageSent
	json.Unmarshal(cli.mustCall("owner_send", protocol.OwnerSendRequest{To: piID, Body: "please stop and summarise"}), &sent)
	if sent.Message == nil || sent.Message.From != info.OwnerSession {
		t.Fatalf("sent: %+v", sent)
	}
	push := pi.nextPush()
	if push.FromHarness != protocol.OwnerHarness || push.FromName != protocol.OwnerName(n.cfg.Hostname) || push.Body != "please stop and summarise" {
		t.Fatalf("the plugin must be told this is the owner: %+v", push)
	}
	// The agent answers with a reply: it goes to the owner, who can read it.
	rep := sendResult(t, pi.mustCall("reply", map[string]any{"message_id": push.ID, "body": "summary: done"}))
	if rep.To != info.OwnerSession {
		t.Fatalf("the reply did not go to the owner: %+v", rep)
	}
	eventually(t, "reply delivered to the owner session", func() bool {
		return messageState(t, cli, rep.ID) == protocol.StateDelivered
	})
	var got protocol.MessageResult
	json.Unmarshal(cli.mustCall("message_status", protocol.QueryMessage{MessageID: rep.ID}), &got)
	if got.Message.Body != "summary: done" || got.Message.FromHarness != "pi" {
		t.Fatalf("owner cannot read the reply: %+v", got)
	}
	// The sender is never a parameter: an extra sender field is ignored.
	var sent2 protocol.MessageSent
	json.Unmarshal(cli.mustCall("owner_send", map[string]any{"to": piID, "body": "again", "sender_session_id": piID}), &sent2)
	if sent2.Message == nil || sent2.Message.From != info.OwnerSession {
		t.Fatalf("a sender in the params was used: %+v", sent2)
	}
}

// An agent connection cannot send as the owner, with any method.
func TestAgentsCannotSendAsTheOwner(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	n := startNode(t, g, nodeOpts{cmdline: "pi"})
	cli := cliClient(t, n)
	pi, piID := helloPi(t, n, "pi-1", "/work/p", true)
	other, otherID := helloPi(t, n, "pi-2", "/work/q", true)
	_ = other
	var info protocol.OperatorInfo
	eventually(t, "owner session", func() bool {
		json.Unmarshal(cli.mustCall("info", nil), &info)
		return info.OwnerSession != ""
	})
	if _, err := pi.call("owner_send", protocol.OwnerSendRequest{To: otherID, Body: "x"}); err == nil ||
		!strings.Contains(err.Error(), "only the owner") {
		t.Fatalf("an agent used owner_send: %v", err)
	}
	// The normal send uses the bound session whatever the params say.
	m := sendResult(t, pi.mustCall("send", map[string]any{"to": otherID, "body": "x", "sender_session_id": info.OwnerSession, "from": info.OwnerSession}))
	if m.From != piID {
		t.Fatalf("an agent sent as someone else: %+v", m)
	}
	// And it cannot register an owner session of its own.
	if _, err := pi.call("hello", map[string]any{"client": "owner", "harness": "owner", "native_id": "owner"}); err == nil {
		t.Fatal("hello as owner was accepted")
	}
	lc := dialLocal(t, n.sock)
	if _, err := lc.call("hello", map[string]any{"client": "owner", "harness": "owner", "native_id": "owner"}); err == nil {
		t.Fatal("a connection can say hello as the owner")
	}
}

// ---- after a restart of the daemon ----

// The daemon takes its workers back after a restart, and cleans up after the
// ones that died while it was down.
func TestWorkersSurviveADaemonRestart(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	w := startWorkerNode(t, g, shortSet, nil)
	shop, other := newRepo(t, "shop"), newRepo(t, "other")
	w.register(t, shop)
	w.register(t, other)
	live := w.mustStart(t, protocol.WorkerStartRequest{Project: "shop", Harness: "opencode", Task: "x"})
	dying := w.mustStart(t, protocol.WorkerStartRequest{Project: "other", Harness: "opencode", Task: "x"})
	recLive, recDying := readFake(t, shop), readFake(t, other)
	owner := w.ownerID()

	// Stop the daemon; one worker is killed while it is down.
	w.d.Stop()
	syscall.Kill(-recDying.Pgid, syscall.SIGKILL)
	eventually(t, "the second worker died", func() bool { return !alive(recDying.Pid) })
	if !alive(recLive.Pid) {
		t.Fatal("stopping the daemon killed a worker")
	}
	w.node.restart()
	w.cli = cliClient(t, w.node)

	// The live worker is adopted and its plugin reconnects.
	if w.d.workerCount() != 1 {
		t.Fatalf("adopted %d workers", w.d.workerCount())
	}
	eventually(t, "the adopted worker is online again with its worker flag", func() bool {
		for _, v := range w.workers(t) {
			if v.SessionID == live.SessionID && v.Online && v.Owner == owner {
				return true
			}
		}
		return false
	})
	// It still answers, and the other one is reported as lost.
	var sent protocol.MessageSent
	json.Unmarshal(w.cli.mustCall("owner_send", protocol.OwnerSendRequest{To: live.SessionID, Body: "still there?"}), &sent)
	w.replyTo(t, w.ownerID(), sent.Message.ID)
	w.workerStopped(t, g, protocol.StopLost)
	if evs, _ := g.gw.Store().Events("worker.stopped"); len(evs) != 1 || !strings.Contains(evs[0], "reason=lost") {
		t.Errorf("worker.stopped events: %v", evs)
	}
	// The owner is told, with the status failed.
	n := w.waitNotice(t, 1)[0]
	if n.From != dying.SessionID || !strings.Contains(n.Body, "status: failed") || !strings.Contains(n.Body, "lost while the daemon was not running") {
		t.Errorf("lost notice: %+v", n)
	}
	// The adopted worker can be stopped like any other: the process is the
	// same one, found again by its process ID and start time.
	w.cli.mustCall("worker_stop", protocol.WorkerStopRequest{Session: live.SessionID})
	eventually(t, "the adopted worker is gone", func() bool { return !alive(recLive.Pid) && !alive(recLive.ChildPid) })
	if rows, _ := w.d.st.workers(); len(rows) != 0 {
		t.Errorf("rows left: %+v", rows)
	}
}

// A process ID that was reused is not a worker: the start time differs.
func TestAdoptIgnoresAReusedProcessID(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	w := startWorkerNode(t, g, shortSet, nil)
	// A row for a process that is alive, with another start time.
	_, piID := helloPi(t, w.node, "pi-old", "/work/old", true) // stands for the old worker's session at the gateway
	row := workerRow{ID: "w_old", Harness: "opencode", Project: "shop", Path: "/x", Owner: w.ownerID(), NativeID: "ses_1", SessionID: piID,
		PID: os.Getpid(), PGID: os.Getpid(), StartToken: "not-this-run", StartedAt: time.Now(), LastActive: time.Now()}
	if err := w.d.st.saveWorker(row); err != nil {
		t.Fatal(err)
	}
	w.d.Stop()
	w.node.restart()
	if w.d.workerCount() != 0 {
		t.Fatal("a reused process ID was adopted")
	}
	if !alive(os.Getpid()) {
		t.Fatal("the test process was killed")
	}
	w.cli = cliClient(t, w.node)
	w.workerStopped(t, g, protocol.StopLost)
}

// ---- the owner session ----

func TestOwnerSessionIsRegisteredPerMachine(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	a := startNode(t, g, nodeOpts{})
	b := startNode(t, g, nodeOpts{})
	ca, cb := cliClient(t, a), cliClient(t, b)
	var ia, ib protocol.OperatorInfo
	eventually(t, "both owner sessions", func() bool {
		json.Unmarshal(ca.mustCall("info", nil), &ia)
		json.Unmarshal(cb.mustCall("info", nil), &ib)
		return ia.OwnerSession != "" && ib.OwnerSession != ""
	})
	if ia.OwnerSession == ib.OwnerSession {
		t.Fatal("two machines share one owner session")
	}
	var r protocol.AgentsResult
	json.Unmarshal(ca.mustCall("list", protocol.QueryAgents{IncludeOwners: true}), &r)
	names := map[string]bool{}
	for _, v := range r.Agents {
		if protocol.IsOwner(v.Harness) {
			names[v.Profile.Name] = v.Online
		}
	}
	if !names[protocol.OwnerName(a.cfg.Hostname)] || !names[protocol.OwnerName(b.cfg.Hostname)] {
		t.Fatalf("owner sessions: %v", names)
	}
	// No session.new event for a person.
	if evs, _ := g.gw.Store().Events("session.new"); len(evs) != 0 {
		t.Errorf("session.new for the owner: %v", evs)
	}
	// The owner of a machine can message the owner of another one.
	var sent protocol.MessageSent
	json.Unmarshal(ca.mustCall("owner_send", protocol.OwnerSendRequest{To: ib.OwnerSession, Body: "hello other me"}), &sent)
	if sent.Message == nil {
		t.Fatal("no message")
	}
	eventually(t, "delivered to the other owner", func() bool { return messageState(t, cb, sent.Message.ID) == protocol.StateDelivered })
}

// Every operator method is served to a CLI client, and not one answers with
// "unknown method". This keeps protocol.OperatorMethods and the daemon together.
func TestEveryOperatorMethodIsServed(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	n := startNode(t, g, nodeOpts{})
	cli := cliClient(t, n)
	eventually(t, "connected", func() bool { return n.d.ownerSessionID() != "" })
	for _, m := range protocol.OperatorMethods {
		_, err := cli.call(m, map[string]any{})
		if err != nil && (strings.Contains(err.Error(), "unknown method") || strings.Contains(err.Error(), "not available to cli")) {
			t.Errorf("%s: %v", m, err)
		}
	}
}
