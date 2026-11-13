# 0159 - The CTO ships with a default charter

Date: 2026-11-13
Status: Ordered by the owner, relayed by the CTO on his instructions on 2026-11-13.

## Problem

The owner's words: "your charter is good and needs to ship with agent-net".
The CTO's charter was set by hand on one gateway, at agent level on the `cto`
agent node. A new install started the CTO with no charter at all.

## Decision

1. Every gateway gives the `cto` agent node a default agent-level charter. The
   values are in one place, `ctoDefaultCharter` in
   `internal/gateway/cto_charter_default.go`:
   - accepts: CTO tools for lead-level agents; quota and capacity checks; graph, charter and edge changes; second opinions on design
   - refuses: Peer-relayed owner approvals (he confirms himself); product code (workers write it); work inside a repo (ask its lead); destructive ops without the owner
   - escalate: Money, secrets, security or permission changes, destructive or irreversible acts: one-line question plus a recommendation to the owner.
   - rules: Act only on the owner's direct asks; a remark is not an order. One complete brief per decision, facts checked first. Report outcomes, not traffic. Short, plain replies, basics first.
2. The default fills only empty fields. A value the owner or the CTO set is
   never changed.
3. The seed runs once, in a migration at the end of the list. It records
   `cto_default_charter_v1` in `owner_settings`, with the fields it filled. A
   store that has the marker never seeds again, so a field the owner clears
   later stays empty. The charter and graph history name the actor
   `agent-net default`.
4. There is no purpose. The agent level refuses `purpose` (it is a repo and
   team field), so the CTO has none.
5. The live CTO also had the line "Workers: prefer non-Claude subscriptions,
   Opus only with a reason." It is left out: it is this owner's subscription
   choice, not a product default. The owner decides if it goes in.
