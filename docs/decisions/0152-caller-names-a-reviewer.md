# 0152 - The caller names a session to report one step

**Date:** 2026-11-12
**Status:** Accepted.

## Context

The chief of staff named the CTO as reviewer of the agent-net lead's design
docs. The CTO passed them, but it has no agent session and no step report tool.
The author could not record the step for the CTO: accept and review belong to
the caller, and the author reporting a pass on its own work is refused. The
change sat at accept and review.

## Decision

The caller of a change may name one other session as the reporter of one accept
or review step. It does this with `agent_step_claim` and a `reviewer` session
id. The gateway sets the step task's claim to that session. The claim is the
whole mechanism: it is how a node step is already reserved for one session, so
there is no new table and no migration.

- Only a session that acts for the caller may name a reviewer.
- The caller and any session of its agent cannot be named.
- The reviewer reports as itself. The evidence shows `by` as the reviewer.
- The reviewer cannot report any other step. Naming ends with the revision.
- The CTO reports through `cto_op step_report`. The CTO may report only a step
  claimed for its own session.
- The chief of staff, and any other agent, uses `agent_step_report`.

## Not done

No independence rule is added. `delivery.pipeline.review.independent` still
decides whether the evidence counts. Merge, test and release ownership do not
change.
