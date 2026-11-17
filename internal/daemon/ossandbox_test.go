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

// node.json: lists of projects, "*" as the value or as an item. Nothing set
// means the default: Pi and OpenCode workers are in the OS sandbox.
func TestNodeWorkersOSSandbox(t *testing.T) {
	for _, tc := range []struct {
		raw     string
		project string
		mode    osSandboxMode
	}{
		{`{"workers":{"os_sandbox":["Shop","blog"]}}`, "shop", osSandboxExplicit},
		{`{"workers":{"os_sandbox":["Shop","blog"]}}`, "BLOG", osSandboxExplicit},
		{`{"workers":{"os_sandbox":["Shop","blog"]}}`, "other", osSandboxDefault},
		{`{"workers":{"os_sandbox":"*"}}`, "anything", osSandboxExplicit},
		{`{"workers":{"os_sandbox":[]}}`, "shop", osSandboxDefault},
		{`{"workers":{"os_sandbox_off":["Shop"]}}`, "shop", osSandboxOff},
		{`{"workers":{"os_sandbox_off":["Shop"]}}`, "blog", osSandboxDefault},
		{`{"workers":{"os_sandbox_off":"*"}}`, "anything", osSandboxOff},
		{`{"workers":{"os_sandbox":["*"],"os_sandbox_off":["shop"]}}`, "shop", osSandboxOff},
		{`{"workers":{"os_sandbox":["*"],"os_sandbox_off":["shop"]}}`, "blog", osSandboxExplicit},
		{`{"workers":{"max":3}}`, "shop", osSandboxDefault},
		{`{}`, "shop", osSandboxDefault},
	} {
		var nc NodeConfig
		if err := json.Unmarshal([]byte(tc.raw), &nc); err != nil {
			t.Fatalf("%s: %v", tc.raw, err)
		}
		s, err := nc.WorkerSettings()
		if err != nil {
			t.Fatalf("%s: %v", tc.raw, err)
		}
		if got := s.osSandboxMode(tc.project); got != tc.mode {
			t.Fatalf("%s: osSandboxMode(%q) = %v, want %v", tc.raw, tc.project, got, tc.mode)
		}
	}
	var nc NodeConfig
	if err := json.Unmarshal([]byte(`{"workers":{"os_sandbox":3}}`), &nc); err == nil {
		t.Fatal("a number is accepted as os_sandbox")
	}
	for _, key := range []string{"os_sandbox", "os_sandbox_off"} {
		var nc NodeConfig
		if err := json.Unmarshal([]byte(`{"workers":{"`+key+`":["shop",""]}}`), &nc); err != nil {
			t.Fatal(err)
		}
		if _, err := nc.WorkerSettings(); err == nil || !strings.Contains(err.Error(), key) {
			t.Fatalf("%s with an empty project name: %v", key, err)
		}
	}
}

// Which starts run in the wrapper, and which are refused. Never a silent
// start without the sandbox, except where node.json turned it off.
func TestOSSandboxDecide(t *testing.T) {
	ready := func() error { return nil }
	missing := func() error { return errors.New("bubblewrap (bwrap) is not installed") }
	for _, tc := range []struct {
		name    string
		mode    osSandboxMode
		harness string
		level   string
		ready   func() error
		want    string
		refuse  string
	}{
		{"turned off", osSandboxOff, "pi", "", missing, "", ""},
		{"turned off, no-network", osSandboxOff, "opencode", protocol.WorkersNoNetwork, missing, "", ""},
		{"default pi", osSandboxDefault, "pi", "", ready, osSandboxName, ""},
		{"default opencode", osSandboxDefault, "opencode", protocol.WorkersSandboxed, ready, osSandboxName, ""},
		{"default read-only", osSandboxDefault, "opencode", protocol.WorkersReadOnly, ready, osSandboxName, ""},
		{"default claude runs on its own sandbox", osSandboxDefault, "claude", "", missing, "", ""},
		{"default codex", osSandboxDefault, "codex", "", missing, "", ""},
		{"default agy", osSandboxDefault, "agy", "", missing, "", ""},
		{"default unknown harness", osSandboxDefault, "other", "", missing, "", ""},
		{"default tool missing", osSandboxDefault, "pi", "", missing, "", "not installed"},
		{"default no-network", osSandboxDefault, "opencode", protocol.WorkersNoNetwork, ready, "", "cut its connection to its model"},
		{"listed pi", osSandboxExplicit, "pi", "", ready, osSandboxName, ""},
		{"listed codex keeps its own", osSandboxExplicit, "codex", protocol.WorkersNoNetwork, missing, "", ""},
		{"listed agy keeps its own", osSandboxExplicit, "agy", "", missing, "", ""},
		{"listed claude refused", osSandboxExplicit, "claude", "", ready, "", "Claude Code's own background service"},
		{"listed no-network refused", osSandboxExplicit, "opencode", protocol.WorkersNoNetwork, ready, "", "cut its connection to its model"},
		{"listed tool missing refused", osSandboxExplicit, "pi", "", missing, "", "not installed"},
		{"listed unknown harness refused", osSandboxExplicit, "other", "", ready, "", "cannot wrap"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := osSandboxDecide(tc.mode, tc.harness, tc.level, tc.ready)
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

// A missing tool is refused with the fix, and the way out names node.json.
func TestOSSandboxDefaultRefusalNamesTheFix(t *testing.T) {
	_, err := osSandboxDecide(osSandboxDefault, "pi", "", func() error {
		return errors.New("bubblewrap (bwrap) is not installed: install it (Debian and Ubuntu: `sudo apt install bubblewrap`)")
	})
	if err == nil {
		t.Fatal("a Pi worker would start without the sandbox")
	}
	for _, want := range []string{"on by default", "sudo apt install bubblewrap", `"os_sandbox_off"`, "was not started without it"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal lacks %q: %v", want, err)
		}
	}
}

// With the sandbox on (by default) and the tool missing, the start is refused
// with the reason, and no worker runs without the sandbox.
func TestOSSandboxRefusesStartWithoutTheTool(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	dir := t.TempDir()
	t.Setenv(envFakePi, "1")
	t.Setenv("PI_CODING_AGENT_SESSION_DIR", dir)
	w := startWorkerNode(t, g, shortSet, func(c *Config) {
		c.Workers.OSSandboxOff = nil // the default: on
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

// The daemon applies the node.json lists. A worker that already ran in the
// wrapper stays in it, even when the project was turned off since.
func TestOSSandboxChoiceUsesNodeSettings(t *testing.T) {
	missing := func() error { return errors.New("bubblewrap (bwrap) is not installed") }
	ok := func() error { return nil }
	for _, tc := range []struct {
		name     string
		set      WorkerSettings
		ready    func() error
		recorded bool
		want     string
		refuse   bool
	}{
		{"nothing set", WorkerSettings{}, ok, false, osSandboxName, false},
		{"nothing set, tool missing", WorkerSettings{}, missing, false, "", true},
		{"project off", WorkerSettings{OSSandboxOff: []string{"shop"}}, missing, false, "", false},
		{"other project off", WorkerSettings{OSSandboxOff: []string{"blog"}}, ok, false, osSandboxName, false},
		{"recorded stays in", WorkerSettings{OSSandboxOff: []string{"*"}}, ok, true, osSandboxName, false},
		{"recorded, tool missing", WorkerSettings{OSSandboxOff: []string{"*"}}, missing, true, "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := &Daemon{cfg: Config{Workers: tc.set, testOSSandboxReady: tc.ready}}
			got, err := d.osSandboxChoice("shop", "pi", "", tc.recorded)
			if (err != nil) != tc.refuse || got != tc.want {
				t.Fatalf("got %q, %v; want %q (refuse %v)", got, err, tc.want, tc.refuse)
			}
		})
	}
}

// A Claude worker starts with the sandbox on by default: Claude Code's own
// sandbox settings apply, and the wrapper is not asked for.
func TestOSSandboxDefaultStartsClaudeWorker(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	c := startClaudeNodeWith(t, g, shortSet, func(c *Config) {
		c.Workers.OSSandboxOff = nil
		c.testOSSandboxReady = func() error { return errors.New("bubblewrap (bwrap) is not installed") }
	})
	c.register(t, newRepo(t, "shop"))
	if _, err := c.start(protocol.WorkerStartRequest{Project: "shop", Harness: "claude", Model: "haiku", Task: "say ok"}); err != nil {
		t.Fatalf("a Claude worker was refused by the default sandbox: %v", err)
	}
}

// The profile that sandbox-exec runs says what the policy says: the worktree,
// the git folders and the private state are writable, the read-only paths are
// denied after them (the later rule wins), and only the agent-net socket is
// reachable.
func TestSeatbeltProfile(t *testing.T) {
	p := buildOSSandboxPolicy(osSandboxInput{
		Level: protocol.WorkersSandboxed, WorkerDir: "/state/workers/w_1", Path: "/work/wt",
		DotGit: "/work/wt/.git", GitCommon: "/repo/.git", GitRoots: []string{"/repo/.git/objects", "/repo/.git/refs"},
		State: []string{"/state/workers/w_1/data"}, Socket: "/state/agent-net.sock",
	})
	got := seatbeltProfile(p)
	for _, want := range []string{
		"(version 1)\n(allow default)\n(deny file-write*)\n",
		`(allow file-write* (subpath "/work/wt") (subpath "/repo/.git/objects") (subpath "/repo/.git/refs") (subpath "/state/workers/w_1/data") (subpath "/state/workers/w_1/tmp"))`,
		`(deny file-write* (subpath "/work/wt/.git"))`,
		`(allow network-outbound (remote unix-socket (path-literal "/state/agent-net.sock")))`,
		`(deny network-outbound (remote unix-socket))`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("profile lacks %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "(deny network*)") {
		t.Errorf("the network is cut at the sandboxed level:\n%s", got)
	}
	if strings.Index(got, `(allow file-write* (subpath "/work/wt")`) > strings.Index(got, `(deny file-write* (subpath "/work/wt/.git"))`) {
		t.Error("the read-only .git is denied before the worktree is allowed, so the allow wins")
	}

	// read-only: the worktree is not writable, the private state still is.
	ro := seatbeltProfile(buildOSSandboxPolicy(osSandboxInput{Level: protocol.WorkersReadOnly, WorkerDir: "/state/workers/w_1", Path: "/work/wt", State: []string{"/state/workers/w_1/data"}}))
	if strings.Contains(ro, `(subpath "/work/wt")`) || !strings.Contains(ro, `(subpath "/state/workers/w_1/data")`) {
		t.Errorf("read-only profile:\n%s", ro)
	}
	// a quote in a path cannot end the string early
	q := seatbeltProfile(osSandboxPolicy{Network: true, Writable: []string{`/a"b) (allow default`}})
	if !strings.Contains(q, `(subpath "/a\"b) (allow default")`) {
		t.Errorf("path not quoted:\n%s", q)
	}
	args := seatbeltArgs(p, "/bin/pi", []string{"--mode", "rpc"})
	if len(args) != 5 || args[0] != "-p" || args[2] != "/bin/pi" || args[4] != "rpc" {
		t.Errorf("sandbox-exec args %q", args)
	}
}

// Where the default is off (macOS), a Pi or OpenCode worker of a project that
// is not listed runs without the wrapper and the notice says why; a listed
// project still gets the wrapper, and a project that is turned off says
// nothing about macOS.
func TestOSSandboxDefaultOffWhereTheOSHasNoDefault(t *testing.T) {
	never := func() error { t.Error("the tool was probed where the default is off"); return nil }
	ok := func() error { return nil }
	for _, tc := range []struct {
		name    string
		mode    osSandboxMode
		harness string
		level   string
		ready   func() error
		want    string
		refuse  bool
	}{
		{"default pi", osSandboxDefault, "pi", "", never, protocol.OSSandboxNoneMacOS, false},
		{"default opencode no-network", osSandboxDefault, "opencode", protocol.WorkersNoNetwork, never, protocol.OSSandboxNoneMacOS, false},
		{"default claude", osSandboxDefault, "claude", "", never, "", false},
		{"default codex", osSandboxDefault, "codex", "", never, "", false},
		{"listed pi wraps", osSandboxExplicit, "pi", "", ok, osSandboxName, false},
		{"listed claude refused", osSandboxExplicit, "claude", "", ok, "", true},
		{"turned off", osSandboxOff, "pi", "", never, "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := osSandboxDecideOn(false, tc.mode, tc.harness, tc.level, tc.ready)
			if (err != nil) != tc.refuse || got != tc.want {
				t.Fatalf("got %q, %v; want %q (refuse %v)", got, err, tc.want, tc.refuse)
			}
		})
	}
	if osSandboxRequired(false, osSandboxDefault) || !osSandboxRequired(false, osSandboxExplicit) || !osSandboxRequired(true, osSandboxDefault) || osSandboxRequired(true, osSandboxOff) {
		t.Error("osSandboxRequired does not follow the default of the OS")
	}
}
