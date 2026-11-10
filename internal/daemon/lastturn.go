package daemon

import (
	"context"
	"strings"
	"time"

	"github.com/niparis/agent-net/internal/markers"
	"github.com/niparis/agent-net/internal/protocol"
	"github.com/niparis/agent-net/internal/redact"
)

// lastTurn reads the last assistant message of one local session and the
// user-role input before it, through the same text readers the TUI uses (the
// Claude transcript and the Pi and OpenCode plugins). A harness stores a
// message another agent delivered as a user turn, so the read also says where
// that input came from and never calls a peer's message the owner's. Secrets
// are masked by localText. A harness with no reader, a missing transcript or an
// unresponsive plugin is reported as unavailable; the text is never guessed.
//
// The read is one-way: it sends nothing to the session and changes nothing.
func (d *Daemon) localLastTurn(ctx context.Context, sessionID string) protocol.AskIntakeTurnAnswer {
	out := protocol.AskIntakeTurnAnswer{Session: sessionID, State: protocol.AskIntakeUnavailable}
	ls := d.sessionByID(sessionID)
	if ls == nil {
		out.Reason = "no such session on this machine"
		return out
	}
	info := d.infoOf(ls)
	page, err := d.localText(ctx, protocol.TextQuery{SessionID: sessionID, Limit: protocol.TextMaxLimit})
	if err != nil {
		out.Reason = protocol.CleanWaitingText(redact.Secrets(err.Error()))
		return out
	}
	entries := page.Entries
	// The last assistant message is in this page. Its prompt may be further
	// back in a tool-heavy turn, so page back a little when needed.
	ai := lastRole(entries, protocol.RoleAssistant)
	for promptIdx(entries, ai) < 0 && page.Older != "" && len(entries) < protocol.TextMaxLimit*4 {
		prev, perr := d.localText(ctx, protocol.TextQuery{SessionID: sessionID, Limit: protocol.TextMaxLimit, Before: page.Older})
		if perr != nil || len(prev.Entries) == 0 {
			break
		}
		entries = append(prev.Entries, entries...)
		page.Older = prev.Older
	}
	ai = lastRole(entries, protocol.RoleAssistant)
	if ai < 0 {
		out.Reason = "the session has no assistant message yet"
		return out
	}
	assistant := entries[ai]
	out.State = protocol.AskIntakeOK
	out.Harness = info.Harness
	out.Ref = assistant.Cursor
	out.At = turnTime(assistant.Time)
	out.Text = assistant.Text
	out.Cut = assistant.Cut
	out.Ended = ai == len(entries)-1
	if pi := promptIdx(entries, ai); pi >= 0 {
		input := entries[pi]
		out.PrecedingInput = input.Text
		out.PrecedingInputAt = turnTime(input.Time)
		out.InputSource, out.InputFrom, out.InputSessionID = inputOrigin(input.Text)
		if out.InputSource == protocol.AskIntakeOwner && input.Cut && (info.Harness == "pi" || info.Harness == "opencode") {
			// A plugin delivery carries its agent-net header last. A long one
			// is cut before the header, and the same cut hides where a typed
			// prompt ends, so this turn cannot be called the owner's.
			out.InputSource = protocol.AskIntakeUnknown
		}
	}
	return out
}

// inputOrigin tells who wrote a user-role entry, from what the harness stores.
// The caller must never guess owner for text it cannot read: a delivered
// message is a user turn in every harness, so an unreadable marker is unknown.
//
//   - Claude wraps a delivered message, task or owner prompt in
//     <cross-session-message>. The adapter breaks the tags in a body, so a
//     wrapper is always written by the daemon.
//   - Pi and OpenCode store the agent-net plugin's delivery text, which ends
//     with an "[agent-net] message ... from ..." or "[agent-net] reply ..."
//     provenance line.
//   - An agent-net notice carries the "[agent-net]" marker without that line.
//   - A plain user turn was typed by the user in the session.
func inputOrigin(text string) (source, from, session string) {
	t := strings.TrimSpace(text)
	if strings.Contains(t, "<cross-session-message") {
		if name, sid, owner, ok := agentNetSender(t); ok {
			// The daemon writes the owner's own deliveries with this line too.
			if owner {
				return protocol.AskIntakeOwner, name, sid
			}
			return protocol.AskIntakePeer, name, sid
		}
		if ownerFrameLine(t) {
			return protocol.AskIntakeOwner, "", ""
		}
		if markers.Pattern.MatchString(t) {
			return protocol.AskIntakeAgentNet, "", ""
		}
		// Injected cross-session text with no readable sender. Claude's own
		// cross-session feature and the daemon both use the wrapper.
		return protocol.AskIntakePeer, "", ""
	}
	if name, sid, owner, ok := agentNetSender(t); ok {
		if owner {
			return protocol.AskIntakeOwner, name, sid
		}
		return protocol.AskIntakePeer, name, sid
	}
	if markers.Pattern.MatchString(t) {
		return protocol.AskIntakeAgentNet, "", ""
	}
	if protocol.InjectedClaudePrompt(t) {
		return protocol.AskIntakeUnknown, "", ""
	}
	return protocol.AskIntakeOwner, "", ""
}

// agentNetSender reads the provenance line agent-net writes after a delivered
// message: "[agent-net] message m_<id> from <name> (<harness>, session <id>)"
// or "[agent-net] reply m_<id> from <name> (session <id>)". It also reads the
// owner form "from your user in person (<name>, session <id>)" and reports it
// as owner. A body cannot fake the line: the adapter breaks the "[agent-net"
// marker inside a body.
func agentNetSender(text string) (from, session string, owner, ok bool) {
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		var rest string
		switch {
		case strings.HasPrefix(line, "[agent-net] message m_"):
			rest = strings.TrimPrefix(line, "[agent-net] message ")
		case strings.HasPrefix(line, "[agent-net] reply m_"):
			rest = strings.TrimPrefix(line, "[agent-net] reply ")
		default:
			continue
		}
		i := strings.Index(rest, " from ")
		if i < 0 {
			continue
		}
		return senderOf(rest[i+len(" from "):])
	}
	return "", "", false, false
}

// senderOf reads the sender part after " from ". owner is true only for the
// owner form the daemon writes for the owner's own message.
func senderOf(rest string) (from, session string, owner, ok bool) {
	rest = strings.TrimSpace(rest)
	owner = strings.HasPrefix(rest, "your user in person (")
	if owner {
		rest = strings.TrimPrefix(rest, "your user in person (")
	}
	if i := strings.Index(rest, "session "); i >= 0 {
		name := strings.TrimRight(strings.TrimSpace(rest[:i]), ", ")
		if !owner {
			// A peer line is "bob (pi, session s_bob)": the name is before
			// the harness parenthesis. An owner name may hold parentheses.
			if j := strings.Index(name, " ("); j > 0 {
				name = strings.TrimSpace(name[:j])
			}
			name = strings.TrimSuffix(name, "(")
		}
		name = strings.TrimSpace(name)
		return name, sessionAfter(rest[i+len("session "):]), owner, name != ""
	}
	// The CTO forms name no session: "from the CTO, on the owner's instructions."
	name := rest
	if i := strings.IndexAny(name, ".,"); i > 0 {
		name = strings.TrimSpace(name[:i])
	}
	return name, "", owner, name != ""
}

// sessionAfter reads one session id and stops at the end of its parenthesis.
func sessionAfter(s string) string {
	s = strings.TrimSpace(s)
	if j := strings.IndexAny(s, "), "); j >= 0 {
		s = s[:j]
	}
	return strings.TrimSpace(s)
}

// ownerFrameLine tells whether the daemon wrote its owner-prompt frame, which
// has no message provenance line in the body.
func ownerFrameLine(text string) bool {
	for _, line := range strings.Split(text, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "[agent-net] This is the owner speaking") {
			return true
		}
	}
	return false
}

// lastRole returns the index of the last entry with role, or -1.
func lastRole(entries []protocol.TextEntry, role string) int {
	for i := len(entries) - 1; i >= 0; i-- {
		if entries[i].Role == role {
			return i
		}
	}
	return -1
}

// promptIdx returns the index of the last user entry before the assistant
// entry at ai, or -1. Tool results and tool calls are not prompts.
func promptIdx(entries []protocol.TextEntry, ai int) int {
	for i := ai - 1; i >= 0; i-- {
		if entries[i].Role == protocol.RoleUser {
			return i
		}
	}
	return -1
}

// turnTime formats a millisecond timestamp as RFC 3339 in UTC. Zero means the
// harness did not give a time, which becomes empty rather than 1970.
func turnTime(ms int64) string {
	if ms <= 0 {
		return ""
	}
	return time.UnixMilli(ms).UTC().Format(time.RFC3339)
}
