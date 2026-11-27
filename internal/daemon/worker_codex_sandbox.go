package daemon

import (
	"context"
	"os"
	"path/filepath"
	"strings"
)

// The permissions of a Codex worker (decision 0020, which amends 0002 W6).
//
// Nobody watches a worker and the daemon declines every approval, so a worker
// must never need one. Instead of approval prompts it runs in Codex's sandbox.
// The fields (codex-cli 0.163.0, `codex app-server generate-json-schema`):
//
//   - thread/start: approvalPolicy "never"; sandbox "workspace-write" or
//     "read-only"; config {"sandbox_workspace_write": {"writable_roots": [...],
//     "network_access": bool}}. The answer repeats the effective sandbox.
//   - turn/start: approvalPolicy "never" and sandboxPolicy (a workspaceWrite
//     policy with writableRoots and networkAccess, or readOnly with
//     networkAccess), sent on every turn so the policy cannot drift.
//   - the app-server command line: -c mcp_servers.agent-net.enabled=false,
//     only when the user's Codex config defines that server (without a
//     definition Codex stops with "invalid transport").
//
// Codex keeps a .git inside a writable root read-only, so the repository's git
// folder is a writable root of its own (workerWriteRoots, decision 0197).

// workerGitDirs is the git folder of the repository that holds the worktree at
// dir, with the worktree's own git folder when it lies outside it. A worker
// may write all of it, so commit, switch -c, push -u and worktree add work
// (decision 0197).
func workerGitDirs(ctx context.Context, dir string) []string {
	common, err := gitOut(ctx, dir, "rev-parse", "--path-format=absolute", "--git-common-dir")
	if err != nil || common == "" {
		return nil
	}
	dirs := []string{common}
	if gd, err := gitOut(ctx, dir, "rev-parse", "--path-format=absolute", "--git-dir"); err == nil && gd != "" && !underAny(gd, dirs) {
		dirs = append(dirs, gd)
	}
	return dirs
}

// workerGitRoots returns the folders of the main repository that a commit in
// dir writes to: the objects, the refs and their logs, and the private folder
// of a linked worktree (its index and HEAD). Codex role sessions use it; a
// worker gets the whole git folder (workerGitDirs).
func workerGitRoots(ctx context.Context, dir string) []string {
	common, err := gitOut(ctx, dir, "rev-parse", "--path-format=absolute", "--git-common-dir")
	if err != nil || common == "" {
		return nil
	}
	var roots []string
	if gd, err := gitOut(ctx, dir, "rev-parse", "--path-format=absolute", "--git-dir"); err == nil && gd != "" && gd != common {
		roots = append(roots, gd)
	}
	for _, sub := range []string{"objects", "refs", "logs"} {
		p := filepath.Join(common, sub)
		if fi, err := os.Stat(p); err == nil && fi.IsDir() {
			roots = append(roots, p)
		}
	}
	return roots
}

// codexRootsFor are the extra writable folders of a worker: the ones every
// worker gets (workerWriteRoots).
func (d *Daemon) codexRootsFor(ctx context.Context, w *worker) []string {
	return d.workerWriteRoots(ctx, w)
}

// codexSandbox is what the worker's level means for Codex.
type codexSandbox struct {
	mode    string         // thread/start "sandbox"
	policy  map[string]any // turn/start "sandboxPolicy"
	config  map[string]any // thread/start "config"
	network bool
}

func newCodexSandbox(roots []string) codexSandbox {
	return writeSandbox(roots, true)
}

func writeSandbox(roots []string, network bool) codexSandbox {
	if roots == nil {
		roots = []string{}
	}
	return codexSandbox{mode: "workspace-write", network: network,
		policy: map[string]any{"type": "workspaceWrite", "writableRoots": roots, "networkAccess": network},
		config: map[string]any{"sandbox_workspace_write": map[string]any{"writable_roots": roots, "network_access": network}}}
}

// threadParams adds the permission fields to the thread/start params.
func (s codexSandbox) threadParams(params map[string]any) {
	params["approvalPolicy"] = "never"
	params["sandbox"] = s.mode
	if s.config != nil {
		params["config"] = s.config
	}
}

// codexSandboxFor works out the sandbox of a worker, with the git folders of
// its project.
func (d *Daemon) codexSandboxFor(ctx context.Context, w *worker) codexSandbox {
	return newCodexSandbox(d.codexRootsFor(ctx, w))
}

// codexHome is the Codex home folder: the configured one, then CODEX_HOME,
// then ~/.codex. The workers, the CTO and the difficulty judge all read it.
func codexHome(cfg Config) string {
	if cfg.CodexHome != "" {
		return cfg.CodexHome
	}
	if home := os.Getenv("CODEX_HOME"); home != "" {
		return home
	}
	return defaultCodexHome()
}

// codexConfigDefinesAgentNet tells whether the Codex config in home defines the
// agent-net MCP server. A worker turns it off: it has no use for the tools, and
// Codex would ask approval to call them. Turning off a server that is not
// defined makes Codex fail, so this is checked first.
func codexConfigDefinesAgentNet(home string) bool {
	b, err := os.ReadFile(filepath.Join(home, "config.toml"))
	if err != nil {
		return false
	}
	for _, l := range strings.Split(string(b), "\n") {
		l = strings.TrimSpace(l)
		if strings.HasPrefix(l, "[mcp_servers.agent-net") || strings.HasPrefix(l, `[mcp_servers."agent-net"`) ||
			strings.HasPrefix(l, "[mcp_servers.'agent-net'") {
			return true
		}
	}
	return false
}
