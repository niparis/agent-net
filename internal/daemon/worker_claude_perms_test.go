package daemon

import (
	"context"
	"os"
	"path/filepath"
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
	for _, want := range []string{res.Worktree, c.d.workerGoCacheDir(), c.d.workerSandboxGoTmpDir()} {
		if !hasString(writes, want) {
			t.Errorf("the worker cannot write %s: %v", want, writes)
		}
	}
	if hasString(writes, "/") || hasString(writes, repo) {
		t.Errorf("the worker may write outside its worktree: %v", writes)
	}
}

// A Claude worker's Bash sandbox keeps one rule (decision 0197): it writes its
// worktree, the repository's whole git folder (so commit, switch -c, push -u
// and worktree add work), its caches and the Go module cache, and nothing
// else; every unix socket and local port stay open.
func TestClaudeWorkerSandboxFollowsTheWriteFence(t *testing.T) {
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

	sandbox := rec.Settings["sandbox"].(map[string]any)
	writes := stringList(t, sandbox["filesystem"].(map[string]any)["allowWrite"], "sandbox.filesystem.allowWrite")
	for _, want := range []string{res.Worktree, filepath.Join(repo, ".git"), modCache, c.d.workerGoCacheDir()} {
		if !hasString(writes, want) {
			t.Errorf("the worker cannot write %s: %v", want, writes)
		}
	}
	for _, p := range writes {
		if p == "/" || p == repo || p == os.Getenv("HOME") {
			t.Errorf("the worker may write %s, outside the fence: %v", p, writes)
		}
	}
	network, _ := sandbox["network"].(map[string]any)
	if network["allowAllUnixSockets"] != true || network["allowLocalBinding"] != true {
		t.Errorf("unix sockets or local ports are closed: %v", network)
	}
}
