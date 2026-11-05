# 0063 - A project merger session

Date: 2026-11-04

## Context

The session that starts workers currently accepts their work, tests it and
merges it. The owner wants a separate session to own testing and merging for
any registered project on either machine, after one approval in the CTO chat.

## Decision

Add the session type merger alongside the existing agent, worker, CTO, owner
and bot roles. Session info and read tools expose the type. The Sessions table
shows TYPE after NAME and retains the existing column definitions.

A gateway record maps `(project, machine)` to a session ID. Only the owner or
an approved CTO proposal may change it. It names a non-worker Claude session
of that project on that machine. Session removal clears the record. The type
is derived from the record, so discovery cannot grant merger authority.

Reserve `@merger` as a delivery owner for test and merge. Route tasks only to
the registered session for the change's project and machine. The gateway
checks the same mapping on claim and report. Offline mergers leave tasks
queued with a waiting-for-merger note in Delivery.

The CTO proposes `session_change` action `merger_start` with project, optional
machine and optional model. Pin the registered project and machine before
approval. Start Claude in that folder with the default model claude-opus-5-5
and name it `<project> merger`. Do not open a view. After discovery registers
the session, atomically record the merger, assign accept to caller and test
and merge to merger. Preserve existing review and approval requirements.
If tests were optional, require a suite before merge and disclose that in the
approval line. Every project already has a pipeline in the current settings
model; no separate enable flag is added.

Use one project-neutral first message. The merger learns build and test rules
from the project, tests changed parts, checks CI and merges in its own merge
worktree. It reports evidence with git hashes. Failures go back to the caller;
it does not fix code or write features.

`merger_stop` clears the selected registration and returns test and merge to
the caller. Clear unfinished claims and queued messages for those steps.
Keep completed evidence and the Claude conversation. The owner may also use
the CLI to register or clear an existing session without changing step owners.

## Limits

Step owners are project-wide, as in decision 0057. Registrations are per
machine: a change waits when its machine has no registered merger. Stopping
routing returns the project's test and merge owners to caller. Git remains
outside agent-net's enforcement; the gateway validates evidence and routing.

## 2026-11-05 - Keep the queue moving

Clarify the merger's first message: failed or rejected tool calls do not mean
the owner paused the session. Retry once, then use another approach or report
the blocked step and continue with ready work. Ask the owner once when a
decision is needed, while continuing work that does not depend on it.
