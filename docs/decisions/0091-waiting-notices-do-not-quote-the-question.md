# 0091 - Waiting notices do not quote the owner's question

Date: 2026-11-08
Status: Accepted by the owner on 2026-11-08. Built. Reverses the "Waiting text
in notices" part of [0060](0060-waiting-for-user-auto-answer.md).

## Context

On 2026-11-04 the owner chose to put the waiting question, or the permission
tool summary, in the notice a sender gets when its request reaches a session
that waits for its user, and in a bot's send receipt (0060).

On 2026-11-08 that leaked. ingest-forge sent homelab-agent 2 a deploy request.
The notice said homelab-agent 2 was waiting for its user's answer to a disk
question about a Proxmox VM. That question had nothing to do with
ingest-forge. ingest-forge repeated it in its report to the owner. The owner's
private question reached another agent and cost that agent context and tokens.

## Decision

A notice to a peer, and a bot's send receipt, carry only the waiting kind and
the start time. A question or input wait says `answer`; a permission wait says
`approval`. The notice never quotes the question, the options or the tool
summary.

The question stays in the session's waiting state and remains visible in the
owner's TUI and CLI, the Inbox and the owner asks. Agent reads
(`agent_sessions_find`, `agent_session_get`) already show only kind and since.

The worker blocked notice to a worker's caller follows the same rule: it names
the kind and the start time, not the worker's question.

## Tests

- `internal/gateway/waiting_notice_test.go` checks that the notice and the bot
  receipt have no question, that the permission notice has no tool summary,
  and that the owner read still has the text.
- `internal/daemon/waiting_test.go` and `harness_wait_test.go` check the worker
  notice and the end-to-end notice.
- `internal/protocol/waiting_test.go` checks the kind wording.
