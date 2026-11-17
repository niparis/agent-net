# 0171 - Switch the harness of a session without losing context

Date: 2026-11-17

The owner asked to move the CTO and the chief of staff between Claude Code,
Codex, OpenCode and Pi, and back, and to run agent-net with no Claude Code at
all. Today a switch keeps the owner's timeline but the new conversation starts
with no context, and the CTO's own skill exists only as a file in a Claude
folder.

## Decisions

1. **One session model.** Any session can switch harness. The CTO and the
   chief use the ordinary session machinery, with no special case for a role.
2. **Phase order 0, 1, 2, 3, 4.** Phase 0 is an audit. Phase 1 is the handover,
   the conversation table, one delivery confirm for every harness, and the same
   start and compaction context everywhere. Phase 2 is the role address and the
   chief started from its model list. Phase 3 is a Codex CTO with full power.
   Phase 4 is first use with no Claude Code. Pi comes later.
3. **The chief keeps gateway notes only**, like the CTO. Its memory is the
   notes the gateway holds, not a harness memory.
4. **Pi is refused for every role** until it can be woken and resumed.
5. **OpenCode and Pi workers are sandboxed by default.** Open until phase 4;
   setup must ask once and show the choice.

## Conversation per session

The daemon keeps one row per session and harness in
`session_conversations(session_id, harness, native_id, updated_at)`. It
replaces the CTO's `claude_session_id` and `session_id` columns, which stay in
the table for one release and are no longer read or written. The CTO's session
key is `cto`.

Migration: the table is created once, and its rows are copied from the two
columns. The copied rows get `updated_at = 0`, so they count as stale. The
active conversation still works, because only a switch checks the age.

A switch to a harness resumes that harness's conversation only when its
`updated_at` is within 24 hours. An older conversation is dropped, and the
switch starts a new one. A stale resume brings back beliefs from days ago.

## Handover on every switch

A switch of harness, from the owner's settings or from the 0162 fallback,
writes one handover note before the old harness stops:

- The **mechanical layer** always comes first. It lists the open asks, the
  asks that wait on the CTO, the workers and changes the CTO started that are
  not done, and the owner's last 10 messages, verbatim. It also lists the
  harness-local work the daemon can see: a busy turn in OpenCode or a running
  Claude background job. It needs only the gateway and the daemon's own
  records, so it is written when the old harness is down.
- The **model layer** follows an owner switch from a harness that was running
  when the switch began. The old conversation has 60 seconds to answer. It holds
  the model's own words about open promises and why threads matter, and it is
  marked "model output, not the owner's words". A busy turn, a harness that was
  not running, a failed send or a timeout skips it. A fallback never has it,
  because the old model has just failed.

The note is named `handover-<UTC time>`, for example
`handover-20261117-100000.123`, because note names allow only lower-case
letters, digits, dots, underscores and hyphens. Each switch writes a new name,
so a handover is never overwritten. The new conversation reads the latest
handover name in its start text, and is told to read that note and its open
asks first.

An owner-started switch during a busy turn first sends the owner one Inbox
notice, then stops the old turn. A fallback never sends that notice.

The handover request and its reply stay in the old conversation, because it is
the same conversation when the harness comes back. The owner's timeline and the
timeline snapshot leave them out.

## The CTO's skill

`agent-net-setup` is no longer written into the harness folders
(`cto/claude/.claude/skills`, `cto/.claude/skills`). Before its first CTO harness
start, and again when the shipped content changes, the daemon sends its embedded
copy to the gateway archive. It adds the skill to the CTO's node list once. Each
harness then gets the skill from
the node's skill folder (decision 0169, task 4): `--plugin-dir` for Claude and
the skills root in `OPENCODE_CONFIG_CONTENT` for OpenCode. The "once" rule
means an owner who removes the skill from the list keeps it removed.

## Part b: the delivery confirm and the start block

One check per delivered message replaces the Claude-only confirm. The check
waits 20 seconds, then reads what the harness reports. A taken message costs one
read, the read that finds the turn. A busy session is checked again 20 seconds
later, and each busy check costs two reads (the busy state and the turn). An
idle, untaken message is sent once more, then the session restarts with the
message as its first prompt. A session has one check at a time: a newer message
replaces the older check, and the check ends when the message is taken, when the
session goes offline, ends or is archived, when agent-net stops it, or when the
daemon stops. On OpenCode, a restart reads the conversation first. When a copy of
the prompt is stored with an assistant reply after it, the restart does nothing.
When a copy is stored with no reply, the restart sends a short nudge that points
at it, not the full text again. When no copy is stored, the restart sends the
prompt once.

The sources are the Claude transcript (the message ID in the bytes added since
the send), the OpenCode conversation (a message the owner did not write, created
at or after the prompt, which includes a turn that fails), and Codex's notice
that its user message item carries the message ID (checked against a real app
server in codex_real_test.go). Pi and plugin sessions answer synchronously, so
they get no check.

After a compaction, the next turn carries the start block again. Claude keeps
its PostCompact hook. OpenCode and Codex take the daemon's compaction event
(noteCompacted, and the CTO's compaction).

## What this does not cover

- The generic delivery confirm and the compaction re-send (phase 1, part b).
- The chief's start from its model list and the role address (phase 2).
- A Codex CTO (phase 3).
- Setup's first-use role step (phase 4).
