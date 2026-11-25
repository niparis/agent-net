package daemon

import "strings"

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
