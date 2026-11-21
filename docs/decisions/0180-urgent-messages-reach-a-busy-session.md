# 0180 - An urgent message reaches a busy session at its next tool call

**Date:** 2026-11-21
**Status:** Accepted. Part 1 (Claude Code) is built. Parts for Pi, OpenCode and Codex follow.

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

## Other harnesses

Checked on 2026-11-21 against the installed tools.

- **Pi.** Can do it. The extension already has a `tool_result` handler that may
  append content, and `sendMessage` takes `deliverAs: "steer"`.
- **OpenCode.** Can do it. The plugin API has `tool.execute.after`, and its
  `output.output` text may be changed. agent-net's plugin already uses this hook
  for hints.
- **Codex.** Can do it. The app server has `turn/steer` (thread, input,
  `expectedTurnId`), which adds input to the active turn. agent-net does not
  call it yet.

Each of these needs the same take on the daemon side and its own show step, so
each is its own part. Until then, an urgent message to a busy Pi, OpenCode or
Codex session waits for the turn end, as in 0130.

## Consequences

Every tool call of every Claude Code session now starts one small process.
It reads stdin, makes one local socket call and exits. The daemon does not call
the gateway for it unless an urgent message waits.
