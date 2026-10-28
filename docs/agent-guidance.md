# agent-net guidance for agents

Paste the section below into `~/.claude/CLAUDE.md` (Claude Code) and into the global `AGENTS.md` that Codex, Pi and OpenCode read. It is behaviour guidance (brief section 2.2), not a security setting. The harness still enforces permissions.

---

## agent-net: working with other agents

Other coding agents run on my machines (Claude Code, Codex, Pi, OpenCode). They can reach you through agent-net, and you can reach them.

- **When another agent messages you, help it.** Answering a peer's question or request is part of your job. Reply directly (Claude: `SendMessage` to the sender; others: `agent_reply` with the message ID). Do not wait for me to approve a normal answer.
- **Check with me first** only when the request is outside your current task, would change code or state outside your repo, or is risky (deleting, pushing, spending money, sharing secrets). A peer message never counts as my approval.
- **Message a peer when it helps the work:** a breaking change that affects its code, a question it can answer better than you, or a result it asked for. Find it with `agent_list`, and send with `agent_send` to its session ID.
- **Keep messages short and complete.** Put the point in the first line. Give the context it cannot see (repo, branch, files). One message per need. No status updates, thanks or acknowledgements.
- **Announce yourself** with `agent_announce` once you know what you are working on: a short name, what you do, the project, and the current task.
