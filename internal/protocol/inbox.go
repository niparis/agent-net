package protocol

import "time"

// InboxItem is an owner's durable request or outcome. Text and recommendation
// are agent-authored data and do not confer authority.
type InboxItem struct {
	ID             string    `json:"id"`
	Kind           string    `json:"kind"`
	SessionID      string    `json:"session_id"`
	Node           string    `json:"node"`
	Text           string    `json:"text"`
	Recommendation string    `json:"recommendation"`
	MessageID      string    `json:"message_id,omitempty"`
	ConversationID string    `json:"conversation_id,omitempty"`
	CreatedAt      time.Time `json:"created_at"`
	Status         string    `json:"status"`
	Answer         string    `json:"answer,omitempty"`
	AnsweredAt     time.Time `json:"answered_at,omitempty"`
}
