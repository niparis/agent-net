# 0060 - A session that waits for its user answers its senders

**Date:** 2026-11-04
**Status:** Built on branch `agent-net/w_697ba101a6243040`. Proposed until the owner has read it.
**Context:** On 2026-11-04 the orchestrator Claude Code session waited a few minutes on an `AskUserQuestion` dialog (a merge question to the owner). In that time a bot session sent four requests, each more urgent. All four were delivered, but nothing answered until the owner answered the dialog. The owner: "at minima we need to answer the node to say it's waiting for user input. Ideally the question appears in the inbox."

Base: [0016](0016-needs-you.md) (the waiting state, the Inbox rows), [0053](0053-delivery-records.md) (delivery reports), W4.3 (the undelivered notice in `internal/gateway/undelivered.go`).

## What was missing

The waiting state and the Inbox row existed. Three gaps let the incident happen:

1. **Claude Code dialogs were not seen.** The needs hooks are opt-in, and `AskUserQuestion` was not handled: its `PreToolUse` hook cleared the wait, and a `PermissionRequest` for it gave the text `AskUserQuestion` without the question. Nothing ended the wait when the model ended its turn after the answer.
2. **Senders heard nothing.** A request to a waiting session was delivered and then sat there.
3. **Agent reads did not say it.** `agent_sessions_find` and `agent_session_get` showed `busy` or `unknown`.

## Signals, per harness

### Claude Code: the session file first, the hook second

Claude Code 2.1.318 writes its own state into `~/.claude/sessions/<pid>.json` on every change: `status: "waiting"` while a dialog blocks the turn, with `waitingFor` naming the dialog (`permission prompt`, `input needed`, `sandbox request`, `worker request`, `goal proposal`, `dialog open`), and `busy` or `idle` again when the user answered. This was read from the installed binary: the interactive status writer calls the pid-file update with `{status, waitingFor}`, and the SDK path maps `requires_action` to `waiting` with `input needed` for `AskUserQuestion`.

That file is the best signal: Claude Code writes it for every session, with or without agent-net's hooks, and it has a clear end. The adapter already polls it every second. `dialog open` is left out: it is a dialog the user opened in the session (for example `/config`), so the user is at it.

The words come from the transcript: the newest tool call of the main conversation with no `tool_result` yet, after the user's last prompt. `AskUserQuestion` gives its first question (`(+N more)` for more) and the option labels; `ExitPlanMode` gives `approve the plan`; any other tool gives `<tool>(<command, path, url or pattern>)`, as the hook always did. Only that line is kept, never other transcript text. Sub-agent entries are skipped.

The needs hook gives the same words from the hook input, through one shared function (`claude.WaitFor`), so the two sources describe one wait:

| Event | Matcher | Meaning |
|---|---|---|
| `PreToolUse` | all tools | for `AskUserQuestion` and `ExitPlanMode`: start the wait with the question; for any other tool: clear (as before) |
| `PermissionRequest` | all tools | `permission` with the tool, or the question for `AskUserQuestion` |
| `PostToolUse` | `AskUserQuestion\|ExitPlanMode` | clear: the dialog was answered |
| `PostToolUseFailure` | `AskUserQuestion\|ExitPlanMode` | clear: the call was refused |

`Notification` and `UserPromptSubmit` stay as in 0016. `PostToolUseFailure` is already used by the hint hook, so every supported Claude Code knows it.

The daemon applies unchanged integration answers without a setup run, so a new hook event never reached a machine set up before. `ApplyIntegrations` now brings the needs hooks up to date when the owner turned them on and agent-net added them. It writes and logs only when they differ, and only when the binary's name says agent-net (otherwise its hooks could not be told from the owner's and every start would add them again).

### Codex: thread flags

The Codex app server marks an active thread with `activeFlags`: `waitingOnApproval` or `waitingOnUserInput` (`ThreadStatus` in the app-server protocol, v2). The adapter already reads each thread's status; a flag becomes a `permission` wait (`approve a command or a change`) or a `question` wait (`answer a question`). The flags carry no question text. `thread/status/changed` already makes the adapter read the threads again.

### Pi and OpenCode: unchanged

OpenCode's plugin already reports `permission.asked` and `form.created` and settles them (0016). Pi has no permission or question event; its sessions use `agent_owner_ask`.

## The daemon rule

A discovery adapter that knows the harness's own state sets `HarnessWaits` on the session (it stays on the machine). The harness then starts and ends the wait:

- While it keeps waiting, the wait keeps its start time, also when a hook reported the same dialog first.
- When it stops waiting, the user answered: every wait but an explicit `agent_owner_ask` ends.
- A worker's wait is never taken from discovery; workers report to their caller (0016).

The status of a Claude session in a dialog stays `unknown`, as before. Calling it `busy` would keep a Claude worker that is stuck on a prompt from ever reaching its idle limit (the worker check treats a busy session as working). The TUI and the agent reads use the wait, not the status.

## The notice

When a daemon reports a request delivered to an online session that waits, and that is not a worker, the gateway stores one notice to the sender, in the same transaction, and dispatches it:

```
[agent-net] bob is waiting for its user's answer to: Merge the branch? (since 2026-11-04 10:12 UTC). Your message was delivered and will be read when the user answers. No reply is needed.
```

A permission wait says `approval of: <tool summary>` instead. The key is `waiting:<recipient>:<since>:<sender>`: one notice per sender per waiting period, also across gateway restarts. A second request from the same sender in the same wait gets none; a different sender gets its own; a new wait is a new period. A gateway that sees the same wait again (same kind and text, for example after a daemon restart) keeps its first start time, so a restart does not start a new period.

No notice is stored for a notification or a reply, for agent-net's own messages (a notice never makes another), for the owner (the TUI shows the wait), or for a bot. A bot reads no queue of its own, so its send receipt carries the same words as `note`. The request itself is delivered as usual. The notice is not a reminder, so it is not held back by the reminder check.

### Waiting text in notices

Reversed on 2026-11-08 by [0091](0091-waiting-notices-do-not-quote-the-question.md):
notices and bot receipts no longer quote the question or tool summary.

On 2026-11-04, the owner chose to include the question in notices to senders
and bot receipts. Permission notices include the tool summary. The text is
cleaned to one line, limited to 200 characters and has secrets redacted. When
there is no text, the notice keeps the original wording.

`agent_sessions_find` and `agent_session_get` continue to show status
`waiting for its user` and a `waiting` object with `kind` and `since` only.

## The Inbox

The Inbox row already came from the session's waiting state, so it clears itself when the wait ends, and Enter opens the session through the herdr rules (0046). Its words are now `<session> waits for you: <question>` (`waits for your approval: <tool>` for a permission), with the panel titled `<session> waits for you`. No stored inbox item is added: it would repeat the row and could go stale.

## What was not built

- Codex question text: the flags do not carry it, and reading the thread's turns on every status change is heavy.
- A wait for Pi permission prompts: Pi has none.
- Claude workers: discovery ignores their waits; a headless worker reports through its caller.
