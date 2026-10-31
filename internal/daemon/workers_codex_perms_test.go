package daemon

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

// setWorkersLevel sets the workers level of the project that repo belongs to,
// as the owner does in the TUI: through a session of that project, at project
// scope.
func setWorkersLevel(t *testing.T, w *workerNode, repo, level string) {
	t.Helper()
	_, sid := helloPi(t, w.node, "perm-"+level, repo, true)
	w.cli.mustCall(protocol.OpSessionPolicy, protocol.SessionPolicyRequest{SessionID: sid, Scope: protocol.ScopeProject,
		Policy: protocol.DelegationPolicy{Workers: level}})
}

func TestWorkerRemainsActiveDuringBranchCleanup(t *testing.T) {
	git, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	marker, release := filepath.Join(dir, "cleanup"), filepath.Join(dir, "release")
	script := "#!/bin/sh\nif [ \"$3\" = branch ] && [ \"$4\" = -D ]; then\n  : > " + strconv.Quote(marker) + "\n  while [ ! -e " + strconv.Quote(release) + " ]; do sleep 0.01; done\nfi\nexec " + strconv.Quote(git) + " \"$@\"\n"
	if err := os.WriteFile(filepath.Join(dir, "git"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	g := startGateway(t, t.TempDir(), "")
	w := startCodexNode(t, g, shortSet)
	repo := newRepo(t, "shop")
	w.register(t, repo)
	w.mustStart(t, protocol.WorkerStartRequest{Project: "shop", Harness: "codex", Isolation: protocol.IsolationWorktree, Task: "say ok"})
	defer os.WriteFile(release, nil, 0o600)
	eventually(t, "branch cleanup", func() bool { _, err := os.Stat(marker); return err == nil })
	if w.d.workerCount() != 1 {
		t.Fatal("worker forgotten before branch cleanup finished")
	}
}

func anyStrings(v any) []string {
	var out []string
	list, _ := v.([]any)
	for _, x := range list {
		s, _ := x.(string)
		out = append(out, s)
	}
	return out
}

// The thread and the turns of a Codex worker carry the permission level of the
// project: approvalPolicy never, and a sandbox that writes only in the
// worktree (the default roots of Codex are the folder and the temp folders),
// the folders of the main repository that a commit needs, and the network on
// or off. Read-only writes nowhere.
func TestCodexWorkerPermissionLevels(t *testing.T) {
	for _, tc := range []struct {
		level, want string
		mode        string
		net         bool
		writes      bool
	}{
		{"", protocol.WorkersSandboxed, "workspace-write", true, true},
		{protocol.WorkersSandboxed, protocol.WorkersSandboxed, "workspace-write", true, true},
		{protocol.WorkersNoNetwork, protocol.WorkersNoNetwork, "workspace-write", false, true},
		{protocol.WorkersReadOnly, protocol.WorkersReadOnly, "read-only", true, false},
	} {
		t.Run("level "+tc.want+"/"+tc.level, func(t *testing.T) {
			g := startGateway(t, t.TempDir(), "")
			w := startCodexNode(t, g, shortSet)
			repo := newRepo(t, "shop")
			w.register(t, repo)
			if tc.level != "" {
				setWorkersLevel(t, w, repo, tc.level)
			}
			res := w.mustStart(t, protocol.WorkerStartRequest{Project: "shop", Harness: "codex", Isolation: protocol.IsolationWorktree, Task: "say ok"})
			if res.Permissions != tc.want {
				t.Errorf("the start result says permissions %q, want %q", res.Permissions, tc.want)
			}
			rec := readCodex(t, res.Worktree, func(r fakeCodexRecord) bool { return len(r.TurnParams) == 1 })
			th := rec.Thread
			if th["approvalPolicy"] != "never" || th["sandbox"] != tc.mode {
				t.Errorf("thread/start: %v", th)
			}
			tp := rec.TurnParams[0]
			pol, _ := tp["sandboxPolicy"].(map[string]any)
			if tp["approvalPolicy"] != "never" || pol == nil || pol["networkAccess"] != tc.net {
				t.Errorf("turn/start: %v", tp)
			}
			common := filepath.Join(repo, ".git")
			if !tc.writes {
				if pol["type"] != "readOnly" || th["config"] != nil {
					t.Errorf("read-only has a write policy: thread %v, turn %v", th, pol)
				}
			} else {
				if pol["type"] != "workspaceWrite" {
					t.Errorf("turn sandbox: %v", pol)
				}
				roots := anyStrings(pol["writableRoots"])
				// A commit in a worktree writes to the main repository's git
				// dir: objects, refs, their logs, and the private folder of
				// the worktree (index, HEAD). Not the whole dir: hooks and
				// config stay read-only.
				for _, want := range []string{filepath.Join(common, "objects"), filepath.Join(common, "refs"),
					filepath.Join(common, "logs"), filepath.Join(common, "worktrees", filepath.Base(res.Worktree))} {
					found := false
					for _, r := range roots {
						found = found || r == want
					}
					if !found {
						t.Errorf("writable roots %v lack %s", roots, want)
					}
				}
				for _, r := range roots {
					if r == common || r == repo || r == "/" || r == os.Getenv("HOME") {
						t.Errorf("writable roots are too wide: %v", roots)
					}
				}
				cfg, _ := th["config"].(map[string]any)
				sww, _ := cfg["sandbox_workspace_write"].(map[string]any)
				if sww == nil || sww["network_access"] != tc.net || len(anyStrings(sww["writable_roots"])) != len(roots) {
					t.Errorf("thread/start config: %v", th["config"])
				}
			}
			// The level is on the worker's session, for the TUI.
			eventually(t, "the worker view has its level", func() bool {
				a := w.agentOf(t, res.SessionID)
				return a != nil && a.Worker != nil && a.Worker.Permissions == tc.want
			})
			w.waitNotice(t, 1)
			eventually(t, "the worker ends", func() bool { return w.d.workerCount() == 0 })
		})
	}
}

// The level is kept with the worker: a follow-up that starts the thread again
// sends the same permissions to thread/resume and to the turn.
func TestCodexWorkerKeepsItsLevelOnResume(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	w := startCodexNode(t, g, shortSet)
	repo := newRepo(t, "shop")
	w.register(t, repo)
	setWorkersLevel(t, w, repo, protocol.WorkersNoNetwork)
	res := w.mustStart(t, protocol.WorkerStartRequest{Project: "shop", Harness: "codex", Isolation: protocol.IsolationWorktree, Task: "say ok"})
	w.waitNotice(t, 1)
	eventually(t, "the worker ends", func() bool { return w.d.workerCount() == 0 })
	// The owner changes the project's level meanwhile: a worker keeps its own.
	setWorkersLevel(t, w, repo, protocol.WorkersReadOnly)
	var sent protocol.MessageSent
	mustJSON(t, w.cli.mustCall("owner_send", protocol.OwnerSendRequest{To: res.SessionID, Body: "and again"}), &sent)
	w.replyTo(t, w.ownerID(), sent.Message.ID)
	rec := readCodex(t, res.Worktree, func(r fakeCodexRecord) bool { return r.Resumed == 1 && len(r.TurnParams) == 1 })
	if rec.ResumeParams["approvalPolicy"] != "never" || rec.ResumeParams["sandbox"] != "workspace-write" {
		t.Errorf("thread/resume: %v", rec.ResumeParams)
	}
	if pol, _ := rec.TurnParams[0]["sandboxPolicy"].(map[string]any); pol["networkAccess"] != false || pol["type"] != "workspaceWrite" {
		t.Errorf("the resumed turn: %v", rec.TurnParams[0])
	}
}

// The agent-net MCP server is turned off in the worker's app server when the
// user's Codex config defines it, and the other servers stay. When the config
// does not define it, no override is passed (Codex would fail on it).
func TestCodexWorkerTurnsOffTheAgentNetMCPServer(t *testing.T) {
	for _, tc := range []struct {
		name, config string
		want         bool
	}{
		{"defined", "[mcp_servers.other]\ncommand = \"x\"\n\n[mcp_servers.agent-net]\ncommand = \"agent-net\"\n", true},
		{"quoted", "[mcp_servers.\"agent-net\"]\ncommand = \"agent-net\"\n", true},
		{"other only", "[mcp_servers.other]\ncommand = \"x\"\n", false},
		{"no config", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			g := startGateway(t, t.TempDir(), "")
			w := startCodexNode(t, g, shortSet)
			home := os.Getenv("CODEX_HOME")
			if tc.config != "" {
				if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte(tc.config), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			repo := newRepo(t, "shop")
			w.register(t, repo)
			w.mustStart(t, protocol.WorkerStartRequest{Project: "shop", Harness: "codex", Task: "say ok"})
			rec := readCodex(t, repo, func(r fakeCodexRecord) bool { return len(r.Turns) == 1 })
			joined := strings.Join(rec.Args, " ")
			if got := strings.Contains(joined, "-c mcp_servers.agent-net.enabled=false"); got != tc.want {
				t.Errorf("override present = %v, want %v: %v", got, tc.want, rec.Args)
			}
			if strings.Contains(joined, "mcp_servers.other") {
				t.Errorf("another MCP server is touched: %v", rec.Args)
			}
			w.waitNotice(t, 1)
			eventually(t, "the worker ends", func() bool { return w.d.workerCount() == 0 })
		})
	}
}

// The brief for a Codex worker says the end of the turn is the report and
// does not tell it to use agent-net tools (it names agent_announce only to say to ignore it).
func TestCodexBriefHasNoAgentNetTools(t *testing.T) {
	b := protocol.WorkerTaskBodyFor("codex", "do it", "", "")
	for _, bad := range []string{"agent_handback", "agent_reply"} {
		if strings.Contains(b, bad) {
			t.Errorf("the Codex brief mentions %s", bad)
		}
	}
	if !strings.Contains(b, "Your last message of the run is your final report") {
		t.Errorf("the Codex brief does not say the end of the turn is the report:\n%s", b)
	}
}

// An approval that still arrives is declined.
func TestCodexApprovalStillDeclinedWithTheSandbox(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	w := startCodexNode(t, g, shortSet)
	repo := newRepo(t, "shop")
	w.register(t, repo)
	w.mustStart(t, protocol.WorkerStartRequest{Project: "shop", Harness: "codex", Task: "FAKE:approve x"})
	rec := readCodex(t, repo, func(r fakeCodexRecord) bool { return len(r.Answers) == 1 })
	if m, _ := rec.Answers[0].(map[string]any); m["decision"] != "decline" {
		t.Errorf("answer: %v", rec.Answers[0])
	}
	// The fake process and its cleanup can still write in the temporary repo
	// after recording the answer. Wait before TempDir removes that repo.
	eventually(t, "worker cleanup", func() bool { return w.d.workerCount() == 0 })
}

// Only the owner sets the level: an agent session is refused, and a session
// scope is refused for everyone because the level belongs to the project.
func TestWorkersLevelGateAndScope(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	w := startCodexNode(t, g, shortSet)
	repo := newRepo(t, "shop")
	w.register(t, repo)
	agent, sid := helloPi(t, w.node, "agent-1", repo, true)
	_, err := agent.call(protocol.OpSessionPolicy, protocol.SessionPolicyRequest{SessionID: sid, Scope: protocol.ScopeProject,
		Policy: protocol.DelegationPolicy{Workers: protocol.WorkersSandboxed}})
	if err == nil || !strings.Contains(err.Error(), "only available to the owner") {
		t.Errorf("an agent set the workers level: %v", err)
	}
	_, err = w.cli.call(protocol.OpSessionPolicy, protocol.SessionPolicyRequest{SessionID: sid, Scope: protocol.ScopeSession,
		Policy: protocol.DelegationPolicy{Workers: protocol.WorkersReadOnly}})
	if err == nil || !strings.Contains(err.Error(), "per project") {
		t.Errorf("session scope: %v", err)
	}
	_, err = w.cli.call(protocol.OpSessionPolicy, protocol.SessionPolicyRequest{SessionID: sid, Scope: protocol.ScopeProject,
		Policy: protocol.DelegationPolicy{Workers: "full-access"}})
	if err == nil || !strings.Contains(err.Error(), "workers must be") {
		t.Errorf("unknown level: %v", err)
	}
	// Nothing was stored by the refusals: the default holds.
	res := w.mustStart(t, protocol.WorkerStartRequest{Project: "shop", Harness: "codex", Task: "say ok"})
	if res.Permissions != protocol.WorkersSandboxed {
		t.Errorf("level after refusals: %q", res.Permissions)
	}
	w.waitNotice(t, 1)
	eventually(t, "the worker ends", func() bool { return w.d.workerCount() == 0 })
}

// A start that waits in the queue keeps the level the project had when it was
// queued, even if the owner changes the level while it waits.
func TestQueuedCodexStartKeepsItsLevel(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	t.Setenv(envFakeCodex, "1")
	t.Setenv("CODEX_HOME", t.TempDir())
	fl := &fakeLoad{l: cpuBusy()}
	w := startWorkerNode(t, g, queueSet, func(c *Config) {
		c.HarnessPaths = map[string]string{"codex": os.Args[0]}
		c.LoadProbe = fl.get
	})
	repo := newRepo(t, "shop")
	w.register(t, repo)
	setWorkersLevel(t, w, repo, protocol.WorkersNoNetwork)
	pi, _ := helloPi(t, w.node, "caller", "/work/caller", true)
	r := startFrom(t, pi, protocol.WorkerStartRequest{Project: "shop", Harness: "codex", Task: "say ok"})
	if r.Status != "queued" || r.Permissions != protocol.WorkersNoNetwork {
		t.Fatalf("queued start: %+v", r)
	}
	setWorkersLevel(t, w, repo, protocol.WorkersReadOnly)
	fl.set(roomyLoad())
	rec := readCodex(t, repo, func(r fakeCodexRecord) bool { return len(r.TurnParams) == 1 })
	pol, _ := rec.TurnParams[0]["sandboxPolicy"].(map[string]any)
	if rec.Thread["sandbox"] != "workspace-write" || pol["type"] != "workspaceWrite" || pol["networkAccess"] != false {
		t.Errorf("the queued worker lost its level: thread %v, turn %v", rec.Thread, pol)
	}
}

// A continued worker keeps the level of the worker it continues, not the
// project's current level.
func TestContinuedCodexWorkerKeepsTheOldLevel(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	w := startCodexNode(t, g, shortSet)
	repo := newRepo(t, "shop")
	w.register(t, repo)
	setWorkersLevel(t, w, repo, protocol.WorkersNoNetwork)
	old := w.mustStart(t, protocol.WorkerStartRequest{Project: "shop", Harness: "codex", Isolation: protocol.IsolationWorktree, Task: "say ok"})
	w.waitNotice(t, 1)
	eventually(t, "the worker ends", func() bool { return w.d.workerCount() == 0 })
	setWorkersLevel(t, w, repo, protocol.WorkersReadOnly)
	res, err := w.startDefault(protocol.WorkerStartRequest{Continue: old.SessionID, Task: "and more"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Permissions != protocol.WorkersNoNetwork {
		t.Errorf("continued worker level %q", res.Permissions)
	}
	rec := readCodex(t, res.Worktree, func(r fakeCodexRecord) bool { return len(r.TurnParams) == 1 })
	if pol, _ := rec.TurnParams[0]["sandboxPolicy"].(map[string]any); pol["type"] != "workspaceWrite" || pol["networkAccess"] != false {
		t.Errorf("the continued worker's turn: %v", rec.TurnParams[0])
	}
}
