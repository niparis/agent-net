package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// The owner's words to the CTO are his approval (decision 0165). When the CTO
// quotes them in owner_words, the daemon checks the quote against what the
// owner typed in the CTO chat in the last ownerPromptWindow. A quote that is
// there is verified: the change runs at once, with no Desk item. A quote that
// is not there is refused. A call with no words goes the old way.

type verifiedWordsKey struct{}

// verifiedWords returns the owner's words the daemon verified for this call,
// or "" when the call carries none.
func verifiedWords(ctx context.Context) string {
	s, _ := ctx.Value(verifiedWordsKey{}).(string)
	return s
}

// rememberCTOOwnerPrompt keeps what the owner typed to the CTO. The key is the
// CTO's network session, the same one the CTO's tools run as.
func (d *Daemon) rememberCTOOwnerPrompt(text string) {
	if sid := d.ctoSessionID(); sid != "" {
		d.rememberOwnerPrompt(sid, text)
	}
}

// isClaudeCTONative tells whether a Claude Code native session id is the
// CTO's own conversation.
func (d *Daemon) isClaudeCTONative(native string) bool {
	if native == "" {
		return false
	}
	d.cto.mu.Lock()
	defer d.cto.mu.Unlock()
	return d.claudeCTO.session == native
}

// verifyCTOOwnerWords returns the quote when the owner typed it in the CTO
// chat, and an error with one line when he did not.
func (d *Daemon) verifyCTOOwnerWords(words string) (string, error) {
	words = strings.TrimSpace(words)
	if words == "" {
		return "", nil
	}
	if sid := d.ctoSessionID(); sid != "" && d.ownerWordsMatch(sid, words) {
		return words, nil
	}
	return "", fmt.Errorf("owner_words %q was not found in what the owner typed in this chat in the last %d hours, so nothing was applied. Quote his exact words, or leave owner_words out", words, int(ownerPromptWindow.Hours()))
}

// ctoWordsTools are the merged change tools that take owner_words next to
// reason.
var ctoWordsTools = map[string]bool{
	"graph_change": true, "session_change": true, "settings_change": true,
	"mcp_change": true, "project_change": true,
}

// withCTOOwnerWords takes owner_words out of a change call and puts the
// verified quote on the context. Later layers never see the field.
func (d *Daemon) withCTOOwnerWords(ctx context.Context, name string, raw json.RawMessage) (context.Context, json.RawMessage, error) {
	if !ctoWordsTools[name] || len(raw) == 0 || string(raw) == "null" {
		return ctx, raw, nil
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil {
		return ctx, raw, nil
	}
	value, ok := fields["owner_words"]
	if !ok {
		return ctx, raw, nil
	}
	var words string
	if json.Unmarshal(value, &words) != nil {
		return ctx, nil, fmt.Errorf("owner_words must be a string")
	}
	delete(fields, "owner_words")
	rest, err := json.Marshal(fields)
	if err != nil {
		return ctx, nil, err
	}
	verified, err := d.verifyCTOOwnerWords(words)
	if err != nil {
		return ctx, nil, err
	}
	if verified == "" {
		return ctx, rest, nil
	}
	return context.WithValue(ctx, verifiedWordsKey{}, verified), rest, nil
}

// onOwnerWords is the text an Inbox line adds when the change ran on the
// owner's words.
func onOwnerWords(words string) string {
	if words == "" {
		return ""
	}
	return fmt.Sprintf(" on the owner's words %q", cleanLine(words))
}
