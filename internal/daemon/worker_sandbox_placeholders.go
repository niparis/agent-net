package daemon

import (
	"context"
	"os"
	"strings"
)

// sandboxPlaceholderNames are the paths Claude Code's bwrap sandbox masks. When
// a worker runs in the sandbox, it creates an empty file at each of these
// paths in the worker's worktree. They are not work, so the leftover commit
// must not add them. They are not written to any git exclude: info/exclude is
// shared with the owner's checkout, where a real .mcp.json must stay visible.
var sandboxPlaceholderNames = []string{
	".bash_profile",
	".bashrc",
	".claude/agents",
	".claude/commands",
	".claude/hooks",
	".claude/launch.json",
	".claude/loop.md",
	".claude/output-styles",
	".claude/routines",
	".claude/settings.json",
	".claude/skills",
	".claude/workflows",
	".gitconfig",
	".gitmodules",
	".idea",
	".mcp.json",
	".profile",
	".ripgreprc",
	".vscode",
	".zprofile",
	".zshrc",
}

// isUntrackedSandboxPlaceholder tells whether path is an empty file the sandbox
// left and git does not track. The leftover commit skips these. A tracked or
// staged file is work, even when it has a placeholder name and is empty.
func isUntrackedSandboxPlaceholder(ctx context.Context, dir, path string, info os.FileInfo) bool {
	if !info.Mode().IsRegular() || info.Size() != 0 {
		return false
	}
	if !isSandboxPlaceholderPath(path) {
		return false
	}
	_, err := gitOut(ctx, dir, "ls-files", "--error-unmatch", "--", path)
	return err != nil
}

// isSandboxPlaceholderPath tells whether a worktree-relative path has a placeholder
// name at the root or in any subfolder. A worker that runs commands in a
// subfolder gets the same masked files there.
func isSandboxPlaceholderPath(path string) bool {
	for _, name := range sandboxPlaceholderNames {
		if path == name || strings.HasSuffix(path, "/"+name) {
			return true
		}
	}
	return false
}
