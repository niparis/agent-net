package protocol

import (
	"errors"
	"regexp"
	"slices"
	"strings"
)

// The Desk item shape of decision 0150: who owns an item, where it stands,
// its category, and the scope of a standing rule.

// Who owns a Desk item: the chief of staff curates the Desk, the CTO owns an
// item routed to it.
const (
	DeskOwnerChief = "chief"
	DeskOwnerCTO   = "cto"
)

// Desk states. The gateway keeps them equal to the item's status and next who.
const (
	DeskVerifying  = "verifying"
	DeskWithCTO    = "with_cto"
	DeskReady      = "ready"
	DeskAnswered   = "answered"
	DeskCarriedOut = "carried_out"
	DeskClosed     = "closed"
)

// Item categories. The safety net of decision 0150 uses them.
const (
	CategorySecurity = "security"
	CategoryOutage   = "outage"
	CategoryMoney    = "money"
	CategoryNormal   = "normal"
)

// Rule categories add the two owner-only ones that are not item categories.
const (
	CategorySecrets      = "secrets"
	CategoryClientFacing = "client_facing"
)

// ItemStatusJoined is the status of a duplicate that joined an open item. It
// is answered or closed with that item.
const ItemStatusJoined = "joined"

// ValidItemCategory says whether c is an item category.
func ValidItemCategory(c string) bool {
	switch c {
	case CategorySecurity, CategoryOutage, CategoryMoney, CategoryNormal:
		return true
	}
	return false
}

// OwnerOnlyCategory says whether c is one of the owner's own calls: money,
// secrets, security and client-facing. A rule in one names a repo or a team.
func OwnerOnlyCategory(c string) bool {
	switch c {
	case CategoryMoney, CategorySecrets, CategorySecurity, CategoryClientFacing:
		return true
	}
	return false
}

// KindCategory is the category a kind fixes, or "" when the kind does not.
func KindCategory(kind string) string {
	switch kind {
	case AskKindSignIn, AskKindPermission:
		return CategorySecurity
	case AskKindMachine:
		return CategoryOutage
	}
	return ""
}

// KindNeedsOptions says whether an owner-bound item of this kind must carry
// options. The other kinds have their answers fixed by the kind (approve,
// accept, confirm, open, dismiss).
func KindNeedsOptions(kind string) bool {
	switch kind {
	case AskKindQuestion, AskKindIdea, AskKindCheck, AskKindWaiting, AskKindStall:
		return true
	}
	return false
}

// A rule area is a repo, a team or the whole network.
const (
	RuleAreaRepo    = "repo:"
	RuleAreaTeam    = "team:"
	RuleAreaNetwork = "network"
)

// RuleScope is the scope of a standing rule: where it holds and what it
// allows. A rule applies only when both match exactly.
type RuleScope struct {
	Area     string `json:"area"`
	Action   string `json:"action"`
	Category string `json:"category,omitempty"`
}

// Normalize trims the parts and folds the action's case and spaces.
func (r RuleScope) Normalize() RuleScope {
	r.Area = strings.TrimSpace(r.Area)
	r.Action = NormalizeRuleAction(r.Action)
	r.Category = strings.TrimSpace(r.Category)
	if r.Category == "" {
		r.Category = CategoryNormal
	}
	return r
}

// NormalizeRuleAction folds case and white space, so "Deploy  to prod" and
// "deploy to prod" are the same action. Nothing else is folded: "deploy" and
// "deploy to prod" stay different.
func NormalizeRuleAction(a string) string {
	return strings.ToLower(strings.Join(strings.Fields(a), " "))
}

// Validate checks a scope. It names the field that is wrong.
func (r RuleScope) Validate() error {
	r = r.Normalize()
	switch {
	case r.Area == RuleAreaNetwork:
	case strings.HasPrefix(r.Area, RuleAreaRepo) && len(r.Area) > len(RuleAreaRepo):
	case strings.HasPrefix(r.Area, RuleAreaTeam) && len(r.Area) > len(RuleAreaTeam):
	default:
		return errors.New(`scope area must be "network", "repo:<name>" or "team:<name>"`)
	}
	if r.Action == "" {
		return errors.New("scope action is required: what the rule allows, for example merge")
	}
	if !ValidItemCategory(r.Category) && r.Category != CategorySecrets && r.Category != CategoryClientFacing {
		return errors.New("scope category must be normal, security, outage, money, secrets or client_facing")
	}
	if r.Area == RuleAreaNetwork && OwnerOnlyCategory(r.Category) {
		return errors.New("scope area: a " + r.Category + " rule names a repo or a team, never the network")
	}
	return nil
}

// Matches says whether the rule holds for an item in repo and team doing
// action. Both parts must match exactly.
func (r RuleScope) Matches(repo, team, action string) bool {
	r = r.Normalize()
	if r.Action == "" || r.Action != NormalizeRuleAction(action) {
		return false
	}
	switch {
	case r.Area == RuleAreaNetwork:
		return true
	case strings.HasPrefix(r.Area, RuleAreaRepo):
		return repo != "" && r.Area == RuleAreaRepo+repo
	case strings.HasPrefix(r.Area, RuleAreaTeam):
		return team != "" && r.Area == RuleAreaTeam+team
	}
	return false
}

// askIDPattern matches ids only: an agent-net id (a short prefix such as i_,
// m_, s_, ch_ or so_ and hex) and a run of 7 or more hex characters, such as
// a commit. Plain numbers stay, so "merge 100 files" and "merge 900 files"
// are different asks.
var askIDPattern = regexp.MustCompile(`\b[a-z]{1,4}_[0-9a-f]{4,}\b|\b[0-9a-f]{7,}\b`)

// NormalizeAskText is the duplicate key of an ask's text: case and white
// space folded, ids removed.
func NormalizeAskText(s string) string {
	s = strings.ToLower(s)
	s = askIDPattern.ReplaceAllString(s, "")
	return strings.Join(strings.Fields(s), " ")
}

// SameAskText says whether two asks ask the same thing. It compares the
// normalized text and keeps the ids apart: a new commit head or a new ask id
// makes a new ask, not a repeat of the old one.
func SameAskText(a, b string) bool {
	if NormalizeAskText(a) != NormalizeAskText(b) {
		return false
	}
	return slices.Equal(askIDs(a), askIDs(b))
}

// askIDs lists the ids of a text in the order they appear.
func askIDs(s string) []string {
	return askIDPattern.FindAllString(strings.ToLower(s), -1)
}

// questionPrefix matches a list marker before a question: "1.", "2)", "-", "*".
var questionPrefix = regexp.MustCompile(`^(?:[0-9]+[.)]|[-*•])\s+`)

// SplitQuestions splits an ask that holds several questions, one per line,
// into one text per question. Lines that are not questions are context and
// stay at the top of each part. An ask with fewer than two questions is not
// split and the result is nil.
func SplitQuestions(text string) []string {
	var context, questions []string
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if strings.HasSuffix(line, "?") {
			questions = append(questions, questionPrefix.ReplaceAllString(line, ""))
		} else {
			context = append(context, line)
		}
	}
	if len(questions) < 2 {
		return nil
	}
	lead := strings.Join(context, "\n")
	out := make([]string, len(questions))
	for i, q := range questions {
		if lead != "" {
			q = lead + "\n" + q
		}
		out[i] = q
	}
	return out
}

// The agent tool agent_owner_judgment_record: the chief of staff and the CTO
// record a decision the owner made in a live chat, with its scope.
const (
	OpJudgmentRecord    = "judgment_record"
	FrameJudgmentRecord = "judgment.record"
)

// JudgmentRecordArgs are the tool's arguments. A scope (area and action) makes
// the decision a standing rule; without one it is a one-time answer.
type JudgmentRecordArgs struct {
	Text     string `json:"text"`
	Area     string `json:"area,omitempty"`
	Action   string `json:"action,omitempty"`
	Category string `json:"category,omitempty"`
}

// Validate checks the arguments and names the field that is wrong.
func (a JudgmentRecordArgs) Validate() error {
	if strings.TrimSpace(a.Text) == "" {
		return errors.New("text is required: the owner's decision in one line")
	}
	if a.Area == "" && a.Action == "" {
		return nil
	}
	return RuleScope{Area: a.Area, Action: a.Action, Category: a.Category}.Validate()
}

// JudgmentRecordRequest is the frame body: the caller's session and its
// arguments.
type JudgmentRecordRequest struct {
	SessionID string `json:"session_id"`
	JudgmentRecordArgs
}

// JudgmentRecordResult is the recorded judgment: a rule when it has a scope,
// else an answer.
type JudgmentRecordResult struct {
	ID       string `json:"id"`
	Kind     string `json:"kind"`
	Area     string `json:"area,omitempty"`
	Action   string `json:"action,omitempty"`
	Category string `json:"category,omitempty"`
}
