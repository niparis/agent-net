package daemon

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

// Entering a session (decision 0002, W16): the daemon plans the command, the
// TUI runs it. These tests check the plan for every kind of session, with fake
// executables, and never start a real harness.

// enterAdapter is a fake Claude adapter that knows the kind of its sessions.
type enterAdapter struct {
	*fakeAdapter
	kind, job string
}

func (a *enterAdapter) EnterRef(string) (string, string) { return a.kind, a.job }

func fakeBin(t *testing.T, names ...string) map[string]string {
	t.Helper()
	dir := t.TempDir()
	out := map[string]string{}
	for _, n := range names {
		p := filepath.Join(dir, n)
		if err := os.WriteFile(p, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
			t.Fatal(err)
		}
		out[n] = p
	}
	return out
}

// fakeTmux puts a tmux on PATH that prints the given list-panes output and
// writes its arguments to the returned file.
func fakeTmux(t *testing.T, panes string) (argsFile string) {
	t.Helper()
	dir := t.TempDir()
	argsFile = filepath.Join(dir, "args")
	script := "#!/bin/sh\necho \"$@\" >> " + argsFile + "\ncat <<'EOF'\n" + panes + "\nEOF\n"
	if err := os.WriteFile(filepath.Join(dir, "tmux"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return argsFile
}

// enterNode starts a node with one session of the given harness and returns
// the node and the session's gateway ID.
func enterNode(t *testing.T, g *testGateway, harness string, info protocol.SessionInfo, bins map[string]string, mutate func(*Config)) (*node, *fakeAdapter, string) {
	t.Helper()
	fa := &fakeAdapter{harness: harness}
	n := startNode(t, g, nodeOpts{adapter: fa, cfg: func(c *Config) {
		c.HarnessPaths = bins
		if mutate != nil {
			mutate(c)
		}
	}})
	info.Harness = harness
	fa.setSessions(info)
	var id string
	eventually(t, "session has a gateway id", func() bool {
		for _, s := range n.d.Sessions() {
			if s.SessionID != "" {
				id = s.SessionID
			}
		}
		return id != ""
	})
	return n, fa, id
}

func enterOf(t *testing.T, n *node, id, tmuxSocket string) protocol.EnterPlan {
	t.Helper()
	raw, err := cliClient(t, n).call("session_enter", protocol.EnterRequest{SessionID: id, TmuxSocket: tmuxSocket})
	if err != nil {
		t.Fatalf("session_enter: %v", err)
	}
	var p protocol.EnterPlan
	if err := json.Unmarshal(raw, &p); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestEnterClaudeBackgroundSessionAttaches(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	bins := fakeBin(t, "claude")
	cwd := t.TempDir()
	fa := &fakeAdapter{harness: "claude"}
	ea := &enterAdapter{fakeAdapter: fa, kind: "bg", job: "abcd1234"}
	n := startNode(t, g, nodeOpts{adapter: nil, cfg: func(c *Config) {
		c.HarnessPaths = bins
		c.Adapters = []Adapter{ea}
	}})
	ea.setSessions(protocol.SessionInfo{Harness: "claude", NativeID: "abcd1234-0000-4000-8000-000000000000", PID: 1, Cwd: cwd, Name: "bgjob", Status: "idle", Mode: protocol.ModeBackground})
	var id string
	eventually(t, "session id", func() bool {
		if s := n.d.Sessions(); len(s) == 1 {
			id = s[0].SessionID
		}
		return id != ""
	})
	p := enterOf(t, n, id, "")
	if want := []string{bins["claude"], "attach", "abcd1234"}; !reflect.DeepEqual(p.Argv, want) {
		t.Fatalf("argv %v, want %v", p.Argv, want)
	}
	if p.Dir != cwd || len(p.Env) != 0 {
		t.Fatalf("dir %q env %v", p.Dir, p.Env)
	}

	// Without a job ID in the session file, the ID is the first 8 characters of the session ID.
	ea.job = ""
	if p := enterOf(t, n, id, ""); p.Argv[len(p.Argv)-1] != "abcd1234" {
		t.Fatalf("fallback id: %v", p.Argv)
	}
}

func TestEnterTmuxPane(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	// This test process is the session's process, and the pane's shell.
	argsFile := fakeTmux(t, "99999|%9|other\n"+itoa(os.Getpid())+"|%7|work")
	ea := &enterAdapter{fakeAdapter: &fakeAdapter{harness: "claude"}, kind: "interactive"}
	n := startNode(t, g, nodeOpts{cfg: func(c *Config) { c.Adapters = []Adapter{ea} }})
	ea.setSessions(protocol.SessionInfo{Harness: "claude", NativeID: "i1", PID: os.Getpid(), Cwd: "/nope", Name: "inter", Status: "idle", Mode: protocol.ModeForeground})
	var id string
	eventually(t, "session id", func() bool {
		if s := n.d.Sessions(); len(s) == 1 {
			id = s[0].SessionID
		}
		return id != ""
	})

	// The TUI is not in tmux: attach a client and show the pane.
	p := enterOf(t, n, id, "")
	want := []string{"tmux", "select-window", "-t", "%7", ";", "select-pane", "-t", "%7", ";", "attach-session", "-t", "work"}
	if !reflect.DeepEqual(p.Argv, want) {
		t.Fatalf("not in tmux: argv %v, want %v", p.Argv, want)
	}
	// The TUI is in tmux: switch its client to the pane.
	p = enterOf(t, n, id, "/tmp/tmux-1000/default")
	if want := []string{"tmux", "switch-client", "-t", "%7"}; !reflect.DeepEqual(p.Argv, want) {
		t.Fatalf("in tmux: argv %v, want %v", p.Argv, want)
	}
	if b, _ := os.ReadFile(argsFile); !strings.Contains(string(b), "list-panes -a") {
		t.Fatalf("tmux was asked: %q", b)
	}
}

// A session whose own $TMUX names another server gets that server's socket, and
// a TUI in a different tmux gets a new client, not a nested one.
func TestEnterTmuxOtherServer(t *testing.T) {
	cmd := exec.Command("sleep", "30")
	cmd.Env = append(os.Environ(), "TMUX=/srv/sess.sock,123,0")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cmd.Process.Kill(); cmd.Wait() })
	argsFile := fakeTmux(t, itoa(cmd.Process.Pid)+"|%3|dev")
	pane, ok := findTmuxPane(cmd.Process.Pid)
	if !ok || pane.socket != "/srv/sess.sock" || pane.id != "%3" || pane.session != "dev" {
		t.Fatalf("pane %+v ok=%v", pane, ok)
	}
	if b, _ := os.ReadFile(argsFile); !strings.HasPrefix(string(b), "-S /srv/sess.sock list-panes") {
		t.Fatalf("tmux was asked: %q", b)
	}
	d := &Daemon{}
	p, _ := d.tmuxPlan(protocol.SessionInfo{PID: cmd.Process.Pid}, protocol.EnterRequest{TmuxSocket: "/other"}, protocol.EnterPlan{Host: "h"}, "s")
	want := []string{"env", "-u", "TMUX", "tmux", "-S", "/srv/sess.sock", "select-window", "-t", "%3", ";", "select-pane", "-t", "%3", ";", "attach-session", "-t", "dev"}
	if !reflect.DeepEqual(p.Argv, want) {
		t.Fatalf("argv %v, want %v", p.Argv, want)
	}
	p, _ = d.tmuxPlan(protocol.SessionInfo{PID: cmd.Process.Pid}, protocol.EnterRequest{TmuxSocket: "/srv/sess.sock"}, protocol.EnterPlan{}, "s")
	if want := []string{"tmux", "-S", "/srv/sess.sock", "switch-client", "-t", "%3"}; !reflect.DeepEqual(p.Argv, want) {
		t.Fatalf("same server: argv %v, want %v", p.Argv, want)
	}
}

func TestEnterWithoutAWayGivesANote(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	fakeTmux(t, "") // tmux is there, with no panes
	for _, harness := range []string{"pi", "claude"} {
		info := protocol.SessionInfo{NativeID: "x1", PID: os.Getpid(), Cwd: "/nope", Name: "win", Status: "idle"}
		n, _, id := enterNode(t, g, harness, info, nil, nil)
		p := enterOf(t, n, id, "")
		if len(p.Argv) != 0 || !strings.Contains(p.Note, "open its window on host-") {
			t.Errorf("%s: plan %+v", harness, p)
		}
		if p.Dir != "" {
			t.Errorf("a folder that does not exist was kept: %q", p.Dir)
		}
	}
}

func TestEnterOpenCodeSessionAndWorker(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	bins := fakeBin(t, "opencode")
	cwd := t.TempDir()
	n, _, id := enterNode(t, g, "opencode", protocol.SessionInfo{NativeID: "ses_1", PID: 1, Cwd: cwd, Name: "oc", Status: "idle"}, bins, nil)
	p := enterOf(t, n, id, "")
	if want := []string{bins["opencode"], "--session", "ses_1"}; !reflect.DeepEqual(p.Argv, want) || p.Dir != cwd || len(p.Env) != 0 {
		t.Fatalf("plain session: %+v", p)
	}

	// A worker lives in its own server. The password is in Env, and only for
	// the asker on this machine: a question from the gateway is refused.
	n.d.wmu.Lock()
	n.d.workers["w_1"] = &worker{id: "w_1", harness: "opencode", sessionID: id, apiBase: "http://127.0.0.1:5555", apiPass: "pw"}
	n.d.wmu.Unlock()
	p = enterOf(t, n, id, "")
	if want := []string{bins["opencode"], "--server", "http://127.0.0.1:5555", "--session", "ses_1"}; !reflect.DeepEqual(p.Argv, want) {
		t.Fatalf("worker argv %v", p.Argv)
	}
	if p.Env["OPENCODE_SERVER_PASSWORD"] != "pw" {
		t.Fatalf("worker env %v", p.Env)
	}
	via, err := n.d.localEnter(context.Background(), protocol.EnterRequest{SessionID: id}, true)
	if err != nil || len(via.Argv) != 0 || len(via.Env) != 0 || !strings.Contains(via.Note, "own machine") {
		t.Fatalf("from the gateway: %+v %v", via, err)
	}
}

func TestEnterCodexWorker(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	bins := fakeBin(t, "codex")
	n, _, id := enterNode(t, g, "codex", protocol.SessionInfo{NativeID: "thr_1", PID: 1, Cwd: t.TempDir(), Name: "cx", Status: "idle"}, bins, nil)
	// Not started by agent-net: there is no way in.
	if p := enterOf(t, n, id, ""); len(p.Argv) != 0 || p.Note == "" {
		t.Fatalf("codex session without a worker: %+v", p)
	}
	w := &worker{id: "w_2", harness: "codex", sessionID: id, ref: "127.0.0.1:6000"}
	n.d.wmu.Lock()
	n.d.workers["w_2"] = w
	n.d.wmu.Unlock()
	dir := n.d.setWorkerDir(w)
	os.MkdirAll(dir, 0o700)
	os.WriteFile(filepath.Join(dir, "token"), []byte("tok\n"), 0o600)
	p := enterOf(t, n, id, "")
	want := []string{bins["codex"], "resume", "thr_1", "--remote", "ws://127.0.0.1:6000", "--remote-auth-token-env", "AGENT_NET_CODEX_TOKEN"}
	if !reflect.DeepEqual(p.Argv, want) || p.Env["AGENT_NET_CODEX_TOKEN"] != "tok" {
		t.Fatalf("codex worker: %+v", p)
	}
}

func TestEnterRefusesTheOwnerAndUnknownSessions(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	n := startNode(t, g, nodeOpts{})
	eventually(t, "owner", func() bool { return n.d.ownerSessionID() != "" })
	p := enterOf(t, n, n.d.ownerSessionID(), "")
	if len(p.Argv) != 0 || p.Note == "" {
		t.Fatalf("owner: %+v", p)
	}
	if _, err := cliClient(t, n).call("session_enter", protocol.EnterRequest{SessionID: "s_nope"}); err == nil {
		t.Fatal("an unknown session was planned")
	}
	if _, err := cliClient(t, n).call("session_enter", map[string]any{}); err == nil {
		t.Fatal("no session_id was accepted")
	}
}

func TestEnterOnAnotherMachineNeedsOptInForSSH(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	bins := fakeBin(t, "claude")
	cwd := t.TempDir()
	ea := &enterAdapter{fakeAdapter: &fakeAdapter{harness: "claude"}, kind: "bg", job: "deadbeef"}
	remote := startNode(t, g, nodeOpts{cfg: func(c *Config) { c.HarnessPaths = bins; c.Adapters = []Adapter{ea} }})
	ea.setSessions(protocol.SessionInfo{Harness: "claude", NativeID: "deadbeef-1", PID: 1, Cwd: cwd, Name: "far", Status: "idle", Mode: protocol.ModeBackground})
	var id string
	eventually(t, "remote session", func() bool {
		if s := remote.d.Sessions(); len(s) == 1 {
			id = s[0].SessionID
		}
		return id != ""
	})
	host := remote.cfg.Hostname
	cmdline := "cd " + cwd + " && exec " + bins["claude"] + " attach deadbeef"

	// No opt-in: no command to run here, the note gives the one to run there.
	local := startNode(t, g, nodeOpts{})
	eventually(t, "session on the gateway", func() bool {
		p := enterOfErr(local, id)
		return p != nil && p.Note != ""
	})
	p := enterOf(t, local, id, "/tmp/mytmux")
	if len(p.Argv) != 0 || !strings.Contains(p.Note, "On "+host+" run: "+cmdline) || !strings.Contains(p.Note, "enter_ssh") {
		t.Fatalf("without opt-in: %+v", p)
	}

	// Opted in: the same command through ssh -t, to the host the gateway lists.
	optIn := startNode(t, g, nodeOpts{cfg: func(c *Config) { c.EnterSSH = true }})
	p = enterOf(t, optIn, id, "")
	if want := []string{"ssh", "-t", "--", host, cmdline}; !reflect.DeepEqual(p.Argv, want) || p.Dir != "" {
		t.Fatalf("with opt-in: %+v", p)
	}
	// A per-host ssh target replaces the name; a bad one is refused.
	alias := startNode(t, g, nodeOpts{cfg: func(c *Config) { c.EnterSSH = true; c.EnterSSHHosts = map[string]string{host: "me@box"} }})
	if p = enterOf(t, alias, id, ""); p.Argv[3] != "me@box" {
		t.Fatalf("alias: %v", p.Argv)
	}
	bad := startNode(t, g, nodeOpts{cfg: func(c *Config) { c.EnterSSH = true; c.EnterSSHHosts = map[string]string{host: "-oProxyCommand=x"} }})
	if p = enterOf(t, bad, id, ""); len(p.Argv) != 0 || !strings.Contains(p.Note, "bad ssh target") {
		t.Fatalf("bad target: %+v", p)
	}
}

func enterOfErr(n *node, id string) *protocol.EnterPlan {
	lc := dialLocal(n.t, n.sock)
	if _, err := lc.call("hello", map[string]any{"client": "cli"}); err != nil {
		return nil
	}
	raw, err := lc.call("session_enter", protocol.EnterRequest{SessionID: id})
	if err != nil {
		return nil
	}
	var p protocol.EnterPlan
	if json.Unmarshal(raw, &p) != nil {
		return nil
	}
	return &p
}

func TestSSHHelpers(t *testing.T) {
	for _, ok := range []string{"box", "me@box.example", "10.0.0.2", "box_1"} {
		if !sshTargetOK(ok) {
			t.Errorf("%q refused", ok)
		}
	}
	for _, bad := range []string{"", "-oProxyCommand=x", "a b", "a;b", "$(x)", "box\n"} {
		if sshTargetOK(bad) {
			t.Errorf("%q passed", bad)
		}
	}
	got := remoteCommand("/a b/it's", []string{"/bin/claude", "attach", "x y"})
	if want := `cd '/a b/it'\''s' && exec /bin/claude attach 'x y'`; got != want {
		t.Errorf("remoteCommand = %q, want %q", got, want)
	}
}

func itoa(n int) string { b, _ := json.Marshal(n); return string(b) }
