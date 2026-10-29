# 0009 - FUNCTION comes from AGENTS.md, and announcing the task is required

**Date:** 2026-10-29
**Status:** Built, not merged
**Context:** the FUNCTION column of `agent-net list` and the TUI was text the model wrote about itself in `agent_announce`. Most sessions never announced, so they showed `-`. The text that existed was vague ("Repository maintenance and verification") and went stale. Announcing could also rename the session. The owner: "it has to be mandatory. it has to be based on the agents.md file." Related: [0003](0003-gateway-record-and-inventory.md) D2 (the daemon works out the project of a session).

## D1 - The daemon reads the description from the project's instruction file

FUNCTION is the one-line description of the project. The daemon reads it, in the same place where it works out the repo info (D2 of 0003), and sends it with the session as `repo.description`. The model does not write it.

- **Which file:** `AGENTS.md` in the main working tree, else `CLAUDE.md` in the main working tree. A linked git worktree uses the main tree's file, as it does for the project name. When the folder is not in a git repository, the folder itself is used. If `AGENTS.md` exists but gives no description, `CLAUDE.md` is tried.
- **Cache:** per file path, valid while the modification time and the size are the same. Each session update costs one `stat` per file. A changed file is read again at the next update, with no wait. Only the first 32 KiB are read.
- **Wire:** a new `description` field in `repo` (left out when empty), stored in a new `sessions.repo_description` column (migration 8, after the worker migration 7). An older gateway ignores the field. A gateway meeting an older daemon gets no field and shows no function. The gateway cleans the text (control characters out, 120 characters at most), because a daemon is not trusted for size.
- **Empty is a real answer:** no file, or no description line, gives an empty FUNCTION. `agent-net list` shows `-`. The TUI shows "no AGENTS.md" dimmed.

## D2 - The extraction rule

The owner's projects use a standard template (the `apply-project-agent-template` skill):

```
# Project: <Project Name>
A <one-sentence description of what it is and why it exists>.
```

The rule, in `internal/repoinfo/description.go` (`ExtractDescription`):

1. **Start.** The first `# Project: ...` heading if there is one. Else the first `# ` heading. Else the top of the file. Front matter and fenced code blocks are ignored.
2. **Read** until the next heading of any level. In the template form, the description is the first non-empty line after the heading. Otherwise it is the first plain paragraph (its lines joined with spaces).
3. **Skip** paragraphs that are not plain text: lists, tables, quotes, HTML, rules, `@file` import lines, and notes to agents. A note to agents is a paragraph that starts with "Notes/Instructions/Guidelines/Rules/Tips/Context for|to ... agents|assistants|Claude|Codex|LLM|AI", or with "This file/document/section ... agents|Claude|AI".
4. **Strip markdown:** images, links (the text stays), inline code, bold, italic, HTML tags. A `[TODO ...]` placeholder is cut off; a line that is only a placeholder gives nothing.
5. **First sentence:** cut at the first `. `, `! ` or `? `, except after `e.g.`, `i.e.`, `vs.`, `etc.`. A final full stop is dropped.
6. **Length:** at most 120 characters, cut at a word where one is near, with `…` at the end.

This repository's own `AGENTS.md` starts with "Notes for coding agents ...", so it gives nothing. A test pins that and shows the one-line fix (a `# Project: agent-net` heading and a description line).

## D3 - What announce does now

`agent_announce` sets the **task** (required) and nothing else that shows as FUNCTION.

- **`function` is ignored, not a fallback.** A fallback would bring back the vague text the owner does not want, and it would make FUNCTION mean two things. With one source, "no AGENTS.md" is a clear message to fix the project. The gateway ignores the field, so it also covers an older daemon. Text stored by earlier announces stays in the `profiles` table but is not shown.
- **`name` renames the session only when given.** Before, the name was required, so every announce renamed the session. Now the gateway keeps the stored name (and project and capabilities) when a later announce leaves them out. Names the harness derives (OpenCode titles, which the plugin reports with `status`) are not touched. Capabilities can be cleared with an empty list.
- **A task is required.** The daemon refuses an announce with no task and no name ("announce needs a task"). A name without a task is still accepted so that a plugin or MCP shim from before this change keeps working with a new daemon.

## D4 - Announcing is required, in five places

At the start of every session, before other work, call `agent_announce` with the current task in one short line. Call it again when the task changes. The text is in:

1. the guidance block that setup writes (`internal/guidance/guidance.md`, for Claude, Pi and OpenCode);
2. the MCP server `instructions` (`mcpserver.Instructions`; Claude Code shows them to the model at the start). They were not set before;
3. the `agent_announce` tool description (MCP server, Pi and OpenCode plugins);
4. the Pi system prompt (`before_agent_start` handler returns the prompt with the rule added) and the OpenCode system prompt (`session.hook("context")` adds a system part);
5. a reminder: until the session has announced, every agent-net tool result ends with the line "Reminder: call agent_announce with your current task." For Claude it is a receiving middleware of the MCP server (tools named `agent_*`, errors too; tools of registered MCP servers are not touched). For Pi and OpenCode it is in the tool code. The state is per process for Claude and Pi, and per session for OpenCode. It is advice to the model, as the guidance always was (see [security.md](../security.md)): nothing blocks a session that does not announce.

The task shows in `agent_list` and `agent_get`, in the TUI, and as TASK in `agent-net list --wide`.

## Compatibility

Two versions side by side work. Old daemon with new gateway: no description, so FUNCTION is empty until the machine upgrades. New daemon with old gateway: the extra `description` field is ignored, and the old gateway shows the announced function, which a new daemon no longer sends (it sends an empty one). New MCP shim, Pi or OpenCode plugin with an old daemon: an announce with only a task fails with "announce needs a name"; the shim follows the daemon version (auto-upgrade), and the plugins are replaced by `agent-net setup`.
