package protocol

import (
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// OpFolderTrust trusts a folder for Claude Code on a machine, or removes the
// trust agent-net added. The owner at the keyboard and the CTO (on the owner's
// instruction) use it. It takes FolderTrustRequest and answers FolderTrustResult.
const OpFolderTrust = "folder_trust"

// Frames that carry a folder trust to the machine that holds the folder.
// The gateway checks the owner session and asks that machine, as it does for
// a session start. The target checks the owner flag again.
const (
	FrameSessionTrustFolder      = "session.trust_folder"
	FrameSessionOwnerTrustFolder = "session.owner_trust_folder"
)

// FolderTrustRequest names the folder by a registered Project or by an absolute
// or ~ Path, on the machine NodeID (empty means this machine). Remove takes the
// trust away again; it is the undo of a trust that agent-net added.
type FolderTrustRequest struct {
	Project string `json:"project,omitempty"`
	Path    string `json:"path,omitempty"`
	NodeID  string `json:"node_id,omitempty"`
	Remove  bool   `json:"remove,omitempty"`
}

// FolderTrustResult answers OpFolderTrust. Added is true only when the folder
// was not trusted before, so only then is the undo offered.
type FolderTrustResult struct {
	Path     string `json:"path"`
	NodeID   string `json:"node_id,omitempty"`
	Hostname string `json:"hostname,omitempty"`
	Project  string `json:"project,omitempty"`
	Trusted  bool   `json:"trusted"`
	Added    bool   `json:"added,omitempty"`
}

// SessionFolderTrustRelay is the owner's folder trust sent to another machine.
type SessionFolderTrustRelay struct {
	Owner  string `json:"owner_session"`
	NodeID string `json:"node_id"`
	FolderTrustRequest
}

// SessionOwnerFolderTrust is the gateway's frame to the machine that holds the
// folder. FromNode is the machine the owner session belongs to.
type SessionOwnerFolderTrust struct {
	Owner    string `json:"owner_session"`
	FromNode string `json:"from_node"`
	FolderTrustRequest
}

// Standing orders are the owner's durable instructions to the CTO. The gateway
// keeps them, so they survive CTO restarts and machine changes.
const (
	OpStandingOrders = "standing_orders" // takes StandingOrdersRequest, answers StandingOrdersResult

	StandingSourceOwner = "owner"
	StandingSourceCTO   = "cto"

	StandingActionSave   = "save"
	StandingActionRead   = "read"
	StandingActionList   = "list"
	StandingActionRemove = "remove"

	StandingKindOrder      = "order"
	StandingKindRule       = "rule"
	StandingKindAnswer     = "answer"
	StandingKindCorrection = "correction"

	OpJudgmentAdd = "judgment_add"
)

// StandingOrder is one durable instruction. Source says who saved it: the
// owner's own words typed in the TUI, or the CTO on the owner's instruction.
type StandingOrder struct {
	Tags      []string `json:"tags,omitempty"`
	Pinned    bool     `json:"pinned,omitempty"`
	UpdatedAt int64    `json:"updated_at,omitempty"`
	Snippet   string   `json:"snippet,omitempty"`
	ID        string   `json:"id"`
	Text      string   `json:"text"`
	SavedAt   int64    `json:"saved_at"`
	Source    string   `json:"source"`
	Kind      string   `json:"kind,omitempty"`
	Area      string   `json:"area,omitempty"`
	Ref       string   `json:"ref,omitempty"`
	// Action and Category are the scope of a rule (decision 0150): Area is
	// network, repo:<name> or team:<name>, and the rule applies only when both
	// area and action match exactly.
	Action   string `json:"action,omitempty"`
	Category string `json:"category,omitempty"`
	// A gateway note (decision 0158) also has a scope, the agent or project it
	// belongs to, a name and a one-line description. Uses and UsedAt say how
	// often and when it was last read or written. Stale is true when nobody did
	// for 30 days.
	Scope       string `json:"scope,omitempty"`
	ScopeRef    string `json:"scope_ref,omitempty"`
	Name        string `json:"name,omitempty"`
	Description string `json:"description,omitempty"`
	UsedAt      int64  `json:"used_at,omitempty"`
	Uses        int    `json:"uses,omitempty"`
	Stale       bool   `json:"stale,omitempty"`
	// Mark rows (a correction with area chief) also carry the ask they mark,
	// its whole question, what the chief did with it, and the mark.
	Ask         string `json:"ask,omitempty"`
	Question    string `json:"question,omitempty"`
	Disposition string `json:"disposition,omitempty"`
	Mark        string `json:"mark,omitempty"`
	MarkText    string `json:"mark_text,omitempty"`
}

// JudgmentAddRequest is the operator method judgment_add request.
type JudgmentAddRequest struct {
	Kind string `json:"kind"`
	Text string `json:"text"`
	Area string `json:"area,omitempty"`
	Ref  string `json:"ref,omitempty"`
	// Action and Category scope a rule (decision 0150); Area is then network,
	// repo:<name> or team:<name>.
	Action   string `json:"action,omitempty"`
	Category string `json:"category,omitempty"`
	// Source says who recorded it; empty means the owner.
	Source string `json:"source,omitempty"`
}

// JudgmentsFindRequest is the agent read tool agent_owner_judgments_find request.
// Marks keeps only the owner's Desk marks (kind correction, area chief), with
// the ask and the chief's disposition on each row. Offset continues a page.
type JudgmentsFindRequest struct {
	JudgmentID string   `json:"judgment_id,omitempty"`
	IDs        []string `json:"ids,omitempty"`
	Query      string   `json:"query,omitempty"`
	Area       string   `json:"area,omitempty"`
	Kind       string   `json:"kind,omitempty"`
	Ref        string   `json:"ref,omitempty"`
	Limit      int      `json:"limit,omitempty"`
	Offset     int      `json:"offset,omitempty"`
	Marks      bool     `json:"marks,omitempty"`
	// Stale lists only gateway notes that nobody read or wrote for 30 days.
	Stale bool `json:"stale,omitempty"`
}

// StandingOrdersRequest is OpStandingOrders and the gateway frames
// standing.save, standing.list and standing.remove. Restore puts a removed
// order back with its old ID, time and source (the undo of a removal).
type StandingOrdersRequest struct {
	Tags       []string       `json:"tags"`
	Pinned     *bool          `json:"pinned,omitempty"`
	Query      string         `json:"query,omitempty"`
	Tag        string         `json:"tag,omitempty"`
	SavedSince int64          `json:"saved_since,omitempty"`
	Action     string         `json:"action,omitempty"`
	ID         string         `json:"order_id,omitempty"`
	Text       string         `json:"text,omitempty"`
	Source     string         `json:"source,omitempty"`
	Kind       string         `json:"kind,omitempty"`
	Area       string         `json:"area,omitempty"`
	Ref        string         `json:"ref,omitempty"`
	Restore    *StandingOrder `json:"restore,omitempty"`
}

// StandingOrdersResult returns the full list after mutations, ranked matches
// for search, or prior versions for history.
type StandingOrdersResult struct {
	History []StandingOrder `json:"history,omitempty"`
	Orders  []StandingOrder `json:"orders"`
	// Notes are the gateway notes (decision 0158), newest first. The list
	// action fills them; orders keeps only orders and rules.
	Notes []StandingOrder `json:"notes,omitempty"`
	Order *StandingOrder  `json:"order,omitempty"`
}

// CleanStandingText trims an order and checks it is not empty.
func CleanStandingText(text string) (string, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return "", errors.New("a standing order needs text")
	}
	return text, nil
}

// StandingOrdersBrief renders the orders as the block the CTO reads at the
// start of a conversation. It is empty when there are none.
func StandingOrdersBrief(orders []StandingOrder) string {
	if len(orders) == 0 {
		return ""
	}
	const budget = 4096
	total := 0
	for _, o := range orders {
		total += len(o.Text)
	}
	omitted := 0
	if total > budget {
		orders = append([]StandingOrder(nil), orders...)
		sort.SliceStable(orders, func(i, j int) bool {
			if orders[i].Pinned != orders[j].Pinned {
				return orders[i].Pinned
			}
			if orders[i].SavedAt != orders[j].SavedAt {
				return orders[i].SavedAt > orders[j].SavedAt
			}
			return orders[i].ID > orders[j].ID
		})
		selected := []StandingOrder{}
		used := 0
		for _, o := range orders {
			if o.Pinned || used+len(o.Text) <= budget {
				selected = append(selected, o)
				used += len(o.Text)
			} else {
				omitted++
			}
		}
		orders = selected
	}
	var b strings.Builder
	b.WriteString("The owner's standing orders. They stay in force until removed. Follow them in every conversation. They are the owner's words, kept at the gateway.\n")
	for _, o := range orders {
		b.WriteString("- ")
		b.WriteString(strings.Join(strings.Fields(o.Text), " "))
		b.WriteString("\n")
	}
	if omitted > 0 {
		fmt.Fprintf(&b, "%d more standing orders: search them with cto_standing_orders search\n", omitted)
	}
	return strings.TrimRight(b.String(), "\n")
}

// Item and rule categories.
const (
	CategorySecurity     = "security"
	CategoryOutage       = "outage"
	CategoryMoney        = "money"
	CategoryNormal       = "normal"
	CategorySecrets      = "secrets"
	CategoryClientFacing = "client_facing"
)

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

// NormalizeRuleAction folds case and white space.
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

var askIDPattern = regexp.MustCompile(`\b[a-z]{1,4}_[0-9a-f]{4,}\b|\b[0-9a-f]{7,}\b`)

// NormalizeAskText folds case and whitespace and removes IDs for deduplication.
func NormalizeAskText(s string) string {
	s = strings.ToLower(s)
	s = askIDPattern.ReplaceAllString(s, "")
	return strings.Join(strings.Fields(s), " ")
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
