package protocol

import "time"

// MaxOwnerAskChars limits a question or approval text in Unicode characters.
const MaxOwnerAskChars = 4000

// OpenAskRef names one open ask of a session: its ID, kind and first line.
type OpenAskRef struct {
	ID   string `json:"id"`
	Kind string `json:"kind"`
	Text string `json:"text"`
}

// InboxItem is an owner's durable request or outcome. Text and recommendation
// are agent-authored data and do not confer authority.
type InboxItem struct {
	ID             string         `json:"id"`
	Kind           string         `json:"kind"`
	SessionID      string         `json:"session_id"`
	Node           string         `json:"node"`
	Text           string         `json:"text"`
	Options        []string       `json:"options,omitempty"`
	AskerName      string         `json:"asker_name,omitempty"`
	ChangeTitle    string         `json:"change_title,omitempty"`
	Recommendation string         `json:"recommendation"`
	MessageID      string         `json:"message_id,omitempty"`
	ConversationID string         `json:"conversation_id,omitempty"`
	CreatedAt      time.Time      `json:"created_at"`
	Status         string         `json:"status"`
	Answer         string         `json:"answer,omitempty"`
	AnsweredAt     time.Time      `json:"answered_at,omitempty"`
	Delivery       *InboxDelivery `json:"delivery,omitempty"`
	Replaces       string         `json:"replaces,omitempty"`
	// Ask fields (decision 0090). An empty NextWho means the owner.
	FromKind  string `json:"from_kind,omitempty"`
	SourceRef string `json:"source_ref,omitempty"`
	AboutKind string `json:"about_kind,omitempty"`
	AboutID   string `json:"about_id,omitempty"`
	NextWho   string `json:"next_who,omitempty"`
	WhyYou    string `json:"why_you,omitempty"`
	Detail    string `json:"detail,omitempty"`
	// Desk item shape (decision 0150). Owner is chief or cto, State one of the
	// Desk states, Category one of the item categories. DupOf names the open
	// item a duplicate joined.
	Owner    string `json:"owner,omitempty"`
	State    string `json:"state,omitempty"`
	Category string `json:"category,omitempty"`
	DupOf    string `json:"dup_of,omitempty"`
}

// InboxDelivery links a step task to its exact revision and commit.
type InboxDelivery struct {
	ChangeID string `json:"change_id"`
	StepRef
	Commit       string   `json:"commit"`
	ReportFields []string `json:"report_fields,omitempty"`
}
