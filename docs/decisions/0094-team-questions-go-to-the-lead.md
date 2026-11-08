# 0094 - A team's questions for the owner go to its lead

Date: 2026-11-08
Status: Requested by the owner on 2026-11-08. Built. Builds on
[0089](0089-authority-along-the-tree.md) and [0090](0090-asks-and-the-desk.md).

## The problem

The owner told the forge lead (tenebrae): "I don't want to review from their
sessions... what can't happen is that they call me." It still happened. Three
members (finget, slither-swap and cogforge) called `agent_owner_ask` before the
lead's order reached them. The global guidance tells every session to call
`agent_owner_ask` when it waits for a decision, and the tool always went to the
owner.

A second leak sat in the charter. A repo's `rules` replaced the team's `rules`,
so a team rule never reached a member whose repo had rules of its own
(cogforge). `refuses` already joined all levels.

## Decision

1. **A Team setting `asks_to`.** `owner` (the default, today's behaviour) or
   `lead`. It is a column on `teams`. The owner sets it in the Teams list with
   `o`. The CTO sets it with `graph_change` `team_charter` and `attr: asks_to`,
   with no proposal, like the other team settings. It is recorded in the graph
   history and can be undone.
2. **A redirect, not a refusal.** When a session in a team with `asks_to=lead`
   calls `agent_owner_ask`, the gateway sends the question to the team lead:
   - a request message from the asker to the lead's live session, delivered and
     woken by the normal rules;
   - a Desk ask for the asker with `next` set to the lead's session, so it is
     with someone and not in front of the owner (the old Inbox leaves it out);
   - the tool result tells the asker: "Your team (forge) sends questions to its
     lead, tenebrae. Sent there; carry on with work that does not need the
     answer." The asker is not marked as waiting for its user.
   The lead answers with a normal reply. It reaches the asker like any reply and
   closes the Desk ask. The lead can still ask the owner with its own
   `agent_owner_ask`. The lead's own asks are never redirected. Pi and OpenCode
   ask with an explicit wait; the daemon asks the gateway first and redirects
   the same way.
3. **Held back items.** The owner always holds back deleting data, spending
   money, and using or sharing secrets that can spend money. agent-net has no
   classifier for the text of an ask, and this decision does not add one: every
   ask of such a team goes to its lead. The message to the lead says what stays
   with the owner, so the lead escalates those itself. This keeps the rule
   simple and adds no new limit.
4. **Rules join across levels.** `rules` unites global, team, repo and agent
   items the way `refuses` does: split on semicolons and newlines, each item
   once, higher level first. A directly assigned agent team keeps its rules as
   well as its refusals when the repo belongs to another team.
5. **The turn text says it.** A member of a team with `asks_to=lead` reads, in
   its "your place" block, "Questions for the owner go to your lead, <name>."

## What stays as it was

- A team with `asks_to=owner` (every team until someone changes it) behaves as
  before.
- A session in no team, the CTO and the owner are not redirected.
- If the lead Agent has no session at all, the ask goes to the owner. A message
  to a lead with no live session waits for the lead's latest session.

## Tests

- `internal/gateway/asks_to_test.go`: the default and the owner and CTO writes
  with undo; the redirect, the Desk ask, the owner Inbox, the reply that closes
  it; the default; the lead's own ask; a session outside a team; the line about
  what stays with the owner; replacing an earlier ask; the rules union.
- `internal/gateway/place_test.go`: the line in the member's place text.
- `internal/daemon/asks_to_test.go`: end to end through the daemon and gateway,
  for the question call and the explicit wait of a plugin.
- `internal/tui/teams_test.go`: the `o` key and its undo.
