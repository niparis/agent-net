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
