package protocol

import "strings"

// injectedClaudeMarkers open the turns Claude Code writes itself: a delivered
// peer message, a task notice, a reminder, a scheduled turn, an autonomous-loop
// turn. They reach the prompt hook like typed input, but they are not the
// owner's words.
var injectedClaudeMarkers = []string{
	"<cross-session-message", "<agent-message", "Another Claude session sent a message",
	"<task-notification", "[SYSTEM NOTIFICATION", "<system-reminder",
	"<scheduled", "<<autonomous-loop",
}

// InjectedClaudePrompt tells whether a Claude Code prompt is a turn that the
// harness injected, not text the owner typed. Only the first 512 bytes count.
// The daemon and the gateway both use it, so the owner's words (owner use and
// the owner-instruction note) never come from a peer or from model output.
func InjectedClaudePrompt(prompt string) bool {
	prompt = strings.TrimSpace(prompt)
	prompt = prompt[:min(len(prompt), 512)]
	for _, marker := range injectedClaudeMarkers {
		if strings.Contains(prompt, marker) {
			return true
		}
	}
	return false
}
