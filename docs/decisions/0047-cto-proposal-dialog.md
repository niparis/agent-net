# 0047. Decide CTO proposals in the harness

The CTO MCP server asks the owner to accept or decline a newly stored proposal
when the harness supports form elicitation. The daemon builds the message from
the stored call and uses the existing proposal runner for the decision. A
cancelled or unavailable dialog leaves the proposal in the Inbox. The private
CTO connection and its process tree are checked again for the answer.

This follows the [CTO approval plan](../design/cto-approve-in-chat.md). The
Inbox remains available and no separate dialog timeout is added.
