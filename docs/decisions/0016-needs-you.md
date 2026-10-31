# 0016 - Needs you: sessions that wait for the owner

**Date:** 2026-10-30
**Status:** Built on branch `agent-net/w_d2130c34a31db838`. Proposed until the owner has read it.
**Context:** The owner wants one screen that says what needs them: a session stopped at a permission prompt, a question, or a plain request for input. Today the TUI shows status `idle` or `busy`, which does not say that somebody is waiting for the person.

Base: [0010](0010-tui-shows-usage-and-conversations.md) (the TUI), [0012](0012-codex-peers.md) and [0013](0013-peer-trust-and-hints.md) (the daemon checks a harness hook or plugin and a local method is not owner-only), [0014](0014-delegation-policy.md) (a per-session field that the gateway stores and every machine reads).

## The state

A session has an optional `waiting`:

```json
{"kind": "permission", "text": "Bash(git push origin main)", "since": 1798632000}
```

- `kind` is `permission`, `question` or `input`.
- `text` is one line, at most 200 characters, with control characters removed. The daemon cleans it and the gateway cleans it again.
- `since` is Unix seconds, set by the daemon when the wait began if the caller did not say.

It is a field of `SessionInfo` and of `AgentView` in `internal/protocol`. The daemon keeps it with the local session, sends it with `session.upsert`, and the gateway stores it in `sessions.waiting` (migration 16, after created_at, the last one on main when this was merged). Every machine reads it from `query.agents`, so every TUI sees the same waits. An older gateway ignores the field; an older daemon sends none.

## When it is cleared

- The session works again: the daemon clears it when a plugin reports status `busy`, and a discovery refresh that moves a session from not busy to busy clears it. Discovery alone does not erase a wait, so the Claude adapter's poll does not flap it.
- The owner answers: the Claude Code `UserPromptSubmit` hook, the OpenCode prompt hook, or a settled permission or form clears it. The Claude Code `PreToolUse` hook also clears it, because a tool that runs after an approval means the session moved on, even when its status was busy the whole time.
- A later `agent_announce` without `waiting` clears it. That is the fallback for every harness.

A plugin reconnect does not clear it: the daemon copies the field from the old local session when the same session says hello again.

## Sources

### Claude Code: two sources, not one

The design asked for a `Notification` hook. The current Claude Code documentation says `permission_prompt` fires only after about six seconds away and its message is often just "Claude needs your permission", without the tool. `PermissionRequest` fires at once and carries `tool_name` and `tool_input`, which gives the line the owner wants: `permission: Bash(git push origin main)`.

So `agent-net needs-hook` handles both, and setup adds four hooks, with the same consent question and the same record file:

| Event | Matcher | Meaning |
|---|---|---|
| `PermissionRequest` | all tools | `permission`, text `<tool>(<command, path, url or pattern>)` |
| `Notification` | `agent_needs_input\|elicitation_dialog\|elicitation_url_dialog` | `input` or `question`, with the notification message |
| `PreToolUse` | all tools | clear: an approved tool ran, so the session moved on |
| `UserPromptSubmit` | none | clear |

A `Notification` with `permission_prompt` is still understood, for an owner who added it by hand, but setup does not add it: it would overwrite the precise text six seconds later with a vaguer one. `idle_prompt` is not a wait: Claude Code sends it about 60 seconds after any turn ends, so it only means the session finished. `needs-hook` ignores it, and the daemon clears a stale `input` wait whose text is exactly `Claude is waiting for your input`. An install that still has the old matcher with `idle_prompt` needs no setup rerun.

The hook reads the JSON on stdin, talks to the local daemon through `waiting`, gives up after 300 ms and prints nothing on any error. It is installed only with a yes, by `--claude-needs-hook`, and removed by `agent-net uninstall`. The record is `~/.config/agent-net/claude-needs-hook.json`.

### OpenCode: events of 2.2.0

The installed OpenCode is 2.2.0. Its event names were read from the binary and the SDK types:

- `permission.asked` with `{id, sessionID, action, resources, ...}` becomes `permission: action(resources)`.
- `permission.replied` with `requestID` settles it.
- `form.created` with `{form: {id, sessionID, title, fields}}` is the question tool of v2 and becomes `question` with the form title (or its first field label). `form.replied` and `form.cancelled` settle it.
- `session.execution.started` clears the wait before the session is marked busy.
- The `prompt` hook clears it, so a prompt from the owner is an answer.

The plugin keeps a set of pending request IDs per root session: settling one ask does not clear the wait while another is still open.

### Every harness: the announce fallback

`agent_announce` takes an optional `waiting` string. The MCP server, the Pi extension and the OpenCode plugin pass it to the same `profile.announce` method, and the daemon stores it as an `input` wait. The guidance text and every tool description say: "When you stop to wait for your user's decision or answer, call agent_announce with waiting set to the question in one line." A later announce without it clears it.

## The TUI

A new section, `view_needs.go`, sits above the machines: `Needs you (N)`, then one row per session, oldest wait first, with a warning mark, name, harness, the other machine when it is not this one, the age, and the question or `permission: ...`. At most five rows; more are counted in one line. The section is hidden when empty and follows the text filter and the offline switch.

The rows are the same sessions as the table rows, so enter, `e` and `m` work on them. The visible list puts them first and leaves them out of the table's part of the list, so a waiting session is not selected twice. A waiting session shows STATUS `needs you` in the warning colour in the main table.

The TUI change is one new file plus small calls in `model.go` and `view_main.go`: the section is drawn in `mainView`, its height is part of the layout, and the table asks it for the STATUS text, the colour and the wider STATUS column.

## What was not built

- A phone app view. The state is in the agent list, so the phone can show it later without a new call.
- A wait for Codex. Codex has no hook checked here; its sessions use `agent_announce` and `waiting`, and the session going busy clears it.
- Server-side expiry for an old wait. A session that dies keeps the last state until the gateway marks it offline; the TUI hides offline waits unless the owner presses `o`.


## Addendum: explicit owner questions and desktop notifications

Agents can call `agent_ask_owner` on Claude Code, Codex, Pi and OpenCode when a turn ends awaiting the owner's input. It sends a one-line question, up to four options and an optional urgency over the existing waiting-state path. The gateway stores the explicit marker with the session. Plain text at the end of a turn does not raise an alert. The announce fallback and harness hooks remain available.

Enter on a waiting session opens an owner answer with the first option filled in. Delivery clears the wait. Claude Code's `UserPromptSubmit` hook and a Codex `turn/started` event also clear it, as do Pi and OpenCode turn starts. The earlier descriptions of Enter and Codex above predate this addendum.

A daemon with an owner TUI open observes the network session list and sends a generic desktop notification for a new explicit ask. It sends at most one per session in five minutes, including across restarts. It uses `osascript` on macOS and `notify-send` on Linux when present. The question is not included in the command or notification. The owner can toggle this machine's persistent setting with `n` in TUI help or with the CTO's `notification_settings` tool. Notifications are on by default.
