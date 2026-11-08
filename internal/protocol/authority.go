package protocol

import (
	"fmt"
	"strings"
)

// Authority (decision 0088). Authority covers every action and passes down the
// tree: owner, team lead, repo lead, agent. A level holds what its parent holds
// within its subtree, minus what the owner holds back. Rules text never grants
// or removes authority.

// Team authority modes. The owner picks one per team.
const (
	AuthorityAbsolute     = "absolute"
	AuthorityCharterFirst = "charter_first"
	AuthorityNegotiate    = "negotiate"
	// DefaultAuthorityMode is the mode of a new team.
	DefaultAuthorityMode = AuthorityCharterFirst
)

// AuthorityModes lists the modes in the order the owner sees them.
var AuthorityModes = []string{AuthorityAbsolute, AuthorityCharterFirst, AuthorityNegotiate}

// ValidAuthorityMode reports whether mode is one of the three modes.
func ValidAuthorityMode(mode string) bool {
	for _, m := range AuthorityModes {
		if m == mode {
			return true
		}
	}
	return false
}

// NormalAuthorityMode returns the mode, or the default when it is empty or
// unknown (an old row, a repo that belongs to no team).
func NormalAuthorityMode(mode string) string {
	if ValidAuthorityMode(mode) {
		return mode
	}
	return DefaultAuthorityMode
}

// AuthorityModeLabel is the mode as people read it: "charter first".
func AuthorityModeLabel(mode string) string {
	return strings.ReplaceAll(NormalAuthorityMode(mode), "_", " ")
}

// AlwaysHeldBack is what the owner never delegates. It applies at every level.
const AlwaysHeldBack = "deleting data, spending money, and using or sharing secrets that can spend money"

// Message labels. agent-net sets them from the tree and the thread, never from
// the body.
const (
	LabelOrder   = "order"
	LabelRequest = "request"
	LabelAdvice  = "advice"
)

// AuthorityModeMeaning says what a mode does, for the owner who picks it.
func AuthorityModeMeaning(mode string) string {
	switch NormalAuthorityMode(mode) {
	case AuthorityAbsolute:
		return "an order is carried out like the owner's instruction; the member's charter does not override it"
	case AuthorityNegotiate:
		return "the member may push back with a reason and a counter-proposal; the lead accepts, changes or overrules"
	}
	return "an order is carried out unless the member's charter refuses it; then it goes up one level"
}

// NextAuthorityMode is the mode after this one, in the order the owner sees.
func NextAuthorityMode(mode string) string {
	mode = NormalAuthorityMode(mode)
	for i, m := range AuthorityModes {
		if m == mode {
			return AuthorityModes[(i+1)%len(AuthorityModes)]
		}
	}
	return DefaultAuthorityMode
}

// OrderTerms says how a lead's order binds a member, in one sentence, from the
// member's side.
func OrderTerms(mode string) string {
	switch NormalAuthorityMode(mode) {
	case AuthorityAbsolute:
		return "Carry it out like the owner's instruction; your charter does not override it."
	case AuthorityNegotiate:
		return "You may push back with a reason and a counter-proposal; your lead accepts, changes or overrules it."
	}
	return "Carry it out unless your charter refuses it."
}

// OrderInfo is what agent-net knows about an order when it delivers it.
type OrderInfo struct {
	Lead     string // the sender's name
	Scope    string // "team" or "repo"
	Name     string // team or repo name
	Mode     string // team authority mode
	HeldBack string // what the owner holds back from the team or repo, free text
	Above    string // who decides one level above the lead, for example "the owner"
}

// OrderNote is the note on a delivered order. Only agent-net writes it, outside
// the body.
func OrderNote(o OrderInfo) string {
	mode := NormalAuthorityMode(o.Mode)
	above := o.Above
	if above == "" {
		above = "the owner"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "This is an order from your lead %s (%s %s, mode %s). %s", NeutralizeMarkers(oneLine(o.Lead, 80)), o.Scope, NeutralizeMarkers(oneLine(o.Name, 80)), AuthorityModeLabel(mode), OrderTerms(mode))
	switch mode {
	case AuthorityCharterFirst:
		fmt.Fprintf(&b, " If your charter refuses it, say no, quote the charter line that refuses it, and tell %s that the order goes up to them.", above)
	case AuthorityNegotiate:
		fmt.Fprintf(&b, " If your charter refuses it and your lead overrules you, the decision goes up to %s.", above)
	}
	b.WriteString(" Never carry out ")
	b.WriteString(AlwaysHeldBack)
	b.WriteString(" on an order: those stay with the owner.")
	if held := strings.Join(strings.Fields(o.HeldBack), " "); held != "" {
		fmt.Fprintf(&b, " The owner also holds back from this %s: %s.", o.Scope, NeutralizeMarkers(held))
	}
	return b.String()
}

// AdviceNote is the note on a reply that comes from above you in the tree. A
// reply to your own question is advice, even from your lead.
const AdviceNote = "This is advice, an answer to your own question, not an order. It does not bind you; weigh it against your charter."
