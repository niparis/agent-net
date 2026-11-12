package protocol

import (
	"errors"
	"strconv"
	"strings"
	"time"
)

// The chief of staff (decision 0114) is a standard agent session. Asks that
// would go to the owner first are routed to it; it sorts them with
// agent_ask_sort and never answers for the owner.

// The four results of a sort.
const (
	AskSortClose = "close"
	AskSortMerge = "merge"
	AskSortSend  = "send"
	AskSortOwner = "owner"
	// AskSortList is the read-only result: list the asks that wait for the
	// chief, and change none of them.
	AskSortList = "list"
	// AskSortUndo is the result of an undo: a row in the sort history that puts
	// an ask back the way it was before the newest sort.
	AskSortUndo = "undo"
	// AskSortConfirm and AskSortPicker answer a held worker start (decision
	// 0151): confirm starts it with the caller's fields, picker without them.
	AskSortConfirm = "confirm"
	AskSortPicker  = "picker"
	// AskSortDone reports that the answer of an answered ask was carried out
	// (decision 0150): the ask moves to carried_out and its askers are told.
	AskSortDone = "done"
)

// ChiefWakeInterval is the wake interval of decision 0150. A wake counts as
// missed when the chief has not claimed or sorted the batch within it. No timer
// sends an ask to the owner while the chief is live and working. When the chief
// is down, an ask in security, outage or money goes to the owner after one
// missed wake and every other ask after two. It is a constant, not a setting.
const ChiefWakeInterval = 10 * time.Minute

// ChiefSilentAfter is how long the chief may sit on asks that were sent to it
// with no sort and no harness activity before agent-net treats it as
// unavailable. A model turn on a batch can take minutes, so a chief that is
// still working is never silent. It is a constant, not a setting.
const ChiefSilentAfter = 10 * time.Minute

// ChiefRetryAfter is how long a chief that was silent stays unavailable. After
// it, the next ask goes to the chief again as a probe, and a sort ends the
// wait at once.
const ChiefRetryAfter = 15 * time.Minute

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
// setting, the sort history of one ask, the sorts of the last day (the Desk's
// Handled for you list), and the owner's undo and marks of the Desk.
const (
	OpChiefGet   = "chief_get"
	OpChiefSet   = "chief_set"
	OpAskHistory = "ask_history"
	OpChiefSorts = "chief_sorts"
	OpAskUndo    = "ask_undo"
	OpAskMark    = "ask_mark"
	// OpAskDiscuss is the owner's "discuss" on a Desk item (decision 0150).
	OpAskDiscuss = "ask_discuss"
)

// AskDiscussRequest names the item the owner wants to discuss.
type AskDiscussRequest struct {
	Ask string `json:"ask"`
}

// AskDiscussResult says where the discussion is: Open is chief (Session is the
// chief's, told about the item and woken when offline) or cto (the owner's
// CTO chat; nothing is woken).
type AskDiscussResult struct {
	Open    string `json:"open"`
	Session string `json:"session,omitempty"`
	Woken   bool   `json:"woken,omitempty"`
}

// The two marks of the Desk (decision 0114, part 2). The owner puts one on an
// ask that reached him (didnt_need_me) or on a handled ask (should_have_asked).
// Each mark is stored as a correction the chief reads in its next batch.
const (
	MarkDidntNeedMe     = "didnt_need_me"
	MarkShouldHaveAsked = "should_have_asked"
)

// ChiefSortsWindow is how far back the Desk's Handled for you list reaches.
const ChiefSortsWindow = 24 * time.Hour

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
// the owner's line for owner. Result list is read-only and needs nothing else,
// but it may name Sorter and narrow the counts to a time range (Since, Until
// in Unix seconds) or an id range (FromID, ToID). Detail "full" reads one ask
// instead of sorting it, and then only Ask is required.
type AskSortArgs struct {
	Mode        string `json:"mode,omitempty"`
	Kind        string `json:"kind,omitempty"`
	HideResult  string `json:"hide_result,omitempty"`
	Before      string `json:"before,omitempty"`
	RepeatsOnly bool   `json:"repeats_only,omitempty"`
	Token       string `json:"token,omitempty"`
	Receipt     string `json:"receipt,omitempty"`
	Ask         string `json:"ask"`
	Result      string `json:"result"`
	Into        string `json:"into,omitempty"`
	To          string `json:"to,omitempty"`
	Reason      string `json:"reason"`
	Priority    int    `json:"priority,omitempty"`
	WhyYou      string `json:"why_you,omitempty"`
	Detail      string `json:"detail,omitempty"`
	Sorter      string `json:"sorter,omitempty"`
	Since       int64  `json:"since,omitempty"`
	Until       int64  `json:"until,omitempty"`
	FromID      int64  `json:"from_id,omitempty"`
	ToID        int64  `json:"to_id,omitempty"`
	// Options and Recommendation make an ask a decision when the chief sends
	// it to the owner: an owner-bound ask needs options (decision 0150).
	Options        []string `json:"options,omitempty"`
	Recommendation string   `json:"recommendation,omitempty"`
	// Category corrects the ask's category on any sort.
	Category string `json:"category,omitempty"`
	// Rule and Action close an ask by a standing rule: the rule's area and
	// action must match the ask exactly, and the close cites the rule id.
	Rule   string `json:"rule,omitempty"`
	Action string `json:"action,omitempty"`
}

// Validate checks a sort before the gateway looks at the ask. A full read is
// one ask id and nothing else. A list may choose its sorter and ranges.
func (a AskSortArgs) Validate() error {
	if a.Result == "hide" {
		if a.Mode != "preview" && a.Mode != "apply" && a.Mode != "undo" {
			return errors.New("hide mode must be preview, apply or undo")
		}
		if a.Mode == "undo" && a.Receipt == "" {
			return errors.New("undo needs receipt")
		}
		if a.Mode == "apply" && a.Token == "" {
			return errors.New("apply needs token")
		}
		return nil
	}
	if a.Result == AskSortList {
		if a.Since < 0 || a.Until < 0 {
			return errors.New("since and until are Unix seconds and cannot be negative")
		}
		if a.Since != 0 && a.Until != 0 && a.Since > a.Until {
			return errors.New("since must not be after until")
		}
		if a.FromID < 0 || a.ToID < 0 {
			return errors.New("from_id and to_id cannot be negative")
		}
		if a.FromID != 0 && a.ToID != 0 && a.FromID > a.ToID {
			return errors.New("from_id must not be after to_id")
		}
		return nil
	}
	if strings.TrimSpace(a.Ask) == "" {
		return errors.New("ask is required")
	}
	if a.Detail != "" {
		if a.Detail != "full" {
			return errors.New(`detail must be "full"`)
		}
		if a.Result != "" || a.Into != "" || a.To != "" || a.Reason != "" || a.Priority != 0 || a.WhyYou != "" {
			return errors.New("detail full reads one ask: leave result and the sort fields empty")
		}
		return nil
	}
	if a.Result == AskSortUndo {
		return nil
	}
	if strings.TrimSpace(a.Reason) == "" {
		return errors.New("reason is required: say why in one line")
	}
	if len(a.Reason) > MaxChiefReasonChars || len(a.WhyYou) > MaxChiefReasonChars {
		return errors.New("reason and why_you are limited to 300 characters")
	}
	if a.Category != "" && !ValidItemCategory(a.Category) {
		return errors.New("category must be security, outage, money or normal")
	}
	if len(a.Options) > 4 {
		return errors.New("options: at most four answers")
	}
	if (a.Rule != "") != (a.Action != "") {
		return errors.New("rule and action go together: the rule id and the action the ask is about")
	}
	if a.Rule != "" && a.Result != AskSortClose {
		return errors.New("rule applies only to close")
	}
	switch a.Result {
	case AskSortClose, AskSortConfirm, AskSortPicker, AskSortDone:
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
		return errors.New("result must be close, merge, send, owner, confirm, picker, done, list or undo")
	}
	return nil
}

// AskSortRequest is the frame body: the caller's session and its arguments.
type AskSortRequest struct {
	SessionID string `json:"session_id"`
	AskSortArgs
}

// AskSortResult says where the ask is after the sort. A list result puts the
// asks that wait for the chief in Asks and the counts of one sorter's sorts in
// Stats, and changes nothing. A full read has Result "read" and fills Full.
type AskSortResult struct {
	HiddenIDs     *[]int64      `json:"hidden_ids,omitempty"`
	VisibleBefore int           `json:"visible_before"`
	VisibleAfter  int           `json:"visible_after"`
	Token         string        `json:"token,omitempty"`
	Receipt       string        `json:"receipt,omitempty"`
	Ask           string        `json:"ask"`
	Result        string        `json:"result"`
	Next          string        `json:"next"`
	State         string        `json:"state"`
	Asks          []AskWaiting  `json:"asks,omitempty"`
	Stats         *AskSortStats `json:"stats,omitempty"`
	Full          *AskFull      `json:"full,omitempty"`
}

// AskSortStats counts the successful sorts of one sorter by result. FirstID and
// LastID are the smallest and largest sort id that matched, and are zero when
// none did. The counts hold only the results that occurred.
type AskSortStats struct {
	Hidden  int            `json:"hidden_count"`
	Sorter  string         `json:"sorter"`
	Counts  map[string]int `json:"counts"`
	FirstID int64          `json:"first_id"`
	LastID  int64          `json:"last_id"`
}

// AskWaiting is one ask that waits for the chief in a list. Age is short and
// human: 45s, 12m, 3h05m or 2d04h.
type AskWaiting struct {
	Ask            string `json:"ask"`
	Kind           string `json:"kind"`
	From           string `json:"from"`
	Text           string `json:"text"`
	Recommendation string `json:"recommendation"`
	Age            string `json:"age"`
}

// AskFull is one ask read in full: the whole question, the recommendation and
// options, the source, the route and sort history, the owner's answer and the
// exact payload of a charter draft or a learned proposal. Secret values are
// masked; a token's scope and destination are kept.
type AskFull struct {
	ID             string              `json:"id"`
	Kind           string              `json:"kind"`
	Status         string              `json:"status"`
	State          string              `json:"state"`
	Question       string              `json:"question"`
	Detail         string              `json:"detail,omitempty"`
	Options        []string            `json:"options,omitempty"`
	Recommendation string              `json:"recommendation,omitempty"`
	FromSession    string              `json:"from_session"`
	FromName       string              `json:"from_name,omitempty"`
	FromKind       string              `json:"from_kind,omitempty"`
	Node           string              `json:"node,omitempty"`
	AboutKind      string              `json:"about_kind,omitempty"`
	AboutID        string              `json:"about_id,omitempty"`
	AboutTitle     string              `json:"about_title,omitempty"`
	Next           string              `json:"next"`
	NextName       string              `json:"next_name,omitempty"`
	WhyYou         string              `json:"why_you,omitempty"`
	Priority       int                 `json:"priority,omitempty"`
	CreatedAt      time.Time           `json:"created_at"`
	Answer         string              `json:"answer,omitempty"`
	AnsweredAt     time.Time           `json:"answered_at,omitempty"`
	SourceRef      string              `json:"source_ref,omitempty"`
	Routing        []AskSort           `json:"routing,omitempty"`
	Charter        []CharterDraftField `json:"charter,omitempty"`
	Learned        *LearnedSuggestion  `json:"learned,omitempty"`
}

// CharterDraftField is one proposed charter value in a draft-charter ask.
type CharterDraftField struct {
	ID        int64     `json:"id"`
	Level     string    `json:"level"`
	Scope     string    `json:"scope"`
	Attr      string    `json:"attr"`
	Value     string    `json:"value"`
	Source    string    `json:"source,omitempty"`
	Status    string    `json:"status"`
	DraftedBy string    `json:"drafted_by,omitempty"`
	CreatedAt time.Time `json:"created_at"`
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

// ChiefSetRequest changes the chief of staff. It does what it names and
// nothing else: SessionID names the chief session, Clear unassigns it, and
// Harness, Provider, Model and Effort set the chief.model setting. A request
// that names none of them is refused, so a call can never unassign the chief
// by accident. Model fields not given keep their value from the current
// setting. The fields may come in one call or in separate calls.
type ChiefSetRequest struct {
	SessionID string `json:"session_id,omitempty" jsonschema:"the live agent session to make the chief"`
	Clear     bool   `json:"clear,omitempty" jsonschema:"true unassigns the chief; it cannot be combined with other fields"`
	Harness   string `json:"harness,omitempty" jsonschema:"harness of the chief model, for example claude"`
	Provider  string `json:"provider,omitempty" jsonschema:"provider of the chief model; derived from the model when empty"`
	Model     string `json:"model,omitempty" jsonschema:"chief model, resolved like a worker model; a model no machine lists is refused"`
	Effort    string `json:"effort,omitempty" jsonschema:"effort of the chief model, for example medium"`
}

// HasModel reports whether the request changes the chief's model.
func (r ChiefSetRequest) HasModel() bool {
	return r.Harness != "" || r.Provider != "" || r.Model != "" || r.Effort != ""
}

// Validate refuses a request that names nothing to do, or that asks to clear
// the chief and set something in the same call.
func (r ChiefSetRequest) Validate() error {
	switch {
	case r.Clear && (r.SessionID != "" || r.HasModel()):
		return errors.New("clear cannot be combined with session_id or model fields")
	case !r.Clear && r.SessionID == "" && !r.HasModel():
		return errors.New("chief_set needs session_id, clear: true, or model fields (harness, provider, model, effort)")
	}
	return nil
}

// ChiefStatus is the registered chief and the model it is set to run on.
type ChiefStatus struct {
	SessionID string `json:"session_id"`
	Name      string `json:"name,omitempty"`
	Online    bool   `json:"online"`
	Model     string `json:"model"`
	// Down is why the chief cannot work now, in a few words, or "" when it
	// can (or when there is no chief). The Desk shows it as
	// "chief of staff offline: <Down>".
	Down string `json:"down,omitempty"`
}

// ChiefOfflineLine is the one line the Desk shows when the chief cannot work.
func ChiefOfflineLine(down string) string {
	return "chief of staff offline: " + down
}

// ChiefQuotaDown says why the chief's provider has no quota, or "" when the
// reading shows some left (or none is usable). harness and model are the
// chief session's. It reads the same quota data the picker uses.
func ChiefQuotaDown(q *Quotas, harness, model string) string {
	providers := []string{QuotaProviderForStart(harness, model)}
	if p := ProfileProvider(harness, model); p != "" && p != providers[0] {
		providers = append(providers, p)
	}
	for _, p := range providers {
		if p == "" {
			continue
		}
		w, ok := q.ExhaustedWindowFor(p, model)
		if !ok {
			continue
		}
		out := p + " quota exhausted (" + QuotaWindowName(w) + " window"
		if w.ResetsAt > 0 {
			out += ", until " + time.Unix(w.ResetsAt, 0).UTC().Format("2006-01-02 15:04 UTC")
		}
		return out + ")"
	}
	return ""
}

// ChiefSilentText says how long the chief was silent, for the Desk line.
func ChiefSilentText(d time.Duration) string {
	if d >= time.Minute && d%time.Minute == 0 {
		n := int(d / time.Minute)
		if n == 1 {
			return "no sorts for 1 minute"
		}
		return "no sorts for " + strconv.Itoa(n) + " minutes"
	}
	return "no sorts for " + strconv.Itoa(int(d/time.Second)) + " seconds"
}

// AskUndoRequest names the ask whose newest sort the owner undoes.
type AskUndoRequest struct {
	Ask string `json:"ask"`
}

// AskMarkRequest is one mark on an ask. Why is optional, one line.
type AskMarkRequest struct {
	Ask  string `json:"ask"`
	Mark string `json:"mark"`
	Why  string `json:"why,omitempty"`
}

// Validate checks a mark before the gateway looks at the ask.
func (m AskMarkRequest) Validate() error {
	if strings.TrimSpace(m.Ask) == "" {
		return errors.New("ask is required")
	}
	if m.Mark != MarkDidntNeedMe && m.Mark != MarkShouldHaveAsked {
		return errors.New("mark must be didnt_need_me or should_have_asked")
	}
	if len(m.Why) > MaxChiefReasonChars {
		return errors.New("why is limited to 300 characters")
	}
	return nil
}

// ChiefSort is one sort of the chief in the Desk's Handled for you list. Undone
// says that the owner put it back since.
type ChiefSort struct {
	AskSort
	Question string `json:"question"`
	Undone   bool   `json:"undone"`
}

// ChiefSortsResult is the chief's sorts of the last day, newest first.
type ChiefSortsResult struct {
	Sorts []ChiefSort `json:"sorts"`
}
