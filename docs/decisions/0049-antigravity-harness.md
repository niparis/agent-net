# 0049 - Antigravity as a full harness

**Date:** 2026-11-03
**Status:** Proposed; plan only, nothing built
**Context:** [0012](0012-codex-peers.md) (how Codex became a peer), [0048](0048-skills-on-off-per-project.md) (skills already reach Antigravity), [0046](0046-open-sessions-in-herdr.md) (herdr).

## Goal

Antigravity (`agy`, Google's CLI) works like Claude Code, Codex, OpenCode and Pi:

- as a **worker** that agent-net starts, briefs, continues and stops;
- as a **main session** the owner opens, which other agents can message and which can message them.

## Facts

Checked on agy 1.4.12 (macOS, read-only, a few tiny model calls) on 2026-11-03, plus the [changelog](https://github.com/google-antigravity/antigravity-cli/blob/main/CHANGELOG.md), the [hooks docs](https://antigravity.google/docs/hooks) and the [MCP docs](https://antigravity.google/docs/mcp). agy is also installed and signed in on research now, so every point below can be checked there.

- **Headless runs.**
  - `agy -p "<prompt>"` runs one turn.
  - `--output-format json` gives one object: `conversation_id`, `status`, `response`, `usage` (input, output, thinking, cache-read tokens).
  - `--output-format stream-json` gives NDJSON: `init` (model, cwd, tools, permission mode), `step_update`, `result`.
  - `--input-format stream-json` reads one `{"event":"user","message":{"content":...}}` per line and runs a turn for each, in one process.
  - `--conversation <id>` resumes; `-c` continues the latest.
  - Errors exit 3 with an `AGY_ERROR: {...}` line; a turn cut short has `finished_reason`.
- **Permissions in headless runs.**
  - Nothing can prompt, so any tool that needs approval is denied, and settings allow-rules do not apply.
  - `--dangerously-skip-permissions` approves all tools.
  - `--mode auto` approves edits and safe commands.
  - `--sandbox` restricts terminal commands.
  - `--add-dir` adds folders to the workspace.
  - No workspace-trust question in print mode or ACP.
- **Models.** `agy models` lists them, and only works when signed in, so it doubles as the sign-in check:
  - Gemini 4 Argon and Gemini 3.8 Flash (low, medium and high);
  - Claude Opus and Sonnet 5.5 (low, medium and high);
  - GPT-OSS 120B.

  `--model <id>` picks a model, `--effort low..max` sets the reasoning level.
- **ACP.** A hidden `agy acp` subcommand runs an Agent Client Protocol server over stdio. It is how the Antigravity IDE drives the CLI. It supports per-session worker processes and exits when stdin closes.
- **Conversations on disk.**
  - Each conversation is `~/.gemini/antigravity-cli/conversations/<uuid>.db` (SQLite, protobuf payloads).
  - `conversation_summaries.db` lists every conversation with its status (`CASCADE_RUN_STATUS_IDLE`, ...), workspace folders, last change and parent conversation.
- **MCP.**
  - Global servers: `~/.gemini/config/mcp_config.json` (`{"mcpServers": {name: {command, args, env}}}`).
  - Project servers: through a plugin.
  - Every `tools/call` carries `_meta["antigravity.google/root_conversation_id"]`, set by agy, not by the model.
- **Hooks** live in `hooks.json`, global, per project or per plugin. They run a command with JSON on stdin:
  - `SessionStart` gets `conversationId`, `workspacePaths` and `transcriptPath`.
  - `PreInvocation` and `PostInvocation` can inject a user or system message.
  - `Stop` (with `fullyIdle`) can return `continue` with a message, which keeps the agent working.
- **Plugins.** One folder `~/.gemini/config/plugins/<name>/` can hold:
  - `plugin.json`;
  - `rules/AGENTS.md`, which is always loaded;
  - `hooks.json`;
  - `mcp_config.json`;
  - `skills/`.

  `agy plugin enable|disable|validate` manage it.
- **Instructions.** `AGENTS.md` and `GEMINI.md` are read from the folder up to the repo root. There is no documented user-level file, except rules in `~/.gemini/config/rules/` or in a plugin.
- **Quota.** quota-axi reads the agy quota by running `agy -p /quota` (fixed for the daemon in v0.4.99).
- **Updates.** agy updates itself in the background; `agy update` also works.

## Decision

### D1 - One agent-net plugin carries everything

Setup and the daemon (the same code that writes the Pi extension and the OpenCode plugin) write `~/.gemini/config/plugins/agent-net/`, only when `~/.gemini` exists:

- `plugin.json`;
- `rules/AGENTS.md`: the agent-net guidance block, as for the other harnesses;
- `mcp_config.json`: the `agent-net` MCP server (`agent-net mcp --antigravity`);
- `hooks.json`: `SessionStart`, `Stop` and `PreInvocation` call `agent-net hook antigravity <event>`, which talks to the local daemon socket.

Uninstall deletes the folder. The owner can switch it off with `agy plugin disable agent-net`. Nothing is written into the owner's own `mcp_config.json`, `settings.json` or `hooks.json`.

### D2 - Workers: one headless process per worker

A worker runs `agy -p --input-format stream-json --output-format stream-json` with:

- `--model` and `--effort` from the picker;
- `--dangerously-skip-permissions` and `--sandbox`;
- the worker's worktree as the working folder, like the other workers.

The brief is the first input line. agent-net reads `init` (the conversation ID becomes the worker's native ID), `step_update` (busy) and `result` (usage tokens, status). A follow-up message or an auto-continue is one more input line in the same process. If the process has ended, agent-net starts a new one with `--conversation <id>`. Stop closes stdin, then kills the process after a grace period.

The worker hands back with the `agent_handback` MCP tool, like OpenCode workers. agent-net knows the caller because `root_conversation_id` in `_meta` matches the worker's conversation, the same check it uses for Codex's `threadId`.

ACP (`agy acp`) is kept in reserve. It adds mid-turn cancel and permission requests, but it is undocumented. The stream-json path is documented and enough for a worker.

### D3 - Main sessions: hooks report, the daemon tracks

- **Discovery:**
  - `SessionStart` tells the daemon the conversation ID, folders and process.
  - The daemon also reads `conversation_summaries.db` (read-only) every few seconds for status and title.
  - It keeps only conversations whose `agy` process is still running, matched by process scan, the way Claude sessions are tracked.
  - Workers and sub-conversations (`parent_conversation_id` set) are not peers.
- **Sending:** the agent-net MCP tools, with identity from `root_conversation_id`.
- **Receiving:** an interactive agy session has no supported way to take input from another program. agent-net uses three paths, in order:
  1. **Session busy:**
     - its `Stop` hook returns `continue` with the waiting message, so the agent reads it as soon as its turn ends;
     - `PreInvocation` injects a waiting message at the start of the next model call.
  2. **Session idle, open in herdr:** agent-net types the framed message into that herdr pane and presses Enter, the same way the owner would. herdr already holds these panes ([0046](0046-open-sessions-in-herdr.md)). This needs herdr to support sending text to a pane; check it in step 1.
  3. **Otherwise:** the message waits and is marked "delivered at its next turn". The TUI and the sender see that state.
- **Opening a session:** Enter in the TUI runs `agy --conversation <id>` in herdr, following [0046](0046-open-sessions-in-herdr.md).

### D4 - agent-net's own parts

- **Harness name:** `agy` (as quota-axi and the daily harness update already call it). Detection finds `agy`, reads `agy --version`, uses `agy models` for the model list (which also says it is signed in), and runs `agy update` in the daily harness update.
- **Picker:** worker profiles such as "argon agy" or "flash agy". Gemini models count against Google's quota, read through quota-axi. Claude models through agy are not the owner's Anthropic subscription, so they are not a protected provider.
- **TUI:** the harness label everywhere (Sessions, Graph, Quality, Workers); usage from the `result` event.
- **Docs:** `docs/antigravity.md`, setup, reference, security (the plugin, the hooks, `--dangerously-skip-permissions` limited to worker worktrees), uninstall.

## Build steps

1. **Spike on research, real agy, throwaway folders.** Check each of these before building:
   - a two-turn stream-json worker;
   - the plugin's MCP server starting, with `root_conversation_id` in `tools/call`;
   - hooks firing in print mode and in an interactive session;
   - `Stop` returning `continue` with a message;
   - what `--sandbox` blocks;
   - herdr sending text to a pane.

   Write the results into this record.
2. **Detection, models, sign-in and picker profiles.**
3. **The plugin** (rules, MCP, hooks) written by setup and the daemon, with uninstall.
4. **Workers:** start, brief, follow-up, auto-continue, hand-back, stop, usage. **Release:** agy workers usable.
5. **Main sessions:** discovery, status, sending, the three receive paths.
6. **Enter in herdr, TUI labels, docs. Release.**

Each step is one worker with failing tests first and fake `agy` binaries in tests. Step 1 and the end of steps 4 and 5 run on the real agy.

## Risks

- **Undocumented storage.** Conversation storage and ACP are not documented, so a release can change them. Only the summaries table is read, and every read tolerates missing columns.
- **Interactive sessions can't be reached directly.** Delivery to an idle session depends on herdr. Outside herdr it waits for the next turn.
- **`--dangerously-skip-permissions` approves every tool.** Workers run in their own worktree with `--sandbox`. Main sessions never get it.
