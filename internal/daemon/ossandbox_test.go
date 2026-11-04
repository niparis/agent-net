package daemon

import (
	"encoding/json"
	"errors"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

// The bubblewrap command line of a worker, for each level. The order matters:
// bubblewrap applies the mounts in order, so the host is read-only first, the
// control folders are hidden next, and the writable and protected paths come
// last, with the agent-net socket bound after everything that could hide it.
func TestBwrapArgs(t *testing.T) {
	base := osSandboxPolicy{
		Network:  true,
		Hide:     []string{"/run/user/1000"},
		Tmp:      "/state/workers/w_1/tmp",
		Mask:     []string{"/run/docker.sock"},
		Visible:  []string{"/state/workers/w_1", "/repo/.git"},
		Writable: []string{"/wt", "/repo/.git/worktrees/w_1", "/repo/.git/objects"},
		ReadOnly: []mountPair{{Src: "/wt/.git", Dst: "/wt/.git"}},
		Socket:   "/run/user/1000/agent-net/daemon.sock",
		Dir:      "/wt",
	}
	sandboxed := []string{
		"--unshare-all", "--share-net",
		"--ro-bind", "/", "/", "--dev", "/dev", "--proc", "/proc",
		"--tmpfs", "/run/user/1000",
		"--bind", "/state/workers/w_1/tmp", "/tmp",
		"--ro-bind", "/dev/null", "/run/docker.sock",
		"--ro-bind", "/state/workers/w_1", "/state/workers/w_1",
		"--ro-bind", "/repo/.git", "/repo/.git",
		"--bind", "/wt", "/wt",
		"--bind", "/repo/.git/worktrees/w_1", "/repo/.git/worktrees/w_1",
		"--bind", "/repo/.git/objects", "/repo/.git/objects",
		"--ro-bind", "/wt/.git", "/wt/.git",
		"--bind", "/run/user/1000/agent-net/daemon.sock", "/run/user/1000/agent-net/daemon.sock",
		"--chdir", "/wt",
		"--", "/bin/pi", "--mode", "rpc",
	}
	noNet := base
	noNet.Network = false
	readOnly := base
	readOnly.Writable = []string{"/state/workers/w_1/pi-agent"}
	readOnly.ReadOnly = []mountPair{{Src: "/home/u/.pi/agent/extensions", Dst: "/state/workers/w_1/pi-agent/extensions"}}
	minimal := osSandboxPolicy{Network: true, Tmp: "/s/tmp", Dir: "/wt"}

	for _, tc := range []struct {
		name string
		p    osSandboxPolicy
		want []string
	}{
		{"sandboxed", base, sandboxed},
		{"no-network unshares the network", noNet, append([]string{"--unshare-all"}, sandboxed[2:]...)},
		{"read-only binds only private state", readOnly, []string{
			"--unshare-all", "--share-net",
			"--ro-bind", "/", "/", "--dev", "/dev", "--proc", "/proc",
			"--tmpfs", "/run/user/1000",
			"--bind", "/state/workers/w_1/tmp", "/tmp",
			"--ro-bind", "/dev/null", "/run/docker.sock",
			"--ro-bind", "/state/workers/w_1", "/state/workers/w_1",
			"--ro-bind", "/repo/.git", "/repo/.git",
			"--bind", "/state/workers/w_1/pi-agent", "/state/workers/w_1/pi-agent",
			"--ro-bind", "/home/u/.pi/agent/extensions", "/state/workers/w_1/pi-agent/extensions",
			"--bind", "/run/user/1000/agent-net/daemon.sock", "/run/user/1000/agent-net/daemon.sock",
			"--chdir", "/wt",
			"--", "/bin/pi", "--mode", "rpc",
		}},
		{"minimal", minimal, []string{
			"--unshare-all", "--share-net",
			"--ro-bind", "/", "/", "--dev", "/dev", "--proc", "/proc",
			"--bind", "/s/tmp", "/tmp",
			"--chdir", "/wt",
			"--", "/bin/pi", "--mode", "rpc",
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := bwrapArgs(tc.p, "/bin/pi", []string{"--mode", "rpc"})
			if !slices.Equal(got, tc.want) {
				t.Fatalf("bwrap args:\n got %q\nwant %q", got, tc.want)
			}
			// A worker outlives the daemon, and the daemon stops it through
			// its process group: neither flag may appear.
			for _, flag := range []string{"--die-with-parent", "--new-session", "--as-pid-1"} {
				if slices.Contains(got, flag) {
					t.Fatalf("bwrap args have %s: %q", flag, got)
				}
			}
		})
	}
}

// The policy level decides what is writable: read-only drops the worktree and
// the git folders, and keeps the harness's own private state.
func TestOSSandboxPolicyLevels(t *testing.T) {
	in := osSandboxInput{
		Level: protocol.WorkersSandboxed, WorkerDir: "/s/w_1", Path: "/wt", DotGit: "/wt/.git",
		GitCommon: "/repo/.git", GitRoots: []string{"/repo/.git/worktrees/w_1", "/repo/.git/objects"},
		State: []string{"/s/w_1/data"}, Socket: "/run/user/1000/agent-net/daemon.sock",
		RuntimeDir: "/run/user/1000", Masks: []string{"/run/docker.sock"},
	}
	p := buildOSSandboxPolicy(in)
	if !p.Network || p.Tmp != "/s/w_1/tmp" || p.Dir != "/wt" || p.Socket != in.Socket {
		t.Fatalf("policy: %+v", p)
	}
	if want := []string{"/wt", "/repo/.git/worktrees/w_1", "/repo/.git/objects", "/s/w_1/data"}; !slices.Equal(p.Writable, want) {
		t.Fatalf("writable %q, want %q", p.Writable, want)
	}
	if want := []string{"/s/w_1", "/repo/.git"}; !slices.Equal(p.Visible, want) {
		t.Fatalf("visible %q, want %q", p.Visible, want)
	}
	if want := []mountPair{{"/wt/.git", "/wt/.git"}}; !slices.Equal(p.ReadOnly, want) {
		t.Fatalf("read-only %q, want %q", p.ReadOnly, want)
	}

	in.Level = protocol.WorkersReadOnly
	p = buildOSSandboxPolicy(in)
	if want := []string{"/s/w_1/data"}; !slices.Equal(p.Writable, want) || !p.Network {
		t.Fatalf("read-only level: writable %q network %v, want %q and on", p.Writable, p.Network, want)
	}
	if want := []string{"/s/w_1", "/repo/.git", "/wt"}; !slices.Equal(p.Visible, want) {
		t.Fatalf("read-only level: visible %q, want %q (the real tree, read-only)", p.Visible, want)
	}

	in.Level = protocol.WorkersNoNetwork
	if p = buildOSSandboxPolicy(in); p.Network {
		t.Fatal("no-network level keeps the network")
	}

	// A harness binary in a hidden folder (a test binary in /tmp) stays visible.
	in.Level, in.Bin = protocol.WorkersSandboxed, "/tmp/go-build1/daemon.test"
	if p = buildOSSandboxPolicy(in); !slices.Contains(p.Visible, in.Bin) {
		t.Fatalf("a harness in /tmp is not bound back: %q", p.Visible)
	}
	in.Bin = "/usr/bin/pi"
	if p = buildOSSandboxPolicy(in); slices.Contains(p.Visible, in.Bin) {
		t.Fatalf("a harness outside the hidden folders is bound again: %q", p.Visible)
	}
}

// node.json: a list of projects, "*" as the value or as an item.
func TestNodeWorkersOSSandbox(t *testing.T) {
	for _, tc := range []struct {
		raw     string
		project string
		on      bool
	}{
		{`{"workers":{"os_sandbox":["Shop","blog"]}}`, "shop", true},
		{`{"workers":{"os_sandbox":["Shop","blog"]}}`, "BLOG", true},
		{`{"workers":{"os_sandbox":["Shop","blog"]}}`, "other", false},
		{`{"workers":{"os_sandbox":"*"}}`, "anything", true},
		{`{"workers":{"os_sandbox":["*"]}}`, "anything", true},
		{`{"workers":{"os_sandbox":[]}}`, "shop", false},
		{`{"workers":{"max":3}}`, "shop", false},
		{`{}`, "shop", false},
	} {
		var nc NodeConfig
		if err := json.Unmarshal([]byte(tc.raw), &nc); err != nil {
			t.Fatalf("%s: %v", tc.raw, err)
		}
		s, err := nc.WorkerSettings()
		if err != nil {
			t.Fatalf("%s: %v", tc.raw, err)
		}
		if got := s.osSandboxOn(tc.project); got != tc.on {
			t.Fatalf("%s: osSandboxOn(%q) = %v, want %v", tc.raw, tc.project, got, tc.on)
		}
	}
	var nc NodeConfig
	if err := json.Unmarshal([]byte(`{"workers":{"os_sandbox":3}}`), &nc); err == nil {
		t.Fatal("a number is accepted as os_sandbox")
	}
	if err := json.Unmarshal([]byte(`{"workers":{"os_sandbox":["shop",""]}}`), &nc); err != nil {
		t.Fatal(err)
	}
	if _, err := nc.WorkerSettings(); err == nil || !strings.Contains(err.Error(), "os_sandbox") {
		t.Fatalf("an empty project name: %v", err)
	}
}

// Which starts run in the wrapper, and which are refused. Never a silent
// start without the sandbox.
func TestOSSandboxDecide(t *testing.T) {
	ready := func() error { return nil }
	missing := func() error { return errors.New("bubblewrap (bwrap) is not installed") }
	for _, tc := range []struct {
		name    string
		on      bool
		harness string
		level   string
		ready   func() error
		want    string
		refuse  string
	}{
		{"switch off", false, "pi", "", missing, "", ""},
		{"pi", true, "pi", "", ready, osSandboxName, ""},
		{"opencode read-only", true, "opencode", protocol.WorkersReadOnly, ready, osSandboxName, ""},
		{"codex keeps its own", true, "codex", protocol.WorkersNoNetwork, missing, "", ""},
		{"agy keeps its own", true, "agy", "", missing, "", ""},
		{"claude refused", true, "claude", "", ready, "", "Claude Code's own background service"},
		{"no-network refused", true, "opencode", protocol.WorkersNoNetwork, ready, "", "cut its connection to its model"},
		{"tool missing refused", true, "pi", "", missing, "", "not installed"},
		{"unknown harness refused", true, "other", "", ready, "", "cannot wrap"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := osSandboxDecide(tc.on, tc.harness, tc.level, tc.ready)
			if tc.refuse != "" {
				if err == nil || !strings.Contains(err.Error(), tc.refuse) {
					t.Fatalf("error %v, want one with %q", err, tc.refuse)
				}
				if !strings.Contains(err.Error(), "os_sandbox") {
					t.Fatalf("the refusal does not name the switch: %v", err)
				}
				return
			}
			if err != nil || got != tc.want {
				t.Fatalf("got %q, %v; want %q", got, err, tc.want)
			}
		})
	}
}

// With the switch on and the tool missing, the start is refused with the
// reason, and no worker runs without the sandbox.
func TestOSSandboxRefusesStartWithoutTheTool(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	dir := t.TempDir()
	t.Setenv(envFakePi, "1")
	t.Setenv("PI_CODING_AGENT_SESSION_DIR", dir)
	set := shortSet
	set.OSSandbox = []string{"shop"}
	w := startWorkerNode(t, g, set, func(c *Config) {
		c.HarnessPaths = map[string]string{"pi": os.Args[0]}
		c.PiSessionsDir = dir
		c.testOSSandboxReady = func() error { return errors.New("bubblewrap (bwrap) is not installed: install it") }
	})
	w.register(t, newRepo(t, "shop"))
	_, err := w.startDefault(protocol.WorkerStartRequest{Project: "shop", Harness: "pi", Model: "openai/gpt-4.1-nano", Task: "say ok", Isolation: protocol.IsolationWorktree})
	if err == nil || !strings.Contains(err.Error(), "not installed") || !strings.Contains(err.Error(), "os_sandbox") {
		t.Fatalf("start: %v, want a refusal that names the missing tool and the switch", err)
	}
	if n := w.d.workerCount(); n != 0 {
		t.Fatalf("%d workers run", n)
	}
}

// A Claude worker of a project with the switch on is refused: claude --bg
// runs outside any wrapper.
func TestOSSandboxRefusesClaudeWorker(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	set := shortSet
	set.OSSandbox = []string{"*"}
	c := startClaudeNode(t, g, set)
	c.register(t, newRepo(t, "shop"))
	_, err := c.start(protocol.WorkerStartRequest{Project: "shop", Harness: "claude", Model: "haiku", Task: "say ok"})
	if err == nil || !strings.Contains(err.Error(), "Claude Code's own background service") {
		t.Fatalf("start: %v, want the Claude refusal", err)
	}
	if n := c.d.workerCount(); n != 0 {
		t.Fatalf("%d workers run", n)
	}
	if len(c.stops()) != 0 {
		t.Fatal("claude was started")
	}
}
