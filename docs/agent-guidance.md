# agent-net guidance for agents

The guidance text lives in `internal/guidance/guidance.go`. The binary embeds it, and `agent-net setup` writes it between `<!-- agent-net:begin -->` and `<!-- agent-net:end -->` markers in four files: `~/.claude/CLAUDE.md`, `~/.pi/agent/AGENTS.md`, `~/.config/opencode/AGENTS.md` and `~/.codex/AGENTS.md`. Three variants (Claude, other, combined) differ in the reply line.

It is behaviour guidance (brief section 2.2), not a security setting. The harness still enforces permissions.
