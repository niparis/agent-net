# 0199 - Remove stored CTO proposals

Date: 2026-11-28
Status: Approved by the owner. His words: "#2 (kill proposals + CTO parallel reads/runtime)" (2026-11-27 22:18 +08:00), "land first" (2026-11-27 22:24 +08:00) and "yes go" (2026-11-28 15:48 +08:00).

## Problem

After decisions 0080 (the CTO acts at once), 0157 (CTO full power), 0165 (the
owner's words are his approval) and 0196 (remove the Desk), almost nothing went
through a stored CTO proposal. Only five owner-only actions still did when the
CTO had no words of the owner: `node_remove`, `agent_tombstone`,
`repo_archive`, `machine_remove` and `machine_rotate`. For those the daemon
kept a proposal state machine in `daemon.db` (open, running, applied, failed,
declined, expired), an approval dialog through MCP elicitation, the
`proposal_ask` and `cto_proposals_find` tools, a TUI proposal detail, history
and approve-all screen, a phone card with Apply and Decline, a Desk ask sync
loop, an outcome note loop, a cross-machine list and answer relay, and a
gateway validation frame. About 2,500 production lines served five actions
that the owner already approves with his words.

## Decision

1. The CTO keeps no proposals. Every CTO change runs at once.
2. The five owner-only actions run at once when the CTO quotes the owner's
   words in `owner_words` and the daemon finds them in what he typed
   (decision 0165). Without his words the call is refused with one line:
   ask the owner in this chat, then call again quoting his words. Nothing is
   stored and nothing waits on a screen.
3. Such an action goes to the gateway with the actor
   `owner (CTO on the owner's words)`. The gateway accepts it as the owner's
   authority, refuses the same actions from the actor `cto` as before, and
   keeps the actor in the graph history, so the CTO can undo a graph change it
   made this way. The actor `owner (CTO proposal p_N)` is no longer accepted.
4. Removed: the `cto_proposals` and `cto_proposal_messages` tables (dropped
   at daemon start, after the open proposals are kept; see Upgrade), `proposal_ask`, the MCP approval dialog, the
   `proposal_list` and `proposal_answer` owner methods and their relays, the
   `cto.proposal.validate` gateway frame, the TUI proposal screens and the
   open proposal count in the CTO box title, and the phone proposal card.
5. `cto_proposals_find` was also the CTO's one read of what waits for the
   owner: charter drafts, held requests and Inbox asks (permission
   proposals went with decision 0198).
   That read stays, under the name `cto_owner_items_find`, without the stored
   proposal kind. A call with the old name is told the new one.

## Upgrade

Nothing the owner was asked disappears without a message. At daemon start,
before the drop, each open proposal is copied into `cto_proposal_carry` in the
same transaction. When the link to the gateway is up, the daemon sends each
one to the chief of staff (to the owner's session when no chief is set) as a
request from the CTO, which made it, so the reply reaches the CTO. The message
names the proposal, the action (tool and action), the target (the stored
arguments), the reason and when it was made, and says that nothing ran: the
chief asks the owner, and if he agrees, he tells the CTO in its chat and the
CTO makes the call again with his words. The idempotency key
`cto_proposal_carry:<machine>:p_<id>` makes a send repeated after a crash give
back the first message, and the row is removed once the gateway took it. A row
that cannot be sent stays for the next connect, and the daemon log says why. A
decided, failed or expired proposal sends nothing.

A Desk ask of kind `proposal` that an older daemon synced stays until the Desk
store goes (decision 0196). An older daemon that approves an old proposal
against a newer gateway is refused with "owner authority required".
