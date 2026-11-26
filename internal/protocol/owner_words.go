package protocol

import "time"

// The owner's words count wherever he typed them. A start's owner_words quote
// is found first in the caller's own session, then in any session he typed in
// on the caller's machine, and then in the gateway's store of what he typed on
// every machine. Only text he typed himself is kept: pasted blocks, injected
// turns and scheduled prompts are dropped before the words leave the machine.

// OwnerWordsWindow is how long a prompt the owner typed counts as his words.
const OwnerWordsWindow = 3 * time.Hour

// Frames of the gateway's store of the owner's words.
const (
	// FrameOwnerWordsKeep is machine to gateway: keep a prompt the owner
	// typed in a session of that machine. Params OwnerWordsKeep.
	FrameOwnerWordsKeep = "owner.words_keep"
	// FrameOwnerWordsFind is machine to gateway: find a quote in what the
	// owner typed in any session in the last OwnerWordsWindow. Params
	// OwnerWordsFind, answer OwnerWordsFound.
	FrameOwnerWordsFind = "owner.words_find"
)

// OwnerWordsKeep is FrameOwnerWordsKeep. At is the time he typed it, in Unix
// nanoseconds.
type OwnerWordsKeep struct {
	Session string `json:"session_id"`
	Text    string `json:"text"`
	At      int64  `json:"at"`
}

// OwnerWordsFind is FrameOwnerWordsFind.
type OwnerWordsFind struct {
	Quote string `json:"quote"`
}

// OwnerWordsFound is the answer to FrameOwnerWordsFind: the session of the
// latest prompt that holds the quote and when he typed it, in Unix nanoseconds.
type OwnerWordsFound struct {
	Found   bool   `json:"found"`
	Session string `json:"session_id,omitempty"`
	At      int64  `json:"at,omitempty"`
}

// OwnerWordsReceipt is the line a start that went ahead on the owner's words
// gives its caller and its notice.
func OwnerWordsReceipt(session string, at time.Time) string {
	session = NeutralizeMarkers(oneLine(session, 80))
	if session == "" {
		session = "unknown"
	}
	return "owner_words found in what the owner typed in " + session + " at about " + at.UTC().Format("2006-01-02 15:04 UTC") + "."
}
