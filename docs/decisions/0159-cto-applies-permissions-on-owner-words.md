# 0159 - The CTO applies permissions on the owner's words

Date: 2026-11-13
Status: Asked for by the owner on 2026-11-13, relayed by the CTO on his instructions.

## Context

The owner told the CTO in its chat: "help wbgpoclead to have merge authority on all his repos". The CTO had no way to apply a Claude Code permission rule. It could only file `agent_permission_propose`, which put the request on the Desk (decision 0127). Then the CTO could not see the proposal in `cto_proposals_find`, `proposal_ask` answered "unknown proposal", and `answer` could not approve it. The owner had to go to the Desk to click what he had already said.

His words: "I DONT WANT TO GO CLICK IN YOUR BLOODY DESK WHEN I GAVE CLEAR INSTRUCTIONS ALREADY... desk is useful if a session i dont monitor needs an authorisation. But in this case what is the bloody point?"

## Decision

1. **The owner's instruction in the CTO chat is his approval.** The CTO applies it at once with `cto_op` method `permission_apply`. The Desk stays for asks from sessions he does not watch.

2. **The owner's words are the record.** `owner_words`, a short quote of his instruction, is required. The gateway stores it on the proposal row (`permission_proposals.owner_words`) with `decided_by` set to `cto`, and puts it in the Desk log line. A call without it is refused. This is the only new check, and the owner asked for the record.

3. **One apply path.** `permission_apply` takes rules and targets (registered repo names), or `kind: trust` with a machine and folders, resolved exactly as `agent_permission_propose` resolves them. It stores an approved proposal, opens no Desk ask, and sends the same `permission.apply` frame to each target machine as a Desk approval does. It returns the stored record: each target's status, the rules added and the settings file written.

4. **A waiting proposal can be approved by id.** `permission_apply` with `id` (a `pp_` proposal) and `owner_words` approves a proposal that waits on the Desk, applies it, and closes its Desk item. The item's answer names the CTO and quotes the owner's words.

5. **Undo is unchanged.** The Desk log line keeps its Undo, and the CTO can run `permission_undo` with the proposal id. Both remove exactly what the apply added.

6. **The CTO can see permission proposals.** `cto_proposals_find` lists them with `kind: permission`, by their `pp_` id and state, with the `permission_apply` call that applies them. `proposal_ask` on a `pp_` id answers the same row instead of "unknown proposal".

7. **Nothing changes for other sessions.** A peer agent still only proposes, and only the owner answers its ask on the Desk. `permission_apply` is an owner operation: it is reached only by the owner's own operator path or by the CTO through its process-checked operator path, and the gateway checks the machine's owner session on the frame.

## Consequences

- The CTO hook allows `permission_apply` and `permission_undo` without a Claude Code prompt, like other reversible owner operations (decision 0157).
- The CTO brief tells it to apply a permission the owner gives in its chat, quote his words, and not file a Desk proposal.
- The CTO can grant a permission without a Desk click. The owner's quoted words, the Desk log line and the undo are the controls.
