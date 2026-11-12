package protocol

import "strings"

// An owner question shows in the agent's own chat AND follows the route to the
// chief and the Desk (decision 0150). A turn that ends on a marked question is
// routed by agent-net when the agent did not send it with agent_owner_ask.

// OwnerQuestionMarker starts the line of a final message that asks the owner.
// The guidance asks agents to mark such a line, so agent-net can tell a
// question from a plain turn end.
const OwnerQuestionMarker = "Owner question:"

// OwnerQuestionLine returns the question of the last line of msg that starts
// with OwnerQuestionMarker, without the marker, or "". Markdown emphasis,
// quote and list marks before the marker are allowed.
func OwnerQuestionLine(msg string) string {
	lines := strings.Split(msg, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimLeft(strings.TrimSpace(lines[i]), "*_>-# ")
		if len(line) < len(OwnerQuestionMarker) || !strings.EqualFold(line[:len(OwnerQuestionMarker)], OwnerQuestionMarker) {
			continue
		}
		q := strings.TrimSpace(strings.TrimLeft(line[len(OwnerQuestionMarker):], "*_ "))
		if q != "" {
			return q
		}
	}
	return ""
}
