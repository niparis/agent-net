package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/client"
	"github.com/niparis/agent-net/internal/protocol"
)

// A fake server records all commands. It does not start Herdr or a terminal.
func fakeOpenHerdr(t *testing.T) (string, string) {
	t.Helper()
	dir := t.TempDir()
	log := filepath.Join(dir, "calls")
	cleanupHerdrEnvironment(t, log)
	script := `#!/bin/sh
printf '%s\n' "$*" >> ` + shellQuote(log) + `
case "$1 $2" in
  "pane list") if [ -f ` + shellQuote(filepath.Join(dir, "created")) + ` ]; then printf '%s\n' '{"result":{"panes":[{"pane_id":"w1:p1","tab_id":"w1:t1"}]}}'; else printf '%s\n' '{"result":{"panes":[]}}'; fi ;;
  "pane process-info") if [ -f ` + shellQuote(filepath.Join(dir, "idle")) + ` ]; then printf '%s\n' '{"result":{"process_info":{"shell_pid":11,"foreground_processes":[{"pid":11}]}}}'; else printf '%s\n' '{"result":{"process_info":{"shell_pid":11,"foreground_processes":[{"pid":22}]}}}'; fi ;;
  "tab create") : > ` + shellQuote(filepath.Join(dir, "created")) + `; printf '%s\n' '{"result":{"tab":{"tab_id":"w1:t1"},"root_pane":{"pane_id":"w1:p1"}}}' ;;
  "pane split") : > ` + shellQuote(filepath.Join(dir, "created")) + `; printf '%s\n' '{"result":{"pane":{"pane_id":"w1:p1","tab_id":"w1:t1"}}}' ;;
  "pane run"|"pane rename"|"agent focus"|"tab focus") printf '%s\n' '{"result":{}}' ;;
  *) exit 1 ;;
esac
`
	if err := os.WriteFile(filepath.Join(dir, "herdr"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return filepath.Join(dir, "herdr"), log
}

func cleanupHerdrEnvironment(t *testing.T, log string) {
	t.Helper()
	paths := regexp.MustCompile(regexp.QuoteMeta(os.TempDir()) + `/agent-net-herdr-env-[0-9]+`)
	t.Cleanup(func() {
		raw, _ := os.ReadFile(log)
		for _, path := range paths.FindAllString(string(raw), -1) {
			_ = os.Remove(path)
		}
	})
}

func herdrNoticeChannel(t *testing.T, n *node) <-chan map[string]any {
	t.Helper()
	_, ch := herdrUIClient(t, n)
	return ch
}

func herdrUIClient(t *testing.T, n *node) (*client.Client, <-chan map[string]any) {
	t.Helper()
	ch := make(chan map[string]any, 4)
	c, err := client.DialPushData(n.sock, func(method string, raw json.RawMessage) {
		if method == "cto_ui" {
			var q map[string]any
			if json.Unmarshal(raw, &q) == nil {
				ch <- q
			}
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	if _, err := c.Call(context.Background(), "hello", map[string]any{"client": "cli", "tui": true}); err != nil {
		t.Fatal(err)
	}
	return c, ch
}

func waitHerdrNotice(t *testing.T, ch <-chan map[string]any) map[string]any {
	t.Helper()
	var notice map[string]any
	eventually(t, "herdr notice", func() bool {
		select {
		case notice = <-ch:
			return true
		default:
			return false
		}
	})
	return notice
}

func TestEnterHerdrSplitsBesideTheTUI(t *testing.T) {
	_, log := fakeOpenHerdr(t)
	g := startGateway(t, t.TempDir(), "")
	bins := fakeBin(t, "claude")
	ea := &enterAdapter{fakeAdapter: &fakeAdapter{harness: "claude"}, kind: "bg", job: "job_42"}
	n := startNode(t, g, nodeOpts{cfg: func(c *Config) { c.HarnessPaths = bins; c.Adapters = []Adapter{ea} }})
	ea.setSessions(protocol.SessionInfo{Harness: "claude", NativeID: "abcdefgh-1234", PID: 1, Name: "garden", Mode: protocol.ModeBackground})
	var id string
	eventually(t, "session", func() bool {
		if s := n.d.Sessions(); len(s) == 1 {
			id = s[0].SessionID
		}
		return id != ""
	})
	var p protocol.EnterPlan
	mustJSON(t, cliClient(t, n).mustCall("session_enter", map[string]string{"session_id": id, "herdr_socket": "/tmp/herdr-test.sock", "herdr_pane_id": "w1:p0"}), &p)
	raw, _ := os.ReadFile(log)
	for _, want := range []string{"pane split --pane w1:p0", "pane rename w1:p1 garden", "pane run w1:p1"} {
		if !strings.Contains(string(raw), want) {
			t.Errorf("calls %q missing %q", raw, want)
		}
	}
	if !p.OpenedHerdr || strings.Contains(string(raw), "tab create") {
		t.Fatalf("plan %+v calls %s", p, raw)
	}
}

func requireHerdrOpened(t *testing.T, log, title string, command ...string) {
	t.Helper()
	raw, err := os.ReadFile(log)
	if err != nil {
		t.Fatalf("herdr was not called: %v", err)
	}
	calls := string(raw)
	// A TUI inside herdr gets its view split beside it. A caller on another
	// machine has no pane here, so the daemon creates a tab for it.
	label := "pane rename w1:p1 " + title
	if strings.Contains(calls, "pane split --pane "+testHerdrPane) {
		if !strings.Contains(calls, label) {
			t.Errorf("herdr calls %q do not include %q", calls, label)
		}
	} else {
		label = "tab create --label " + title
		if !strings.Contains(calls, label) {
			t.Errorf("herdr calls %q do not include %q", calls, label)
		}
	}
	for _, want := range append([]string{"pane run w1:p1"}, command...) {
		if !strings.Contains(calls, want) {
			t.Errorf("herdr calls %q do not include %q", calls, want)
		}
	}
	if strings.Contains(calls, "CLAUDE_CODE_DISABLE_MOUSE") {
		t.Errorf("mouse disabled in herdr calls: %q", calls)
	}
}

func TestEnterHerdrOpensClaudeBackground(t *testing.T) {
	_, log := fakeOpenHerdr(t)
	g := startGateway(t, t.TempDir(), "")
	bins := fakeBin(t, "claude")
	ea := &enterAdapter{fakeAdapter: &fakeAdapter{harness: "claude"}, kind: "bg", job: "job_42"}
	n := startNode(t, g, nodeOpts{cfg: func(c *Config) { c.HarnessPaths = bins; c.Adapters = []Adapter{ea} }})
	ea.setSessions(protocol.SessionInfo{Harness: "claude", NativeID: "abcdefgh-1234", PID: 1, Cwd: t.TempDir(), Name: "garden", Mode: protocol.ModeBackground})
	var id string
	eventually(t, "session", func() bool {
		if s := n.d.Sessions(); len(s) == 1 {
			id = s[0].SessionID
		}
		return id != ""
	})
	p := enterOfHerdr(t, n, id, fakeHerdrSocket(log))
	if len(p.Argv) != 0 || !strings.Contains(p.Note, "opened garden in herdr") {
		t.Errorf("plan %+v", p)
	}
	requireHerdrOpened(t, log, "garden", bins["claude"], "attach", "job_42")
}

func TestEnterHerdrOpensCTO(t *testing.T) {
	for _, harness := range []string{"claude", "opencode"} {
		t.Run(harness, func(t *testing.T) {
			_, log := fakeOpenHerdr(t)
			t.Setenv("AGENT_NET_CONFIG_DIR", t.TempDir())
			sessions, _ := useFakeClaude(t)
			g := startGateway(t, t.TempDir(), "")
			n := startNode(t, g, nodeOpts{detect: fakeInventory, interval: time.Hour, cfg: func(c *Config) {
				c.ClaudeSessionsDir = sessions
				c.HarnessPaths = map[string]string{harness: os.Args[0]}
			}})
			cli := cliClient(t, n)
			cli.mustCall("cto_settings", protocol.CTOSettingsRequest{Harness: &harness})
			cli.mustCall("cto_enter", nil) // register the CTO; no TUI location, so no view
			var p protocol.EnterPlan
			q := inHerdrEnter(protocol.EnterRequest{SessionID: protocol.CTONodeName}, fakeHerdrSocket(log))
			if err := json.Unmarshal(cli.mustCall("session_enter", q), &p); err != nil {
				t.Fatal(err)
			}
			if len(p.Argv) != 0 || !strings.Contains(p.Note, "opened CTO in herdr") {
				t.Errorf("plan %+v", p)
			}
			if harness == "claude" {
				requireHerdrOpened(t, log, "CTO", "attach", n.d.claudeCTO.ref)
			} else {
				requireHerdrOpened(t, log, "CTO", "--server", "--session")
			}
		})
	}
}

func TestSessionStartHerdrCodex(t *testing.T) {
	_, log := fakeOpenHerdr(t)
	g := startGateway(t, t.TempDir(), "")
	bins := fakeBin(t, "codex")
	n := startWorkerNode(t, g, shortSet, func(c *Config) { c.HarnessPaths = bins })
	repo := newRepo(t, "garden")
	n.register(t, repo)
	var r protocol.StartedSession
	q := inHerdrStart(protocol.SessionStartRequest{Project: repo, Harness: "codex", FirstMessage: "hello"}, fakeHerdrSocket(log))
	mustJSON(t, n.cli.mustCall("session_start", q), &r)
	if r.EnterPlan == nil || len(r.EnterPlan.Argv) != 0 || !strings.Contains(r.EnterPlan.Note, "opened garden in herdr") {
		t.Errorf("start %+v", r)
	}
	requireHerdrOpened(t, log, "garden", bins["codex"], "hello")
}

// A worker opens no view by itself: a view would take the owner's focus and,
// for Codex, run its self-updater. Enter opens it in herdr.
func TestWorkerStartOpensNoViewUntilEnter(t *testing.T) {
	for _, harness := range []string{"claude", "opencode", "codex"} {
		t.Run(harness, func(t *testing.T) {
			_, log := fakeOpenHerdr(t)
			g := startGateway(t, t.TempDir(), "")
			var n *workerNode
			switch harness {
			case "claude":
				n = startClaudeNode(t, g, shortSet).workerNode
			case "codex":
				n = startCodexNode(t, g, shortSet)
			default:
				n = startWorkerNode(t, g, shortSet, nil)
			}
			repo := newRepo(t, "garden")
			n.register(t, repo)
			r := n.mustStart(t, protocol.WorkerStartRequest{Model: "fake/m1", Project: "garden", Harness: harness, Task: "FAKE:ignore"})
			if raw, _ := os.ReadFile(log); strings.Contains(string(raw), "pane run") || strings.Contains(string(raw), "tab create") {
				t.Fatalf("worker start opened a view: %s", raw)
			}
			info := n.d.infoOf(n.d.sessionByID(r.SessionID))
			agent := n.agentOf(t, r.SessionID)
			if agent == nil || agent.Profile.Name == "" {
				t.Fatal("worker has no display name")
			}
			plan := enterOfHerdr(t, n.node, r.SessionID, fakeHerdrSocket(log))
			if !plan.OpenedHerdr {
				t.Fatalf("enter did not open the worker: %+v", plan)
			}
			switch harness {
			case "claude":
				requireHerdrOpened(t, log, agent.Profile.Name, "attach")
			case "opencode":
				requireHerdrOpened(t, log, agent.Profile.Name, "--server", "--session", info.NativeID)
			case "codex":
				requireHerdrOpened(t, log, agent.Profile.Name, "resume", info.NativeID, "--remote-auth-token-env")
			}
		})
	}
}
func TestWorkerResumeOpensNoView(t *testing.T) {
	_, log := fakeOpenHerdr(t)
	g := startGateway(t, t.TempDir(), "")
	n := startWorkerNode(t, g, shortSet, nil)
	repo := newRepo(t, "garden")
	n.register(t, repo)
	r := n.mustStart(t, protocol.WorkerStartRequest{Model: "fake/m1", Project: "garden", Harness: "opencode", Task: "FAKE:ignore"})
	n.cli.mustCall("owner_send", protocol.OwnerSendRequest{To: r.SessionID, Body: "FAKE:handback first"})
	n.waitNotice(t, 1)
	eventually(t, "worker ended", func() bool { return n.d.workerCount() == 0 })
	n.cli.mustCall("owner_send", protocol.OwnerSendRequest{To: r.SessionID, Body: "one more thing"})
	eventually(t, "worker resumed", func() bool { return n.d.workerCount() == 1 })
	if raw, _ := os.ReadFile(log); strings.Contains(string(raw), "pane run") {
		t.Fatalf("resumed worker opened a view: %s", raw)
	}
}
func TestEnterRemoteStartedWorkerUsesHerdrClient(t *testing.T) {
	remoteBin, remoteLog := fakeOpenHerdr(t)
	localBin, localLog := fakeOpenHerdr(t)
	g := startGateway(t, t.TempDir(), "")
	local := startWorkerNode(t, g, shortSet, func(c *Config) { c.HarnessPaths = map[string]string{"herdr": localBin} })
	remote := startWorkerNode(t, g, shortSet, func(c *Config) { c.HarnessPaths = map[string]string{"herdr": remoteBin} })
	repo := newRepo(t, "garden")
	remote.register(t, repo)
	r := remote.mustStart(t, protocol.WorkerStartRequest{Model: "fake/m1", Project: "garden", Harness: "opencode", Task: "FAKE:ignore"})
	if raw, _ := os.ReadFile(remoteLog); strings.Contains(string(raw), "pane run") || strings.Contains(string(raw), "tab create") {
		t.Fatalf("worker start opened a view: %s", raw)
	}
	waitSessionOnGateway(t, local.node, r.SessionID)
	p := enterOfHerdr(t, local.node, r.SessionID, fakeHerdrSocket(localLog))
	if !p.OpenedHerdr {
		t.Fatalf("remote worker plan: %+v", p)
	}
	agent := remote.agentOf(t, r.SessionID)
	if agent == nil {
		t.Fatal("worker absent from gateway")
	}
	requireHerdrOpened(t, localLog, agent.Profile.Name, "--remote", remote.d.hostName())
	// The remote machine has no asking TUI, so it opens one tab for the session.
	raw, _ := os.ReadFile(remoteLog)
	if strings.Count(string(raw), "pane run w1:p1") != 1 || strings.Count(string(raw), "tab create") != 1 {
		t.Fatalf("remote worker view: %s", raw)
	}
}

func TestEnterPiRPCWorkerExplainsNoTerminal(t *testing.T) {
	_, log := fakeOpenHerdr(t)
	g := startGateway(t, t.TempDir(), "")
	n := startPiNode(t, g, shortSet)
	repo := newRepo(t, "garden")
	n.register(t, repo)
	r := n.mustStart(t, protocol.WorkerStartRequest{Model: "fake/m1", Project: "garden", Harness: "pi", Isolation: protocol.IsolationWorktree, Task: "FAKE:ignore"})
	p := enterOf(t, n.node, r.SessionID, "")
	if p.OpenedHerdr || len(p.Argv) != 0 || !strings.Contains(p.Note, "RPC") || !strings.Contains(p.Note, "agent-net show") {
		t.Fatalf("Pi RPC worker did not explain the missing terminal: %+v", p)
	}
	raw, _ := os.ReadFile(log)
	if strings.Contains(string(raw), "tab create") || strings.Contains(string(raw), "pane run") {
		t.Fatalf("Pi RPC worker got a false terminal view: %s", raw)
	}
}

func TestEnterHerdrFocusesExistingPane(t *testing.T) {
	t.Parallel()
	f := newFakeHerdr(t)
	f.panes(t, herdrAgentPane("w1:pB", "term_b"))
	pid := f.session(t, "w1:pB")
	f.holds(t, "w1:pB", pid)
	g := startGateway(t, t.TempDir(), "")
	ea := &enterAdapter{fakeAdapter: &fakeAdapter{harness: "claude"}, kind: "interactive"}
	n := startNode(t, g, nodeOpts{cfg: func(c *Config) { c.Adapters = []Adapter{ea} }})
	ea.setSessions(protocol.SessionInfo{Harness: "claude", NativeID: "inter", PID: pid, Name: "garden"})
	var id string
	eventually(t, "session", func() bool {
		if s := n.d.Sessions(); len(s) == 1 {
			id = s[0].SessionID
		}
		return id != ""
	})
	p := enterOfHerdr(t, n, id, f.sock)
	calls := f.calls(t)
	if !strings.Contains(calls, "agent focus w1:pB") || strings.Contains(calls, "tab create") || len(p.Argv) != 0 || !strings.Contains(p.Note, "opened garden in herdr") {
		t.Fatalf("plan %+v; calls %q", p, calls)
	}
}

func TestEnterWithoutHerdrExplainsFallback(t *testing.T) {
	withoutLocalHerdr(t)
	g := startGateway(t, t.TempDir(), "")
	bins := fakeBin(t, "claude")
	ea := &enterAdapter{fakeAdapter: &fakeAdapter{harness: "claude"}, kind: "bg", job: "job_42"}
	n := startNode(t, g, nodeOpts{cfg: func(c *Config) { c.HarnessPaths = bins; c.Adapters = []Adapter{ea} }})
	ea.setSessions(protocol.SessionInfo{Harness: "claude", NativeID: "abcdefgh-1234", PID: 1, Name: "garden", Mode: protocol.ModeBackground})
	var id string
	eventually(t, "session", func() bool {
		if s := n.d.Sessions(); len(s) == 1 {
			id = s[0].SessionID
		}
		return id != ""
	})
	p := enterOf(t, n, id, "")
	if len(p.Argv) != 3 || p.Argv[1] != "attach" || p.OpenedHerdr {
		t.Fatalf("fallback %+v", p)
	}
}

// A TUI outside herdr cannot see a herdr pane, so Enter hands over the
// terminal instead of opening a view the owner would not see.
func TestEnterOutsideHerdrHandsOverTerminal(t *testing.T) {
	bins := fakeBin(t, "claude")
	_, log := fakeOpenHerdr(t)
	g := startGateway(t, t.TempDir(), "")
	ea := &enterAdapter{fakeAdapter: &fakeAdapter{harness: "claude"}, kind: "bg", job: "job_42"}
	n := startNode(t, g, nodeOpts{cfg: func(c *Config) { c.HarnessPaths = bins; c.Adapters = []Adapter{ea} }})
	ea.setSessions(protocol.SessionInfo{Harness: "claude", NativeID: "abcdefgh-1234", PID: 1, Name: "garden", Mode: protocol.ModeBackground})
	var id string
	eventually(t, "session", func() bool {
		if s := n.d.Sessions(); len(s) == 1 {
			id = s[0].SessionID
		}
		return id != ""
	})
	p := enterOf(t, n, id, "")
	if p.OpenedHerdr || len(p.Argv) != 3 || p.Argv[0] != bins["claude"] || p.Argv[1] != "attach" {
		t.Fatalf("plan %+v", p)
	}
	if raw, _ := os.ReadFile(log); strings.Contains(string(raw), "tab create") || strings.Contains(string(raw), "pane split") || strings.Contains(string(raw), "pane run") {
		t.Fatalf("herdr opened a view for a TUI outside herdr: %s", raw)
	}
}

// A start asked by a TUI outside herdr hands the terminal plan back, and no
// herdr view is opened.
func TestSessionStartOutsideHerdrHandsOverTerminal(t *testing.T) {
	bins := fakeBin(t, "codex")
	_, log := fakeOpenHerdr(t)
	g := startGateway(t, t.TempDir(), "")
	n := startWorkerNode(t, g, shortSet, func(c *Config) { c.HarnessPaths = bins })
	repo := newRepo(t, "garden")
	n.register(t, repo)
	var r protocol.StartedSession
	mustJSON(t, n.cli.mustCall("session_start", protocol.SessionStartRequest{Project: repo, Harness: "codex", FirstMessage: "hello"}), &r)
	if r.EnterPlan == nil || r.EnterPlan.OpenedHerdr || len(r.EnterPlan.Argv) != 2 || r.EnterPlan.Argv[0] != bins["codex"] || r.EnterPlan.Argv[1] != "hello" {
		t.Fatalf("start %+v", r)
	}
	if raw, _ := os.ReadFile(log); strings.Contains(string(raw), "tab create") || strings.Contains(string(raw), "pane split") || strings.Contains(string(raw), "pane run") {
		t.Fatalf("herdr opened a view for a TUI outside herdr: %s", raw)
	}
}

// Herdr is installed here, so a start from a TUI outside it keeps the session
// in the background and must not claim that herdr is missing.
func TestSessionStartOutsideHerdrDoesNotClaimHerdrMissing(t *testing.T) {
	_, log := fakeOpenHerdr(t)
	g := startGateway(t, t.TempDir(), "")
	c := startClaudeNode(t, g, shortSet)
	repo := newRepo(t, "garden")
	c.register(t, repo)
	t.Cleanup(func() { killFakeClaude(t, repo) })
	var r protocol.StartedSession
	mustJSON(t, c.cli.mustCall("session_start", protocol.SessionStartRequest{Project: "garden", Harness: "claude", FirstMessage: "hello"}), &r)
	eventually(t, "started", func() bool {
		mustJSON(t, c.cli.mustCall("session_start_get", map[string]string{"id": r.ID}), &r)
		if r.Status == "failed" {
			t.Fatalf("start %+v", r)
		}
		return r.Status == "started"
	})
	if r.EnterPlan != nil || r.EnterNote != "Enter opens it." {
		t.Fatalf("start %+v", r)
	}
	if raw, _ := os.ReadFile(log); strings.Contains(string(raw), "pane run") || strings.Contains(string(raw), "tab create") {
		t.Fatalf("herdr opened a view for a TUI outside herdr: %s", raw)
	}
}

func TestSessionStartAndResumeHerdrClaude(t *testing.T) {
	for _, resume := range []bool{false, true} {
		t.Run(map[bool]string{false: "start", true: "resume"}[resume], func(t *testing.T) {
			_, log := fakeOpenHerdr(t)
			g := startGateway(t, t.TempDir(), "")
			c := startClaudeNode(t, g, shortSet)
			repo := newRepo(t, "garden")
			c.register(t, repo)
			t.Cleanup(func() { killFakeClaude(t, repo) })
			socket := fakeHerdrSocket(log)
			var r protocol.StartedSession
			if resume {
				id, err := g.gw.Store().UpsertSession(c.d.cfg.NodeID, protocol.SessionInfo{Harness: "claude", NativeID: "11111111-0000-4000-8000-000000000000", Cwd: repo, Name: "garden"})
				if err != nil {
					t.Fatal(err)
				}
				if err := g.gw.Store().SetSessionOffline(c.d.cfg.NodeID, id); err != nil {
					t.Fatal(err)
				}
				mustJSON(t, c.cli.mustCall("session_resume", inHerdrResume(protocol.SessionResumeRequest{SessionID: id}, socket)), &r)
			} else {
				mustJSON(t, c.cli.mustCall("session_start", inHerdrStart(protocol.SessionStartRequest{Project: "garden", Harness: "claude", FirstMessage: "hello"}, socket)), &r)
			}
			eventually(t, "started", func() bool {
				mustJSON(t, c.cli.mustCall("session_start_get", map[string]string{"id": r.ID}), &r)
				if r.Status == "failed" {
					t.Fatalf("start %+v", r)
				}
				return r.Status == "started"
			})
			requireHerdrOpened(t, log, "garden", "attach", r.JobID)
		})
	}
}

func TestEnterHerdrRemoteClientInLocalTab(t *testing.T) {
	remoteBin, remoteLog := fakeOpenHerdr(t)
	localBin, localLog := fakeOpenHerdr(t)
	g := startGateway(t, t.TempDir(), "")
	bins := fakeBin(t, "claude")
	bins["herdr"] = remoteBin
	ea := &enterAdapter{fakeAdapter: &fakeAdapter{harness: "claude"}, kind: "bg", job: "job_42"}
	remote := startNode(t, g, nodeOpts{cfg: func(c *Config) { c.HarnessPaths = bins; c.Adapters = []Adapter{ea} }})
	local := startNode(t, g, nodeOpts{cfg: func(c *Config) { c.HarnessPaths = map[string]string{"herdr": localBin} }})
	ea.setSessions(protocol.SessionInfo{Harness: "claude", NativeID: "abcdefgh-1234", PID: 1, Name: "garden", Cwd: t.TempDir(), Mode: protocol.ModeBackground})
	var id string
	eventually(t, "remote session", func() bool {
		if s := remote.d.Sessions(); len(s) == 1 {
			id = s[0].SessionID
		}
		return id != ""
	})
	// Allow the gateway to observe the session without opening it early.
	eventually(t, "remote on gateway", func() bool {
		a, err := local.d.agentView(context.Background(), id)
		return err == nil && a.SessionID == id
	})
	p := enterOfHerdr(t, local, id, fakeHerdrSocket(localLog))
	if !p.OpenedHerdr || len(p.Argv) != 0 || p.Note != "opened garden in herdr" {
		t.Fatalf("plan %+v", p)
	}
	requireHerdrOpened(t, remoteLog, "garden", "attach", "job_42")
	requireHerdrOpened(t, localLog, "garden", "--remote", remote.cfg.Hostname)
	p = enterOfHerdr(t, local, id, fakeHerdrSocket(localLog))
	if !p.OpenedHerdr {
		t.Fatalf("second plan %+v", p)
	}
	raw, _ := os.ReadFile(localLog)
	if strings.Count(string(raw), "pane split") != 1 || !strings.Contains(string(raw), "tab focus w1:t1") {
		t.Fatalf("second enter duplicated the tab: %s", raw)
	}
}

func TestSessionStartHerdrRemoteCodex(t *testing.T) {
	remoteBin, remoteLog := fakeOpenHerdr(t)
	localBin, localLog := fakeOpenHerdr(t)
	g := startGateway(t, t.TempDir(), "")
	local := startWorkerNode(t, g, shortSet, func(c *Config) { c.HarnessPaths = map[string]string{"herdr": localBin} })
	bins := fakeBin(t, "codex")
	bins["herdr"] = remoteBin
	remote := startWorkerNode(t, g, shortSet, func(c *Config) { c.HarnessPaths = bins })
	repo := newRepo(t, "garden")
	remote.register(t, repo)
	var r protocol.StartedSession
	q := inHerdrStart(protocol.SessionStartRequest{Project: "garden", Harness: "codex", NodeID: remote.d.NodeID()}, fakeHerdrSocket(localLog))
	mustJSON(t, local.cli.mustCall("session_start", q), &r)
	if r.EnterPlan == nil || !r.EnterPlan.OpenedHerdr {
		t.Fatalf("remote start %+v", r)
	}
	if raw, _ := os.ReadFile(remoteLog); strings.Contains(string(raw), "pane run") || strings.Contains(string(raw), "tab create") {
		t.Fatalf("remote start opened a view: %s", raw)
	}
	requireHerdrOpened(t, localLog, "garden", "ssh", remote.d.cfg.Hostname, bins["codex"])
}

func TestEnterHerdrFocusesPlainTerminal(t *testing.T) {
	t.Parallel()
	f := newFakeHerdr(t)
	f.panes(t, `{"pane_id":"w1:pB","terminal_id":"term_b","tab_id":"w1:t1"}`)
	pid := f.session(t, "w1:pB")
	f.holds(t, "w1:pB", pid)
	g := startGateway(t, t.TempDir(), "")
	ea := &enterAdapter{fakeAdapter: &fakeAdapter{harness: "claude"}, kind: "interactive"}
	n := startNode(t, g, nodeOpts{cfg: func(c *Config) { c.Adapters = []Adapter{ea} }})
	ea.setSessions(protocol.SessionInfo{Harness: "claude", NativeID: "interactive", PID: pid, Name: "garden"})
	var id string
	eventually(t, "session", func() bool {
		if s := n.d.Sessions(); len(s) == 1 {
			id = s[0].SessionID
		}
		return id != ""
	})
	p := enterOfHerdr(t, n, id, f.sock)
	if !p.OpenedHerdr || !strings.Contains(f.calls(t), "tab focus w1:t1") {
		t.Fatalf("plan %+v calls %q", p, f.calls(t))
	}
}

func TestHerdrStartsServerAndFallsBackWhenUnavailable(t *testing.T) {
	for _, start := range []bool{true, false} {
		t.Run(map[bool]string{true: "start", false: "unavailable"}[start], func(t *testing.T) {
			dir := t.TempDir()
			log, marker := filepath.Join(dir, "log"), filepath.Join(dir, "server")
			cleanupHerdrEnvironment(t, log)
			script := `#!/bin/sh
printf '%s\n' "$*" >> ` + shellQuote(log) + `
case "$1 $2" in
  "server ") ` + map[bool]string{true: ": > " + shellQuote(marker), false: "exit 1"}[start] + ` ;;
  "pane list") [ -f ` + shellQuote(marker) + ` ] || exit 1; printf '%s\n' '{"result":{"panes":[]}}' ;;
  "tab create") printf '%s\n' '{"result":{"tab":{"tab_id":"w1:t1"},"root_pane":{"pane_id":"w1:p1"}}}' ;;
  "pane run") exit 0 ;;
  *) exit 1 ;;
esac
`
			if err := os.WriteFile(filepath.Join(dir, "herdr"), []byte(script), 0700); err != nil {
				t.Fatal(err)
			}
			t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
			p := (&Daemon{}).openInHerdr("session", "garden", "", "", protocol.EnterPlan{Argv: []string{"claude", "attach", "job"}, Note: "fallback"})
			calls, _ := os.ReadFile(log)
			if !strings.Contains(string(calls), "server\n") {
				t.Fatalf("no server start: %s", calls)
			}
			if start && (!p.OpenedHerdr || !strings.Contains(string(calls), "pane run w1:p1")) {
				t.Fatalf("plan %+v calls %s", p, calls)
			}
			if !start && (p.OpenedHerdr || len(p.Argv) != 3 || !strings.Contains(p.Note, "install")) {
				t.Fatalf("fallback %+v", p)
			}
		})
	}
}

func TestHerdrKeepsCredentialsOutOfCommand(t *testing.T) {
	_, log := fakeOpenHerdr(t)
	secret := "a secret 'with spaces'"
	p := (&Daemon{}).openInHerdr("worker", "garden", "", "", protocol.EnterPlan{Argv: []string{"opencode", "--server", "http://127.0.0.1:9"}, Env: map[string]string{"OPENCODE_SERVER_PASSWORD": secret, "CLAUDE_CODE_DISABLE_MOUSE": "1"}})
	if !p.OpenedHerdr || len(p.Env) != 0 {
		t.Fatalf("plan %+v", p)
	}
	raw, _ := os.ReadFile(log)
	if strings.Contains(string(raw), secret) || strings.Contains(string(raw), "CLAUDE_CODE_DISABLE_MOUSE") {
		t.Fatalf("secret or mouse switch in command: %s", raw)
	}
	file := regexp.MustCompile(regexp.QuoteMeta(os.TempDir()) + `/agent-net-herdr-env-[0-9]+`).FindString(string(raw))
	if file == "" {
		t.Fatalf("no environment file in %s", raw)
	}
	defer os.Remove(file)
	info, err := os.Stat(file)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("environment permissions %v %v", info, err)
	}
	data, err := os.ReadFile(file)
	if err != nil || !strings.Contains(string(data), "unset CLAUDE_CODE_DISABLE_MOUSE") || !strings.Contains(string(data), shellQuote(secret)) {
		t.Fatalf("environment content missing, err %v", err)
	}
}

func TestSessionStartHerdrPiDoesNotNeedTmux(t *testing.T) {
	_, log := fakeOpenHerdr(t)
	_, tmuxLog := fakeTmuxHost(t, false)
	g := startGateway(t, t.TempDir(), "")
	bins := fakeBin(t, "pi")
	n := startWorkerNode(t, g, shortSet, func(c *Config) { c.HarnessPaths = bins })
	repo := newRepo(t, "garden")
	n.register(t, repo)
	var r protocol.StartedSession
	q := inHerdrStart(protocol.SessionStartRequest{Project: "garden", Harness: "pi"}, fakeHerdrSocket(log))
	mustJSON(t, n.cli.mustCall("session_start", q), &r)
	if r.EnterPlan == nil || !r.EnterPlan.OpenedHerdr {
		t.Fatalf("initial start %+v", r)
	}
	helloPi(t, n.node, r.NativeID, repo, true)
	eventually(t, "started Pi", func() bool {
		mustJSON(t, n.cli.mustCall("session_start_get", map[string]string{"id": r.ID}), &r)
		if r.Status == "failed" {
			t.Fatalf("start %+v", r)
		}
		return r.Status == "started"
	})
	requireHerdrOpened(t, log, "garden", bins["pi"], "--session-id", r.NativeID)
	if raw, _ := os.ReadFile(tmuxLog); len(raw) > 0 {
		t.Fatalf("tmux used: %s", raw)
	}
}

func TestCTOSessionProposalStartsInBackground(t *testing.T) {
	_, log := fakeOpenHerdr(t)
	g := startGateway(t, t.TempDir(), "")
	home := pathTestHome(t)
	c := startClaudeNode(t, g, shortSet)
	path := filepath.Join(home, "code", "garden")
	args, _ := json.Marshal(map[string]any{"action": "create", "path": path, "harness": "claude", "first_message": "hello"})
	if _, err := c.d.runCTOToolRaw(context.Background(), "project_change", args); err != nil {
		t.Fatal(err)
	}
	items, err := c.d.proposalRunner().list()
	if err != nil || len(items) != 1 {
		t.Fatalf("proposals %v %v", items, err)
	}
	c.cli.mustCall("proposal_answer", inHerdrAnswer(protocol.CTOProposalAnswer{ID: items[0].ID, Action: "a"}, fakeHerdrSocket(log)))
	items, err = c.d.proposalRunner().list()
	if err != nil {
		t.Fatal(err)
	}
	var r protocol.StartedSession
	if err := json.Unmarshal(items[0].Result, &r); err != nil {
		t.Fatal(err)
	}
	r = waitSessionStartPath(t, c.workerNode, r)
	t.Cleanup(func() { killFakeClaude(t, path) })
	if r.Status != "started" || r.EnterPlan != nil {
		t.Fatalf("start %+v", r)
	}
	if raw, _ := os.ReadFile(log); strings.Contains(string(raw), "pane run") || strings.Contains(string(raw), "tab create") {
		t.Fatalf("proposal opened a view: %s", raw)
	}
}

func TestHerdrRunsQuotedCommandFromAnyShell(t *testing.T) {
	_, log := fakeOpenHerdr(t)
	dir := t.TempDir()
	output := filepath.Join(dir, "output")
	bin := filepath.Join(dir, "harness")
	script := "#!/bin/sh\nprintf '%s\\n' \"$1\" \"$TOKEN\" \"${CLAUDE_CODE_DISABLE_MOUSE-unset}\" > " + shellQuote(output) + "\n"
	if err := os.WriteFile(bin, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	p := (&Daemon{}).openInHerdr("a", "garden", "", "", protocol.EnterPlan{Argv: []string{bin, "a 'quoted' $prompt"}, Env: map[string]string{"TOKEN": "secret with ' quote"}})
	if !p.OpenedHerdr {
		t.Fatalf("plan %+v", p)
	}
	data, _ := os.ReadFile(log)
	_, line, ok := strings.Cut(string(data), "pane run w1:p1 ")
	if !ok {
		t.Fatalf("calls %s", data)
	}
	line = strings.TrimSpace(line)
	if !strings.HasPrefix(line, "sh -c ") {
		t.Fatalf("not a shell-independent command: %s", line)
	}
	cmd := exec.Command("sh", "-c", line)
	cmd.Env = append(os.Environ(), "CLAUDE_CODE_DISABLE_MOUSE=1")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("command failed: %s %v", out, err)
	}
	got, err := os.ReadFile(output)
	if err != nil || string(got) != "a 'quoted' $prompt\nsecret with ' quote\nunset\n" {
		t.Fatalf("result %q err %v", got, err)
	}
}

func TestEnterHerdrFindsExistingClientBySessionID(t *testing.T) {
	t.Parallel()
	f := newFakeHerdr(t)
	native := "abcdefgh-1234-5678-0000-012345678901"
	f.panes(t, `{"pane_id":"w1:pB","tab_id":"w1:t1","terminal_id":"term_b","agent":"claude","agent_session":{"agent":"claude","kind":"id","value":"`+native+`"}}`)
	g := startGateway(t, t.TempDir(), "")
	bins := fakeBin(t, "claude")
	bins["herdr"] = f.bin
	ea := &enterAdapter{fakeAdapter: &fakeAdapter{harness: "claude"}, kind: "bg", job: "abcdefgh"}
	n := startNode(t, g, nodeOpts{cfg: func(c *Config) { c.HarnessPaths = bins; c.Adapters = []Adapter{ea} }})
	ea.setSessions(protocol.SessionInfo{Harness: "claude", NativeID: native, PID: 1, Name: "garden", Mode: protocol.ModeBackground})
	var id string
	eventually(t, "session", func() bool {
		if s := n.d.Sessions(); len(s) == 1 {
			id = s[0].SessionID
		}
		return id != ""
	})
	p := enterOfHerdr(t, n, id, f.sock)
	if !p.OpenedHerdr || p.HerdrPane != "w1:pB" || !strings.Contains(f.calls(t), "agent focus w1:pB") || strings.Contains(f.calls(t), "tab create") {
		t.Fatalf("plan %+v calls %q", p, f.calls(t))
	}
}

func TestEnterHerdrReopensWhenClientExited(t *testing.T) {
	bin, log := fakeOpenHerdr(t)
	d := &Daemon{}
	plan := protocol.EnterPlan{Argv: []string{"claude", "attach", "job"}}
	if p := d.openInHerdr("session", "garden", "", "", plan); !p.OpenedHerdr {
		t.Fatalf("first plan %+v", p)
	}
	if err := os.WriteFile(filepath.Join(filepath.Dir(bin), "idle"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	old := d.herdrOpened["session"]
	old.started = time.Now().Add(-2 * time.Second)
	d.herdrOpened["session"] = old
	if p := d.openInHerdr("session", "garden", "", "", plan); !p.OpenedHerdr {
		t.Fatalf("second plan %+v", p)
	}
	raw, _ := os.ReadFile(log)
	if strings.Count(string(raw), "tab create") != 2 {
		t.Fatalf("did not reopen an exited client: %s", raw)
	}
}

func TestEnterHerdrRemotePrivateWorkerKeepsCredentialsLocal(t *testing.T) {
	remoteBin, remoteLog := fakeOpenHerdr(t)
	localBin, localLog := fakeOpenHerdr(t)
	g := startGateway(t, t.TempDir(), "")
	bins := fakeBin(t, "opencode")
	bins["herdr"] = remoteBin
	remote, _, id := enterNode(t, g, "opencode", protocol.SessionInfo{NativeID: "ses_private", PID: 1, Cwd: t.TempDir(), Name: "worker"}, bins, nil)
	remote.d.wmu.Lock()
	remote.d.workers["w_test"] = &worker{id: "w_test", harness: "opencode", sessionID: id, apiBase: "http://127.0.0.1:5555", apiPass: "private-password"}
	remote.d.wmu.Unlock()
	local := startNode(t, g, nodeOpts{cfg: func(c *Config) { c.HarnessPaths = map[string]string{"herdr": localBin} }})
	waitSessionOnGateway(t, local, id)
	p := enterOfHerdr(t, local, id, fakeHerdrSocket(localLog))
	if !p.OpenedHerdr || p.Note != "opened worker in herdr" || len(p.Env) != 0 {
		t.Fatalf("plan %+v", p)
	}
	requireHerdrOpened(t, remoteLog, "worker", "--server", "--session", "ses_private")
	requireHerdrOpened(t, localLog, "worker", "--remote", remote.cfg.Hostname)
	remoteCalls, _ := os.ReadFile(remoteLog)
	localCalls, _ := os.ReadFile(localLog)
	raw, _ := json.Marshal(p)
	if strings.Contains(string(raw)+string(remoteCalls)+string(localCalls), "private-password") {
		t.Fatal("private worker password escaped the target machine")
	}
}

func TestEnterHerdrRemotePrivateCodexKeepsTokenLocal(t *testing.T) {
	remoteBin, remoteLog := fakeOpenHerdr(t)
	localBin, localLog := fakeOpenHerdr(t)
	g := startGateway(t, t.TempDir(), "")
	bins := fakeBin(t, "codex")
	bins["herdr"] = remoteBin
	remote, _, id := enterNode(t, g, "codex", protocol.SessionInfo{NativeID: "thread_1", PID: 1, Cwd: t.TempDir(), Name: "worker"}, bins, nil)
	w := &worker{id: "w_test", harness: "codex", sessionID: id, ref: "127.0.0.1:5555"}
	remote.d.wmu.Lock()
	remote.d.workers[w.id] = w
	remote.d.wmu.Unlock()
	dir := remote.d.setWorkerDir(w)
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "token"), []byte("private-token"), 0600); err != nil {
		t.Fatal(err)
	}
	local := startNode(t, g, nodeOpts{cfg: func(c *Config) { c.HarnessPaths = map[string]string{"herdr": localBin} }})
	waitSessionOnGateway(t, local, id)
	p := enterOfHerdr(t, local, id, fakeHerdrSocket(localLog))
	if !p.OpenedHerdr || len(p.Env) != 0 {
		t.Fatalf("plan %+v", p)
	}
	requireHerdrOpened(t, remoteLog, "worker", "resume", "thread_1", "--remote-auth-token-env")
	requireHerdrOpened(t, localLog, "worker", "--remote", remote.cfg.Hostname)
	remoteCalls, _ := os.ReadFile(remoteLog)
	localCalls, _ := os.ReadFile(localLog)
	raw, _ := json.Marshal(p)
	if strings.Contains(string(raw)+string(remoteCalls)+string(localCalls), "private-token") {
		t.Fatal("private token escaped the target machine")
	}
}

func TestCTOEnterHerdrReportsConversationRestart(t *testing.T) {
	fakeOpenHerdr(t)
	p := (&Daemon{}).openInHerdr("cto", "CTO", "", "", protocol.EnterPlan{Argv: []string{"claude", "attach", "job"}, Note: claudeCTORestartNotice})
	if !p.OpenedHerdr || !strings.Contains(p.Note, "opened CTO in herdr") || !strings.Contains(p.Note, claudeCTORestartNotice) {
		t.Fatalf("restart notice lost: %+v", p)
	}
}

func TestCTOSessionProposalRemoteStartOpensLocalHerdr(t *testing.T) {
	remoteBin, remoteLog := fakeOpenHerdr(t)
	localBin, localLog := fakeOpenHerdr(t)
	g := startGateway(t, t.TempDir(), "")
	pathTestHome(t)
	sessions, _ := useFakeClaude(t)
	local := startWorkerNode(t, g, shortSet, func(c *Config) { c.HarnessPaths = map[string]string{"herdr": localBin} })
	remote := startWorkerNode(t, g, shortSet, func(c *Config) {
		c.ClaudeSessionsDir = sessions
		c.Adapters = append(c.Adapters, &fsClaudeAdapter{dir: sessions})
		c.HarnessPaths = map[string]string{"claude": os.Args[0], "herdr": remoteBin}
	})
	repo := newRepo(t, "garden")
	remote.register(t, repo)
	t.Cleanup(func() { killFakeClaude(t, repo) })
	args, _ := json.Marshal(map[string]any{"action": "session_start", "project": "garden", "node_id": remote.d.NodeID(), "harness": "claude", "first_message": "hello"})
	if _, err := local.d.runCTOToolRaw(context.Background(), "session_change", args); err != nil {
		t.Fatal(err)
	}
	items, err := local.d.proposalRunner().list()
	if err != nil || len(items) != 1 {
		t.Fatalf("proposals %v %v", items, err)
	}
	local.cli.mustCall("proposal_answer", inHerdrAnswer(protocol.CTOProposalAnswer{ID: items[0].ID, Action: "a"}, fakeHerdrSocket(localLog)))
	eventually(t, "remote proposal opened locally", func() bool { data, _ := os.ReadFile(localLog); return strings.Contains(string(data), "pane run w1:p1") })
	if raw, _ := os.ReadFile(remoteLog); strings.Contains(string(raw), "pane run") || strings.Contains(string(raw), "tab create") {
		t.Fatalf("remote proposal opened a view there: %s", raw)
	}
	requireHerdrOpened(t, localLog, "garden", "ssh", remote.d.cfg.Hostname)
}

func TestEnterWithoutHerdrOrTerminalExplainsChecks(t *testing.T) {
	withoutLocalHerdr(t)
	fakeTmux(t, "")
	g := startGateway(t, t.TempDir(), "")
	n, _, id := enterNode(t, g, "pi", protocol.SessionInfo{NativeID: "pi-1", PID: 1, Name: "garden"}, nil, nil)
	p := enterOf(t, n, id, "")
	if p.OpenedHerdr || len(p.Argv) != 0 || !strings.Contains(strings.ToLower(p.Note), "herdr") || !strings.Contains(strings.ToLower(p.Note), "tmux") {
		t.Fatalf("note %+v", p)
	}
}

func TestSessionStartHerdrClaudeWithoutJobID(t *testing.T) {
	_, log := fakeOpenHerdr(t)
	t.Setenv("FAKE_CLAUDE_NO_JOB_ID", "1")
	g := startGateway(t, t.TempDir(), "")
	c := startClaudeNode(t, g, shortSet)
	repo := newRepo(t, "garden")
	c.register(t, repo)
	t.Cleanup(func() { killFakeClaude(t, repo) })
	var r protocol.StartedSession
	q := inHerdrStart(protocol.SessionStartRequest{Project: "garden", Harness: "claude"}, fakeHerdrSocket(log))
	mustJSON(t, c.cli.mustCall("session_start", q), &r)
	eventually(t, "started without job ID", func() bool {
		mustJSON(t, c.cli.mustCall("session_start_get", map[string]string{"id": r.ID}), &r)
		if r.Status == "failed" {
			t.Fatalf("start %+v", r)
		}
		return r.Status == "started"
	})
	if len(r.NativeID) < 8 {
		t.Fatalf("native ID %q", r.NativeID)
	}
	requireHerdrOpened(t, log, "garden", "attach "+r.NativeID[:8])
}

func TestSessionResumeHerdrUsesIDWhenNameMissing(t *testing.T) {
	_, log := fakeOpenHerdr(t)
	g := startGateway(t, t.TempDir(), "")
	c := startClaudeNode(t, g, shortSet)
	repo := newRepo(t, "garden")
	native := "11111111-0000-4000-8000-000000000000"
	t.Cleanup(func() { killFakeClaude(t, repo) })
	r, err := c.d.resumeAgent(context.Background(), protocol.AgentView{Harness: "claude", NativeID: native, Cwd: repo}, fakeHerdrSocket(log), testHerdrPane)
	if err != nil {
		t.Fatal(err)
	}
	eventually(t, "resumed unnamed session", func() bool {
		var err error
		r, err = c.d.sessionStartGet(context.Background(), r.ID)
		if err != nil || r.Status == "failed" {
			t.Fatalf("resume %+v %v", r, err)
		}
		return r.Status == "started"
	})
	requireHerdrOpened(t, log, native, "attach")
}

func TestCTOProposalApprovalSplitsBesideOwnerTUI(t *testing.T) {
	_, log := fakeOpenHerdr(t)
	g := startGateway(t, t.TempDir(), "")
	home := pathTestHome(t)
	c := startClaudeNode(t, g, shortSet)
	notices := herdrNoticeChannel(t, c.node)
	path := filepath.Join(home, "code", "garden")
	t.Cleanup(func() { killFakeClaude(t, path) })
	args, _ := json.Marshal(map[string]any{"action": "create", "path": path, "harness": "claude"})
	if _, err := c.d.runCTOToolRaw(context.Background(), "project_change", args); err != nil {
		t.Fatal(err)
	}
	items, err := c.d.proposalRunner().list()
	if err != nil || len(items) != 1 {
		t.Fatalf("proposal %v %v", items, err)
	}
	c.cli.mustCall("proposal_answer", map[string]string{"proposal_id": items[0].ID, "action": "a", "herdr_socket": "/tmp/herdr-test.sock", "herdr_pane_id": "w1:p0"})
	eventually(t, "approved session opened beside TUI", func() bool { raw, _ := os.ReadFile(log); return strings.Contains(string(raw), "pane run w1:p1") })
	raw, _ := os.ReadFile(log)
	if !strings.Contains(string(raw), "pane split --pane w1:p0") || strings.Contains(string(raw), "tab create") {
		t.Fatalf("calls %s", raw)
	}
	if q := waitHerdrNotice(t, notices); q["text"] != "opened garden in herdr" || q["herdr_opened"] != true {
		t.Fatalf("notice %v", q)
	}
}

func TestRemoteCTOProposalApprovalOpensOwnerHerdr(t *testing.T) {
	testRemoteCTOProposalApproval(t, false)
}

func TestRemoteCTOProposalApproveAllOpensOwnerHerdr(t *testing.T) {
	testRemoteCTOProposalApproval(t, true)
}

func testRemoteCTOProposalApproval(t *testing.T, all bool) {
	t.Helper()
	remoteBin, remoteLog := fakeOpenHerdr(t)
	localBin, localLog := fakeOpenHerdr(t)
	g := startGateway(t, t.TempDir(), "")
	home := pathTestHome(t)
	sessions, _ := useFakeClaude(t)
	local := startWorkerNode(t, g, shortSet, func(c *Config) { c.HarnessPaths = map[string]string{"herdr": localBin} })
	notices := herdrNoticeChannel(t, local.node)
	remote := startWorkerNode(t, g, shortSet, func(c *Config) {
		c.ClaudeSessionsDir = sessions
		c.Adapters = append(c.Adapters, &fsClaudeAdapter{dir: sessions})
		c.HarnessPaths = map[string]string{"claude": os.Args[0], "herdr": remoteBin}
	})
	path := filepath.Join(home, "code", "garden")
	t.Cleanup(func() { killFakeClaude(t, path) })
	args, _ := json.Marshal(map[string]any{"action": "create", "path": path, "harness": "claude"})
	if _, err := remote.d.runCTOToolRaw(context.Background(), "project_change", args); err != nil {
		t.Fatal(err)
	}
	items, err := remote.d.proposalRunner().list()
	if err != nil || len(items) != 1 {
		t.Fatalf("proposal %v %v", items, err)
	}
	socket := fakeHerdrSocket(localLog)
	if all {
		local.cli.mustCall("proposal_answer", inHerdrAnswer(protocol.CTOProposalAnswer{Action: "A"}, socket))
	} else {
		local.cli.mustCall("proposal_answer", inHerdrAnswer(protocol.CTOProposalAnswer{NodeID: remote.d.NodeID(), ID: items[0].ID, Action: "a"}, socket))
	}
	eventually(t, "remote proposal opened on owner", func() bool { raw, _ := os.ReadFile(localLog); return strings.Contains(string(raw), "pane run w1:p1") })
	if raw, _ := os.ReadFile(remoteLog); strings.Contains(string(raw), "pane run") || strings.Contains(string(raw), "tab create") {
		t.Fatalf("remote proposal opened a view there: %s", raw)
	}
	requireHerdrOpened(t, localLog, "garden", "ssh", remote.d.cfg.Hostname)
	if q := waitHerdrNotice(t, notices); q["text"] != "opened garden in herdr" || q["herdr_opened"] != true {
		t.Fatalf("notice %v", q)
	}
}

func TestCTOProposalWithoutHerdrShowsInstallNotice(t *testing.T) {
	withoutLocalHerdr(t)
	g := startGateway(t, t.TempDir(), "")
	home := pathTestHome(t)
	c := startClaudeNode(t, g, shortSet)
	notices := herdrNoticeChannel(t, c.node)
	path := filepath.Join(home, "code", "garden")
	t.Cleanup(func() { killFakeClaude(t, path) })
	args, _ := json.Marshal(map[string]any{"action": "create", "path": path, "harness": "claude"})
	if _, err := c.d.runCTOToolRaw(context.Background(), "project_change", args); err != nil {
		t.Fatal(err)
	}
	items, err := c.d.proposalRunner().list()
	if err != nil || len(items) != 1 {
		t.Fatalf("proposals %v %v", items, err)
	}
	c.cli.mustCall("proposal_answer", map[string]string{"proposal_id": items[0].ID, "action": "a"})
	q := waitHerdrNotice(t, notices)
	text, _ := q["text"].(string)
	if q["daemon_notice"] != true || !strings.Contains(text, "Herdr was not found") || !strings.Contains(text, "install") {
		t.Fatalf("notice %v", q)
	}
}

func TestCTOApprovedCodexWithoutHerdrStartsInApprovingTUI(t *testing.T) {
	withoutLocalHerdr(t)
	g := startGateway(t, t.TempDir(), "")
	home := pathTestHome(t)
	bins := fakeBin(t, "codex")
	n := startWorkerNode(t, g, shortSet, func(c *Config) { c.HarnessPaths = bins })
	repo := newHomeRepo(t, home, "code/garden")
	n.register(t, repo)
	other := herdrNoticeChannel(t, n.node)
	approver, notices := herdrUIClient(t, n.node)
	args, _ := json.Marshal(map[string]any{"action": "session_start", "project": "garden", "harness": "codex", "first_message": "hello"})
	if _, err := n.d.runCTOToolRaw(context.Background(), "session_change", args); err != nil {
		t.Fatal(err)
	}
	items, err := n.d.proposalRunner().list()
	if err != nil || len(items) != 1 {
		t.Fatalf("proposals %v %v", items, err)
	}
	if _, err := approver.Call(context.Background(), "proposal_answer", inHerdrAnswer(protocol.CTOProposalAnswer{ID: items[0].ID, Action: "a"}, filepath.Join(t.TempDir(), "h.sock"))); err != nil {
		t.Fatal(err)
	}
	q := waitHerdrNotice(t, notices)
	plan, ok := q["start_plan"].(map[string]any)
	if !ok || q["start_name"] != "garden ("+n.d.hostName()+")" {
		t.Fatalf("approving TUI did not receive the start: %v", q)
	}
	argv, ok := plan["argv"].([]any)
	if !ok || len(argv) != 2 || argv[0] != bins["codex"] || argv[1] != "hello" || plan["dir"] != repo || !strings.Contains(plan["note"].(string), "Herdr was not found") {
		t.Fatalf("incorrect fallback plan: %v", plan)
	}
	select {
	case q := <-other:
		if q["start_plan"] != nil {
			t.Fatalf("another TUI received the same start: %v", q)
		}
	default:
	}
}

func TestRemoteCTOApprovedCodexWithoutHerdrStartsInApprovingTUI(t *testing.T) {
	for _, all := range []bool{false, true} {
		t.Run(fmt.Sprint("approve-all=", all), func(t *testing.T) {
			withoutLocalHerdr(t)
			g := startGateway(t, t.TempDir(), "")
			home := pathTestHome(t)
			local := startWorkerNode(t, g, shortSet, nil)
			bins := fakeBin(t, "codex")
			remote := startWorkerNode(t, g, shortSet, func(c *Config) { c.HarnessPaths = bins })
			repo := newHomeRepo(t, home, "code/garden")
			remote.register(t, repo)
			other := herdrNoticeChannel(t, local.node)
			approver, notices := herdrUIClient(t, local.node)
			args, _ := json.Marshal(map[string]any{"action": "session_start", "project": "garden", "harness": "codex", "first_message": "hello"})
			if _, err := remote.d.runCTOToolRaw(context.Background(), "session_change", args); err != nil {
				t.Fatal(err)
			}
			items, err := remote.d.proposalRunner().list()
			if err != nil || len(items) != 1 {
				t.Fatalf("proposals %v %v", items, err)
			}
			socket := filepath.Join(t.TempDir(), "h.sock")
			answer := inHerdrAnswer(protocol.CTOProposalAnswer{NodeID: remote.d.NodeID(), ID: items[0].ID, Action: "a"}, socket)
			if all {
				answer = inHerdrAnswer(protocol.CTOProposalAnswer{Action: "A"}, socket)
			}
			if _, err := approver.Call(context.Background(), "proposal_answer", answer); err != nil {
				t.Fatal(err)
			}
			q := waitHerdrNotice(t, notices)
			plan, ok := q["start_plan"].(map[string]any)
			if !ok || q["start_name"] != "garden ("+remote.d.hostName()+")" {
				t.Fatalf("approving TUI did not receive the remote start: %v", q)
			}
			argv, ok := plan["argv"].([]any)
			if !ok || len(argv) == 0 || argv[0] != "ssh" || !strings.Contains(fmt.Sprint(argv), bins["codex"]) {
				t.Fatalf("incorrect remote fallback plan: %v", plan)
			}
			select {
			case q := <-other:
				if q["start_plan"] != nil {
					t.Fatalf("another TUI received the same start: %v", q)
				}
			default:
			}
		})
	}
}

func TestHerdrCreateFailureDoesNotUseTerminalFallback(t *testing.T) {
	dir := t.TempDir()
	script := `#!/bin/sh
case "$1 $2" in
  "pane list") printf '%s\n' '{"result":{"panes":[]}}' ;;
  *) exit 1 ;;
esac
`
	if err := os.WriteFile(filepath.Join(dir, "herdr"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	p := (&Daemon{}).openInHerdr("a", "garden", "", "", protocol.EnterPlan{Argv: []string{"claude", "attach", "job"}})
	if p.OpenedHerdr || len(p.Argv) != 0 || !strings.Contains(p.Note, "could not create") {
		t.Fatalf("plan %+v", p)
	}
}

func TestHerdrEnvironmentFailureDoesNotUseTerminalFallback(t *testing.T) {
	fakeOpenHerdr(t)
	p := (&Daemon{}).openInHerdr("a", "garden", "", "", protocol.EnterPlan{Argv: []string{"claude", "attach", "job"}, Env: map[string]string{"BAD-KEY": "value"}})
	if p.OpenedHerdr || len(p.Argv) != 0 || !strings.Contains(strings.ToLower(p.Note), "could not prepare") {
		t.Fatalf("plan %+v", p)
	}
}
