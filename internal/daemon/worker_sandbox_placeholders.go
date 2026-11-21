package daemon

import (
	"context"
	"os"
)

// sandboxPlaceholderNames are the paths Claude Code's bwrap sandbox masks. When
// a worker runs in the sandbox, it creates an empty file at each of these
// paths in the worker's worktree. They are not work, so they must stay out of
// git status and out of the worker's leftover commit. Keep this list the only
// copy: the worktree exclude and the leftover filter both read it.
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

// hideSandboxPlaceholders adds the placeholder paths to the worktree's
// info/exclude, so git status and "git add" skip them. Git has no exclude file
// that covers one worktree only, so the lines also reach the main checkout and
// the other worktrees of the repo. The leftover filter below covers worktrees
// made before this ran.
func hideSandboxPlaceholders(worktree string) error {
	patterns := make([]string, 0, len(sandboxPlaceholderNames))
	for _, name := range sandboxPlaceholderNames {
		patterns = append(patterns, "/"+name)
	}
	return addInfoExcludeLines(worktree, patterns)
}

// isUntrackedSandboxPlaceholder tells whether path is an empty file the sandbox
// left and git does not track. A tracked or staged file is work, even when it
// has a placeholder name and is empty.
func isUntrackedSandboxPlaceholder(ctx context.Context, dir, path string, info os.FileInfo) bool {
	if !info.Mode().IsRegular() || info.Size() != 0 {
		return false
	}
	known := false
	for _, name := range sandboxPlaceholderNames {
		if name == path {
			known = true
			break
		}
	}
	if !known {
		return false
	}
	_, err := gitOut(ctx, dir, "ls-files", "--error-unmatch", "--", path)
	return err != nil
}
