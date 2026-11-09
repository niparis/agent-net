package daemon

import (
	"context"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
	"github.com/niparis/agent-net/internal/redact"
)

// lastTurn reads the last assistant message of one local session and the
// owner's last typed prompt before it, through the same text readers the TUI
// uses (the Claude transcript and the Pi and OpenCode plugins). Secrets are
// masked by localText. A harness with no reader, a missing transcript or an
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
		out.Prompt = entries[pi].Text
		out.PromptAt = turnTime(entries[pi].Time)
	}
	return out
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
