package protocol

import (
	"fmt"
	"regexp"
	"strings"
)

// Cited message notes (decision 0120). When a message body names another
// message ID, agent-net may add a note with the cited message's sender,
// recipient, time and body, so the receiver can trust a forwarded approval. The
// gateway decides whether the note is shown (the new message's sender must have
// been in the cited message). These helpers only find the IDs and write the text.

// CitedMaxIDs is how many cited IDs of one message get a note.
const CitedMaxIDs = 3

// CitedBodyMax is the longest cited body shown, in characters.
const CitedBodyMax = 1500

var citedIDPattern = regexp.MustCompile(`\bm_[0-9a-f]{16}\b`)

// CitedIDs returns the first CitedMaxIDs distinct message IDs named in a body,
// in order of appearance. A duplicate counts once.
func CitedIDs(body string) []string {
	var ids []string
	seen := map[string]bool{}
	for _, id := range citedIDPattern.FindAllString(body, -1) {
		if seen[id] {
			continue
		}
		seen[id] = true
		ids = append(ids, id)
		if len(ids) == CitedMaxIDs {
			break
		}
	}
	return ids
}

// CitedNote writes the text of one note, without the "[agent-net] " prefix that
// every delivery adds. cited is the stored message; fromName and toName are the
// display names of its sender and recipient. All text that a session wrote is
// neutralized, so it cannot pose as agent-net's own framing.
func CitedNote(cited Message, fromName, toName string) string {
	clean := func(s, fallback string) string {
		s = NeutralizeMarkers(oneLine(s, 80))
		if s == "" {
			return fallback
		}
		return s
	}
	from := fmt.Sprintf("from %s (session %s)", clean(fromName, "unknown"), cited.From)
	if cited.FromHarness == OwnerHarness {
		from = fmt.Sprintf("from your user in person (%s, session %s)", clean(fromName, "unknown"), cited.From)
	}
	to := clean(toName, cited.To)
	kind := cited.Type
	if kind == "" {
		kind = TypeRequest
	}
	body := strings.TrimRight(cited.Body, "\n")
	cut := ""
	if r := []rune(body); len(r) > CitedBodyMax {
		cut = fmt.Sprintf(" (cut, %d more characters)", len(r)-CitedBodyMax)
		body = string(r[:CitedBodyMax])
	}
	return fmt.Sprintf("cited %s: %s to %s at %s, %s: %q%s", cited.ID, from, to,
		cited.CreatedAt.UTC().Format("2006-01-02 15:04 UTC"), kind, NeutralizeMarkers(body), cut)
}
