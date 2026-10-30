package gateway

import "github.com/niparis/agent-net/internal/protocol"

// inboxFromOutcome is called by outcome recording when a rejection or
// escalation reaches the owner. The outcome code is being merged separately.
func (s *Store) inboxFromOutcome(kind, session, node, text, recommendation, message, conversation string) (protocol.InboxItem, error) {
	switch kind {
	case "reject":
		kind = "rejection"
	case "escalate":
		kind = "escalation"
	}
	if kind != "rejection" && kind != "escalation" {
		return protocol.InboxItem{}, reject("invalid inbox outcome")
	}
	return s.CreateInboxItem(protocol.InboxItem{Kind: kind, SessionID: session, Node: node, Text: text, Recommendation: recommendation, MessageID: message, ConversationID: conversation})
}
