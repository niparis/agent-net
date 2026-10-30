package protocol

import (
	"fmt"
	"strings"
)

// "Who can help" hints (decision 0012). When a tool call fails, the harness
// sends the error text to the daemon (method MethodHint). The daemon matches it
// against the other online sessions and answers with at most MaxHints
// suggestions and one line of text to show to the model.

// MethodHint is the local method. It is not an operator method: a session
// asks for a hint about its own failure.
const MethodHint = "hint"

// MaxHints is the most suggestions one answer holds.
const MaxHints = 2

// HintRequest is MethodHint. A plugin or MCP client is bound to its session
// and leaves Harness and NativeID empty. The command line (the Claude Code
// hook) names the session it runs for.
type HintRequest struct {
	// Text is the error output of the failed tool call (the daemon cuts it).
	Text string `json:"text"`
	// Failed says the harness itself saw the call fail (non-zero exit or an
	// error result). When false, the daemon first checks that Text looks like
	// a failure.
	Failed   bool   `json:"failed,omitempty"`
	Harness  string `json:"harness,omitempty"`
	NativeID string `json:"native_id,omitempty"`
}

// HintSuggestion is one session that may help.
type HintSuggestion struct {
	SessionID string `json:"session_id"`
	Name      string `json:"name"`
	Hostname  string `json:"hostname,omitempty"`
	// Why says what matched, in a few words.
	Why string `json:"why"`
}

// HintResult answers MethodHint. Text is empty when there is no suggestion.
type HintResult struct {
	Suggestions []HintSuggestion `json:"suggestions,omitempty"`
	Text        string           `json:"text,omitempty"`
}

// HintText is the one line shown to the model for a list of suggestions.
func HintText(s []HintSuggestion) string {
	if len(s) == 0 {
		return ""
	}
	parts := make([]string, 0, len(s))
	for _, h := range s {
		where := h.SessionID
		if h.Hostname != "" {
			where += ", " + h.Hostname
		}
		parts = append(parts, fmt.Sprintf("%s (session %s) - %s", h.Name, where, h.Why))
	}
	return "agent-net: another session may own or see this. " + strings.Join(parts, "; ") +
		". Ask it with agent_send before more debugging."
}
