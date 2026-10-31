# 0030 - The graph learns from the owner's instructions

**Date:** 2026-10-31
**Status:** Proposed.

The owner told the sovl-clone session: "get the homelab agent to do its job. all this github token things are his". Sovl-clone did not know homelab owned GitHub secrets and started the work itself. Homelab then replied "Stop: this work is already in main". That lesson was lost too. The graph should learn from the owner's real decisions, and agents should know what connected projects handle.

## Decision

Each turn includes short accepts and refuses lines for directly connected projects with charters. The same information goes into worker briefs. The lines are bounded and prefer relationships with rules.

The gateway records a short excerpt of an owner prompt when a session sends to another session soon after that prompt. It links reply outcomes to the request. Signals are retained for 90 days and only the owner can read them.

The CTO can read rule-based charter suggestions with the evidence and show them as diffs. It applies a change only after the owner approves it. Nothing changes a charter without the owner's approval.

## Out of scope

This does not infer authority from peer messages, automatically edit charters, or use a model to mine signals. It does not capture every conversation or guarantee that a prompt and send belong to the same turn.
