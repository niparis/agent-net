# 0180 - An urgent message reaches a busy session at its next tool call

**Date:** 2026-11-21
**Status:** Accepted. Built for Claude Code, Pi, OpenCode and Codex.

## Context

Decision 0130 holds every message for a busy session at the gateway until the
turn ends. A long turn lasts 10 to 20 minutes, and the session reads nothing in
that time. On 2026-11-21 a stop order sat in the gateway during a data copy, and
the agent copied the data twice. The owner asked, through the CTO, that an
urgent message reach a busy session inside its turn, at its next tool call.

The `urgent` field already exists on `agent_message_send`. Before this decision
it only moved a message ahead of older held ones.

## Decision

**a. Only an urgent message goes early.** A message that is not urgent keeps
the 0130 hold until the turn ends. Nothing else changes for it.

**b. The gateway still holds it, and the session fetches it.** An urgent
message to a busy session is held as before. The gateway also sends the node
that owns the session one frame, `node.urgent_held`. The node remembers it in
memory. The message goes out only when the session asks.

**c. Claude Code asks through a hook.** Setup adds `agent-net urgent-hook` to
`PostToolUse` and `PostToolUseFailure` for every tool, without a question. After
each tool call the hook asks the local daemon. The daemon answers from memory
and does not call the gateway unless a frame said an urgent message waits. When
one does, the daemon sends `msg.take_urgent` to the gateway. The gateway checks
that the session is on the calling node, then runs the checks of a normal
delivery (request rule, trust, notes). It marks each message `dispatched` and
takes it out of the hold. The daemon records it for reply matching, reports it
delivered, and the hook prints it as `additionalContext` with a line that says
it is urgent. The model reads it before its next step.

**c2. Pi and OpenCode ask from their plugin.** Their plugins already run code
at every tool call. After a tool call the plugin sends the local method
`urgent_take` on its own bound connection. The daemon answers from memory, as
for Claude Code, and otherwise takes the messages from the gateway, records them
delivered and returns them in the answer. Pi appends the text to the tool
result, which the model reads next. OpenCode has no hook that changes a tool
result, so the plugin holds the text and adds it to the system prompt of the
next model request, as it does for hints. Each plugin keeps the IDs it showed
and never shows one twice. Nothing is pushed to the plugin for it.

**c3. Codex is pushed by the daemon.** Codex has no hook at a tool call, but its
app server takes input into the running turn with `turn/steer`. The daemon keeps
the active turn of each thread (from `turn/started` to `turn/completed`). When
the gateway's frame says an urgent message is held for a Codex thread, the daemon
takes it and sends `turn/steer` with that turn. If the app server refuses, or no
turn is known, the message is queued as a normal delivery. A daemon that started
during a turn does not know the turn, so the message then waits for the turn end.

**d. Delivered once.** The take clears the hold in the same statement that
claims the message. The turn-end release does not see it. A second hook call
finds nothing.

**e. A message with files waits.** The daemon takes files only with a normal
delivery (decision 0054), so an urgent message with files stays held until the
turn ends.

**f. The fallback is today's behavior.** If the hook is off, the frame is lost
(a node reconnect), the gateway link is slow, or the turn has no tool call, the
message goes at the turn end, as in 0130. It is never lost.

**g. Who can do it.** Any sender may mark a message `urgent`, as in 0130. It
still counts toward the inbox limit. No new limit or approval gate is added.

## Checked APIs

Checked on 2026-11-21 against the installed tools.

- **Pi.** `tool_result` handlers may return changed content. The extension
  already used this for hints.
- **OpenCode.** The plugin has an `execute.after` hook for tool calls, and a
  `context` hook that changes the system prompt of the next request.
- **Codex.** The app server has `turn/steer` (thread, input, `expectedTurnId`).

## Consequences

Every tool call of every Claude Code session now starts one small process.
It reads stdin, makes one local socket call and exits. Every tool call of a Pi
or OpenCode session makes one local socket call. The daemon does not call the
gateway for any of these unless an urgent message waits.

One edge: if the daemon hands a message over and the hook or plugin dies before
it shows it, the message is marked delivered and the model never sees it. The
gateway call is bounded to 1.5 seconds so the window is small.

A Claude Code session with no tool call in its turn, or a session whose
harness has no active turn known, still gets the message at the turn end.
