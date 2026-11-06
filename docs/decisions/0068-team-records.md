# 0068 - Additive Team records

Date: 2026-11-06
Status: Built, first step only.

Follow [Teams and relationships](../design/teams-relationships.md), including
its changes after review. Teams are durable typed identities. They introduce
no settings inheritance, relationship rules or delivery defaults in this step.

Store membership in `team_members`, keyed by member kind and ID, rather than
in the legacy `graph_edges` table. Its legacy key cannot distinguish an Agent
and Repo with the same ID joining the same Team. Typed Graph reads combine
these rows into `member_of` edges. Legacy Graph reads and the legacy node union
never query Team tables. This preserves every existing behavior consumer.

Team writes use a dedicated owner operator method, authenticated again at the
gateway with the machine's owner session. They require the typed Graph cutover.
Rename checks the old name. Membership moves and removals check the old Team
and retain one immutable history entry visible from both Teams. Archive refuses
members and retains identity, purpose, owner and history. Archived Teams remain
visible in typed reads with `active: false`.

Creation sets the optional owner. Owner edits, undo and CTO write proposals
are later work. Database triggers prevent deleting or tombstoning an owning
Agent, even for archived Teams. No Team settings or assignments exist yet, so
there are none to reconcile on archive. Migration 126 follows migration 125. Teams are labels the Graph can filter by
and the CTO can reach. They enforce nothing.
