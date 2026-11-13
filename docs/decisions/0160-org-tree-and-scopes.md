# 0160 - Org tree and scopes

Date: 2026-11-13
Status: Approved by the owner on 2026-11-13, through the CTO (message m_e270c4df23d68cd7, on his instructions). This file covers part T1: the stored tree. Later parts move the readers to it.

## Context

The owner's words:

- "You need to have access to the full hierarchy graph. That needs to be stored and accessible. workers report to an agent; merge Q report to an agent; agents report to team lead; team lead report to cto/chief."
- "a single data structure embedding these relationships, and Session and Graph are 2 views of the same."
- "make sure it builds on what we have rather than from scratch."

Today nothing stores who reports to whom. Five places guess it: the place index (`place.go`), the TUI's `mergerParent`, the repo-lead columns of the session list, the lead routing and the Team views. The guesses disagree and break. Two examples from the live gateway on 2026-11-13: the merger is not shown under its lead, and the merger counts as the repo lead, because it runs as the Repo's Agent `agent-net`.

## Decision

The tree is stored by extending the records the graph already has. There is no new graph framework and no new table. A lead has no fixed level: a lead is any node that others report to, at any depth.

Table by table:

| Table | Change | Why |
|---|---|---|
| `graph_edges` | New kind `reports_to`. The source is an Agent. The target is an Agent, or the root `owner`. Written by the existing `SetEdge` and `RemoveEdge`, so history and undo work as for every edge. | The graph already has typed edges between Agents. One more kind is the smallest change that stores the tree. |
| `agents` | New column `lead_session`. The existing `lead_set` writes it when it names a session, and answers the stored row. | One Agent can run several sessions (the homelab Agent has three). One of them is the lead session; the others report to it. |
| `sessions` | New column `started_by`: `agent-net` or `owner`. | Sessions agent-net starts (a start with a chosen Agent or a reserved Repo place, `merger_start`, a worker, the CTO) are told apart from the ones it only discovers. |
| `teams` | Stays as the Team settings: name, purpose, mode, held back text, `asks_to`, `lead_merges`. `teams.owner_agent_id` stays the Team lead. | It holds settings, not only the tree. |
| `teams.lead_session`, `repo_leads.lead_session` | Become read-only copies of `agents.lead_session`. A copy that names a session follows the Agent's lead session; a copy that is empty (any session of the Agent) stays empty. | The current readers keep working until they move to the Agent (T4). |
| `team_members` (Agent members) | Becomes derived from `reports_to` edges once the readers move. In T1 it is unchanged. | One place to say who is under whom. |
| `repo_leads` | Becomes a read-only copy. In T1 `lead_set` still writes it. | Same reason as above. |
| `project_mergers` | Unchanged. Each merger also gets its own `agents` row, `<project> merger`, role `merger`, with a `reports_to` edge to the Repo's top Agent (its lead, else its owner). `merger_start` and `merger_set` create or reuse that row and bind the merger session to it. | The merger no longer runs as the Repo's Agent, so it is never taken for the Repo lead, and it has its own place in the tree. |

The rules the owner's design asks for, and no others:

1. An Agent has at most one parent. A second `reports_to` from the same Agent is refused.
2. The tree has no cycle. An edge that would make one is refused.
3. A merger is never a lead. `lead_set` refuses an Agent with role `merger`, nothing may report to one, and the implicit Repo lead of the session list leaves it out.

`session_replace` moves `agents.lead_session`, the caller of the session's workers (`sessions.worker.owner`) and its `project_mergers` place to the new session, as it already did for the copies.

### The one-time build

One migration, appended at the end, builds the tree from today's records. It runs once: an `owner_settings` row (`org_tree_built`) stops a replay from undoing later edits. An Agent keeps the first parent it gets, in this order:

1. The `cto` Agent reports to `owner`.
2. The chief of staff's Agent (the Agent of the `chief_session`) reports to `owner`.
3. Team members report to their Team lead (`teams.owner_agent_id`): Agent members, then the top Agent of each member Repo.
4. Team leads with no parent yet report to `cto`.
5. An Agent that works in a Repo it does not own, and has no parent yet, reports to the Repo owner (`repos.owner_agent_id`).
6. Each `project_mergers` row gets its merger Agent, under the Repo's top Agent. The live `agent-net` merger becomes Agent `agent-net merger`, under `agent-net`.
7. `agents.lead_session` is taken from `repo_leads`, then from `teams`.
8. `sessions.started_by` is `agent-net` for workers, mergers, the CTO and sessions that match a pending Agent start; `owner` for the rest.

An edge the rules refuse during the build is skipped, not an error.

### What does not change in T1

The readers stay as they are: `place.go`, the TUI (`mergerParent` included) and routing. The Graph reads leave `reports_to` edges out, so the graph view shows no new edges and no node for the root. The new merger Agents do show as Agent nodes in the typed graph, and the merger row's Agent reads `<project> merger`.

Because the merger no longer shares the Repo's Agent, two readers get a one-line guard so they behave as before: lead routing delivers a message addressed to a merger to the merger (it is not redirected to the Repo lead), and the Repo lead's place text does not list the merger as crew.

## Consequences

- `edge_set` answers the stored edge with typed endpoints, and `lead_set` answers the stored lead row, both with `ok: true` kept for older callers.
- The Graph and the Session views can read the same tree once T3 and T4 move them.
- A parallel task (T2, scopes) also appends a migration. The merger orders the two at merge time.
