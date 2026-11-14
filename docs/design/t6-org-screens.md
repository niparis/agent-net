# T6: the org tree and delegated authority on screen

Status: built on branch agent-net/w_e685e961247028cc (T6 of the org tree plan).

## What the owner decided

- Display only. No form, key or action changes a scope, a lead or the tree. The
  CTO does those. The existing `l` (lead) key stays as it is.
- No new screen or tab. The work goes into the Sessions list, the Agent card
  and the session detail pane.
- The Sessions list has no CTO row. The CTO has its own panel.
- The chief of staff is a normal row. It is the first row of its band, with no
  separate header.

## What each screen shows

Agent card (Graph tab, Enter on an Agent): under the PLACE block, two rows.

- `agent lead`: the lead session of the Agent. It shows the name and
  `· online`, `· offline since HH:MM`, or `· gone` in the warning colour.
  `no lead session` when the Agent has none.
- `authority`: the scopes the Agent holds, as `authority.Text` writes them.
  That is the same text a session reads in its instructions. `no delegated
  authority` when the Agent holds none.

Sessions list: a session hangs under the session of the Agent it reports to
(decision 0160), the way a merger always did.

- The parent is the session of the reported-to Agent. A pinned lead session
  (`RepoLeadExplicit` or `TeamLead`) wins over an online one, then the lowest
  ID.
- A session whose parent has no shown session stays at the top level.
- The chief of staff sorts first in its band.
- A session whose Agent's pinned lead session is gone shows its name in the
  warning colour. "Gone" means the snapshot does not list that session.

Session detail pane: one line `Delegated authority: ...` from `authority.Text`
on the session's Agent scopes. A session with no scope shows no line.

## Data

The session list read already runs once per refresh. It now also carries two
fields on each session: `scopes` and `agent_lead_session`. The gateway reads
both with one query for all Agents in the list (`applyAgentScopes`, in
`internal/gateway/agent_scopes.go`). The list read cache also watches
`node_scopes`, so a scope write shows on the next read.

The Graph card reads the lead session from the typed graph it already loads.

## Limits

- The warning colour sits on every row of the Agent whose pinned lead is gone.
  If no session of that Agent is shown, no row carries the warning. The Agent
  card still says `gone`.
- The Sessions list has no screen for changing anything. Changes stay with the
  CTO.
