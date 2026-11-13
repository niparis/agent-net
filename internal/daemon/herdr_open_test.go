package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/client"
	"github.com/niparis/agent-net/internal/protocol"
)

// A fake herdr records every command, prefixed with the socket it was sent
// to ([] for the default server). It does not start Herdr or a terminal. Its
// "agent-net" session runs once the daemon starts it; its socket is
// fakeOwnHerdrSocket.
func fakeOpenHerdr(t *testing.T) (string, string) {
	t.Helper()
	dir := t.TempDir()
	log := filepath.Join(dir, "calls")
	cleanupHerdrEnvironment(t, log)
	// A socket path must stay short (104 bytes on macOS), so the agent-net
	// session's folder is not under the test's long temporary folder.
	short, err := os.MkdirTemp("", "hs")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(short) })
	own := filepath.Join(short, "sessions", "agent-net", "herdr.sock")
	if err := os.WriteFile(filepath.Join(dir, "own-socket"), []byte(own), 0o600); err != nil {
		t.Fatal(err)
	}
	running := filepath.Join(dir, "own-running")
	created := filepath.Join(dir, "created")
	defaults := filepath.Join(dir, "default-config")
	if err := os.WriteFile(defaults, []byte(fakeHerdrDefaultConfig), 0o600); err != nil {
		t.Fatal(err)
	}
	script := `#!/bin/sh
printf '%s|%s\n' "$*" "$HERDR_CONFIG_PATH" >> ` + shellQuote(filepath.Join(dir, "env")) + `
printf '[%s] %s\n' "$HERDR_SOCKET_PATH" "$*" >> ` + shellQuote(log) + `
case "$1 $2" in
  "--default-config ") cat ` + shellQuote(defaults) + ` ;;
  "session list") if [ -f ` + shellQuote(running) + ` ]; then r=true; else r=false; fi
    printf '{"sessions":[{"name":"default","running":true,"socket_path":"/nonexistent/herdr.sock"},{"name":"agent-net","running":%s,"socket_path":"%s"}]}\n' "$r" ` + shellQuote(own) + ` ;;
  "--session agent-net") [ "$3" = server ] && : > ` + shellQuote(running) + ` ;;
  "pane list") if [ -f ` + shellQuote(created) + ` ]; then printf '%s\n' '{"result":{"panes":[{"pane_id":"w1:p1","tab_id":"w1:t1"}]}}'; else printf '%s\n' '{"result":{"panes":[]}}'; fi ;;
  "pane process-info") if [ -f ` + shellQuote(filepath.Join(dir, "info")) + ` ]; then cat ` + shellQuote(filepath.Join(dir, "info")) + `; elif [ -f ` + shellQuote(filepath.Join(dir, "idle")) + ` ]; then printf '%s\n' '{"result":{"process_info":{"shell_pid":11,"foreground_processes":[{"pid":11}]}}}'; else printf '%s\n' '{"result":{"process_info":{"shell_pid":11,"foreground_processes":[{"pid":22}]}}}'; fi ;;
  "workspace list") if [ -f ` + shellQuote(created) + ` ]; then printf '%s\n' '{"result":{"workspaces":[{"workspace_id":"w1"}]}}'; else printf '%s\n' '{"result":{"workspaces":[]}}'; fi ;;
  "tab create"|"workspace create") : > ` + shellQuote(created) + `; printf '%s\n' '{"result":{"tab":{"tab_id":"w1:t1"},"root_pane":{"pane_id":"w1:p1","terminal_id":"term_1"}}}' ;;
  "pane run"|"pane rename"|"agent focus"|"tab focus"|"pane close") printf '%s\n' '{"result":{}}' ;;
  "server --handoff-import") ;;
  *) exit 1 ;;
esac
`
	if err := os.WriteFile(filepath.Join(dir, "herdr"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return filepath.Join(dir, "herdr"), log
}

// fakeHerdrDefaultConfig is what the fake prints for `herdr --default-config`:
// a herdr that knows the sidebar setting.
const fakeHerdrDefaultConfig = "[ui]\n# sidebar_collapsed_mode = \"compact\"\n"

// fakeHerdrEnvLog is the fake's record of each call with the HERDR_CONFIG_PATH
// it ran with, one line per call as "args|config path".
func fakeHerdrEnvLog(log string) string {
	return filepath.Join(filepath.Dir(log), "env")
}

// fakeOwnHerdrSocket is the socket of the fake's "agent-net" session.
func fakeOwnHerdrSocket(log string) string {
	raw, _ := os.ReadFile(filepath.Join(filepath.Dir(log), "own-socket"))
	return string(raw)
}

// herdrCalls splits the fake's log into the socket and the command of each call.
func herdrCalls(t *testing.T, log string) [][2]string {
	t.Helper()
	raw, _ := os.ReadFile(log)
	var calls [][2]string
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		socket, command, ok := strings.Cut(strings.TrimPrefix(line, "["), "] ")
		if ok {
			calls = append(calls, [2]string{socket, command})
		}
	}
	return calls
}

// requireHerdrRules checks the two rules of decision 0154 on the fake's log:
// a herdr agent-net does not own gets only reads (pane list, process-info;
// session list and the start of agent-net's session do not go to a server),
// and anything agent-net creates or runs goes to its own server.
func requireHerdrRules(t *testing.T, log string) {
	t.Helper()
	own := fakeOwnHerdrSocket(log)
	for _, c := range herdrCalls(t, log) {
		socket, command := c[0], c[1]
		if socket == own {
			continue
		}
		read := false
		// --default-config prints herdr's defaults and reaches no server.
		for _, ok := range []string{"pane list", "pane process-info", "session list", "--session agent-net server", "--default-config"} {
			read = read || strings.HasPrefix(command, ok)
		}
		if !read {
			t.Errorf("herdr %q got %q; only agent-net's own server may get it", socket, command)
		}
	}
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

// requireHerdrOpened checks that agent-net hosted a command in its own herdr
// server: a tab or workspace titled title, and the command run in its pane.
func requireHerdrOpened(t *testing.T, log, title string, command ...string) {
	t.Helper()
	calls := string(mustRead(t, log))
	requireHerdrRules(t, log)
	if !hasOwnHerdrCall(t, log, "tab create --label "+title) && !hasOwnHerdrCall(t, log, "workspace create --label "+title) {
		t.Errorf("herdr calls %q do not create %q in agent-net's herdr", calls, title)
	}
	if !hasOwnHerdrCall(t, log, "pane run w1:p1") {
		t.Errorf("herdr calls %q do not run in agent-net's herdr", calls)
	}
	for _, want := range command {
		if !strings.Contains(calls, want) {
			t.Errorf("herdr calls %q do not include %q", calls, want)
		}
	}
	if strings.Contains(calls, "CLAUDE_CODE_DISABLE_MOUSE") {
		t.Errorf("mouse disabled in herdr calls: %q", calls)
	}
}

// requireNoHerdrCalls checks that a handover left herdr alone.
func requireNoHerdrCalls(t *testing.T, log string) {
	t.Helper()
	if calls := herdrCalls(t, log); len(calls) != 0 {
		t.Fatalf("herdr was called: %v", calls)
	}
}

// The CTO opens like any session: the TUI in the owner's herdr hands over
// its pane.
func TestEnterCTOInOwnerHerdrHandsOverPane(t *testing.T) {
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
			want := "--server"
			if harness == "claude" {
				want = "attach " + n.d.claudeCTO.ref
			}
			if p.OpenedHerdr || !strings.Contains(strings.Join(p.Argv, " "), want) {
				t.Errorf("plan %+v, want %q", p, want)
			}
			requireNoHerdrCalls(t, log)
		})
	}
}

// A worker opens no view by itself: a view would take the owner's focus and,
// for Codex, run its self-updater. Enter hands the TUI's pane to it.
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
			argv := strings.Join(plan.Argv, " ")
			want := map[string]string{"claude": "attach", "opencode": "--session " + info.NativeID, "codex": "resume " + info.NativeID}[harness]
			if plan.OpenedHerdr || !strings.Contains(argv, want) {
				t.Fatalf("enter plan %+v, want %q", plan, want)
			}
			requireNoHerdrCalls(t, log)
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
	if want := []string{localBin, "--remote", remote.d.hostName(), "--session", "agent-net"}; !reflect.DeepEqual(p.Argv, want) {
		t.Fatalf("remote worker plan %+v, want argv %v", p, want)
	}
	agent := remote.agentOf(t, r.SessionID)
	if agent == nil {
		t.Fatal("worker absent from gateway")
	}
	// The remote machine hosts one view of the session in agent-net's herdr.
	requireHerdrOpened(t, remoteLog, agent.Profile.Name)
	requireHerdrRules(t, localLog)
	if raw := string(mustRead(t, remoteLog)); strings.Count(raw, "pane run w1:p1") != 1 || strings.Count(raw, "create --label") != 1 {
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

// A start keeps the session in the daemon and opens no view, whether the TUI
// runs in the owner's herdr or in a plain terminal: Enter opens it later.
func TestSessionStartCodexOpensNoView(t *testing.T) {
	for _, inHerdr := range []bool{false, true} {
		t.Run(map[bool]string{false: "outside herdr", true: "in the owner's herdr"}[inHerdr], func(t *testing.T) {
			bins := fakeBin(t, "codex")
			_, log := fakeOpenHerdr(t)
			g := startGateway(t, t.TempDir(), "")
			f := startFakeShared(t)
			n := startWorkerNode(t, g, shortSet, func(c *Config) { c.HarnessPaths = bins; c.CodexHome = f.home; c.CodexInterval = 20 * time.Millisecond })
			repo := newRepo(t, "garden")
			n.register(t, repo)
			q := protocol.SessionStartRequest{Project: repo, Harness: "codex", FirstMessage: "hello"}
			if inHerdr {
				q = inHerdrStart(q, fakeHerdrSocket(log))
			}
			var r protocol.StartedSession
			mustJSON(t, n.cli.mustCall("session_start", q), &r)
			if r.Status != "started" || r.EnterPlan != nil || r.EnterNote != "Enter opens it." {
				t.Fatalf("start %+v", r)
			}
			requireNoHerdrCalls(t, log)
		})
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
	trustClaudeFolder(t, repo)
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

// A Claude start or resume from a TUI in the owner's herdr opens nothing
// there; Enter opens it.
func TestSessionStartAndResumeClaudeInOwnerHerdr(t *testing.T) {
	for _, resume := range []bool{false, true} {
		t.Run(map[bool]string{false: "start", true: "resume"}[resume], func(t *testing.T) {
			_, log := fakeOpenHerdr(t)
			g := startGateway(t, t.TempDir(), "")
			c := startClaudeNode(t, g, shortSet)
			repo := newRepo(t, "garden")
			c.register(t, repo)
			trustClaudeFolder(t, repo)
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
			if r.EnterPlan != nil || r.EnterNote != "Enter opens it." {
				t.Fatalf("start %+v", r)
			}
			requireNoHerdrCalls(t, log)
		})
	}
}

// A second Enter on a session of another machine reuses the view that
// machine hosts in agent-net's herdr instead of making another one.
func TestEnterRemoteViewReusesAgentNetTab(t *testing.T) {
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
	want := []string{localBin, "--remote", remote.cfg.Hostname, "--session", "agent-net"}
	for i := 0; i < 2; i++ {
		if p := enterOfHerdr(t, local, id, fakeHerdrSocket(localLog)); !reflect.DeepEqual(p.Argv, want) {
			t.Fatalf("plan %d %+v, want argv %v", i, p, want)
		}
	}
	requireHerdrOpened(t, remoteLog, "garden", "attach", "job_42")
	requireNoHerdrCalls(t, localLog)
	if raw := string(mustRead(t, remoteLog)); strings.Count(raw, "create --label") != 1 || !hasOwnHerdrCall(t, remoteLog, "tab focus w1:t1") {
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
	f := startFakeShared(t)
	remote := startWorkerNode(t, g, shortSet, func(c *Config) { c.HarnessPaths = bins; c.CodexHome = f.home; c.CodexInterval = 20 * time.Millisecond })
	repo := newRepo(t, "garden")
	remote.register(t, repo)
	var r protocol.StartedSession
	q := inHerdrStart(protocol.SessionStartRequest{Project: "garden", Harness: "codex", NodeID: remote.d.NodeID()}, fakeHerdrSocket(localLog))
	mustJSON(t, local.cli.mustCall("session_start", q), &r)
	if want := []string{localBin, "--remote", remote.d.cfg.Hostname, "--session", "agent-net"}; r.EnterPlan == nil || !reflect.DeepEqual(r.EnterPlan.Argv, want) {
		t.Fatalf("remote start %+v, want argv %v", r.EnterPlan, want)
	}
	// A Codex thread has no name of its own, so the tab carries its ID, as when
	// the owner presses Enter on it.
	requireHerdrOpened(t, remoteLog, r.NativeID, bins["codex"])
	requireNoHerdrCalls(t, localLog)
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
  "--session agent-net") ` + map[bool]string{true: ": > " + shellQuote(marker), false: "exit 1"}[start] + ` ;;
  "session list") [ -f ` + shellQuote(marker) + ` ] && r=true || r=false; printf '{"sessions":[{"name":"agent-net","running":%s,"socket_path":"/tmp/an.sock"}]}\n' "$r" ;;
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
			p := (&Daemon{}).openInHerdr("session", "garden", protocol.EnterPlan{Argv: []string{"claude", "attach", "job"}, Note: "fallback"})
			calls, _ := os.ReadFile(log)
			if !strings.Contains(string(calls), "--session agent-net server\n") {
				t.Fatalf("no server start: %s", calls)
			}
			if start && (!p.OpenedHerdr || !strings.Contains(string(calls), "pane run w1:p1")) {
				t.Fatalf("plan %+v calls %s", p, calls)
			}
			if !start && (p.OpenedHerdr || len(p.Argv) != 3 || !strings.Contains(p.Note, "herdr at "+dir+"/herdr failed: the agent-net herdr server exited: exit status 1")) {
				t.Fatalf("fallback %+v", p)
			}
		})
	}
}

func TestHerdrKeepsCredentialsOutOfCommand(t *testing.T) {
	_, log := fakeOpenHerdr(t)
	secret := "a secret 'with spaces'"
	p := (&Daemon{}).openInHerdr("worker", "garden", protocol.EnterPlan{Argv: []string{"opencode", "--server", "http://127.0.0.1:9"}, Env: map[string]string{"OPENCODE_SERVER_PASSWORD": secret, "CLAUDE_CODE_DISABLE_MOUSE": "1"}})
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

func TestCTOSessionProposalStartsInBackground(t *testing.T) {
	_, log := fakeOpenHerdr(t)
	g := startGateway(t, t.TempDir(), "")
	home := pathTestHome(t)
	c := startClaudeNode(t, g, shortSet)
	path := filepath.Join(home, "code", "garden")
	args, _ := json.Marshal(map[string]any{"action": "create", "path": path, "harness": "claude", "first_message": "hello"})
	if _, err := legacyProposalFixture(context.Background(), c.d, "project_change", args); err != nil {
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
	p := (&Daemon{}).openInHerdr("a", "garden", protocol.EnterPlan{Argv: []string{bin, "a 'quoted' $prompt"}, Env: map[string]string{"TOKEN": "secret with ' quote"}})
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

func TestEnterHerdrReopensWhenClientExited(t *testing.T) {
	bin, log := fakeOpenHerdr(t)
	d := &Daemon{}
	plan := protocol.EnterPlan{Argv: []string{"claude", "attach", "job"}}
	if p := d.openInHerdr("session", "garden", plan); !p.OpenedHerdr {
		t.Fatalf("first plan %+v", p)
	}
	if err := os.WriteFile(filepath.Join(filepath.Dir(bin), "idle"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	old := d.herdrOpened["session"]
	old.started = time.Now().Add(-2 * time.Second)
	d.herdrOpened["session"] = old
	if p := d.openInHerdr("session", "garden", plan); !p.OpenedHerdr {
		t.Fatalf("second plan %+v", p)
	}
	raw, _ := os.ReadFile(log)
	if strings.Count(string(raw), "create --label") != 2 {
		t.Fatalf("did not reopen an exited client: %s", raw)
	}
}

// lastPaneRun returns the shell command the daemon typed into a pane, the way
// the pane's login shell would receive it.
func lastPaneRun(t *testing.T, log string) string {
	t.Helper()
	raw, _ := os.ReadFile(log)
	var line string
	for _, l := range strings.Split(string(raw), "\n") {
		if strings.Contains(l, "] pane run ") {
			line = l
		}
	}
	_, command, ok := strings.Cut(line, "pane run w1:p1 ")
	if !ok {
		t.Fatalf("no pane run in %s", raw)
	}
	return command
}

func TestEnterHerdrReusesViewAfterDaemonRestart(t *testing.T) {
	bin, log := fakeOpenHerdr(t)
	plan := protocol.EnterPlan{Argv: []string{"claude", "attach", "job"}}
	if p := (&Daemon{}).openInHerdr("session", "garden", plan); !p.OpenedHerdr {
		t.Fatalf("first plan %+v", p)
	}
	// Herdr keeps the pane; show it the way herdr does: the shell that runs
	// the viewer, with the words the daemon gave it.
	command := strings.Fields(lastPaneRun(t, log))
	marker := command[len(command)-2:]
	info := `{"result":{"process_info":{"shell_pid":11,"foreground_processes":[{"pid":22,"argv":["sh","-c","x","` + marker[0] + `","` + marker[1] + `"]}]}}}`
	if err := os.WriteFile(filepath.Join(filepath.Dir(bin), "info"), []byte(info), 0600); err != nil {
		t.Fatal(err)
	}
	// A new Daemon has no memory of the first tab, as after an upgrade.
	if p := (&Daemon{}).openInHerdr("session", "garden", plan); !p.OpenedHerdr || p.HerdrPane != "w1:p1" {
		t.Fatalf("second plan %+v", p)
	}
	raw, _ := os.ReadFile(log)
	// agent-net's own herdr makes its first tab with workspace create.
	if n := strings.Count(string(raw), "] tab create") + strings.Count(string(raw), "] workspace create"); n != 1 {
		t.Fatalf("created %d tabs: %s", n, raw)
	}
	if !strings.Contains(string(raw), "tab focus w1:t1") {
		t.Fatalf("did not focus the open tab: %s", raw)
	}
}

func TestEnterHerdrOtherSessionGetsOwnTab(t *testing.T) {
	bin, log := fakeOpenHerdr(t)
	plan := protocol.EnterPlan{Argv: []string{"claude", "attach", "job"}}
	if p := (&Daemon{}).openInHerdr("one", "garden", plan); !p.OpenedHerdr {
		t.Fatalf("first plan %+v", p)
	}
	command := strings.Fields(lastPaneRun(t, log))
	marker := command[len(command)-2:]
	info := `{"result":{"process_info":{"shell_pid":11,"foreground_processes":[{"pid":22,"argv":["sh","-c","x","` + marker[0] + `","` + marker[1] + `"]}]}}}`
	if err := os.WriteFile(filepath.Join(filepath.Dir(bin), "info"), []byte(info), 0600); err != nil {
		t.Fatal(err)
	}
	if p := (&Daemon{}).openInHerdr("two", "orchard", plan); !p.OpenedHerdr {
		t.Fatalf("second plan %+v", p)
	}
	raw, _ := os.ReadFile(log)
	if n := strings.Count(string(raw), "] tab create") + strings.Count(string(raw), "] workspace create"); n != 2 {
		t.Fatalf("a different session reused the tab: %s", raw)
	}
}

func TestEnterHerdrViewerExitClosesItsPane(t *testing.T) {
	for name, script := range map[string]string{"clean exit": "exit 0", "failure": "exit 3"} {
		t.Run(name, func(t *testing.T) {
			_, log := fakeOpenHerdr(t)
			dir := t.TempDir()
			viewer := filepath.Join(dir, "viewer")
			if err := os.WriteFile(viewer, []byte("#!/bin/sh\n"+script+"\n"), 0700); err != nil {
				t.Fatal(err)
			}
			if p := (&Daemon{}).openInHerdr("session", "garden", protocol.EnterPlan{Argv: []string{viewer}}); !p.OpenedHerdr {
				t.Fatalf("plan %+v", p)
			}
			raw, _ := os.ReadFile(log)
			if strings.Contains(string(raw), "] pane close") {
				t.Fatalf("closed the pane before the viewer ran: %s", raw)
			}
			// The pane's shell runs the typed command, then the viewer ends.
			if out, err := exec.Command("sh", "-c", lastPaneRun(t, log)).CombinedOutput(); err != nil {
				t.Fatalf("command failed: %s %v", out, err)
			}
			raw, _ = os.ReadFile(log)
			if !strings.Contains(string(raw), "] pane close w1:p1") {
				t.Fatalf("viewer exit left its pane open: %s", raw)
			}
		})
	}
}

func TestEnterHerdrConcurrentEntersCreateOneTab(t *testing.T) {
	_, log := fakeOpenHerdr(t)
	d := &Daemon{}
	plan := protocol.EnterPlan{Argv: []string{"claude", "attach", "job"}}
	var wg sync.WaitGroup
	for i := 0; i < 6; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if p := d.openInHerdr("session", "garden", plan); !p.OpenedHerdr {
				t.Errorf("plan %+v", p)
			}
		}()
	}
	wg.Wait()
	raw, _ := os.ReadFile(log)
	// agent-net's own herdr makes its first tab with workspace create.
	if n := strings.Count(string(raw), "] tab create") + strings.Count(string(raw), "] workspace create"); n != 1 {
		t.Fatalf("created %d tabs: %s", n, raw)
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
	if want := []string{localBin, "--remote", remote.cfg.Hostname, "--session", "agent-net"}; !reflect.DeepEqual(p.Argv, want) || len(p.Env) != 0 {
		t.Fatalf("plan %+v, want argv %v", p, want)
	}
	requireHerdrOpened(t, remoteLog, "worker", "--server", "--session", "ses_private")
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
	if want := []string{localBin, "--remote", remote.cfg.Hostname, "--session", "agent-net"}; !reflect.DeepEqual(p.Argv, want) || len(p.Env) != 0 {
		t.Fatalf("plan %+v, want argv %v", p, want)
	}
	requireHerdrOpened(t, remoteLog, "worker", "resume", "thread_1", "--remote-auth-token-env")
	remoteCalls, _ := os.ReadFile(remoteLog)
	localCalls, _ := os.ReadFile(localLog)
	raw, _ := json.Marshal(p)
	if strings.Contains(string(raw)+string(remoteCalls)+string(localCalls), "private-token") {
		t.Fatal("private token escaped the target machine")
	}
}

func TestCTOEnterHerdrReportsConversationRestart(t *testing.T) {
	fakeOpenHerdr(t)
	p := (&Daemon{}).openInHerdr("cto", "CTO", protocol.EnterPlan{Argv: []string{"claude", "attach", "job"}, Note: claudeCTORestartNotice})
	if !p.OpenedHerdr || !strings.Contains(p.Note, "Started CTO in agent-net's herdr.") || !strings.Contains(p.Note, claudeCTORestartNotice) {
		t.Fatalf("restart notice lost: %+v", p)
	}
}

func TestCTOSessionProposalRemoteStartsInBackground(t *testing.T) {
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
	trustClaudeFolder(t, repo)
	t.Cleanup(func() { killFakeClaude(t, repo) })
	args, _ := json.Marshal(map[string]any{"action": "session_start", "project": "garden", "node_id": remote.d.NodeID(), "harness": "claude", "first_message": "hello"})
	if _, err := legacyProposalFixture(context.Background(), local.d, "session_change", args); err != nil {
		t.Fatal(err)
	}
	items, err := local.d.proposalRunner().list()
	if err != nil || len(items) != 1 {
		t.Fatalf("proposals %v %v", items, err)
	}
	local.cli.mustCall("proposal_answer", inHerdrAnswer(protocol.CTOProposalAnswer{ID: items[0].ID, Action: "a"}, fakeHerdrSocket(localLog)))
	items, err = local.d.proposalRunner().list()
	if err != nil {
		t.Fatal(err)
	}
	var r protocol.StartedSession
	if err := json.Unmarshal(items[0].Result, &r); err != nil {
		t.Fatal(err)
	}
	eventually(t, "remote proposal starts", func() bool {
		var err error
		r, err = local.d.sessionStartGet(context.Background(), r.ID)
		return err == nil && r.Status == "started"
	})
	waitSessionOnGateway(t, local.node, r.SessionID)
	for _, log := range []string{localLog, remoteLog} {
		if raw, _ := os.ReadFile(log); strings.Contains(string(raw), "pane run") || strings.Contains(string(raw), "tab create") {
			t.Fatalf("proposal opened a view: %s", raw)
		}
	}
}

// A start the CTO asked for on another machine can fail there after the start
// was answered as starting. The machine that runs it logs and records the
// failure once. The machine the owner uses tells the owner, once.
func TestCTOSessionProposalRemoteStartFailureNotifiesOwner(t *testing.T) {
	remoteBin, _ := fakeOpenHerdr(t)
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
	trustClaudeFolder(t, repo)
	t.Cleanup(func() { killFakeClaude(t, repo) })
	notices := herdrNoticeChannel(t, local.node)
	t.Setenv(envFakeStartFails, "1")
	args, _ := json.Marshal(map[string]any{"action": "session_start", "project": "garden", "node_id": remote.d.NodeID(), "harness": "claude"})
	if _, err := legacyProposalFixture(context.Background(), local.d, "session_change", args); err != nil {
		t.Fatal(err)
	}
	items, err := local.d.proposalRunner().list()
	if err != nil || len(items) != 1 {
		t.Fatalf("proposals %v %v", items, err)
	}
	local.cli.mustCall("proposal_answer", inHerdrAnswer(protocol.CTOProposalAnswer{ID: items[0].ID, Action: "a"}, fakeHerdrSocket(localLog)))
	items, err = local.d.proposalRunner().list()
	if err != nil {
		t.Fatal(err)
	}
	var r protocol.StartedSession
	if err := json.Unmarshal(items[0].Result, &r); err != nil || r.ID == "" {
		t.Fatalf("approved start: %s %v", items[0].Result, err)
	}
	told := false
	eventually(t, "the owner is told the remote start failed", func() bool {
		select {
		case q := <-notices:
			if q["daemon_notice"] == true && strings.Contains(fmt.Sprint(q["text"]), "does not trust") {
				told = true
			}
		default:
		}
		return told
	})
	if lines := remote.logs.lines("event=session.start_failed", "start="+r.ID); len(lines) != 1 {
		t.Fatalf("want one failure log line on the machine that ran the start, got %q", remote.logs.String())
	}
	if lines := local.logs.lines("event=session.start_failed"); len(lines) != 0 {
		t.Fatalf("the requesting machine logged the failure too: %q", local.logs.String())
	}
}

func TestEnterWithoutHerdrOrTerminalExplainsChecks(t *testing.T) {
	withoutLocalHerdr(t)
	g := startGateway(t, t.TempDir(), "")
	n, _, id := enterNode(t, g, "pi", protocol.SessionInfo{NativeID: "pi-1", PID: 1, Name: "garden"}, nil, nil)
	p := enterOf(t, n, id, "")
	if p.OpenedHerdr || len(p.Argv) != 0 || !strings.Contains(strings.ToLower(p.Note), "herdr") {
		t.Fatalf("note %+v", p)
	}
}

// A start from the owner's herdr works when Claude Code reports no job ID.
func TestSessionStartClaudeWithoutJobIDInOwnerHerdr(t *testing.T) {
	_, log := fakeOpenHerdr(t)
	t.Setenv("FAKE_CLAUDE_NO_JOB_ID", "1")
	g := startGateway(t, t.TempDir(), "")
	c := startClaudeNode(t, g, shortSet)
	repo := newRepo(t, "garden")
	c.register(t, repo)
	trustClaudeFolder(t, repo)
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
	requireNoHerdrCalls(t, log)
}

func TestCTOProposalApprovalLeavesOwnerTUIViewUnchanged(t *testing.T) {
	_, log := fakeOpenHerdr(t)
	g := startGateway(t, t.TempDir(), "")
	home := pathTestHome(t)
	c := startClaudeNode(t, g, shortSet)
	notices := herdrNoticeChannel(t, c.node)
	path := filepath.Join(home, "code", "garden")
	t.Cleanup(func() { killFakeClaude(t, path) })
	args, _ := json.Marshal(map[string]any{"action": "create", "path": path, "harness": "claude"})
	if _, err := legacyProposalFixture(context.Background(), c.d, "project_change", args); err != nil {
		t.Fatal(err)
	}
	items, err := c.d.proposalRunner().list()
	if err != nil || len(items) != 1 {
		t.Fatalf("proposal %v %v", items, err)
	}
	c.cli.mustCall("proposal_answer", map[string]string{"proposal_id": items[0].ID, "action": "a", "herdr_socket": "/tmp/herdr-test.sock", "herdr_pane_id": "w1:p0"})
	q := waitHerdrNotice(t, notices)
	if q["text"] != "Enter opens it." || q["daemon_notice"] != true {
		t.Fatalf("notice %v", q)
	}
	if raw, _ := os.ReadFile(log); strings.Contains(string(raw), "pane run") || strings.Contains(string(raw), "pane split") || strings.Contains(string(raw), "tab create") {
		t.Fatalf("proposal opened a view: %s", raw)
	}
}

func TestRemoteCTOProposalApprovalBackground(t *testing.T) {
	testRemoteCTOProposalApproval(t, false)
}

func TestRemoteCTOProposalApproveAllBackground(t *testing.T) {
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
	remote := startWorkerNode(t, g, shortSet, func(c *Config) {
		c.ClaudeSessionsDir = sessions
		c.Adapters = append(c.Adapters, &fsClaudeAdapter{dir: sessions})
		c.HarnessPaths = map[string]string{"claude": os.Args[0], "herdr": remoteBin}
	})
	path := filepath.Join(home, "code", "garden")
	t.Cleanup(func() { killFakeClaude(t, path) })
	args, _ := json.Marshal(map[string]any{"action": "create", "path": path, "harness": "claude"})
	if _, err := legacyProposalFixture(context.Background(), remote.d, "project_change", args); err != nil {
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
	items, err = remote.d.proposalRunner().list()
	if err != nil {
		t.Fatal(err)
	}
	var r protocol.StartedSession
	if err := json.Unmarshal(items[0].Result, &r); err != nil {
		t.Fatal(err)
	}
	eventually(t, "remote proposal starts", func() bool {
		var err error
		r, err = remote.d.sessionStartGet(context.Background(), r.ID)
		return err == nil && r.Status == "started"
	})
	waitSessionOnGateway(t, local.node, r.SessionID)
	for _, log := range []string{localLog, remoteLog} {
		if raw, _ := os.ReadFile(log); strings.Contains(string(raw), "pane run") || strings.Contains(string(raw), "tab create") {
			t.Fatalf("proposal opened a view: %s", raw)
		}
	}
}

func TestCTOProposalWithoutHerdrShowsEnterNotice(t *testing.T) {
	withoutLocalHerdr(t)
	g := startGateway(t, t.TempDir(), "")
	home := pathTestHome(t)
	c := startClaudeNode(t, g, shortSet)
	notices := herdrNoticeChannel(t, c.node)
	path := filepath.Join(home, "code", "garden")
	t.Cleanup(func() { killFakeClaude(t, path) })
	args, _ := json.Marshal(map[string]any{"action": "create", "path": path, "harness": "claude"})
	if _, err := legacyProposalFixture(context.Background(), c.d, "project_change", args); err != nil {
		t.Fatal(err)
	}
	items, err := c.d.proposalRunner().list()
	if err != nil || len(items) != 1 {
		t.Fatalf("proposals %v %v", items, err)
	}
	c.cli.mustCall("proposal_answer", map[string]string{"proposal_id": items[0].ID, "action": "a"})
	q := waitHerdrNotice(t, notices)
	text, _ := q["text"].(string)
	if q["daemon_notice"] != true || text != "Enter opens it." {
		t.Fatalf("notice %v", q)
	}
}

// A Codex start needs no herdr pane and no approving TUI: the daemon creates
// the thread on the app server and sends the first message itself.
func TestCTOApprovedCodexWithoutHerdrNeedsNoTUI(t *testing.T) {
	withoutLocalHerdr(t)
	g := startGateway(t, t.TempDir(), "")
	home := pathTestHome(t)
	f := startFakeShared(t)
	bins := fakeBin(t, "codex")
	n := startWorkerNode(t, g, shortSet, func(c *Config) { c.HarnessPaths = bins; c.CodexHome = f.home; c.CodexInterval = 20 * time.Millisecond })
	repo := newHomeRepo(t, home, "code/garden")
	n.register(t, repo)
	approver, _ := herdrUIClient(t, n.node)
	args, _ := json.Marshal(map[string]any{"action": "session_start", "project": "garden", "harness": "codex", "first_message": "hello"})
	if _, err := legacyProposalFixture(context.Background(), n.d, "session_change", args); err != nil {
		t.Fatal(err)
	}
	items, err := n.d.proposalRunner().list()
	if err != nil || len(items) != 1 {
		t.Fatalf("proposals %v %v", items, err)
	}
	if _, err := approver.Call(context.Background(), "proposal_answer", protocol.CTOProposalAnswer{ID: items[0].ID, Action: "a"}); err != nil {
		t.Fatal(err)
	}
	items, err = n.d.proposalRunner().list()
	if err != nil || items[0].Status != "applied" {
		t.Fatalf("proposal: %+v error %v", items, err)
	}
	var start protocol.StartedSession
	if err := json.Unmarshal(items[0].Result, &start); err != nil || start.Status != "started" || start.NativeID == "" || start.SessionID == "" || start.Error != "" {
		t.Fatalf("start: %+v error %v", start, err)
	}
}

func TestHerdrCreateFailureDoesNotUseTerminalFallback(t *testing.T) {
	dir := t.TempDir()
	script := `#!/bin/sh
case "$1 $2" in
  "session list") printf '%s\n' '{"sessions":[{"name":"agent-net","running":true,"socket_path":"/tmp/an.sock"}]}' ;;
  "pane list") printf '%s\n' '{"result":{"panes":[]}}' ;;
  "workspace list") printf '%s\n' '{"result":{"workspaces":[{"workspace_id":"w1"}]}}' ;;
  *) exit 1 ;;
esac
`
	if err := os.WriteFile(filepath.Join(dir, "herdr"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	p := (&Daemon{}).openInHerdr("a", "garden", protocol.EnterPlan{Argv: []string{"claude", "attach", "job"}})
	if p.OpenedHerdr || len(p.Argv) != 0 || !strings.Contains(p.Note, "could not create") {
		t.Fatalf("plan %+v", p)
	}
}

func TestHerdrEnvironmentFailureDoesNotUseTerminalFallback(t *testing.T) {
	fakeOpenHerdr(t)
	p := (&Daemon{}).openInHerdr("a", "garden", protocol.EnterPlan{Argv: []string{"claude", "attach", "job"}, Env: map[string]string{"BAD-KEY": "value"}})
	if p.OpenedHerdr || len(p.Argv) != 0 || !strings.Contains(strings.ToLower(p.Note), "could not prepare") {
		t.Fatalf("plan %+v", p)
	}
}

func TestViewPlanHandsOverOrHostsInAgentNetHerdr(t *testing.T) {
	_, log := fakeOpenHerdr(t)
	d := &Daemon{}
	base := protocol.EnterPlan{Argv: []string{"claude", "attach", "job"}}
	handover := d.viewPlan("view", "garden", false, base)
	if handover.OpenedHerdr || !reflect.DeepEqual(handover.Argv, base.Argv) || len(herdrCalls(t, log)) != 0 {
		t.Fatalf("handover plan %+v, calls %v", handover, herdrCalls(t, log))
	}
	hosted := d.viewPlan("host", "garden", true, base)
	if !hosted.OpenedHerdr || hosted.HerdrSession != "agent-net" || hosted.HerdrPane != "w1:p1" {
		t.Fatalf("hosted plan %+v", hosted)
	}
	requireHerdrRules(t, log)
	if !hasOwnHerdrCall(t, log, "workspace create --label garden") || !hasOwnHerdrCall(t, log, "pane run w1:p1") {
		t.Fatalf("calls %v", herdrCalls(t, log))
	}
}
