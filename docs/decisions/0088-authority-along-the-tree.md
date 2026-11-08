# 0088 - Authority passes down the tree

Date: 2026-11-08
Status: Accepted by the owner. Replaces [0068](0068-team-records.md) where it
says teams grant no authority. Builds on [0076](0076-leads-and-max-agents.md)
and [0086](0086-charter-levels.md). The design is "How agents are organised",
sections 2 and 3.

## The tree

Every lasting agent has one place in a tree under the owner: owner, team lead,
repo lead, agent session. Authority covers every action and passes down this
tree. A level holds everything its parent holds, inside its subtree, minus what
the owner holds back. Nobody passes down more than they hold. A worker sits
under the session that started it.

The CTO and the chief of staff sit beside the owner. They do not command a team
on their own account. The CTO's orders keep coming "on the owner's
instructions" (decision 0087).

## Held back

The owner holds back these actions at every level, always: deleting data,
spending money, and using or sharing secrets that can spend money. Nothing is
delegated down the tree for these.

Beyond that, "held back" is one free text field on a team and one on a repo, set
only by the owner, for example "deploy to production". A repo also holds what
its team holds back. The text is shown to the lead and the members in the
"your place" block and in the note on an order. Code does not parse it: it tells
agents what they must not do, and the owner's rules and settings still decide
what runs.

The team field is the column `teams.held_back`. The repo field is the table
`repo_held_back`, because live triggers insert into `repos` by position and a
new column on `repos` would break them.

## Orders, requests and advice

agent-net puts the label on a message from the tree and the thread. A sender
cannot write it into the body.

- **order**: from a lead above the recipient, and the sender set `order` on
  `agent_message_send`. A team lead is above every repo, repo lead and agent
  of its team. A repo lead is above the other agents that work in its repo.
  agent-net checks the tree when it stores the message. From anyone else the
  flag is dropped, the message goes as a request, and the sender's receipt says
  so. The flag is never an authority grant.
- **request**: from a peer, or from outside your branch. The recipient judges
  it against its charter, as before.
- **advice**: a reply to a question you asked, even from above. It never binds.
  A reply cannot be an order.

The gateway sets `is_order` on the stored message and builds the note on every
delivery from the live tree: `order from your lead <name> (team <team>, mode
<mode>)`, with one sentence on what the mode means. A change of mode shows on
the next delivery.

## Authority mode

The owner picks a mode per team. A new team is `charter_first`.

- `absolute`: an order is carried out like the owner's instruction. The
  member's charter does not override it. The always-held-back list still
  applies.
- `charter_first` (default): an order is carried out unless the member's charter
  refuses it. Then the member says no, quotes the charter line, and the order
  goes up one level, above the lead who gave it, to decide.
- `negotiate`: the member may push back with a reason and a counter-proposal.
  The lead accepts, changes or overrules. Overruling a charter refusal goes up
  one level.

A repo that belongs to no team uses `charter_first` and answers to the owner.

## Merge authority

`merge_authority: delegated` now means: merge needs an approval from anyone
above you in the tree who holds merge authority, or from the owner. The
approvers are the repo lead and the team lead of the repo. `full` and `ask`
are unchanged.

## What stays

Request rules on edges (`auto`, `ask`, `never`) stay. They are trust between
peers, not command. Delivery step assignments stay and may name a level.
Rules text never grants or removes authority.

## What is removed

- The `governs` edge, its modes `advisor`, `arbiter` and `owner`, the setting
  `delivery.delegated_approver_modes`, and the tools and Graph screens that
  set or draw them. The owner reported 0 such edges in use. A migration deletes
  any rows left.
- The architect is a role in the tree, not a node kind with edges of its own.
- The advisor right to send to a group. Only the CTO sends to a group.

## Storage

Migration 143 adds `teams.authority_mode`, `teams.held_back`,
`messages.is_order` and the table `repo_held_back`. Migration 144 deletes any
`governs` edge and the setting `delivery.delegated_approver_modes`. The column checks make the
migration safe to replay. Team history snapshots keep both new fields. An older
snapshot has neither key, so an undo leaves those two fields alone.
