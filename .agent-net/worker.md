Read AGENTS.md before working. Go is at /home/nicolas/.local/go/bin; add it to PATH. If you cannot write the default Go build cache, use GOCACHE=/tmp/agent-net-gocache, shared by all workers. Never create another cache folder: the disk fills up. Also set GOTMPDIR=/tmp/agent-net-gotmp (create it if missing), so build folders of interrupted test runs land in one place that can be cleared.

Never touch the real setup: ~/.config/agent-net, ~/.local/state/agent-net (except your own worktree), ~/.local/bin, the real daemon socket, ~/.claude, ~/.config/opencode, ~/.local/share/opencode, ~/.pi, ~/.codex, or any running server. Use temporary directories and fake harnesses for tests. Do not make paid model calls.

Write a failing test first for each behavior. Run gofmt -l . and go vet on the packages you changed. Run go test -race ONLY for the packages you changed (for example go test -race ./internal/protocol/ ./internal/tui/), and in internal/daemon only the tests for your change (-run with their names, -timeout 10m). Never run the whole suite (go test ./... or scripts/test.sh): CI runs it before every release. Run each test command once after your change and once more only if you fixed something.

Stay on the branch you started from. Do not merge or rebase main, even if main moves; the caller merges at release time. Commit on your branch. Do not push or merge.

In your report say what you built, list the files, show the failing tests first, and give the check results. Use absolute paths.
