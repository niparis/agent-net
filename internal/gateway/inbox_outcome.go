package gateway

import (
	"database/sql"
	"github.com/niparis/agent-net/internal/protocol"
)

// inboxFromOutcome records a rejection or escalation in the outcome transaction.
func (s *Store) inboxFromOutcome(tx *sql.Tx, kind, session, node, text, recommendation, message, conversation string) (protocol.InboxItem, error) {
	switch kind {
	case "reject":
		kind = "rejection"
	case "escalate":
		kind = "escalation"
	}
	if kind != "rejection" && kind != "escalation" {
		return protocol.InboxItem{}, reject("invalid inbox outcome")
	}
	return s.createInboxItem(tx, protocol.InboxItem{Kind: kind, SessionID: session, Node: node, Text: text, Recommendation: recommendation, MessageID: message, ConversationID: conversation})
}
