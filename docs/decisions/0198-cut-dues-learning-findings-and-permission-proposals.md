# 0198 - Cut session dues, learned suggestions, findings and permission proposals

Date: 2026-11-27
Status: Decided by the owner as part of the brutalist simplification refactor. The agent-net lead passed it to a worker with the four cuts below.

## Context

Four subsystems each kept their own table, tick and tools, and each did a job that a plain message or an owner note already does:

- **Session dues** (`agent_due`, table `session_dues`, decision [0177](0177-owed-replies.md) addition). A session recorded a step for later and the minute tick woke it. Follow-ups and messages cover the same need.
- **Learned charter suggestions** (decision [0030](0030-graph-learns-from-instructions.md), table `learned_suggestions` and its side tables). The gateway read message traffic and proposed charter changes and edges. Charter drafts from AGENTS.md (decision [0088](0088-charter-drafts.md)) rode on the same list with negative IDs.
- **Findings** (`agent_learn`, table `findings`, decision [0022](0022-component-graph-and-cto.md)). Agents recorded findings, and the gateway promoted automatic playbook notes from them and from bad track records.
- **Permission proposals** (`agent_permission_propose`, `permission_reject`, `permission_undo`, table `permission_proposals`, decision [0127](0127-claude-code-permission-proposals.md)). A proposal waited in a state machine until the CTO applied or rejected it on the owner's words.

## Decision

1. **Dues are gone.** No `agent_due`, no tick, no table. A session that needs to come back to something uses a follow-up or a message.
2. **Learned suggestions are gone.** The gateway no longer proposes charter changes from traffic. Charter drafts stay, on their own path: the owner and the CTO list them with `drafts_list` and decide one with `draft_answer {draft_id, action}`. The Graph card shows drafts with `,` `.` to choose, `y` to accept and `x` to reject; the learned section and its `u` undo key are gone.
3. **Findings are gone.** No `agent_learn`, no automatic notes. The `playbook` table stays as the owner's and the CTO's notes about a model, harness, project or machine (`playbook_set`, `playbook_get`), and a note still reaches a worker in its start result. Agents keep what they learn in gateway notes (`agent_note_write`). Unpinned automatic notes are deleted; pinned ones stay.
4. **Permission proposals are gone.** An agent that needs a Claude Code permission rule or folder trust asks the chief of staff or the CTO with a message. Only the owner's direct instruction grants it, through `permission_apply` (the owner's command line, or the CTO with his words, which the daemon verifies). The gateway applies on each machine at once, logs the result with his words, and stores nothing. A folder that answers `needs_trust` at a start is one request to the chief, once per machine and folder.

One appended migration drops `session_dues`, `learned_suggestions` and its side tables, `findings` and `permission_proposals`.

## What we give up

- **The permission undo.** With nothing stored, there is no `permission_undo`. The apply result names exactly the rules and the trust it added, so a person can take them out by hand.
- **Scope-approved proposals** (decision [0166](0166-delegated-authority-table.md)). A session that held the `permissions` scope could get its own proposal applied without the owner. That path went with the proposal tool; the scope now names only `permission_apply`.
- **Automatic learning.** Nothing turns traffic or worker results into charter changes or notes by itself. The owner, the CTO and agents write them.

Old open asks of kind `learned` or `permission` stay in `inbox_items` as they are; the Desk removal (decision [0196](0196-remove-the-desk.md)) handles every stored ask.
