package daemon

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// Workers for Claude Code, Pi and Codex (decision 0008). Each runs against a
// fake of its harness (fakeharnesses_test.go).

func noFlagThatSkipsPermissions(t *testing.T, args []string) {
	t.Helper()
	for _, a := range args {
		l := strings.ToLower(a)
		if strings.Contains(l, "skip-permissions") || strings.Contains(l, "bypass") || strings.Contains(l, "dangerous") ||
			strings.Contains(l, "yolo") || strings.Contains(l, "approval-policy") {
			t.Errorf("a flag that turns permission checks off: %v", args)
		}
	}
}

// ---- Claude Code ----

type claudeNode struct {
	*workerNode
	sessions, stopLog string
}

func startClaudeNode(t *testing.T, g *testGateway, set WorkerSettings) *claudeNode {
	t.Helper()
	sessions, stopLog := useFakeClaude(t)
	w := startWorkerNode(t, g, set, func(c *Config) {
		c.ClaudeSessionsDir = sessions
		c.Adapters = append(c.Adapters, &fsClaudeAdapter{dir: sessions})
		c.HarnessPaths = map[string]string{"claude": os.Args[0]}
	})
	return &claudeNode{workerNode: w, sessions: sessions, stopLog: stopLog}
}

func (c *claudeNode) stops() []string {
	raw, _ := os.ReadFile(c.stopLog)
	return strings.Fields(strings.ReplaceAll(string(raw), "stop ", ""))
}

func readClaude(t *testing.T, dir string) fakeClaudeRecord {
	t.Helper()
	var rec fakeClaudeRecord
	readRecord(t, dir, "fake-claude.json", &rec)
	return rec
}

func (w *workerNode) agentOf(t *testing.T, session string) *protocol.AgentView {
	t.Helper()
	for _, a := range w.agents(t, true) {
		a := a
		if a.SessionID == session {
			return &a
		}
	}
	return nil
}

// A Claude worker is a background session: `claude --bg` with the model, the
// settings of the worker, and the project folder. The session is found from its
// session file, flagged as a worker, told the task as a normal message, hands
// back through the MCP server, and ends with `claude stop`.
func TestClaudeWorkerLifecycle(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	c := startClaudeNode(t, g, shortSet)
	repo := newRepo(t, "shop")
	c.register(t, repo)
	owner := c.ownerID()

	res := c.mustStart(t, protocol.WorkerStartRequest{Project: "shop", Harness: "claude", Model: "haiku", Task: "FAKE:handback say ok"})
	rec := readClaude(t, repo)
	if rec.Cwd != repo {
		t.Errorf("the worker runs in %s, not in the project %s", rec.Cwd, repo)
	}
	if rec.Args[0] != "--bg" || argValue(rec.Args, "--model") != "haiku" || !strings.HasPrefix(argValue(rec.Args, "--name"), "worker-shop-") {
		t.Errorf("args: %v", rec.Args)
	}
	noFlagThatSkipsPermissions(t, rec.Args)
	if rec.Settings["crossSessionInbound"] != "accept" {
		t.Errorf("settings: %v", rec.Settings)
	}
	env, _ := rec.Settings["env"].(map[string]any)
	if env["AGENT_NET_SOCKET"] != c.cfg.SocketPath || !strings.HasPrefix(rec.Env["AGENT_NET_WORKER"], "w_") {
		t.Errorf("settings env %v, session env %v", env, rec.Env)
	}
	// The MCP server of the session is this daemon's binary, whatever the user
	// has installed.
	var mcpCfg struct {
		Servers map[string]struct {
			Command string   `json:"command"`
			Args    []string `json:"args"`
		} `json:"mcpServers"`
	}
	json.Unmarshal([]byte(argValue(rec.Args, "--mcp-config")), &mcpCfg)
	if srv := mcpCfg.Servers["agent-net"]; srv.Command != c.d.cfg.Exe || len(srv.Args) != 1 || srv.Args[0] != "mcp" || c.d.cfg.Exe == "" {
		t.Errorf("--mcp-config: %v", argValue(rec.Args, "--mcp-config"))
	}
	if rec.Pgid != rec.Pid {
		t.Errorf("the session is not the leader of its group: %d %d", rec.Pid, rec.Pgid)
	}

	// The worker is the process of the session file, bound to its session.
	rows, _ := c.d.st.workers()
	if len(rows) != 1 || rows[0].PID != rec.Pid || rows[0].PGID != rec.Pgid || rows[0].Ref != rec.ID || rows[0].NativeID != rec.Session {
		t.Fatalf("worker rows: %+v (session %+v)", rows, rec)
	}
	a := c.agentOf(t, res.SessionID)
	if a == nil || a.Worker == nil || a.Worker.Owner != owner || a.Mode != protocol.ModeBackground || a.Harness != "claude" {
		t.Fatalf("worker view: %+v", a)
	}
	if !strings.HasPrefix(res.Task.Body, protocol.WorkerPreamble) || !strings.Contains(res.Task.Body, "select:mcp__agent-net__agent_handback") {
		t.Errorf("task body: %q", res.Task.Body)
	}

	// The task reached the session; its MCP server handed back.
	n := c.waitNotice(t, 1)[0]
	if n.From != res.SessionID || n.To != owner || n.ParentID != res.Task.ID {
		t.Fatalf("notice: %+v", n)
	}
	for _, want := range []string{"status: done", "(claude, haiku)", protocol.ReportMarker, "finished the task in " + repo} {
		if !strings.Contains(n.Body, want) {
			t.Errorf("notice lacks %q:\n%s", want, n.Body)
		}
	}
	eventually(t, "the handback line", func() bool { return len(c.logs.lines("worker.handback", "status=done")) == 1 })

	// A worker that hands back ends. The follow-up from its owner starts the
	// same Claude session again with --resume, and it hands back again.
	eventually(t, "the worker process is gone after its hand-back", func() bool {
		return !alive(rec.Pid) && !alive(rec.ChildPid)
	})
	var sent protocol.MessageSent
	mustJSON(t, c.cli.mustCall("owner_send", protocol.OwnerSendRequest{To: res.SessionID, Body: "FAKE:handback and again"}), &sent)
	c.waitNotice(t, 2)
	var rec2 fakeClaudeRecord
	eventually(t, "the resumed Claude session", func() bool {
		raw, err := os.ReadFile(filepath.Join(repo, "fake-claude.json"))
		rec2 = fakeClaudeRecord{}
		return err == nil && json.Unmarshal(raw, &rec2) == nil && rec2.Pid != rec.Pid && rec2.Pid != 0
	})
	if rec2.Session != rec.Session || argValue(rec2.Args, "--resume") != rec.Session {
		t.Errorf("the follow-up did not resume session %s: %+v", rec.Session, rec2)
	}
	eventually(t, "the resumed worker ends too", func() bool { return !alive(rec2.Pid) && !alive(rec2.ChildPid) })
	// `claude stop` ran for each run, and the ended worker keeps only its
	// resumable record.
	if got := c.stops(); len(got) != 2 || got[0] != rec.ID || got[1] != rec2.ID {
		t.Errorf("claude stop calls: %v, want [%s %s]", got, rec.ID, rec2.ID)
	}
	c.workerStopped(t, g, protocol.StopHandback)
	eventually(t, "only the resumable record is left", func() bool {
		rows, _ := c.d.st.workers()
		return len(rows) == 1 && rows[0].Resumable
	})
}

// If `claude stop` does nothing, and the process ignores SIGTERM, the group is
// still killed.
func TestClaudeWorkerStopFallsBackToKillingTheGroup(t *testing.T) {
	t.Setenv(envFakeStopIgnored, "1")
	t.Setenv(envFakeIgnoreTerm, "1")
	g := startGateway(t, t.TempDir(), "")
	set := shortSet
	set.KillGrace = 200 * time.Millisecond
	c := startClaudeNode(t, g, set)
	repo := newRepo(t, "shop")
	c.register(t, repo)
	res := c.mustStart(t, protocol.WorkerStartRequest{Project: "shop", Harness: "claude", Task: "x"})
	rec := readClaude(t, repo)
	c.cli.mustCall("worker_stop", protocol.WorkerStopRequest{Session: res.SessionID})
	eventually(t, "the session is killed", func() bool { return !alive(rec.Pid) && !alive(rec.ChildPid) })
	if len(c.stops()) != 1 {
		t.Errorf("claude stop was not tried: %v", c.stops())
	}
	if err := syscall.Kill(-rec.Pgid, 0); err != syscall.ESRCH {
		t.Errorf("the group is still there: %v", err)
	}
}

// A hand-back with status blocked arrives as blocked. A worktree is the working
// folder of the session.
func TestClaudeWorkerBlockedAndWorktree(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	c := startClaudeNode(t, g, shortSet)
	repo := newRepo(t, "shop")
	c.register(t, repo)
	res := c.mustStart(t, protocol.WorkerStartRequest{Project: "shop", Harness: "claude", Isolation: protocol.IsolationWorktree,
		Task: "FAKE:edit FAKE:handback FAKE:blocked"})
	if res.Worktree == "" {
		t.Fatal("no worktree")
	}
	rec := readClaude(t, res.Worktree)
	if rec.Cwd != res.Worktree {
		t.Errorf("the session runs in %s, not in the worktree %s", rec.Cwd, res.Worktree)
	}
	n := c.waitNotice(t, 1)[0]
	for _, want := range []string{"status: blocked", "work.txt | 2 ++", "worktree: " + res.Worktree} {
		if !strings.Contains(n.Body, want) {
			t.Errorf("notice lacks %q:\n%s", want, n.Body)
		}
	}
}

// A worker that Claude does not start (no trust, not installed) is refused with
// the words of the harness, and leaves nothing.
func TestClaudeWorkerStartFails(t *testing.T) {
	t.Setenv(envFakeStartFails, "1")
	g := startGateway(t, t.TempDir(), "")
	c := startClaudeNode(t, g, shortSet)
	repo := newRepo(t, "shop")
	c.register(t, repo)
	_, err := c.start(protocol.WorkerStartRequest{Project: "shop", Harness: "claude", Task: "x"})
	if err == nil || !strings.Contains(err.Error(), "Workspace not trusted") {
		t.Fatalf("error: %v", err)
	}
	if c.d.workerCount() != 0 {
		t.Errorf("%d workers left", c.d.workerCount())
	}
}

// The daemon takes a Claude worker back after a restart: the session is the
// same process, the adapter finds it again, and it is still flagged. It is
// ended by the id kept in the database when it hands back.
func TestClaudeWorkerSurvivesADaemonRestart(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	c := startClaudeNode(t, g, shortSet)
	repo := newRepo(t, "shop")
	c.register(t, repo)
	res := c.mustStart(t, protocol.WorkerStartRequest{Project: "shop", Harness: "claude", Task: "FAKE:ignore"})
	rec := readClaude(t, repo)
	owner := c.ownerID()

	c.d.Stop()
	if !alive(rec.Pid) {
		t.Fatal("stopping the daemon killed the Claude session")
	}
	c.node.restart()
	c.cli = cliClient(t, c.node)
	if c.d.workerCount() != 1 {
		t.Fatalf("adopted %d workers", c.d.workerCount())
	}
	eventually(t, "the adopted worker is online and flagged", func() bool {
		a := c.agentOf(t, res.SessionID)
		return a != nil && a.Online && a.Worker != nil && a.Worker.Owner == owner
	})
	var sent protocol.MessageSent
	mustJSON(t, c.cli.mustCall("owner_send", protocol.OwnerSendRequest{To: res.SessionID, Body: "FAKE:handback still there?"}), &sent)
	c.waitNotice(t, 1)

	// Its hand-back ends it with `claude stop`, using the id kept in the
	// database across the restart.
	eventually(t, "the adopted session is gone", func() bool { return !alive(rec.Pid) })
	if got := c.stops(); len(got) != 1 || got[0] != rec.ID {
		t.Errorf("claude stop calls after the restart: %v, want [%s]", got, rec.ID)
	}
}

// A Claude worker that dies while the daemon runs is noticed, and the owner is
// told it failed.
func TestClaudeWorkerCrashSendsANotice(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	c := startClaudeNode(t, g, shortSet)
	repo := newRepo(t, "shop")
	c.register(t, repo)
	c.mustStart(t, protocol.WorkerStartRequest{Project: "shop", Harness: "claude", Task: "FAKE:ignore"})
	rec := readClaude(t, repo)
	syscall.Kill(-rec.Pgid, syscall.SIGKILL)
	n := c.waitNotice(t, 1)[0]
	if !strings.Contains(n.Body, "status: failed") {
		t.Errorf("notice: %s", n.Body)
	}
	c.workerStopped(t, g, protocol.StopExited)
}

// The task is sent when the start returns, and a Claude turn that begins before
// the MCP server is connected has no agent_handback. So the start waits for the
// MCP server of the session, and a settle time after it.
func TestClaudeWorkerWaitsForItsMCPServer(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	set := shortSet
	set.MCPSettle = 900 * time.Millisecond
	c := startClaudeNode(t, g, set)
	repo := newRepo(t, "shop")
	c.register(t, repo)
	began := time.Now()
	c.mustStart(t, protocol.WorkerStartRequest{Project: "shop", Harness: "claude", Task: "x"})
	if took := time.Since(began); took < 900*time.Millisecond {
		t.Errorf("the start returned after %s, before the MCP server had settled", took)
	}
}

func TestClaudeTranscriptStats(t *testing.T) {
	dir := t.TempDir()
	proj := filepath.Join(dir, "-work-shop")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	// One message written twice (once per content block) counts once.
	lines := []string{
		`{"type":"user","message":{"role":"user","content":"hi"}}`,
		`{"type":"assistant","message":{"id":"m1","model":"claude-haiku-5-5","usage":{"input_tokens":10,"output_tokens":2,"cache_read_input_tokens":100,"cache_creation_input_tokens":5}}}`,
		`{"type":"assistant","message":{"id":"m1","model":"claude-haiku-5-5","usage":{"input_tokens":10,"output_tokens":7,"cache_read_input_tokens":100,"cache_creation_input_tokens":5}}}`,
		`{"type":"assistant","message":{"id":"m2","model":"claude-haiku-5-5","usage":{"input_tokens":3,"output_tokens":4,"cache_read_input_tokens":0,"cache_creation_input_tokens":0}}}`,
		`not json`,
	}
	if err := os.WriteFile(filepath.Join(proj, "abc.jsonl"), []byte(strings.Join(lines, "\n")), 0o644); err != nil {
		t.Fatal(err)
	}
	st := claudeTranscriptStats(dir, "abc")
	if st.Tokens != "13 in, 11 out, 100 cache read, 5 cache write" || st.Model != "claude-haiku-5-5" {
		t.Errorf("stats: %+v", st)
	}
	// The last assistant message is m2: its input plus cache read and write.
	// A haiku 5 has a 1M context window.
	if st.ContextTokens != 3 || st.ContextLimit != 1000000 || st.TotalTokens != 24 {
		t.Errorf("context stats: %+v", st)
	}
	if got := claudeContextLimit("claude-sonnet-4-5"); got != 200000 {
		t.Errorf("sonnet 4.5 limit = %d", got)
	}
	if got := claudeContextLimit("gpt-5"); got != 0 {
		t.Errorf("unknown model limit = %d", got)
	}
	if st := claudeTranscriptStats(dir, "missing"); st != (workerStats{}) {
		t.Errorf("stats of a missing transcript: %+v", st)
	}
}

// ---- Pi ----

func startPiNode(t *testing.T, g *testGateway, set WorkerSettings) *workerNode {
	t.Helper()
	dir := t.TempDir()
	t.Setenv(envFakePi, "1")
	t.Setenv("PI_CODING_AGENT_SESSION_DIR", dir)
	return startWorkerNode(t, g, set, func(c *Config) {
		c.HarnessPaths = map[string]string{"pi": os.Args[0]}
		c.PiSessionsDir = dir
	})
}

func readPi(t *testing.T, dir string) fakePiRecord {
	t.Helper()
	var rec fakePiRecord
	readRecord(t, dir, "fake-pi.json", &rec)
	return rec
}

// A Pi worker is `pi --mode rpc` in the project folder. Its standard input is a
// pipe that never closes, so Pi does not end. The extension registers the
// session that the daemon chose, the worker answers, and only its own process
// can be that session.
func TestPiWorkerLifecycle(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	w := startPiNode(t, g, shortSet)
	repo := newRepo(t, "shop")
	w.register(t, repo)
	owner := w.ownerID()

	res := w.mustStart(t, protocol.WorkerStartRequest{Project: "shop", Harness: "pi", Model: "openai/gpt-4.1-nano:low", Task: "say ok", Isolation: protocol.IsolationWorktree})
	rec := readPi(t, res.Worktree)
	if rec.Cwd != res.Worktree {
		t.Errorf("the worker runs in %s, not its worktree", rec.Cwd)
	}
	session := argValue(rec.Args, "--session-id")
	if rec.Args[0] != "--mode" || rec.Args[1] != "rpc" || len(session) != 36 || argValue(rec.Args, "--model") != "openai/gpt-4.1-nano:low" {
		t.Errorf("args: %v", rec.Args)
	}
	noFlagThatSkipsPermissions(t, rec.Args)
	if !strings.HasPrefix(rec.Env["AGENT_NET_WORKER"], "w_") || rec.Env["AGENT_NET_SOCKET"] != w.cfg.SocketPath {
		t.Errorf("env: %v", rec.Env)
	}
	if !strings.Contains(rec.Stdin, "p") { // a named pipe
		t.Errorf("stdin is %q, want a named pipe", rec.Stdin)
	}
	if rec.Pgid != rec.Pid {
		t.Errorf("not the leader of its group: %d %d", rec.Pid, rec.Pgid)
	}
	rows, _ := w.d.st.workers()
	if len(rows) != 1 || rows[0].NativeID != session || rows[0].PID != rec.Pid {
		t.Fatalf("rows: %+v", rows)
	}
	reply := w.replyTo(t, owner, res.Task.ID)
	if !strings.HasPrefix(reply.Body, "ok from worker") {
		t.Errorf("reply: %+v", reply)
	}
	// Pi must still be running: its standard input did not close.
	time.Sleep(300 * time.Millisecond)
	if !alive(rec.Pid) {
		t.Fatal("Pi ended: its standard input closed")
	}
	if a := w.agentOf(t, res.SessionID); a == nil || a.Worker == nil || a.Mode != protocol.ModeBackground || a.Harness != "pi" {
		t.Errorf("worker view: %+v", a)
	}
	// Another process cannot be this session.
	lc := dialLocal(t, w.sock)
	if _, err := lc.call("hello", map[string]any{"client": "pi", "harness": "pi", "native_id": session, "cwd": res.Worktree}); err == nil ||
		!strings.Contains(err.Error(), "belongs to a worker") {
		t.Errorf("a foreign process connected as the worker's session: %v", err)
	}

	w.cli.mustCall("worker_stop", protocol.WorkerStopRequest{Session: res.SessionID})
	eventually(t, "the process and its child are gone", func() bool { return !alive(rec.Pid) && !alive(rec.ChildPid) })
	w.workerStopped(t, g, protocol.StopRequested)
}

// Pi writes its usage in its session file. The daemon reads the last assistant
// message and the model's context window from `pi --list-models`, so a running
// Pi worker shows both.
func TestPiWorkerUsageReachesTheWorkersView(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	w := startPiNode(t, g, shortSet)
	w.register(t, newRepo(t, "shop"))
	res := w.mustStart(t, protocol.WorkerStartRequest{Project: "shop", Harness: "pi", Task: "FAKE:ignore keep working", Isolation: protocol.IsolationWorktree})
	eventually(t, "the Pi worker's context and tokens", func() bool {
		ws := w.workers(t)
		return len(ws) == 1 && ws[0].SessionID == res.SessionID &&
			ws[0].ContextTokens == 95000 && ws[0].ContextLimit == 200000 && ws[0].Tokens == 60000
	})
}

// piSessionStats reads the assistant usage from the session file: the last
// assistant message gives the context, and every assistant and usage entry
// adds to the run total.
func TestPiSessionStats(t *testing.T) {
	dir := t.TempDir()
	folder := filepath.Join(dir, "--work-shop--")
	if err := os.MkdirAll(folder, 0o700); err != nil {
		t.Fatal(err)
	}
	lines := []string{
		`{"type":"session","version":3,"id":"abc","cwd":"/work/shop"}`,
		`{"type":"message","id":"a1","message":{"role":"assistant","provider":"openai","model":"gpt-5","usage":{"input":100,"output":10,"cacheRead":50,"cacheWrite":5}}}`,
		`{"type":"message","id":"a2","message":{"role":"assistant","provider":"openai","model":"gpt-5","usage":{"input":200,"output":20,"cacheRead":10,"cacheWrite":5}}}`,
		`{"type":"usage","id":"u1","usage":{"input":30,"output":3,"cacheRead":0,"cacheWrite":0}}`,
		`not json`,
	}
	if err := os.WriteFile(filepath.Join(folder, "1_abc.jsonl"), []byte(strings.Join(lines, "\n")), 0o600); err != nil {
		t.Fatal(err)
	}
	st := piSessionStats(dir, "abc")
	if st.Model != "openai/gpt-5" || st.ContextTokens != 215 || st.TotalTokens != 363 ||
		st.Tokens != "330 in, 33 out, 60 cache read, 10 cache write" {
		t.Errorf("stats: %+v", st)
	}
	if st := piSessionStats(dir, "missing"); st != (workerStats{}) {
		t.Errorf("stats of a missing session: %+v", st)
	}
	table := "provider  model   context  max-out\nopenai    gpt-5   400K     128K\n"
	if got := piModelLimitFromTable(table, "openai/gpt-5:low"); got != 400000 {
		t.Errorf("model limit = %d", got)
	}
	if got := piModelLimitFromTable(table, "other/model"); got != 0 {
		t.Errorf("unknown model limit = %d", got)
	}
}

// A Pi worker hands back with agent_handback (the extension has it), with the
// facts; a Pi that cannot start is reported with its own output.
func TestPiWorkerHandbackAndStartFailure(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	w := startPiNode(t, g, shortSet)
	repo := newRepo(t, "shop")
	w.register(t, repo)
	res := w.mustStart(t, protocol.WorkerStartRequest{Project: "shop", Harness: "pi", Isolation: protocol.IsolationWorktree, Task: "FAKE:edit FAKE:handback"})
	n := w.waitNotice(t, 1)[0]
	for _, want := range []string{"status: done", "(pi", "work.txt | 2 ++", "finished the task in " + res.Worktree} {
		if !strings.Contains(n.Body, want) {
			t.Errorf("notice lacks %q:\n%s", want, n.Body)
		}
	}
	rec := readPi(t, res.Worktree)
	if rec.Cwd != res.Worktree {
		t.Errorf("the worker runs in %s, not its worktree", rec.Cwd)
	}
}

func TestPiWorkerStartFails(t *testing.T) {
	t.Setenv(envFakeStartFails, "1")
	g := startGateway(t, t.TempDir(), "")
	w := startPiNode(t, g, shortSet)
	repo := newRepo(t, "shop")
	w.register(t, repo)
	_, err := w.start(protocol.WorkerStartRequest{Project: "shop", Harness: "pi", Model: "nope/none", Task: "x", Isolation: protocol.IsolationWorktree})
	if err == nil || !strings.Contains(err.Error(), "Model not found") {
		t.Fatalf("error: %v", err)
	}
	if w.d.workerCount() != 0 {
		t.Errorf("%d workers left", w.d.workerCount())
	}
}

// The extension in a Pi that survives a restart of the daemon connects again,
// and the daemon, which took the worker back, accepts it as the worker.
func TestPiWorkerSurvivesADaemonRestart(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	w := startPiNode(t, g, shortSet)
	repo := newRepo(t, "shop")
	w.register(t, repo)
	res := w.mustStart(t, protocol.WorkerStartRequest{Project: "shop", Harness: "pi", Task: "x", Isolation: protocol.IsolationWorktree})
	rec := readPi(t, res.Worktree)
	owner := w.ownerID()
	w.d.Stop()
	time.Sleep(200 * time.Millisecond)
	if !alive(rec.Pid) {
		t.Fatal("Pi ended when the daemon stopped: its standard input closed")
	}
	w.node.restart()
	w.cli = cliClient(t, w.node)
	eventually(t, "the adopted worker is online with its flag", func() bool {
		a := w.agentOf(t, res.SessionID)
		return a != nil && a.Online && a.Worker != nil && a.Worker.Owner == owner
	})
	var sent protocol.MessageSent
	mustJSON(t, w.cli.mustCall("owner_send", protocol.OwnerSendRequest{To: res.SessionID, Body: "still there?"}), &sent)
	w.replyTo(t, owner, sent.Message.ID)
	w.cli.mustCall("worker_stop", protocol.WorkerStopRequest{Session: res.SessionID})
	eventually(t, "the process is gone", func() bool { return !alive(rec.Pid) })
}

// Pi has no permission prompts, so a Pi worker is refused unless it gets its own
// worktree, and nothing is started.
func TestPiWorkerNeedsAWorktree(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	w := startPiNode(t, g, shortSet)
	repo := newRepo(t, "shop")
	w.register(t, repo)
	_, err := w.start(protocol.WorkerStartRequest{Project: "shop", Harness: "pi", Task: "x"})
	if err == nil || !strings.Contains(err.Error(), `a Pi worker needs isolation "worktree"`) {
		t.Fatalf("error: %v", err)
	}
	if w.d.workerCount() != 0 {
		t.Errorf("%d workers left", w.d.workerCount())
	}
	if _, statErr := os.Stat(filepath.Join(repo, "fake-pi.json")); statErr == nil {
		t.Error("Pi was started")
	}
	if len(w.logs.lines("worker.refused", "harness=pi")) == 0 {
		t.Error("no worker.refused line")
	}
}

// ---- Codex ----

func startCodexNode(t *testing.T, g *testGateway, set WorkerSettings) *workerNode {
	t.Helper()
	t.Setenv(envFakeCodex, "1")
	// The user's Codex config is not read by tests: an empty one.
	t.Setenv("CODEX_HOME", t.TempDir())
	return startWorkerNode(t, g, set, func(c *Config) { c.HarnessPaths = map[string]string{"codex": os.Args[0]} })
}

func readCodex(t *testing.T, dir string, ok func(fakeCodexRecord) bool) fakeCodexRecord {
	t.Helper()
	var rec fakeCodexRecord
	eventually(t, "the fake codex record", func() bool {
		raw, err := os.ReadFile(filepath.Join(dir, "fake-codex.json"))
		rec = fakeCodexRecord{}
		return err == nil && json.Unmarshal(raw, &rec) == nil && ok(rec)
	})
	return rec
}

// A Codex worker is `codex app-server` on a loopback WebSocket with a token in
// a private file. The daemon registers the thread as the worker session, runs
// the task as a turn, and hands the final answer back, with the tokens. A
// follow-up from the person is another turn, answered to the person.
func TestCodexWorkerLifecycle(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	w := startCodexNode(t, g, shortSet)
	repo := newRepo(t, "shop")
	w.register(t, repo)
	owner := w.ownerID()

	res := w.mustStart(t, protocol.WorkerStartRequest{Project: "shop", Harness: "codex", Model: "gpt-fake", Task: "say ok"})
	rec := readCodex(t, repo, func(r fakeCodexRecord) bool { return len(r.Turns) == 1 })
	if rec.Cwd != repo {
		t.Errorf("the worker runs in %s", rec.Cwd)
	}
	args := strings.Join(rec.Args, " ")
	tokenFile := argValue(rec.Args, "--ws-token-file")
	if rec.Args[0] != "app-server" || !strings.HasPrefix(argValue(rec.Args, "--listen"), "ws://127.0.0.1:") ||
		argValue(rec.Args, "--ws-auth") != "capability-token" || !strings.HasPrefix(tokenFile, filepath.Join(w.cfg.StateDir, "workers")) {
		t.Errorf("args: %v", rec.Args)
	}
	noFlagThatSkipsPermissions(t, rec.Args)
	tok, _ := os.ReadFile(tokenFile)
	if len(tok) < 32 || strings.Contains(args, string(tok)) {
		t.Errorf("the token is on the command line, or missing (%d bytes)", len(tok))
	}
	if fi, _ := os.Stat(tokenFile); fi == nil || fi.Mode().Perm() != 0o600 {
		t.Errorf("the token file is not private: %v", fi)
	}
	// The thread has the folder and the model, never asks for approval and
	// runs in the sandbox of the project's level (decision 0020).
	if rec.Thread["cwd"] != repo || rec.Thread["model"] != "gpt-fake" {
		t.Errorf("thread/start: %v", rec.Thread)
	}
	if rec.Thread["approvalPolicy"] != "never" || rec.Thread["sandbox"] != "workspace-write" {
		t.Errorf("thread/start permissions: %v", rec.Thread)
	}
	// The brief for Codex does not name a tool that Codex does not have.
	if !strings.HasPrefix(res.Task.Body, protocol.WorkerPreambleCodex) || strings.Contains(res.Task.Body, "agent_handback") {
		t.Errorf("task body: %q", res.Task.Body)
	}
	if rec.Turns[0] != res.Task.Body {
		t.Errorf("the turn is not the task message: %q", rec.Turns[0])
	}
	rows, _ := w.d.st.workers()
	if len(rows) != 1 || !strings.HasPrefix(rows[0].Ref, "127.0.0.1:") || !strings.HasPrefix(rows[0].NativeID, "th-") {
		t.Fatalf("rows: %+v", rows)
	}

	// The hand-back is automatic.
	n := w.waitNotice(t, 1)[0]
	if n.From != res.SessionID || n.To != owner || n.ParentID != res.Task.ID {
		t.Fatalf("notice: %+v", n)
	}
	for _, want := range []string{"status: done", "(codex, gpt-fake)", "tokens: 500 in, 200 out, 1000 cache read", "context at end: 2k / 200k (1%)", protocol.ReportMarker, "codex done in " + repo} {
		if !strings.Contains(n.Body, want) {
			t.Errorf("notice lacks %q:\n%s", want, n.Body)
		}
	}
	eventually(t, "the ended worker session is offline", func() bool {
		a := w.agentOf(t, res.SessionID)
		return a != nil && !a.Online
	})
	a := w.agentOf(t, res.SessionID)
	if a == nil || a.Worker == nil || a.Worker.Owner != owner || a.Mode != protocol.ModeBackground || a.Harness != "codex" {
		t.Fatalf("worker view: %+v", a)
	}
	// The hand-back ends the worker: it leaves the list.
	eventually(t, "the worker ends after its hand-back", func() bool {
		return !alive(rec.Pid) && !alive(rec.ChildPid) && len(w.workers(t)) == 0
	})

	// A follow-up from the person starts the same thread again and is a turn,
	// and its answer goes to the person.
	var sent protocol.MessageSent
	mustJSON(t, w.cli.mustCall("owner_send", protocol.OwnerSendRequest{To: res.SessionID, Body: "and run the tests"}), &sent)
	got := w.replyTo(t, owner, sent.Message.ID)
	if !strings.Contains(got.Body, "status: done") {
		t.Errorf("follow-up answer: %s", got.Body)
	}
	rec = readCodex(t, repo, func(r fakeCodexRecord) bool { return r.Resumed == 1 && len(r.Turns) == 1 })
	if !strings.Contains(rec.Turns[0], "Message from your user, in person:") || !strings.Contains(rec.Turns[0], "and run the tests") {
		t.Errorf("the resumed turn: %q", rec.Turns[0])
	}
	eventually(t, "the resumed worker ends too", func() bool { return !alive(rec.Pid) && !alive(rec.ChildPid) })
	w.workerStopped(t, g, protocol.StopHandback)
	// Its folder is kept with the record, so the owner can start it again
	// while it is younger than the longest run time.
	if _, err := os.Stat(filepath.Dir(tokenFile)); err != nil {
		t.Errorf("the worker folder (with the token) is gone: %v", err)
	}
}

func TestCodexWorkerQueuedTurnAfterHandback(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	w := startCodexNode(t, g, shortSet)
	repo := newRepo(t, "shop")
	w.register(t, repo)
	owner := w.ownerID()
	res := w.mustStart(t, protocol.WorkerStartRequest{Project: "shop", Harness: "codex", Task: "FAKE:wait"})
	readCodex(t, repo, func(r fakeCodexRecord) bool { return len(r.Turns) == 1 })
	var sent protocol.MessageSent
	mustJSON(t, w.cli.mustCall("owner_send", protocol.OwnerSendRequest{To: res.SessionID, Body: "second turn FAKE:wait-second"}), &sent)
	if err := os.WriteFile(filepath.Join(repo, "fake-codex-finish"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	first := w.replyTo(t, owner, res.Task.ID)
	if !strings.Contains(first.Body, "codex done") {
		t.Fatalf("first report: %s", first.Body)
	}
	rec := readCodex(t, repo, func(r fakeCodexRecord) bool { return len(r.Turns) == 2 })
	if !strings.Contains(rec.Turns[1], "second turn") {
		t.Fatalf("second turn: %q", rec.Turns[1])
	}
	if w.d.workerCount() != 1 {
		t.Fatal("worker ended before the queued turn reported")
	}
	if err := os.WriteFile(filepath.Join(repo, "fake-codex-finish-second"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	second := w.replyTo(t, owner, sent.Message.ID)
	if !strings.Contains(second.Body, "codex done") {
		t.Fatalf("second report: %s", second.Body)
	}
	eventually(t, "worker ends after the second report", func() bool { return w.d.workerCount() == 0 })
}

// An approval is never given: Codex gets "decline", and the hand-back says
// blocked, with what was asked and where to approve it (W6, W15).
func TestCodexWorkerBlockedOnAnApproval(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	w := startCodexNode(t, g, shortSet)
	repo := newRepo(t, "shop")
	w.register(t, repo)
	res := w.mustStart(t, protocol.WorkerStartRequest{Project: "shop", Harness: "codex", Task: "FAKE:approve clean the build"})
	n := w.waitNotice(t, 1)[0]
	for _, want := range []string{"status: blocked", "BLOCKED:", "action: run the command: rm -rf build", "reason: outside the sandbox",
		"approve_in: Codex thread th-", "codex resume th-", "approval_text:"} {
		if !strings.Contains(n.Body, want) {
			t.Errorf("notice lacks %q:\n%s", want, n.Body)
		}
	}
	rec := readCodex(t, repo, func(r fakeCodexRecord) bool { return len(r.Answers) == 1 })
	if m, _ := rec.Answers[0].(map[string]any); m["decision"] != "decline" {
		t.Errorf("the approval was answered %v", rec.Answers[0])
	}
	eventually(t, "a blocked worker ends after its hand-back", func() bool { return w.d.workerCount() == 0 })
	_ = res
}

// A failed turn is a failed hand-back with the reason; the worktree facts are
// in the notice.
func TestCodexWorkerFailedTurnAndWorktree(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	w := startCodexNode(t, g, shortSet)
	repo := newRepo(t, "shop")
	w.register(t, repo)
	res := w.mustStart(t, protocol.WorkerStartRequest{Project: "shop", Harness: "codex", Isolation: protocol.IsolationWorktree, Task: "FAKE:edit FAKE:fail"})
	n := w.waitNotice(t, 1)[0]
	for _, want := range []string{"status: failed", "The Codex turn failed: model overloaded", "work.txt | 2 ++", "worktree: " + res.Worktree} {
		if !strings.Contains(n.Body, want) {
			t.Errorf("notice lacks %q:\n%s", want, n.Body)
		}
	}
	rec := readCodex(t, res.Worktree, func(r fakeCodexRecord) bool { return len(r.Turns) == 1 })
	if rec.Cwd != res.Worktree || rec.Thread["cwd"] != res.Worktree {
		t.Errorf("the worker runs in %s (thread %v), not in its worktree", rec.Cwd, rec.Thread["cwd"])
	}
}

// Only the session that started a Codex worker, and the person, can send to
// it. A Codex worker ends after the hand-back of its turn, so there are two
// states to check, each on its own worker: running (the daemon refuses the
// message at delivery) and ended (the gateway refuses it at send).
func TestCodexWorkerRefusesOtherAgents(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	w := startCodexNode(t, g, shortSet)
	repo := newRepo(t, "shop")
	w.register(t, repo)
	stranger := dialLocal(t, w.sock)
	stranger.mustCall("hello", map[string]any{"client": "opencode", "harness": "opencode", "native_id": "ses_stranger", "cwd": "/w/x"})
	stranger.AutoAck = true

	t.Run("ended", func(t *testing.T) {
		res := w.mustStart(t, protocol.WorkerStartRequest{Project: "shop", Harness: "codex", Task: "x"})
		w.waitNotice(t, 1)
		eventually(t, "the worker ends after its hand-back", func() bool { return w.d.workerCount() == 0 })
		_, err := stranger.call("send", map[string]any{"to": res.SessionID, "body": "hello"})
		if err == nil || !strings.Contains(err.Error(), "only the session that started it") {
			t.Fatalf("a stranger was not refused: %v", err)
		}
	})

	t.Run("running", func(t *testing.T) {
		res := w.mustStart(t, protocol.WorkerStartRequest{Project: "shop", Harness: "codex", Task: "FAKE:slow"})
		readCodex(t, repo, func(r fakeCodexRecord) bool { return len(r.Turns) == 1 })
		raw, err := stranger.call("send", map[string]any{"to": res.SessionID, "body": "hello"})
		if err != nil {
			t.Fatal(err)
		}
		var sent protocol.MessageSent
		mustJSON(t, raw, &sent)
		if sent.Message == nil {
			t.Fatalf("send gave %s", raw)
		}
		eventually(t, "the message failed", func() bool {
			var st protocol.MessageResult
			json.Unmarshal(w.cli.mustCall("message_status", protocol.QueryMessage{MessageID: sent.Message.ID}), &st)
			return st.Message.State == protocol.StateFailed
		})
		rec := readCodex(t, repo, func(r fakeCodexRecord) bool { return len(r.Turns) >= 1 })
		if len(rec.Turns) != 1 {
			t.Errorf("a message from another agent started a turn: %d turns", len(rec.Turns))
		}
	})
}

// A Codex worker survives a restart of the daemon: the app server keeps
// running, the daemon connects again from the address in the database and the
// token file, and resumes the thread.
func TestCodexWorkerSurvivesADaemonRestart(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	w := startCodexNode(t, g, shortSet)
	repo := newRepo(t, "shop")
	w.register(t, repo)
	res := w.mustStart(t, protocol.WorkerStartRequest{Project: "shop", Harness: "codex", Task: "FAKE:slow"})
	rec := readCodex(t, repo, func(r fakeCodexRecord) bool { return len(r.Turns) == 1 })
	owner := w.ownerID()

	w.d.Stop()
	if !alive(rec.Pid) {
		t.Fatal("stopping the daemon killed the Codex app server")
	}
	w.node.restart()
	w.cli = cliClient(t, w.node)
	if w.d.workerCount() != 1 {
		t.Fatalf("adopted %d workers", w.d.workerCount())
	}
	eventually(t, "the adopted worker is online with its flag", func() bool {
		a := w.agentOf(t, res.SessionID)
		return a != nil && a.Online && a.Worker != nil && a.Worker.Owner == owner
	})
	readCodex(t, repo, func(r fakeCodexRecord) bool { return r.Resumed == 1 })
	if err := os.WriteFile(filepath.Join(repo, "fake-codex-finish"), []byte("finish"), 0600); err != nil {
		t.Fatal(err)
	}
	w.waitNotice(t, 1)
	eventually(t, "the adopted app server ends after the hand-back", func() bool { return !alive(rec.Pid) && !alive(rec.ChildPid) })
}

func TestCodexWorkerStartFails(t *testing.T) {
	t.Setenv(envFakeStartFails, "1")
	g := startGateway(t, t.TempDir(), "")
	w := startCodexNode(t, g, shortSet)
	repo := newRepo(t, "shop")
	w.register(t, repo)
	_, err := w.start(protocol.WorkerStartRequest{Project: "shop", Harness: "codex", Task: "x"})
	if err == nil || !strings.Contains(err.Error(), "exited at start") {
		t.Fatalf("error: %v", err)
	}
	if w.d.workerCount() != 0 {
		t.Errorf("%d workers left", w.d.workerCount())
	}
}

// A worker does not start workers, whatever the harness (the flag is on the
// session that the daemon registered).
func TestStartedHarnessesAreListed(t *testing.T) {
	want := []string{"opencode", "claude", "pi", "codex"}
	if strings.Join(protocol.WorkerHarnesses, ",") != strings.Join(want, ",") {
		t.Errorf("worker harnesses: %v", protocol.WorkerHarnesses)
	}
	for _, h := range want {
		if !protocol.WorkerHarnessSupported(h) {
			t.Errorf("%s is not supported", h)
		}
	}
	if protocol.WorkerHarnessSupported("gemini") {
		t.Error("gemini is supported")
	}
}

func TestAdoptedCodexCompletesOriginalTurn(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	w := startCodexNode(t, g, shortSet)
	repo := newRepo(t, "shop")
	w.register(t, repo)
	w.mustStart(t, protocol.WorkerStartRequest{Project: "shop", Harness: "codex", Task: "FAKE:slow"})
	rec := readCodex(t, repo, func(r fakeCodexRecord) bool { return len(r.Turns) == 1 })
	w.d.Stop()
	w.node.restart()
	w.cli = cliClient(t, w.node)
	readCodex(t, repo, func(r fakeCodexRecord) bool { return r.Resumed == 1 })
	if err := os.WriteFile(filepath.Join(repo, "fake-codex-finish"), []byte("finish"), 0600); err != nil {
		t.Fatal(err)
	}
	n := w.waitNotice(t, 1)[0]
	if !strings.Contains(n.Body, "finished after restart") {
		t.Fatalf("notice: %s", n.Body)
	}
	eventually(t, "adopted Codex process ends", func() bool { return !alive(rec.Pid) })
}

func TestAdoptedOpenCodeStartsTurn(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	w := startWorkerNode(t, g, shortSet, nil)
	repo := newRepo(t, "shop")
	w.register(t, repo)
	res := w.mustStart(t, protocol.WorkerStartRequest{Project: "shop", Harness: "opencode", Task: "initial"})
	w.replyTo(t, w.ownerID(), res.Task.ID)
	w.d.Stop()
	w.node.restart()
	w.cli = cliClient(t, w.node)
	eventually(t, "adopted OpenCode online", func() bool { a := w.agentOf(t, res.SessionID); return a != nil && a.Online })
	var sent protocol.MessageSent
	mustJSON(t, w.cli.mustCall("owner_send", protocol.OwnerSendRequest{To: res.SessionID, Body: "after restart"}), &sent)
	eventually(t, "new OpenCode turn", func() bool {
		r := readFake(t, repo)
		for _, turn := range r.Turns {
			if strings.Contains(turn, "after restart") {
				return true
			}
		}
		return false
	})
}

// A turn can finish while there is no daemon to receive its completion event.
func TestAdoptedCodexTurnFinishedWhileDaemonDown(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	w := startCodexNode(t, g, shortSet)
	repo := newRepo(t, "shop")
	w.register(t, repo)
	w.mustStart(t, protocol.WorkerStartRequest{Project: "shop", Harness: "codex", Task: "FAKE:offline"})
	rec := readCodex(t, repo, func(r fakeCodexRecord) bool { return len(r.Turns) == 1 })
	w.d.Stop()
	if err := os.WriteFile(filepath.Join(repo, "fake-codex-offline-finish"), []byte("finish"), 0600); err != nil {
		t.Fatal(err)
	}
	readCodex(t, repo, func(r fakeCodexRecord) bool { return r.CompletedOffline })
	w.node.restart()
	w.cli = cliClient(t, w.node)
	n := w.waitNotice(t, 1)[0]
	if !strings.Contains(n.Body, "finished while daemon was down") {
		t.Fatalf("notice: %s", n.Body)
	}
	eventually(t, "adopted Codex process ends", func() bool { return !alive(rec.Pid) })
}
