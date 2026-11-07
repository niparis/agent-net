# 0075 - Leads and max agents

Date: 2026-11-07
Status: implemented; the TUI confirmation is a later step.

This changes decision 0068: Teams no longer only label identities. A Repo and
Team may each have a lead Agent. A Repo defaults to its owner Agent unless the
owner explicitly sets or clears its lead. The Team's existing owner is its lead.
The owner may set either lead; the CTO may propose the change for approval.

A Repo has a `max_agents` setting. Zero or an unset value means no limit. The
count includes live owner agent sessions on all machines, including sessions
started in a terminal, and excludes workers, mergers and the CTO. If terminal
sessions put a Repo over its max, show that state and never kill sessions.

An agent-net start over the max is refused. The result suggests raising
`max_agents` to one more than the current max so the owner can choose that
change and the CTO can propose it. Concurrent starts reserve places at the
gateway. A place is released on failure or when its session registers. For a terminal
handoff without a native ID, a new session of that harness in the Repo takes
the place.
A reservation left by an interrupted launch expires after two minutes. A late
session is still registered and may put the Repo over max.

An outside sender addressing a non-lead session is redirected to a live lead
session. A Repo crew consists of its sessions. A Team crew consists of its
members' sessions. The owner and CTO may always address any session directly;
a worker may always message its caller. A redirect identifies the lead and the
original target in the message and the sender's receipt. Without a lead or a
live lead session, deliver to the original target and report that in the
receipt. `repo:<name>` and `team:<name>` address a live lead directly. Team
membership does not grant authority, change settings, or change request rules.

## Session leads

A Repo or Team lead may name one session. The lead's Agent is recorded too.
Messages go to that session while it is online, and otherwise fall back to a
live session of the Agent. The session lead flag marks only the chosen session.
