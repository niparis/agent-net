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
// Codex has no read-only list inside a writable root, so a Codex worker gets
// only the git folders a commit writes (workerGitRoots), never the git folder
// whose config and hooks run code (decision 0197).

// gitFence is the git part of a worker's write fence (decision 0197). Write is
// the repository's git folder, with the worktree's own git folder when it lies
// outside it, so commit, switch -c and worktree add work. ReadOnly lists what
// inside Write makes git run code: config, config.worktree, hooks, info,
// modules, the folder core.hooksPath names, and the folders of the other
// linked worktrees (their commondir file says which config git reads). Git
// runs that code with no sandbox when the owner, the merger or the daemon run
// git there. Only paths that exist are listed: hooks and info are made when
// they are missing, so a worker cannot make them.
type gitFence struct{ Write, ReadOnly []string }

func workerGitFence(ctx context.Context, dir string) gitFence {
	common, err := gitOut(ctx, dir, "rev-parse", "--path-format=absolute", "--git-common-dir")
	if err != nil || common == "" {
		return gitFence{}
	}
	f := gitFence{Write: []string{common}}
	own, _ := gitOut(ctx, dir, "rev-parse", "--path-format=absolute", "--git-dir")
	if own != "" && !underAny(own, f.Write) {
		f.Write = append(f.Write, own)
	}
	for _, sub := range []string{"hooks", "info"} {
		_ = os.MkdirAll(filepath.Join(common, sub), 0o755)
	}
	ro := []string{"config", "config.worktree", "hooks", "info", "modules"}
	for i, sub := range ro {
		ro[i] = filepath.Join(common, sub)
	}
	// The worker's own git config settings (GIT_CONFIG_*) are left out: the
	// folder that counts is the one git uses outside the worker.
	if hp, _ := gitOutWithEnv(ctx, dir, []string{"GIT_CONFIG_COUNT=0"}, "config", "--get", "core.hooksPath"); hp != "" {
		if strings.HasPrefix(hp, "~/") {
			hp = filepath.Join(home(), hp[2:])
		}
		if filepath.IsAbs(hp) {
			ro = append(ro, filepath.Clean(hp))
		}
	}
	others, _ := os.ReadDir(filepath.Join(common, "worktrees"))
	for _, e := range others {
		if p := filepath.Join(common, "worktrees", e.Name()); e.IsDir() && p != own {
			ro = append(ro, p)
		}
	}
	for _, p := range ro {
		if fileExists(p) && !containsString(f.ReadOnly, p) {
			f.ReadOnly = append(f.ReadOnly, p)
		}
	}
	return f
}

// workerGitRoots returns the folders of the main repository that a commit in
// dir writes to: the objects, the refs and their logs, and the private folder
// of a linked worktree (its index and HEAD). Codex and agy workers and Codex
// role sessions use it: their sandboxes have no read-only list (gitFence).
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

// codexRootsFor are the extra writable folders of a worker (workerFence).
func (d *Daemon) codexRootsFor(ctx context.Context, w *worker) []string {
	return d.workerFence(ctx, w).Narrow
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
