package protocol

import (
	"errors"
	"strings"
	"time"
)

// The chief of staff (decision 0111) is a standard agent session. Asks that
// would go to the owner first are routed to it; it sorts them with
// agent_ask_sort and never answers for the owner.

// The four results of a sort.
const (
	AskSortClose = "close"
	AskSortMerge = "merge"
	AskSortSend  = "send"
	AskSortOwner = "owner"
)

// ChiefTimeout is how long the chief has to sort an ask before it goes to the
// owner. It is a constant, not a setting.
const ChiefTimeout = 15 * time.Minute

// ChiefQuietWindow is how long asks for the chief wait so that a burst goes
// out as one message. ChiefMaxWait sends a batch even when asks keep coming.
const (
	ChiefQuietWindow = 5 * time.Second
	ChiefMaxWait     = 30 * time.Second
	ChiefBatchCap    = 25
)

// ChiefModelKey is the settings key of the model the chief session runs on.
// The default is codex, openai, gpt-6.1-sol, medium.
const (
	ChiefModelKey     = "chief.model"
	DefaultChiefModel = "codex · openai · gpt-6.1-sol · medium"
)

// Operator methods of the owner's TUI and the CTO: the chief's status and
// setting, and the sort history of one ask.
const (
	OpChiefGet   = "chief_get"
	OpChiefSet   = "chief_set"
	OpAskHistory = "ask_history"
)

// OpAskSort is the agent tool agent_ask_sort. FrameAskSort carries it from the
// daemon to the gateway, with the caller's session in the request.
const (
	OpAskSort    = "ask_sort"
	FrameAskSort = "ask.sort"
)

// MaxChiefReasonChars limits the reason and the why_you line of a sort.
const MaxChiefReasonChars = 300

// AskSortArgs are the arguments of agent_ask_sort. Ask is the ask id. Into is
// the target of merge, To the session of send, Priority (1 to 3) and WhyYou
// the owner's line for owner.
type AskSortArgs struct {
	Ask      string `json:"ask"`
	Result   string `json:"result"`
	Into     string `json:"into,omitempty"`
	To       string `json:"to,omitempty"`
	Reason   string `json:"reason"`
	Priority int    `json:"priority,omitempty"`
	WhyYou   string `json:"why_you,omitempty"`
}

// Validate checks a sort before the gateway looks at the ask.
func (a AskSortArgs) Validate() error {
	if strings.TrimSpace(a.Ask) == "" {
		return errors.New("ask is required")
	}
	if strings.TrimSpace(a.Reason) == "" {
		return errors.New("reason is required: say why in one line")
	}
	if len(a.Reason) > MaxChiefReasonChars || len(a.WhyYou) > MaxChiefReasonChars {
		return errors.New("reason and why_you are limited to 300 characters")
	}
	switch a.Result {
	case AskSortClose:
	case AskSortMerge:
		if a.Into == "" {
			return errors.New("merge needs into: the ask id to merge into")
		}
	case AskSortSend:
		if a.To == "" {
			return errors.New("send needs to: the session id")
		}
	case AskSortOwner:
		if a.Priority < 1 || a.Priority > 3 {
			return errors.New("owner needs priority 1 to 3")
		}
		if strings.TrimSpace(a.WhyYou) == "" {
			return errors.New("owner needs why_you: why the owner must decide")
		}
	default:
		return errors.New("result must be close, merge, send or owner")
	}
	return nil
}

// AskSortRequest is the frame body: the caller's session and its arguments.
type AskSortRequest struct {
	SessionID string `json:"session_id"`
	AskSortArgs
}

// AskSortResult says where the ask is after the sort.
type AskSortResult struct {
	Ask    string `json:"ask"`
	Result string `json:"result"`
	Next   string `json:"next"`
	State  string `json:"state"`
}

// AskSort is one row of an ask's sort history. The Prev fields hold the ask as
// it was before the sort, so a later undo can put it back.
type AskSort struct {
	ID             int64     `json:"id"`
	Ask            string    `json:"ask"`
	At             time.Time `json:"at"`
	Sorter         string    `json:"sorter"`
	Result         string    `json:"result"`
	Into           string    `json:"into,omitempty"`
	To             string    `json:"to,omitempty"`
	Reason         string    `json:"reason,omitempty"`
	Priority       int       `json:"priority,omitempty"`
	WhyYou         string    `json:"why_you,omitempty"`
	PrevNext       string    `json:"prev_next"`
	PrevStatus     string    `json:"prev_status"`
	PrevAnswer     string    `json:"prev_answer,omitempty"`
	PrevAnsweredAt time.Time `json:"prev_answered_at,omitempty"`
}

// AskHistoryRequest asks for the sort history of one ask.
type AskHistoryRequest struct {
	Ask string `json:"ask"`
}

// AskHistoryResult is the sort history, oldest first.
type AskHistoryResult struct {
	Sorts []AskSort `json:"sorts"`
}

// ChiefSetRequest names the chief session. An empty id clears the role.
type ChiefSetRequest struct {
	SessionID string `json:"session_id"`
}

// ChiefStatus is the registered chief and the model it is set to run on.
type ChiefStatus struct {
	SessionID string `json:"session_id"`
	Name      string `json:"name,omitempty"`
	Online    bool   `json:"online"`
	Model     string `json:"model"`
}
