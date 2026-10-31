# 0030 - The graph learns from the owner's instructions

**Date:** 2026-10-31
**Status:** Accepted.

The owner told the sovl-clone session: "get the homelab agent to do its job. all this github token things are his". Sovl-clone did not know homelab owned GitHub secrets and started the work itself. Homelab then replied "Stop: this work is already in main". That lesson was lost too. The graph should learn from the owner's real decisions, and agents should know what connected projects handle.

## Decision

Each turn includes short accepts and refuses lines for directly connected projects with charters. The same information goes into worker briefs. The lines are bounded and prefer relationships with rules.

The gateway records a short excerpt of an owner prompt when a session sends to another session soon after that prompt. It links reply outcomes to the request. Signals are retained for 90 days and only the owner can read them.

The gateway also evaluates signals and messages as they arrive. It proposes an `accepts` item and a sender `needs` item when the owner's words assign work to a known node. Two matching redirects within seven days propose a `needs` item and a `depends` edge. Three matching refusals within seven days propose a `refuses` item. Five messages between otherwise unconnected nodes in seven days propose a `depends` edge. Evidence includes signal or message IDs and excerpts. The rules are literal and do not understand every wording. Matching an instruction to a send is not proof of intent.

Suggestions for a node become ready after two quiet minutes. Duplicate pending proposals merge their evidence. Minor additions can apply automatically: an owner-derived `accepts` item, a `needs` or `consumers` item, or a `depends` edge. Removal, narrowing or restrictive wording, `refuses`, `escalate`, maturity, deploy or request rules, and `governs` or `deploys` edges are major and wait for the owner. Any change on a critical node is major; an edge touching a critical node is major too. At most three changes per node apply automatically in a rolling 24 hours; later proposals become major. A rejected phrase is never offered again for the same node and field.

A ready batch of owner phrases may be shortened by one worker selected through the normal automatic chooser with trivial difficulty, no specified model, no worktree isolation, and read-only project access. Protected providers and quota rules apply. The gateway starts at most one such worker per two minutes and six per rolling hour. If selection, execution, the three-minute deadline, or JSON validation fails, it keeps the owner's words. The original words remain available for review and rejection matching.

Automatic changes use the ordinary graph setters with actor `learned`, so history and `graph_undo` apply. Map shows a count of learned changes since the owner last viewed a node and lists the field, value, time, and evidence. `U` selects the next learned change and `u` undoes the selected one. Ready major proposals appear in Needs you for the owner to accept with `a` or reject with `r`; the CTO can also list them with `charter_suggestions`.

## Out of scope

The rules do not infer authority from peer messages, capture every conversation, or guarantee that a prompt and send belong to the same turn. The phrasing worker formats candidate text; it does not decide which changes are safe.
