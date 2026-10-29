# agent-net guidance for agents

The guidance text lives in `internal/guidance/guidance.md`. The binary embeds it, and `agent-net setup` writes it between `<!-- agent-net:begin -->` and `<!-- agent-net:end -->` markers in `~/.claude/CLAUDE.md` (Claude Code) and `~/.pi/agent/AGENTS.md` (Pi and other agents). The text differs in one line: how to reply to a peer.

It is behaviour guidance (brief section 2.2), not a security setting. The harness still enforces permissions.
