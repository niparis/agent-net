# Step 1 spike results

Date: 2026-10-28. Machine: Linux VM (this one). Throwaway sessions in tmux, never real work sessions.

| Harness / question | Status | Page |
|---|---|---|
| Claude Code: discovery, idle wake, busy, native reply | Works | [claude.md](claude.md) |
| Claude Code: receiver in bypass-permissions mode | Not tested (docs say: held, dropped after 5 min) | [claude.md](claude.md) |
| Codex CLI: idle wake, busy queue | Works | [codex.md](codex.md) |
| Codex Desktop | Not tested (needs the MacBook) | - |
| Per-session MCP process knows its session (Claude) | Works | [claude.md](claude.md) |
| Per-session MCP process knows its session (Codex) | Not tested | [codex.md](codex.md) |
| Pi: idle wake, busy follow-up, reply tool | Works | [pi.md](pi.md) |
| OpenCode v2: idle wake, busy queue, session events | Works | [opencode.md](opencode.md) |
| OpenCode v2: reply tool in the plugin, session ID in tool context | Not tested | [opencode.md](opencode.md) |
| Per-session MCP process knows its session (OpenCode) | No: MCP servers run under the shared service. Use plugin tools. | [opencode.md](opencode.md) |
| Grok Build | Deferred | - |

Spike code: [`spike/claude/main.go`](../../spike/claude/main.go).
