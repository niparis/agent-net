package protocol

import (
	"errors"
	"strings"
	"unicode/utf8"
)

// A caller (an agent) cannot override the worker picker without a human
// confirming it. Selection fields are harness, model, provider, effort,
// not_family and machine on a worker start. An agent that sets one has the
// start go ahead only when the owner's own words are found in his prompts of
// that session (owner_words), or when the owner confirms the override on the
// Desk. The CTO and the owner's own command line and TUI are not agents here.

// How a start with selection fields went ahead, in WorkerStartResult.SelectionPath.
const (
	SelectionOwnerWords = "owner_words"
	// SelectionScope: the caller's role node holds the admin scope over the
	// project (decision 0166), so the owner already delegated the choice.
	SelectionScope = "scope"
	SelectionHeld  = "held"
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

// OverrideFacts are what the confirm rule checks (decision 0151): the
// override's harness, model and effort, the provider it picks, the model
// family of the caller and of the override, the level the start runs at
// (empty when the caller set none), and whether the task reviews work.
type OverrideFacts struct {
	Harness        string `json:"harness,omitempty"`
	Model          string `json:"model,omitempty"`
	Effort         string `json:"effort,omitempty"`
	Level          string `json:"level,omitempty"`
	Provider       string `json:"provider,omitempty"`
	CallerFamily   string `json:"caller_family,omitempty"`
	OverrideFamily string `json:"override_family,omitempty"`
	Review         bool   `json:"review,omitempty"`
}

// OverrideProvider is the provider a held start picks, as the start resolves
// it: the named provider, else the one of the model, else the one a harness
// fixes by itself. It is "" when only a start could tell.
func OverrideProvider(p WorkerStartRequest) string {
	if provider := strings.TrimSpace(p.Provider); provider != "" {
		return strings.ToLower(provider)
	}
	if model := strings.TrimSpace(p.Model); model != "" && model != "auto" {
		return ProfileProvider(p.Harness, model)
	}
	switch strings.ToLower(strings.TrimSpace(p.Harness)) {
	case "claude":
		return "anthropic"
	case "codex":
		return "openai"
	}
	return ""
}

// ConfirmContext is what the gateway reads when a confirm comes: the
// protected providers and the scores and level minimums the picker uses. The
// quota, floor and burn checks belong to the start's gate, not to the confirm.
type ConfirmContext struct {
	Protected []string
	Scores    []ModelScore
	Minimums  map[string]float64
}

// ConfirmOverride checks a confirm of a held override (decision 0151).
//
// Nobody confirms an override that picks the caller's own model family for a
// review of its own work. The chief confirms only when all of these hold, and
// otherwise the ask goes to the CTO: the provider is known and not protected,
// and the override is the same tier or cheaper: the highest level its score
// meets is at or below the level the start runs at. A fact that is missing (an
// unknown provider, caller family, level or score) never opens the gate: the
// chief sends the ask to the CTO. The start's gate still refuses a model whose
// quota cannot run now.
func ConfirmOverride(f OverrideFacts, c ConfirmContext, chief bool) error {
	if f.Review && f.OverrideFamily != "" && f.OverrideFamily == f.CallerFamily {
		if chief {
			return errors.New("this override picks the caller's own model family (" + f.OverrideFamily + ") for a review of its own work: nobody confirms it. Send it to the CTO, or answer picker")
		}
		return errors.New("this override picks the caller's own model family (" + f.OverrideFamily + ") for a review of its own work: nobody confirms it; answer picker")
	}
	if !chief {
		return nil
	}
	route := func(why string) error { return errors.New(why + ": the chief does not confirm it. Send it to the CTO") }
	if f.Review && (f.CallerFamily == "" || f.OverrideFamily == "") {
		return route("the task reviews work and the caller's or the override's model family is unknown")
	}
	if f.Provider == "" {
		return route("the override's provider is unknown")
	}
	for _, p := range c.Protected {
		if strings.EqualFold(strings.TrimSpace(p), f.Provider) {
			return route("provider " + f.Provider + " is protected")
		}
	}
	model := strings.TrimSpace(f.Model)
	if model == "" || model == "auto" {
		return route("the override names no model, so its tier is unknown")
	}
	start := levelIndex(f.Level)
	if start < 0 {
		return route("the start's level is not known")
	}
	_, level, _, scored := WorkerBenchmarkCeiling(WorkerModelCandidate{Harness: f.Harness, Model: model, Effort: f.Effort}, c.Scores, c.Minimums)
	if !scored {
		return route("model " + model + " has no score")
	}
	if levelIndex(level) > start {
		return route("model " + model + " meets " + level + ", above the " + f.Level + " level of the start")
	}
	return nil
}

// levelIndex is the place of a level in DifficultyLevels, or -1.
func levelIndex(level string) int {
	for i, l := range DifficultyLevels {
		if l == level {
			return i
		}
	}
	return -1
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
