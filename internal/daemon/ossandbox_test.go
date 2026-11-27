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

// The bubblewrap command line of a worker (decision 0197): the host is
// read-only and shares its network, nothing is hidden, and the writable
// folders and the read-only owner files come after it, in order.
func TestBwrapArgs(t *testing.T) {
	p := osSandboxPolicy{
		Writable: []string{"/wt", "/state/workers/w_1/tmp", "/repo/.git"},
		ReadOnly: []mountPair{{Src: "/home/me/.pi/agent/settings.json", Dst: "/state/workers/w_1/pi-agent/settings.json"}},
		Dir:      "/wt",
	}
	want := []string{
		"--unshare-all", "--share-net",
		"--ro-bind", "/", "/", "--dev", "/dev", "--proc", "/proc",
		"--bind", "/wt", "/wt",
		"--bind", "/state/workers/w_1/tmp", "/state/workers/w_1/tmp",
		"--bind", "/repo/.git", "/repo/.git",
		"--ro-bind", "/home/me/.pi/agent/settings.json", "/state/workers/w_1/pi-agent/settings.json",
		"--chdir", "/wt",
		"--", "/bin/pi", "--mode", "rpc",
	}
	got := bwrapArgs(p, "/bin/pi", []string{"--mode", "rpc"})
	if !slices.Equal(got, want) {
		t.Fatalf("bwrap args:\n got %q\nwant %q", got, want)
	}
	// A worker outlives the daemon, and the daemon stops it through its
	// process group: neither flag may appear.
	for _, flag := range []string{"--die-with-parent", "--new-session", "--as-pid-1"} {
		if slices.Contains(got, flag) {
			t.Fatalf("bwrap args have %s: %q", flag, got)
		}
	}
}

// The policy has one rule (decision 0197): the worktree, the worker's own temp
// folder, the folders every worker may write, the harness's private state and
// the agent-net socket folder are writable, each once; the git config and
// hooks are bound read-only over them, so bubblewrap mounts them after.
func TestOSSandboxPolicyWritable(t *testing.T) {
	in := osSandboxInput{
		WorkerDir: "/s/w_1", Path: "/wt",
		Roots:     []string{"/repo/.git", "/gocache", "/s/w_1/cache"},
		RootsRO:   []string{"/repo/.git/config", "/repo/.git/hooks"},
		State:     []string{"/s/w_1/data", "/repo/.git"},
		StateRO:   []mountPair{{"/home/me/.pi/agent/models.json", "/s/w_1/pi-agent/models.json"}},
		SocketDir: "/run/user/1000/agent-net/sock",
	}
	p := buildOSSandboxPolicy(in)
	want := []string{"/wt", "/s/w_1/tmp", "/repo/.git", "/gocache", "/s/w_1/cache", "/s/w_1/data", "/run/user/1000/agent-net/sock"}
	if !slices.Equal(p.Writable, want) {
		t.Fatalf("writable %q, want %q", p.Writable, want)
	}
	ro := append(append([]mountPair(nil), in.StateRO...), mountPair{"/repo/.git/config", "/repo/.git/config"}, mountPair{"/repo/.git/hooks", "/repo/.git/hooks"})
	if p.Dir != "/wt" || !slices.Equal(p.ReadOnly, ro) {
		t.Fatalf("policy: %+v", p)
	}
	args := strings.Join(bwrapArgs(p, "/bin/pi", nil), " ")
	if bind, ro := strings.Index(args, "--bind /repo/.git /repo/.git"), strings.Index(args, "--ro-bind /repo/.git/hooks /repo/.git/hooks"); bind < 0 || ro < bind {
		t.Fatalf("the hooks are not bound read-only after the git folder: %s", args)
	}
}

// Inside the wrapper every temp folder variable names the worker's own temp
// folder, so tools that read TMP or TEMP stay inside the fence too.
func TestOSSandboxSetEnvNamesTheWorkerTemp(t *testing.T) {
	set := osSandboxSetEnv("/s/w_1/tmp")
	for _, k := range []string{"TMPDIR", "TMP", "TEMP", "GOTMPDIR"} {
		if set[k] != "/s/w_1/tmp" {
			t.Errorf("%s = %q, want the worker's temp folder", k, set[k])
		}
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
		{"default pi", osSandboxDefault, "pi", "", ready, osSandboxName, ""},
		{"default opencode", osSandboxDefault, "opencode", protocol.WorkersSandboxed, ready, osSandboxName, ""},
		{"default claude runs on its own sandbox", osSandboxDefault, "claude", "", missing, "", ""},
		{"default codex", osSandboxDefault, "codex", "", missing, "", ""},
		{"default agy", osSandboxDefault, "agy", "", missing, "", ""},
		{"default unknown harness", osSandboxDefault, "other", "", missing, "", ""},
		{"default tool missing", osSandboxDefault, "pi", "", missing, "", "not installed"},
		{"listed pi", osSandboxExplicit, "pi", "", ready, osSandboxName, ""},
		{"listed codex keeps its own", osSandboxExplicit, "codex", "", missing, "", ""},
		{"listed agy keeps its own", osSandboxExplicit, "agy", "", missing, "", ""},
		{"listed claude refused", osSandboxExplicit, "claude", "", ready, "", "Claude Code's own background service"},
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

// An OS whose default is on wraps Pi and OpenCode workers, and never asks for
// the marker an older Mac daemon wrote. macOS now takes this path, so Linux CI
// checks it too.
func TestOSSandboxDefaultOnWraps(t *testing.T) {
	ready := func() error { return nil }
	for _, harness := range []string{"pi", "opencode"} {
		got, err := osSandboxDecideOn(true, osSandboxDefault, harness, "", ready)
		if err != nil || got != osSandboxName {
			t.Errorf("%s: %q, %v; want %q", harness, got, err, osSandboxName)
		}
	}
	if !osSandboxRequired(true, osSandboxDefault) {
		t.Error("a default-on OS does not require the wrapper by default")
	}
}

// The profile that sandbox-exec runs says what the policy says (decision 0197):
// writes only in the writable folders, the owner files of the private state
// denied after them (the later rule wins), and no rule for reads, the network
// or unix sockets.
func TestSeatbeltProfile(t *testing.T) {
	p := buildOSSandboxPolicy(osSandboxInput{
		WorkerDir: "/state/workers/w_1", Path: "/work/wt", Roots: []string{"/repo/.git"}, RootsRO: []string{"/repo/.git/hooks"},
		State:   []string{"/state/workers/w_1/pi-agent"},
		StateRO: []mountPair{{"/home/me/.pi/agent/settings.json", "/state/workers/w_1/pi-agent/settings.json"}},
	})
	got := seatbeltProfile(p)
	want := "(version 1)\n(allow default)\n(deny file-write*)\n" +
		`(allow file-write* (subpath "/work/wt") (subpath "/state/workers/w_1/tmp") (subpath "/repo/.git") (subpath "/state/workers/w_1/pi-agent"))` + "\n" +
		`(allow file-write* (literal "/dev/null") (literal "/dev/zero") (literal "/dev/dtracehelper") (regex #"^/dev/tty") (regex #"^/dev/fd/"))` + "\n" +
		`(deny file-write* (subpath "/state/workers/w_1/pi-agent/settings.json") (subpath "/repo/.git/hooks"))` + "\n"
	if got != want {
		t.Errorf("profile:\n%s\nwant:\n%s", got, want)
	}

	// a quote in a path cannot end the string early
	q := seatbeltProfile(osSandboxPolicy{Writable: []string{`/a"b) (allow default`}})
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
		{"default opencode", osSandboxDefault, "opencode", protocol.WorkersSandboxed, never, protocol.OSSandboxNoneMacOS, false},
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
