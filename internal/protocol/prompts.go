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

// InjectedClaudeMarkers returns the markers of the turns Claude Code injects.
// A test that must refuse every such turn reads this list, so a new kind of
// turn is covered by each check that uses it.
func InjectedClaudeMarkers() []string {
	return append([]string(nil), injectedClaudeMarkers...)
}

// StartsInjectedClaudeTurn tells whether a prompt begins, after white space,
// with a marker of a turn Claude Code injects. Case does not matter.
//
// The check reads the raw prompt, before any block is removed. A peer's text
// can carry a closing reminder or paste tag, and removing blocks first would
// drop the text before that tag, marker and all, so the rest would look typed.
// A marker elsewhere in the prompt is not this check: the text the owner typed
// before a reminder Claude Code appends is still his.
func StartsInjectedClaudeTurn(prompt string) bool {
	prompt = strings.TrimSpace(prompt)
	for _, marker := range injectedClaudeMarkers {
		if len(prompt) >= len(marker) && strings.EqualFold(prompt[:len(marker)], marker) {
			return true
		}
	}
	return false
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
