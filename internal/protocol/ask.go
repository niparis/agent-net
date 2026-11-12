package protocol

import "time"

// An ask is one thing that waits on a person (decision 0090). Every source
// writes its asks to one gateway store, and the Desk reads that one list.

// Who must act on an ask now. A session id (or a name) can also stand here.
const (
	AskNextOwner = "owner"
	AskNextChief = "chief"
)

// Ask states. "with_someone" means the ask is open but another party than the
// owner must act on it now.
const (
	AskStateOpen   = "open"
	AskWithSomeone = "with_someone"
	AskAnswered    = "answered"
	AskClosed      = "closed"
)

// What an ask is about.
const (
	AskAboutChange  = "change"
	AskAboutProject = "project"
	AskAboutSession = "session"
	AskAboutMachine = "machine"
)

// Where an ask comes from, besides a session.
const (
	AskFromSession = "session"
	AskFromCTO     = "cto"
	AskFromGateway = "gateway"
	AskFromDisk    = "disk"
	AskFromApps    = "apps"
)

// Why an ask closed without an answer. The reason is kept on the ask.
const (
	AskReasonWithdrawn    = "withdrawn"
	AskReasonReplaced     = "replaced"
	AskReasonChangeDone   = "change_done"
	AskReasonSessionEnded = "session_ended"
	AskReasonResolved     = "resolved"
	AskReasonExpired      = "expired"
	AskReasonActivity     = "activity_or_end"
	AskReasonOwnerReplied = "owner_replied"      // the owner typed in the asking session
	AskReasonMovedOn      = "moved_on"           // the asking session announced new work
	AskReasonMovedOnClean = "moved_on (cleanup)" // the one-time cleanup of asks the session left behind
	AskReasonAnswered     = "answered"           // the escalated request was answered later
	AskReasonMovedToLog   = "moved_to_log"
	AskReasonGrouped      = "grouped" // a per-field charter draft ask folded into the one ask of its node
	// AskReasonSignedIn closes a sign_in ask after its provider's quota check
	// no longer reports the login remedy.
	AskReasonSignedIn = "signed_in"
	// AskReasonReturned closes the ask about a machine that went silent when
	// its daemon connects again.
	AskReasonReturned = "returned"
	// AskReasonSlept closes that ask when a sleep report explains the gap.
	AskReasonSlept = "slept"
)

// Ask kinds name the source and so the owner's actions on a row.
const (
	AskKindQuestion   = "question"
	AskKindApproval   = "approval"
	AskKindIdea       = "feature_idea"
	AskKindRejection  = "rejection"
	AskKindEscalation = "escalation"
	AskKindHeld       = "held"
	AskKindLearned    = "learned"
	AskKindDraft      = "draft"
	AskKindProposal   = "proposal"
	// AskKindPermission is an owner-only ask to allow exact Claude Code rules
	// in registered repositories. Only the owner can answer it.
	AskKindPermission = "permission"
	AskKindStep       = "step"
	AskKindWaiting    = "waiting"
	AskKindStall      = "session_stall"
	AskKindCheck      = "check"
	// AskKindBrief is the chief of staff's daily brief: information for the
	// owner, dismissed when read. It never asks for an answer.
	AskKindBrief = "brief"
	// AskKindSignIn is an owner-only ask to open a CLI's interactive login in
	// a herdr pane. Its provider is in Ref; the command comes from the fixed
	// allowlist in signin.go, never from an agent.
	AskKindSignIn = "sign_in"
	// AskKindMachine is the owner's notice that a machine's link went silent
	// without a sleep report for longer than the gateway waits. It closes by
	// itself when the machine returns.
	AskKindMachine = "machine"
	// AskKindCTOAlert is a wake of the CTO (decision 0152): a P1 item in the
	// chief's queue for one CTO intake entry. The chief routes it to the lead
	// that owns the cause. It never starts a CTO turn.
	AskKindCTOAlert = "cto_alert"
	// AskKindQuotaStuck is the chief's item for work that stopped because a
	// provider has no quota left. It links the CTO intake entry of the
	// provider, and closes with it.
	AskKindQuotaStuck = "quota_stuck"
)

// Ask is one row of the Desk. It has the shape of a delivery step: who waits,
// what about, the question with its options and a recommendation, who must act
// now, and a state with a close reason.
type Ask struct {
	ID    string `json:"id"`
	Kind  string `json:"kind"`
	State string `json:"state"`
	// From names the waiting session or source. FromKind is session, cto,
	// gateway, disk or apps.
	FromKind    string `json:"from_kind"`
	From        string `json:"from"`
	FromSession string `json:"from_session,omitempty"`
	// About is a change, project or session, when there is one.
	AboutKind  string `json:"about_kind,omitempty"`
	AboutID    string `json:"about_id,omitempty"`
	AboutTitle string `json:"about_title,omitempty"`
	// Node is the project or machine the ask belongs to, as the Inbox kept it.
	Node           string   `json:"node,omitempty"`
	Question       string   `json:"question"`
	Options        []string `json:"options,omitempty"`
	Recommendation string   `json:"recommendation,omitempty"`
	Detail         string   `json:"detail,omitempty"`
	// Next is who must act now: owner, a session id, or chief.
	Next   string `json:"next"`
	WhyYou string `json:"why_you,omitempty"`
	// Owner (chief or cto), DeskState and Category are the Desk item shape of
	// decision 0150.
	Owner     string `json:"owner,omitempty"`
	DeskState string `json:"desk_state,omitempty"`
	Category  string `json:"category,omitempty"`
	// Priority (1 to 3) is the chief's rank for an ask it sent to the owner.
	Priority    int    `json:"priority,omitempty"`
	Answer      string `json:"answer,omitempty"`
	CloseReason string `json:"close_reason,omitempty"`
	// Ref is the source's own key (a message id, a draft id) and links the ask
	// to the operation that answers it. MessageID and ConversationID keep the
	// Inbox meaning: the reply of an outcome, or the marker of a notice.
	Ref            string         `json:"ref,omitempty"`
	MessageID      string         `json:"message_id,omitempty"`
	ConversationID string         `json:"conversation_id,omitempty"`
	CreatedAt      time.Time      `json:"created_at"`
	ClosedAt       time.Time      `json:"closed_at,omitempty"`
	Delivery       *InboxDelivery `json:"delivery,omitempty"`
}

// AsksResult is what the gateway answers to asks.list: only open asks.
type AsksResult struct {
	Asks []Ask `json:"asks"`
}

// DeskLogEntry is one line of the event log. Options carry what the owner
// needs to undo the event, when it can be undone.
type DeskLogEntry struct {
	ID        int64     `json:"id"`
	At        time.Time `json:"at"`
	Source    string    `json:"source"`
	AboutKind string    `json:"about_kind,omitempty"`
	AboutID   string    `json:"about_id,omitempty"`
	Text      string    `json:"text"`
	Detail    string    `json:"detail,omitempty"`
	Options   []string  `json:"options,omitempty"`
}

// DeskLogQuery asks for the newest log lines.
type DeskLogQuery struct {
	Limit int `json:"limit,omitempty"`
}

// DeskLogResult is the event log, newest first.
type DeskLogResult struct {
	Entries []DeskLogEntry `json:"entries"`
}

// DeskNote adds one line to the Desk's event log. The daemon uses it for a
// report that needs no answer, such as a Claude session whose wheel is off.
type DeskNote struct {
	Text   string `json:"text"`
	Detail string `json:"detail,omitempty"`
}

// AskOpen is a source outside the gateway that opens or closes an ask, for
// example the daemon for a CTO proposal. Ref is the source's own key and makes
// the call safe to repeat.
type AskOpen struct {
	Kind           string   `json:"kind"`
	Ref            string   `json:"ref"`
	From           string   `json:"from"`
	AboutKind      string   `json:"about_kind,omitempty"`
	AboutID        string   `json:"about_id,omitempty"`
	Question       string   `json:"question"`
	Options        []string `json:"options,omitempty"`
	Recommendation string   `json:"recommendation,omitempty"`
	Detail         string   `json:"detail,omitempty"`
}

// AskClose closes the open ask for a source key.
type AskClose struct {
	Ref    string `json:"ref"`
	Reason string `json:"reason"`
}

// AskSync replaces all open asks of one external kind from one machine with
// the given list: it opens the missing ones and closes the rest. A daemon
// sends it after a change and when its link to the gateway comes back, so a
// call lost while offline cannot leave a stale ask.
type AskSync struct {
	Kind string    `json:"kind"`
	Asks []AskOpen `json:"asks"`
}

// Where the questions of a team's members go (decision 0100).
const (
	AsksToOwner = "owner"
	AsksToLead  = "lead"
)

// ValidAsksTo says whether v is a team asks_to value.
func ValidAsksTo(v string) bool { return v == AsksToOwner || v == AsksToLead }

// AskRedirect says that a member's agent_owner_ask went to its team lead
// instead of the owner. Note is what the asking agent reads.
type AskRedirect struct {
	Team        string `json:"team"`
	Lead        string `json:"lead"`
	LeadSession string `json:"lead_session"`
	MessageID   string `json:"message_id"`
	ItemID      string `json:"item_id"`
	Note        string `json:"note"`
}
