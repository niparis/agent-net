# Spike: OpenCode v2

OpenCode v2.2.0 (`npm i -g @opencode/cli`), Linux. Target: a throwaway TUI session in tmux, in a scratch project whose `opencode.jsonc` loads `spike/opencode/agent-net-spike` through `"plugins": [{"package": "<abs path>"}]`. Model `opencode/beam-free`.

Auth on this VM: OpenAI OAuth gives `401` on every call, the Google OAuth key is "not valid", and OpenCode Console has "Insufficient account funds". Only the free OpenCode models worked. Fix these logins before daily use.

## Architecture finding: one shared service

- A plain `opencode` TUI connects to a **shared background service**, `opencode serve --service` (one process per user). It survives TUI restarts.
- Plugins load **in the service**, once per project directory, not per TUI or per session. The plugin's socket is named after the service PID.
- **MCP servers are also started by the service**. Two `github-mcp-server` processes ran under it. So a per-session stdio MCP process cannot learn its session from its parent PID. For OpenCode, the plugin must register the network tools itself (it sees the session ID). This matches the Pi approach.

## Session events (not in the docs)

Seen through `ctx.event.subscribe()` (`event.data.sessionID` on every one):

| Event | Meaning |
|---|---|
| `session.created` | New session (`sessionID`, `projectID`, `location.directory`) |
| `session.inbox.enqueued` | Prompt accepted. `item.delivery` is `steer` (TUI default) or `queue` |
| `session.inbox.delivered` | Prompt entered the turn |
| `session.execution.started` | Busy |
| `session.execution.succeeded` / `session.execution.failed` | Idle again. `failed` carries `error.type`, for example `provider.auth` |
| `session.viewed` | Carries an `idle` timestamp |
| `session.reasoning.*`, `session.text.*`, `session.step.*`, `session.usage.updated` | Streaming detail |

Events carry `durable.aggregateID` and `durable.seq`, an ordered per-session event log. Presence is `execution.started` versus `execution.succeeded` / `failed`.

## Results

| Test | Result |
|---|---|
| Idle target, `ctx.session.prompt({sessionID, text, delivery: "queue"})` | Returns at once with the stored user message (`msg_...` ID). A turn started and the model answered "PONG-O1" in the same session. |
| Busy target (25 s shell command) | Enqueued with `delivery: "queue"` while busy, delivered after the running turn ended with "DONE", then answered. Not disturbed. |
| Reply back to the sender | Not tested. There is no reply tool yet. Step 2: register an `agent_reply` tool in the plugin and check that the tool context gives the calling `sessionID`. |

## Design consequences

- The OpenCode adapter is an in-process plugin, like Pi. It registers presence from `session.*` events, delivers with `session.prompt` (`delivery: "queue"`), and offers the network tools itself.
- One plugin instance serves many sessions, so the plugin must route by `sessionID`, unlike Pi, where one extension serves one session.
- Discovery: `ctx.session.list({directory})` per project. The plugin loads only for projects whose config lists it, so ship it in the global config (`~/.config/opencode/`).
