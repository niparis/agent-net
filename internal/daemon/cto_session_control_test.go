package daemon

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// ctoControl runs session_change through the CTO entry point, as the CTO does.
func ctoControl(n *node, action, session string) (any, error) {
	args, _ := json.Marshal(map[string]any{"action": action, "session_id": session, "reason": "test"})
	return n.d.runCTOTool(context.Background(), "session_change", args)
}

// liveStopAdapter answers the stop path with the real job ID of the fake
// background session, so the fake `claude stop` ends it as the real one does.
type liveStopAdapter struct {
	*fsClaudeAdapter
}

func (a *liveStopAdapter) EnterRef(native string) (kind, job string) {
	entries, _ := os.ReadDir(a.dir)
	for _, e := range entries {
		var sf claudeSessionFile
		if raw, err := os.ReadFile(filepath.Join(a.dir, e.Name())); err == nil && json.Unmarshal(raw, &sf) == nil && sf.SessionID == native {
			return "bg", sf.JobID
		}
	}
	return "", ""
}

// A machine that holds an offline Claude conversation, started through fake
// Claude, plus a second machine that plays the CTO's machine.
type ctoControlFixture struct {
	g      *testGateway
	cto    *workerNode
	remote *workerNode
	repo   string
	native string
	old    string
	stops  string
}

func newCTOControlFixture(t *testing.T) *ctoControlFixture {
	t.Helper()
	g := startGateway(t, t.TempDir(), "")
	cto := startWorkerNode(t, g, shortSet, nil)
	sessions, stops := useFakeClaude(t)
	native := "11111111-0000-4000-8000-000000000000"
	remote := startWorkerNode(t, g, shortSet, func(c *Config) {
		c.ClaudeSessionsDir = sessions
		c.Adapters = append(c.Adapters, &liveStopAdapter{fsClaudeAdapter: &fsClaudeAdapter{dir: sessions}})
		c.HarnessPaths = map[string]string{"claude": os.Args[0]}
	})
	repo := newRepo(t, "tenebrae")
	remote.register(t, repo)
	old, err := g.gw.Store().UpsertSession(remote.d.NodeID(), protocol.SessionInfo{Harness: "claude", NativeID: native, Cwd: repo, Name: "tenebrae"})
	if err != nil {
		t.Fatal(err)
	}
	if err := g.gw.Store().SetSessionOffline(remote.d.NodeID(), old); err != nil {
		t.Fatal(err)
	}
	if err := cto.d.registerCTOSession(context.Background()); err != nil {
		t.Fatal(err)
	}
	return &ctoControlFixture{g: g, cto: cto, remote: remote, repo: repo, native: native, old: old, stops: stops}
}

func (f *ctoControlFixture) stopLog() string {
	raw, _ := os.ReadFile(f.stops)
	return string(raw)
}

// online returns the session ID of the one live Claude session on the remote machine.
func (f *ctoControlFixture) online(t *testing.T) string {
	t.Helper()
	var id string
	eventually(t, "the remote machine reports a live session", func() bool {
		for _, s := range f.remote.d.Sessions() {
			if s.SessionID != "" {
				id = s.SessionID
			}
		}
		return id != ""
	})
	return id
}

func TestCTOSessionResumeRelaysToTheRightMachine(t *testing.T) {
	f := newCTOControlFixture(t)
	result, err := ctoControl(f.cto.node, "resume", f.old)
	if err != nil {
		t.Fatal(err)
	}
	assertCTOImmediate(t, result)
	live := f.online(t)
	rec := readClaude(t, f.repo)
	if argValue(rec.Args, "--resume") != f.native || rec.Cwd != f.repo {
		t.Fatalf("resume did not reopen the recorded conversation in its folder: %+v", rec)
	}
	if f.cto.d.sessionByID(live) != nil {
		t.Fatal("the session started on the CTO's machine")
	}
	settingsEnv, _ := rec.Settings["env"].(map[string]any)
	if _, set := settingsEnv["CLAUDE_CODE_DISABLE_MOUSE"]; set || rec.Env["CLAUDE_CODE_DISABLE_MOUSE"] != "" {
		t.Fatalf("a managed owner launch must leave mouse tracking to the user settings: %+v", rec)
	}
	items, err := inboxAndLog(f.g.gw.Store())
	if err != nil || len(items) != 1 || !strings.Contains(items[0].Text, "resume") || len(items[0].Options) != 0 {
		t.Fatalf("resume leaves a notice and no undo: %+v %v", items, err)
	}
}

func TestCTOSessionStopIsGuardedAndHasResumeUndo(t *testing.T) {
	f := newCTOControlFixture(t)
	if _, err := ctoControl(f.cto.node, "resume", f.old); err != nil {
		t.Fatal(err)
	}
	live := f.online(t)
	if _, err := ctoControl(f.cto.node, "stop", live); err != nil {
		t.Fatal(err)
	}
	eventually(t, "the owner stop command reached the other machine", func() bool { return strings.HasPrefix(f.stopLog(), "stop ") })
	items, err := inboxAndLog(f.g.gw.Store())
	if err != nil || len(items) != 2 {
		t.Fatalf("inbox: %+v %v", items, err)
	}
	stop := items[len(items)-1]
	if len(stop.Options) != 2 || stop.Options[0] != "session_resume" {
		t.Fatalf("stop undo is resume: %+v", stop)
	}
	var undo protocol.SessionResumeRequest
	if json.Unmarshal([]byte(stop.Options[1]), &undo) != nil || undo.SessionID != live {
		t.Fatalf("undo names the stopped session: %+v", stop.Options)
	}
	lines, err := auditLines(f.cto.node)
	if err != nil || !strings.Contains(strings.Join(lines, "\n"), "session_stop") {
		t.Fatalf("audit lines %v %v", lines, err)
	}
}

func auditLines(n *node) ([]string, error) {
	rows, err := n.d.st.db.Query(`SELECT text FROM cto_actions ORDER BY at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

func TestCTOSessionStopUsesTheGuardedProcessCheck(t *testing.T) {
	pid, _ := startSleeper(t)
	g := startGateway(t, t.TempDir(), "")
	n, _, id := enterNode(t, g, "claude", protocol.SessionInfo{NativeID: "terminal", PID: pid}, nil, nil)
	ls := n.d.sessionByID(id)
	n.d.mu.Lock()
	ls.stopToken = "different run"
	n.d.mu.Unlock()
	if _, err := ctoControl(n, "stop", id); err == nil {
		t.Fatalf("stop accepted a changed process: %v", err)
	}
	if _, live := procToken(pid); !live {
		t.Fatal("the CTO signalled a process that is not the registered harness")
	}
}

func TestCTOSessionRestartStopsThenResumesTheSameConversation(t *testing.T) {
	f := newCTOControlFixture(t)
	if _, err := ctoControl(f.cto.node, "resume", f.old); err != nil {
		t.Fatal(err)
	}
	live := f.online(t)
	first := readClaude(t, f.repo)
	if err := os.Remove(filepath.Join(f.repo, "fake-claude.json")); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	result, err := ctoControl(f.cto.node, "restart", live)
	if err != nil {
		t.Fatal(err)
	}
	assertCTOImmediate(t, result)
	if !strings.HasPrefix(f.stopLog(), "stop ") {
		t.Fatalf("restart did not stop first: %q", f.stopLog())
	}
	second := readClaude(t, f.repo)
	if argValue(second.Args, "--resume") != first.Session || second.Session == "" {
		t.Fatalf("restart resumed %q, want the stopped native session %q", argValue(second.Args, "--resume"), first.Session)
	}
	if time.Since(start) > 30*time.Second {
		t.Fatal("restart is too slow")
	}
	items, err := inboxAndLog(f.g.gw.Store())
	if err != nil || len(items) != 2 || len(items[1].Options) != 0 || !strings.Contains(items[1].Text, "restart") {
		t.Fatalf("restart leaves one notice and no undo: %+v %v", items, err)
	}
}

func TestCTOSessionControlRefusesWorkersAndTheCTO(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	n := startNode(t, g, nodeOpts{})
	if err := n.d.registerCTOSession(context.Background()); err != nil {
		t.Fatal(err)
	}
	worker, err := g.gw.Store().UpsertSession(n.cfg.NodeID, protocol.SessionInfo{Harness: "claude", NativeID: "w", Cwd: "/tmp/w", Worker: &protocol.WorkerInfo{Project: "w", Task: "t"}})
	if err != nil {
		t.Fatal(err)
	}
	cto := n.d.ctoSessionID()
	if cto == "" {
		t.Fatal("no CTO session")
	}
	for _, action := range []string{"stop", "resume", "restart"} {
		if _, err := ctoControl(n, action, worker); err == nil || !strings.Contains(err.Error(), "worker") {
			t.Errorf("%s accepted a worker: %v", action, err)
		}
		if _, err := ctoControl(n, action, cto); err == nil || !strings.Contains(err.Error(), "CTO") {
			t.Errorf("%s accepted the CTO itself: %v", action, err)
		}
	}
}

func TestCTOSessionResumeRefusesPi(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	n := startNode(t, g, nodeOpts{})
	if err := n.d.registerCTOSession(context.Background()); err != nil {
		t.Fatal(err)
	}
	repo := t.TempDir()
	offline, err := g.gw.Store().UpsertSession(n.cfg.NodeID, protocol.SessionInfo{Harness: "pi", NativeID: "old", Cwd: repo})
	if err != nil {
		t.Fatal(err)
	}
	if err := g.gw.Store().SetSessionOffline(n.cfg.NodeID, offline); err != nil {
		t.Fatal(err)
	}
	live, err := g.gw.Store().UpsertSession(n.cfg.NodeID, protocol.SessionInfo{Harness: "pi", NativeID: "live", Cwd: repo})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ctoControl(n, "resume", offline); err == nil || !strings.Contains(err.Error(), "Pi") {
		t.Fatalf("resume of Pi: %v", err)
	}
	// A restart must refuse before it stops anything, so the Pi session lives on.
	if _, err := ctoControl(n, "restart", live); err == nil || !strings.Contains(err.Error(), "Pi") {
		t.Fatalf("restart of Pi: %v", err)
	}
	if a, ok, err := g.gw.Store().Agent(live); err != nil || !ok || !a.Online {
		t.Fatalf("the Pi session was touched: %+v %v %v", a, ok, err)
	}
}

func TestCTOSessionControlActionsAreClassified(t *testing.T) {
	for _, action := range []string{"stop", "resume", "restart"} {
		class, err := ctoClass("session_change", json.RawMessage(`{"action":"`+action+`","session_id":"s_1"}`))
		if err != nil || class != ctoMinor {
			t.Errorf("%s: %v %v", action, class, err)
		}
		concrete, args, _, err := ctoConcreteCall("session_change", json.RawMessage(`{"action":"`+action+`","session_id":"s_1"}`))
		if err != nil || concrete != "session_"+action || !strings.Contains(string(args), "s_1") {
			t.Errorf("%s: %s %s %v", action, concrete, args, err)
		}
	}
}

// The owner stops, resumes and restarts the CTO through the same functions as
// any session (session_stop, session_resume, restartSession; decision 0157).
// The CTO keeps its session ID and its conversation.
func TestOwnerStopsResumesAndRestartsTheCTO(t *testing.T) {
	t.Setenv("AGENT_NET_CONFIG_DIR", t.TempDir())
	sessions, _ := useFakeClaude(t)
	g := startGateway(t, t.TempDir(), "")
	n := startNode(t, g, nodeOpts{detect: fakeInventory, interval: time.Hour, cfg: func(c *Config) {
		c.ClaudeSessionsDir = sessions
		c.HarnessPaths = map[string]string{"claude": os.Args[0]}
	}})
	cli := cliClient(t, n)
	harness := "claude"
	cli.mustCall("cto_settings", protocol.CTOSettingsRequest{Harness: &harness})
	cli.mustCall("cto_enter", nil)
	id := n.d.ctoSessionID()
	conversation := func() string {
		n.d.cto.mu.Lock()
		defer n.d.cto.mu.Unlock()
		return n.d.claudeCTO.session
	}
	first := conversation()
	if id == "" || first == "" || !n.d.ctoRunning() {
		t.Fatalf("the CTO did not start: id=%q conversation=%q", id, first)
	}

	if _, err := cli.call("session_stop", protocol.SessionResumeRequest{SessionID: id}); err != nil {
		t.Fatalf("session_stop on the CTO: %v", err)
	}
	if n.d.ctoRunning() {
		t.Fatal("the CTO still runs after session_stop")
	}
	if _, err := cli.call("session_resume", protocol.SessionResumeRequest{SessionID: id}); err != nil {
		t.Fatalf("session_resume on the CTO: %v", err)
	}
	rec := readClaude(t, filepath.Join(n.cfg.StateDir, "cto", "claude"))
	if !n.d.ctoRunning() || argValue(rec.Args, "--resume") != first {
		t.Fatalf("resume: running=%v args=%q, want --resume %s", n.d.ctoRunning(), rec.Args, first)
	}

	n.d.cto.mu.Lock()
	job := n.d.claudeCTO.ref
	n.d.cto.mu.Unlock()
	if _, err := n.d.restartSession(context.Background(), id, true); err != nil {
		t.Fatalf("restart of the CTO: %v", err)
	}
	n.d.cto.mu.Lock()
	newJob := n.d.claudeCTO.ref
	n.d.cto.mu.Unlock()
	if !n.d.ctoRunning() || newJob == "" || newJob == job || n.d.ctoSessionID() != id {
		t.Fatalf("restart: running=%v job %s -> %s, id %s -> %s", n.d.ctoRunning(), job, newJob, id, n.d.ctoSessionID())
	}
}
