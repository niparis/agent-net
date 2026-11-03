# 0047. Decide CTO proposals in the harness

The CTO MCP server asks the owner to accept or decline a newly stored proposal
when the harness supports form elicitation. The daemon builds the message from
the stored call and uses the existing proposal runner for the decision. A
cancelled or unavailable dialog leaves the proposal in the Inbox. The private
CTO connection and its process tree are checked again for the answer.

MCP protocol 2026-07-28 and newer uses a multi round-trip request. The server
keeps a random, single-use token for each pending answer. The token is bound to
the MCP session and tool call. Older protocol versions use direct elicitation.

This follows the [CTO approval plan](../design/cto-approve-in-chat.md). The
Inbox remains available and no separate dialog timeout is added.

2026-11-03: Dialog handling now wraps every CTO tool handler. The SDK assigns
resultType inside its tool dispatch, after the handler returns. Results built
by receiving middleware bypassed that step, so clients using protocol
2026-07-28 rejected both the input request and the completed answer. Returning
these results from the handler lets the SDK mark them input_required or
complete. The session and tool binding and single-use random token remain.

2026-11-03: Added `proposal_ask` (`{"id": "p_..."}`) so the CTO can reopen the
approval dialog for an open proposal that is already waiting. The daemon checks
that the proposal exists and is open, then returns the same proposal payload
built from the stored call, so the MCP dialog handler and `cto_dialog_answer`
run unchanged. Closed or unknown IDs fail with a short error.
