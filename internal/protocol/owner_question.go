package protocol

import "strings"

// No agent asks the owner in its own chat (decision 0150). Owner questions go
// to the chief with agent_owner_ask. A turn that ends on a question to the
// owner anyway is caught when the owner is not working in that session.

// OwnerQuestionMarker starts the line of a final message that asks the owner.
// The guidance asks agents to mark such a line, so agent-net can tell a
// question from a plain turn end.
const OwnerQuestionMarker = "Owner question:"

// OwnerQuestionRedirect is what a session hears, once per question, when its
// turn ended on a question to the owner that went to the chief.
const OwnerQuestionRedirect = "Send this question to the chief with agent_owner_ask and carry on"

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
