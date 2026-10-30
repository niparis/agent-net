Read AGENTS.md before working. Go is at /home/nicolas/.local/go/bin; add it to PATH.

Never touch the real setup: ~/.config/agent-net, ~/.local/state/agent-net (except your own worktree), ~/.local/bin, the real daemon socket, ~/.claude, ~/.config/opencode, ~/.local/share/opencode, ~/.pi, ~/.codex, or any running server. Use temporary directories and fake harnesses for tests. Do not make paid model calls.

Write a failing test first for each behavior. Run gofmt, go vet ./..., and go test -race ./... (use -timeout 25m for the daemon tests). Commit on your branch. Do not push or merge.

In your report say what you built, list the files, show the failing tests first, and give the check results. Use absolute paths.
