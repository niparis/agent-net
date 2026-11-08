package daemon

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// A worktree is a new folder for Claude Code, so the answer to "new MCP server
// needs approval" that the owner gave for the main checkout does not apply to
// it. A worker would sit on the prompt. The daemon copies the main checkout's
// choice into the worktree before it launches Claude (decision 0101).

// commitMCPJSON writes a .mcp.json into the repository and commits it, as a
// project that shares its MCP servers does.
func commitMCPJSON(t *testing.T, repo string, servers map[string]any) {
	t.Helper()
	writeMCPJSON(t, repo, servers)
	gitIn(t, repo, "add", ".mcp.json")
	gitIn(t, repo, "commit", "-q", "-m", "mcp servers")
}

func writeSettingsLocal(t *testing.T, dir, body string) {
	t.Helper()
	file := filepath.Join(dir, ".claude", "settings.local.json")
	if err := os.MkdirAll(filepath.Dir(file), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func inheritWorktree(t *testing.T, d *Daemon, repo string) string {
	t.Helper()
	w := &worker{id: protocol.NewID("w"), path: repo}
	if err := d.createWorktree(context.Background(), w); err != nil {
		t.Fatal(err)
	}
	return w.worktree
}

func porcelain(t *testing.T, dir string) string {
	t.Helper()
	return gitOutT(t, dir, "status", "--porcelain", "--untracked-files=all")
}

func TestClaudeMCPInheritCopiesTheMainCheckoutChoice(t *testing.T) {
	d := commitTestDaemon(t)
	repo := newRepo(t, "strats")
	commitMCPJSON(t, repo, map[string]any{"a": map[string]any{"command": "x"}, "b": map[string]any{"command": "y"}, "c": map[string]any{"command": "z"}})
	// The owner approved a, refused b, never answered for c. Other keys of the
	// file are not ours and are not copied.
	writeSettingsLocal(t, repo, `{"permissions":{"allow":["Bash(ls)"]},"enabledMcpjsonServers":["a","other"],"disabledMcpjsonServers":["b"]}`)
	wt := inheritWorktree(t, d, repo)

	res, err := claudeMCPInherit(repo, wt)
	if err != nil {
		t.Fatal(err)
	}
	got := readLocalSettings(t, wt)
	if !reflect.DeepEqual(got["enabledMcpjsonServers"], []any{"a"}) || !reflect.DeepEqual(got["disabledMcpjsonServers"], []any{"b"}) {
		t.Fatalf("worktree settings %v", got)
	}
	if got["permissions"] != nil || got["enableAllProjectMcpServers"] != nil {
		t.Fatalf("only the two lists are copied: %v", got)
	}
	if !reflect.DeepEqual(res.Enabled, []string{"a"}) || !reflect.DeepEqual(res.Disabled, []string{"b"}) || !reflect.DeepEqual(res.Pending, []string{"c"}) {
		t.Fatalf("result %+v", res)
	}
	if out := porcelain(t, wt); out != "" {
		t.Fatalf("the settings file must not dirty the worktree:\n%s", out)
	}
	// The main checkout's own file is read, never changed.
	if raw, _ := os.ReadFile(filepath.Join(repo, ".claude", "settings.local.json")); !strings.Contains(string(raw), `"other"`) || !strings.Contains(string(raw), "Bash(ls)") {
		t.Fatalf("main file changed: %s", raw)
	}
	// A second run changes nothing.
	again, err := claudeMCPInherit(repo, wt)
	if err != nil || again.Changed {
		t.Fatalf("second run %+v %v", again, err)
	}
}

// The exclude entry is written only when git does not already ignore the file.
func TestClaudeMCPInheritLeavesTheExcludeAloneWhenTheFileIsIgnored(t *testing.T) {
	d := commitTestDaemon(t)
	repo := newRepo(t, "strats")
	if err := os.WriteFile(filepath.Join(repo, ".gitignore"), []byte(".claude/settings.local.json\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitIn(t, repo, "add", ".gitignore")
	gitIn(t, repo, "commit", "-q", "-m", "ignore")
	commitMCPJSON(t, repo, map[string]any{"a": map[string]any{"command": "x"}})
	writeSettingsLocal(t, repo, `{"enabledMcpjsonServers":["a"]}`)
	exclude := filepath.Join(repo, ".git", "info", "exclude")
	before, _ := os.ReadFile(exclude)
	wt := inheritWorktree(t, d, repo)
	if _, err := claudeMCPInherit(repo, wt); err != nil {
		t.Fatal(err)
	}
	if after, _ := os.ReadFile(exclude); string(after) != string(before) {
		t.Fatalf("exclude changed:\n%s", after)
	}
	if out := porcelain(t, wt); out != "" {
		t.Fatalf("worktree dirty:\n%s", out)
	}
}

// enableAllProjectMcpServers in the main checkout folder or in a folder above
// it approves every server of .mcp.json. The worktree gets the names, not the
// switch, so a server added later still asks.
func TestClaudeMCPInheritHonoursEnableAllFromAParentFolder(t *testing.T) {
	d := commitTestDaemon(t)
	repo := newRepo(t, "strats")
	commitMCPJSON(t, repo, map[string]any{"x": map[string]any{"command": "1"}, "y": map[string]any{"command": "2"}})
	writeSettingsLocal(t, filepath.Dir(repo), `{"enabledMcpjsonServers":["unrelated"],"enableAllProjectMcpServers":true}`)
	writeSettingsLocal(t, repo, `{"disabledMcpjsonServers":["y"]}`)
	wt := inheritWorktree(t, d, repo)
	if _, err := claudeMCPInherit(repo, wt); err != nil {
		t.Fatal(err)
	}
	got := readLocalSettings(t, wt)
	if !reflect.DeepEqual(got["enabledMcpjsonServers"], []any{"x"}) || !reflect.DeepEqual(got["disabledMcpjsonServers"], []any{"y"}) {
		t.Fatalf("a refusal beats enable-all: %v", got)
	}
	if _, ok := got["enableAllProjectMcpServers"]; ok {
		t.Fatalf("the switch must not be copied: %v", got)
	}
	// The nearest explicit false turns the switch off again.
	writeSettingsLocal(t, repo, `{"enableAllProjectMcpServers":false}`)
	wt2 := inheritWorktree(t, d, repo)
	res, err := claudeMCPInherit(repo, wt2)
	if err != nil || res.Changed || !reflect.DeepEqual(res.Pending, []string{"x", "y"}) {
		t.Fatalf("explicit false: %+v %v", res, err)
	}
}

func TestClaudeMCPInheritWithoutAChoiceWritesNothing(t *testing.T) {
	d := commitTestDaemon(t)
	repo := newRepo(t, "strats")
	commitMCPJSON(t, repo, map[string]any{"a": map[string]any{"command": "x"}})
	wt := inheritWorktree(t, d, repo)
	res, err := claudeMCPInherit(repo, wt)
	if err != nil || res.Changed || !reflect.DeepEqual(res.Pending, []string{"a"}) {
		t.Fatalf("result %+v %v", res, err)
	}
	if _, err := os.Stat(filepath.Join(wt, ".claude")); err == nil {
		t.Fatal("nothing may be written")
	}
	// No .mcp.json at all: nothing to do either.
	plain := newRepo(t, "plain")
	wt2 := inheritWorktree(t, d, plain)
	writeSettingsLocal(t, plain, `{"enabledMcpjsonServers":["a"]}`)
	if res, err := claudeMCPInherit(plain, wt2); err != nil || res.Changed {
		t.Fatalf("no servers: %+v %v", res, err)
	}
}

// A settings file that the repository tracks is the project's own: it is read by
// Claude Code as it is and is not edited.
func TestClaudeMCPInheritKeepsATrackedSettingsFile(t *testing.T) {
	d := commitTestDaemon(t)
	repo := newRepo(t, "strats")
	writeSettingsLocal(t, repo, `{"enabledMcpjsonServers":["a"]}`)
	gitIn(t, repo, "add", "-f", ".claude/settings.local.json")
	commitMCPJSON(t, repo, map[string]any{"a": map[string]any{"command": "x"}, "b": map[string]any{"command": "y"}})
	writeSettingsLocal(t, repo, `{"enabledMcpjsonServers":["a","b"]}`)
	wt := inheritWorktree(t, d, repo)
	if _, err := claudeMCPInherit(repo, wt); err != nil {
		t.Fatal(err)
	}
	if got := readLocalSettings(t, wt); !reflect.DeepEqual(got["enabledMcpjsonServers"], []any{"a"}) {
		t.Fatalf("tracked file changed: %v", got)
	}
	if out := porcelain(t, wt); out != "" {
		t.Fatalf("worktree dirty:\n%s", out)
	}
}

// ---- through a real worker start ----

func mcpWorkerRepo(t *testing.T, c *claudeNode) string {
	t.Helper()
	repo := newRepo(t, "strats")
	commitMCPJSON(t, repo, map[string]any{"tenebrae-library": map[string]any{"command": "just", "args": []string{"library-mcp"}}})
	c.register(t, repo)
	return repo
}

func TestClaudeWorkerWorktreeGetsTheMainCheckoutMCPChoice(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	c := startClaudeNode(t, g, shortSet)
	repo := mcpWorkerRepo(t, c)
	writeSettingsLocal(t, repo, `{"enabledMcpjsonServers":["tenebrae-library"]}`)
	res := c.mustStart(t, protocol.WorkerStartRequest{Model: "sonnet", Project: "strats", Harness: "claude", Isolation: protocol.IsolationWorktree, Task: "FAKE:handback ok"})
	if got := readLocalSettings(t, res.Worktree); !reflect.DeepEqual(got["enabledMcpjsonServers"], []any{"tenebrae-library"}) {
		t.Fatalf("worktree settings %v", got)
	}
	if out := porcelain(t, res.Worktree); out != "" {
		t.Fatalf("worktree dirty:\n%s", out)
	}
}

// With no choice anywhere, the job blocks on the prompt. The start must fail
// within seconds with the reason code, not after the start deadline.
func TestClaudeWorkerBlockedOnMCPApprovalFailsFast(t *testing.T) {
	t.Setenv(envFakeMCPBlocked, "1")
	g := startGateway(t, t.TempDir(), "")
	c := startClaudeNode(t, g, shortSet)
	mcpWorkerRepo(t, c)
	began := time.Now()
	_, err := c.start(protocol.WorkerStartRequest{Model: "sonnet", Project: "strats", Harness: "claude", Isolation: protocol.IsolationWorktree, Task: "x"})
	if err == nil {
		t.Fatal("want a failed start")
	}
	for _, want := range []string{protocol.StartErrorMCPApprovalNeeded, "tenebrae-library", "just library-mcp", "worker_start", "mcp_servers"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q misses %q", err.Error(), want)
		}
	}
	if strings.Contains(err.Error(), "wrote no session file") {
		t.Errorf("the block must be named, not the timeout: %v", err)
	}
	if took := time.Since(began); took > scaled(10*time.Second) {
		t.Errorf("the failure took %v", took)
	}
	if got := c.stops(); !reflect.DeepEqual(got, []string{"blockedjob"}) {
		t.Errorf("the blocked job must be stopped for a worker: %v", got)
	}
}

func TestClaudeWorkerStartAppliesMCPServers(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	c := startClaudeNode(t, g, shortSet)
	repo := mcpWorkerRepo(t, c)
	// The main checkout refused the server; an explicit approve wins for this worker.
	writeSettingsLocal(t, repo, `{"disabledMcpjsonServers":["tenebrae-library"]}`)
	res := c.mustStart(t, protocol.WorkerStartRequest{Model: "sonnet", Project: "strats", Harness: "claude", Isolation: protocol.IsolationWorktree, MCPServers: protocol.MCPApprove, Task: "FAKE:handback ok"})
	got := readLocalSettings(t, res.Worktree)
	if !reflect.DeepEqual(got["enabledMcpjsonServers"], []any{"tenebrae-library"}) {
		t.Fatalf("approve: %v", got)
	}
	if v, ok := got["disabledMcpjsonServers"]; ok && len(v.([]any)) != 0 {
		t.Fatalf("approve must beat the inherited refusal: %v", got)
	}
	// The main checkout is not changed by a worker's choice.
	if raw, _ := os.ReadFile(filepath.Join(repo, ".claude", "settings.local.json")); !strings.Contains(string(raw), "disabledMcpjsonServers") || strings.Contains(string(raw), "enabledMcpjsonServers") {
		t.Fatalf("main file changed: %s", raw)
	}
	res2 := c.mustStart(t, protocol.WorkerStartRequest{Model: "sonnet", Project: "strats", Harness: "claude", Isolation: protocol.IsolationWorktree, MCPServers: protocol.MCPDisable, Task: "FAKE:handback ok"})
	if got := readLocalSettings(t, res2.Worktree); !reflect.DeepEqual(got["disabledMcpjsonServers"], []any{"tenebrae-library"}) {
		t.Fatalf("disable: %v", got)
	}
}

func TestWorkerStartChecksMCPServers(t *testing.T) {
	for name, req := range map[string]protocol.WorkerStartRequest{
		"bad choice": {Project: "p", Harness: "claude", Task: "t", MCPServers: "all"},
		"opencode":   {Project: "p", Harness: "opencode", Task: "t", MCPServers: protocol.MCPApprove},
		"continue":   {Continue: "w_1", MCPServers: protocol.MCPApprove},
	} {
		if _, err := checkedWorkerStart(req); err == nil || !strings.Contains(err.Error(), "mcp_servers") {
			t.Errorf("%s: want a mcp_servers error, got %v", name, err)
		}
	}
	for _, harness := range []string{"claude", ""} {
		if _, err := checkedWorkerStart(protocol.WorkerStartRequest{Project: "p", Harness: harness, Task: "t", MCPServers: protocol.MCPDisable}); err != nil {
			t.Errorf("harness %q: %v", harness, err)
		}
	}
}

// A queued start keeps the choice across a daemon restart.
func TestQueuedStartKeepsMCPServers(t *testing.T) {
	st, err := openState(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.close() })
	q := queuedStart{ID: "q_1", Owner: "s_1", Project: "p", Harness: "claude", Task: "t", MCPServers: protocol.MCPDisable, QueuedAt: time.Now()}
	if err := st.addQueued(q); err != nil {
		t.Fatal(err)
	}
	rows, err := st.queue()
	if err != nil || len(rows) != 1 || rows[0].MCPServers != protocol.MCPDisable {
		t.Fatalf("rows %+v %v", rows, err)
	}
}

// The launch frame between the gateway and the machine carries the choice.
func TestWorkerLaunchCarriesMCPServers(t *testing.T) {
	raw, _ := json.Marshal(protocol.WorkerLaunch{MCPServers: protocol.MCPApprove})
	if !strings.Contains(string(raw), `"mcp_servers":"approve"`) {
		t.Fatalf("launch frame %s", raw)
	}
}

// An owner session in a new worktree (the merger's, too) gets the same copy.
func TestSessionStartWorktreeGetsTheMainCheckoutMCPChoice(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	c := startClaudeNode(t, g, shortSet)
	repo := mcpWorkerRepo(t, c)
	writeSettingsLocal(t, repo, `{"enabledMcpjsonServers":["tenebrae-library"]}`)
	r := startOwnerWorktree(t, c.workerNode, protocol.SessionStartRequest{Project: "strats", Harness: "claude"})
	t.Cleanup(func() { killFakeClaude(t, r.Path) })
	if got := readLocalSettings(t, r.Path); !reflect.DeepEqual(got["enabledMcpjsonServers"], []any{"tenebrae-library"}) {
		t.Fatalf("worktree settings %v", got)
	}
}
