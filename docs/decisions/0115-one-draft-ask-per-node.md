# 0115 - One charter draft ask for each node, sent to the team lead

Date: 2026-11-09
Status: Built on the branch of worker w_2ca08ad659a20747. Not yet released.

## Context

The reconcile of decision 0090 opened one ask for each drafted charter field.
On 2026-11-09 the live gateway had 27 such asks in front of the owner, 7 to 9
for each of four nodes. Most of those nodes are members of the forge team,
which has asks_to=lead (decision 0100), and the lead could review them. The
owner said the Desk was overwhelming and many asks did not save time.

## Decision

- **One ask for each node.** The pending drafts of a repo and its agent of the
  same name make one ask: `Charter draft for <node>: N fields`. A team has its
  own ask. The detail lists each field and its proposed value. The source key is
  `drafts:node:<scope>` or `drafts:team:<scope>`.
- **Accept and reject apply to every field.** To decide one field, the answer is
  `accept <level>:<field>` or `reject <level>:<field>`, for example
  `accept repo:purpose`. The detail says so. The ask stays open with the fields
  that remain. The old per-field paths still work: the charter card (`y`, `x`)
  and `learned_answer` with the negative draft ID.
- **Members ask the lead.** A node that is a member of a team with asks_to=lead,
  and is not the lead, sends the ask to the lead through `RedirectOwnerAsk`.
  The request comes from a session of that node. The lead replies, or sets the
  fields itself; a field with a value is no longer a draft and the ask closes.
  The lead is asked again only when a new field appears. The gateway waits two
  minutes after the last field arrives, so a session that submits seven fields
  one by one makes one request. A lead node, a team, a node outside a team, and
  a lead with no session go to the owner.
- **Learned edge suggestions** for a team member go to the lead the same way,
  once for each suggestion. Other learned suggestions stay with the owner.
- **No schema change.** The reconcile folds every open per-field draft ask into
  the grouped ask on its next run and closes the old rows with reason
  `grouped`. The drafts are not touched, so nothing is lost. The run is
  idempotent, so it needs no marker.

## Consequences

- The owner has one row for each node that needs the owner, and none for nodes
  whose lead can decide.
- A lead that replies without setting the fields leaves the drafts pending and
  hidden. It can ask the owner with `agent_owner_ask`.
