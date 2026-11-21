package daemon

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/harnesses"
	"github.com/niparis/agent-net/internal/protocol"
)

func stringList(t *testing.T, v any, what string) []string {
	t.Helper()
	if items, ok := v.([]string); ok {
		return items
	}
	items, ok := v.([]any)
	if !ok {
		t.Fatalf("%s is %T, want a list: %v", what, v, v)
	}
	out := make([]string, 0, len(items))
	for _, item := range items {
		s, ok := item.(string)
		if !ok {
			t.Fatalf("%s holds %T, want strings: %v", what, item, items)
		}
		out = append(out, s)
	}
	return out
}

func hasString(items []string, want string) bool {
	for _, item := range items {
		if item == want {
			return true
		}
	}
	return false
}

// A Claude worker's launch carries the write boundary of its permission level
// to the session: its file tools and its Bash sandbox may write only in the
// worker's own worktree, the shared Go caches and the worker's temp folders.
func TestClaudeWorkerLaunchRestrictsWritesToItsWorktree(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	c := startClaudeNode(t, g, shortSet)
	repo := newRepo(t, "shop")
	c.register(t, repo)

	res := c.mustStart(t, protocol.WorkerStartRequest{Project: "shop", Harness: "claude", Model: "fake/m1",
		Task: "FAKE:ignore hold the turn", Isolation: protocol.IsolationWorktree})
	rec := readClaude(t, res.Worktree)

	perms, ok := rec.Settings["permissions"].(map[string]any)
	if !ok {
		t.Fatalf("the launch settings carry no permissions: %v", rec.Settings)
	}
	if perms["blockWritesOutsideWorkingDirectories"] != true {
		t.Errorf("file-tool writes outside the working directory are not refused: %v", perms)
	}
	if allow := stringList(t, perms["allow"], "permissions.allow"); !hasString(allow, "WebFetch(domain:*)") {
		t.Errorf("a sandboxed worker's Bash has no host allowed, so its network calls would wait for an answer: %v", allow)
	}

	sandbox, ok := rec.Settings["sandbox"].(map[string]any)
	if !ok {
		t.Fatalf("the launch settings carry no Bash sandbox: %v", rec.Settings)
	}
	if sandbox["enabled"] != true {
		t.Errorf("the Bash sandbox is off: %v", sandbox)
	}
	if sandbox["allowUnsandboxedCommands"] != false {
		t.Errorf("a command could retry outside the sandbox: %v", sandbox)
	}
	files, ok := sandbox["filesystem"].(map[string]any)
	if !ok {
		t.Fatalf("the sandbox carries no filesystem rules: %v", sandbox)
	}
	writes := stringList(t, files["allowWrite"], "sandbox.filesystem.allowWrite")
	for _, want := range []string{res.Worktree, c.d.workerGoCacheDir(), c.d.workerGoTmpDir()} {
		if !hasString(writes, want) {
			t.Errorf("the worker cannot write %s: %v", want, writes)
		}
	}
	if hasString(writes, "/") || hasString(writes, repo) {
		t.Errorf("the worker may write outside its worktree: %v", writes)
	}
}

// The other levels keep their meaning: no-network blocks the sandbox's hosts,
// and read-only also refuses writes inside the worktree.
func TestClaudeWorkerPermissionLevels(t *testing.T) {
	dir := "/state/worktrees/w_1"
	writes := []string{dir, "/tmp/agent-net-gocache", "/tmp/agent-net-gotmp"}

	sandboxed := claudeWorkerSandbox(protocol.WorkersSandboxed, dir, writes)
	fs := sandboxed["filesystem"].(map[string]any)
	if got := stringList(t, fs["allowWrite"], "allowWrite"); len(got) != len(writes) {
		t.Errorf("sandboxed allowWrite = %v, want %v", got, writes)
	}
	if perms := claudeWorkerPermissions(protocol.WorkersSandboxed, dir); perms["allow"] == nil {
		t.Error("a sandboxed worker's Bash has no host allowed")
	}

	offline := claudeWorkerPermissions(protocol.WorkersNoNetwork, dir)
	if offline["allow"] != nil {
		t.Errorf("a no-network worker's hosts are pre-allowed: %v", offline["allow"])
	}
	if fs := claudeWorkerSandbox(protocol.WorkersNoNetwork, dir, writes)["filesystem"].(map[string]any); fs["allowWrite"] == nil {
		t.Error("a no-network worker lost its worktree writes")
	}

	readOnly := claudeWorkerPermissions(protocol.WorkersReadOnly, dir)
	if deny := stringList(t, readOnly["deny"], "permissions.deny"); !hasString(deny, "Edit(//state/worktrees/w_1/**)") {
		t.Errorf("a read-only worker may write its worktree with a file tool: %v", deny)
	}
	if readOnly["allow"] == nil {
		t.Errorf("a read-only worker's network is on but its hosts are not pre-allowed: %v", readOnly["allow"])
	}
	fs = claudeWorkerSandbox(protocol.WorkersReadOnly, dir, writes)["filesystem"].(map[string]any)
	if fs["allowWrite"] != nil {
		t.Errorf("a read-only worker may write: %v", fs["allowWrite"])
	}
	if deny := stringList(t, fs["denyWrite"], "denyWrite"); !hasString(deny, dir) {
		t.Errorf("a read-only worker may write its worktree with Bash: %v", deny)
	}
}

// A worktree's git data lives in its repository, and a Go change may need a
// module the machine has not downloaded: both stay writable, or git commit and
// go mod download fail inside the sandbox. Hooks and config stay closed.
func TestClaudeWorkerWritesCoverGitDataAndModuleCache(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	c := startClaudeNode(t, g, shortSet)
	base := t.TempDir()
	modCache := filepath.Join(base, "modules")
	script := "#!/bin/sh\ncase \"$*\" in\n'env GOMODCACHE') echo \"$FAKE_GO_CACHE\";;\n*) exit 1;;\nesac\n"
	if err := os.WriteFile(filepath.Join(base, "go"), []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FAKE_GO_CACHE", modCache)
	c.d.cfg.HarnessSearch = &harnesses.Options{PATH: base, Dirs: []string{}, Home: t.TempDir(),
		LoginShellPATH: func(context.Context) string { return "" }}
	repo := newRepo(t, "shop")
	c.register(t, repo)

	res := c.mustStart(t, protocol.WorkerStartRequest{Project: "shop", Harness: "claude", Model: "fake/m1",
		Task: "FAKE:ignore hold the turn", Isolation: protocol.IsolationWorktree})
	rec := readClaude(t, res.Worktree)

	files := rec.Settings["sandbox"].(map[string]any)["filesystem"].(map[string]any)
	writes := stringList(t, files["allowWrite"], "sandbox.filesystem.allowWrite")
	gitRoots := workerGitRoots(context.Background(), res.Worktree)
	if len(gitRoots) < 3 {
		t.Fatalf("test setup: git roots of the worktree = %v", gitRoots)
	}
	for _, want := range append(gitRoots, modCache) {
		if !hasString(writes, want) {
			t.Errorf("the worker cannot write %s: %v", want, writes)
		}
	}
	for _, p := range writes {
		if p == filepath.Join(repo, ".git") || strings.HasPrefix(p, filepath.Join(repo, ".git", "hooks")) || p == filepath.Join(repo, ".git", "config") {
			t.Errorf("the worker may write %s, which can run code outside the sandbox: %v", p, writes)
		}
	}
}
