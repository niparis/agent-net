package protocol

import (
	"strings"
	"unicode/utf8"
)

// A caller cannot override the worker picker without the owner. Selection
// fields are harness, model, provider, effort, not_family and machine on a
// worker start. A start that sets one goes ahead only when the owner's own
// words are found in what he typed in any session (owner_words, see
// owner_words.go). Without them the start is refused with OverrideNeedsOwner
// (decision 0196): there is no Desk ask and no held start. A start held before
// that change is still answered. The owner's own command line and TUI need no
// such proof.

// OverrideNeedsOwner is the one line that refuses a start with selection
// fields and no matching owner words.
const OverrideNeedsOwner = "model choice needs the owner: ask the chief of staff, then start again with owner_words quoting what he typed"

// How a start with selection fields went ahead, in WorkerStartResult.SelectionPath.
const (
	SelectionOwnerWords = "owner_words"
	SelectionHeld       = "held"
)

// The answers of the Desk ask that holds an override. Only these are answers:
// any other text is refused with the valid answers and the start stays held. A
// dismissal ends the start without a worker.
const (
	StartAnswerConfirm = "confirm"
	StartAnswerPicker  = "picker"
)

// StartOverrideRefPrefix starts the source key of the Desk ask that holds an
// override. The key is the start id.
const StartOverrideRefPrefix = "wstart:"

// FrameStartDecision is the gateway telling a node that the owner answered the
// ask that holds a start. Its params are StartDecision.
const FrameStartDecision = "node.start_decision"

// StartDecision is FrameStartDecision.
type StartDecision struct {
	ItemID string `json:"item_id"`
	// Ref is the source key of the ask (StartOverrideRefPrefix + start id).
	Ref string `json:"ref"`
	// Session is the caller that asked for the override.
	Session string `json:"session_id"`
	Answer  string `json:"answer"`
}

// StartOverrideAsk is "worker.override_ask", machine to gateway: put the ask
// that holds an override on the owner's Desk. The gateway checks that Session
// belongs to the sending machine.
type StartOverrideAsk struct {
	Session string `json:"session_id"`
	// StartID is the start id the caller was given.
	StartID string `json:"start_id"`
	Text    string `json:"text"`
	OverrideFacts
}

// OverrideFacts are the facts of a held override the owner reads and the
// notice on a lost start names: the selection the caller wants.
type OverrideFacts struct {
	Harness string `json:"harness,omitempty"`
	Model   string `json:"model,omitempty"`
	Effort  string `json:"effort,omitempty"`
}

// MinOwnerWordsRunes and MaxOwnerWordsRunes bound the quote of owner_words. A
// shorter quote proves nothing, and a long one is not a quote.
const (
	MinOwnerWordsRunes = 8
	MaxOwnerWordsRunes = 300
)

// SelectionFields names the selection fields the request sets, as name=value
// in a fixed order. A model of "auto" is the picker's own choice and counts as
// not set. A start that sets none is not an override.
func (p WorkerStartRequest) SelectionFields() []string {
	var out []string
	add := func(name, value string) {
		if value = strings.TrimSpace(value); value != "" {
			out = append(out, name+"="+value)
		}
	}
	add("harness", p.Harness)
	if m := strings.TrimSpace(p.Model); m != "auto" {
		add("model", m)
	}
	add("provider", p.Provider)
	add("effort", p.Effort)
	var families []string
	for _, f := range p.NotFamily {
		if f = strings.TrimSpace(f); f != "" {
			families = append(families, f)
		}
	}
	add("not_family", strings.Join(families, ","))
	add("machine", p.Machine)
	return out
}

// WithoutSelection is the request with the selection fields, the owner's words
// and the reason cleared: what the picker decides is then the picker's.
func (p WorkerStartRequest) WithoutSelection() WorkerStartRequest {
	p.Harness, p.Model, p.Provider, p.Effort, p.Machine = "", "", "", "", ""
	p.NotFamily = nil
	p.OwnerWords, p.Reason = "", ""
	return p
}

// NormalizeOwnerWords lower-cases text and folds every run of white space to
// one space, so a quote matches whatever the line breaks and capitals were.
func NormalizeOwnerWords(s string) string {
	return strings.ToLower(strings.Join(strings.Fields(s), " "))
}

// OwnerWordsMatch tells whether the quote appears in one of the owner's
// prompts, after both are normalised. A quote that is too short or too long
// never matches.
func OwnerWordsMatch(quote string, prompts []string) bool {
	quote = NormalizeOwnerWords(quote)
	if n := utf8.RuneCountInString(quote); n < MinOwnerWordsRunes || n > MaxOwnerWordsRunes {
		return false
	}
	for _, p := range prompts {
		if strings.Contains(NormalizeOwnerWords(p), quote) {
			return true
		}
	}
	return false
}

// StartOverrideText is the question the owner sees for a held override.
func StartOverrideText(caller string, fields []string, title, reason string) string {
	reason = oneLine(reason, 400)
	if reason == "" {
		reason = "no reason given"
	}
	text := caller + " wants " + strings.Join(fields, " ") + " for '" + oneLine(title, 80) + "': " + reason + ". Confirm, or let the picker choose."
	if r := []rune(text); len(r) > MaxOwnerAskChars {
		text = string(r[:MaxOwnerAskChars])
	}
	return text
}

// DifficultyLevels is the tier order, weakest first.
var DifficultyLevels = []string{"trivial", "simple", "normal", "hard", "very_complex"}

// levelIndex is the place of a level in DifficultyLevels, or -1.
func levelIndex(level string) int {
	for i, l := range DifficultyLevels {
		if l == level {
			return i
		}
	}
	return -1
}
