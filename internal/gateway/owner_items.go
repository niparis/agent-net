package gateway

import (
	"fmt"
	"strings"

	"github.com/niparis/agent-net/internal/protocol"
)

// Decision 0196: the owner has no Desk. An item that would wait for him (next
// who the owner, the chief or nobody yet) is never stored. An item an agent
// raised becomes one request to the chief of staff, who asks the owner in its
// own chat only what only he can decide. An item agent-net raised itself (its
// checks, stalls, brief checks, owed replies, loops, tree check, follow-ups and
// due items) is one log line. An item for another session is stored as before.

// ownerItemKeyPrefix starts the idempotency key of a request that carries an
// owner item to the chief. flushHolderAsks delivers these messages.
const ownerItemKeyPrefix = "owner_item:"

// Item states that say where an owner item went instead of the Desk.
const (
	ownerItemSentToChief = "sent_to_chief"
	ownerItemLogged      = "logged"
)

// keepDeskItems makes new stores keep owner items on the Desk as before
// decision 0196. Only tests set it, in TestMain: they test the Desk code that
// stays until a later part deletes it. A store copies it when it
// opens, so a test of the new behaviour clears it on its own store.
var keepDeskItems = false

// KeepDeskItemsForTests sets keepDeskItems for the stores opened after it.
// Only the TestMain of a package whose tests run the Desk code through a real
// gateway calls it (the daemon's). Production never does.
func KeepDeskItemsForTests() { keepDeskItems = true }

// ownerItemToChiefOrLog tells whether createInboxItem sends the item to the
// chief or the log instead of storing it.
func (s *Store) ownerItemToChiefOrLog(item protocol.InboxItem) bool {
	return ownerBound(item) && !s.keepDeskItems
}

// ownerBound tells whether an item would wait for the owner. A Connected apps
// consent stays stored: the owner started it on his own device, and his
// answer is what approves the app.
func ownerBound(item protocol.InboxItem) bool {
	switch item.NextWho {
	case "", protocol.AskNextOwner, protocol.AskNextChief:
	default:
		return false
	}
	return item.FromKind != protocol.AskFromApps
}

// raisedByGateway tells whether agent-net made the item itself. A sign-in is
// raised by agent-net but only the owner can do it, so it goes to the chief.
func raisedByGateway(item protocol.InboxItem) bool {
	if item.Kind == protocol.AskKindSignIn {
		return false
	}
	switch item.FromKind {
	case protocol.AskFromGateway, protocol.AskFromDisk:
		return true
	}
	return item.Kind == protocol.AskKindCheck || item.Kind == protocol.AskKindStall
}

// ownerItemToChief sends an owner item to the chief, or logs it. It stores no
// item. The message is stored in the caller's transaction and goes out on the
// next flushHolderAsks tick, so a rolled back item sends nothing.
func (s *Store) ownerItemToChief(db askDB, item protocol.InboxItem) (protocol.InboxItem, error) {
	if strings.TrimSpace(item.SessionID) == "" || strings.TrimSpace(item.Text) == "" {
		return item, reject("inbox session and text are required")
	}
	item.ID, item.Status = "", ownerItemLogged
	if raisedByGateway(item) {
		s.logOwnerItem("desk.dropped", item, "text", askFirstLine(item.Text, 160))
		return item, nil
	}
	chief, err := chiefSession(db)
	if err != nil {
		return item, err
	}
	if chief == "" || chief == item.SessionID {
		// The chief's own asks are for the owner in the chief's chat.
		s.logOwnerItem("desk.no_chief", item)
		return item, nil
	}
	now := s.now().UTC()
	from, typ := item.SessionID, protocol.TypeRequest
	var known int
	if err := db.QueryRow(`SELECT COUNT(*) FROM sessions WHERE id=?`, item.SessionID).Scan(&known); err != nil {
		return item, err
	}
	if known == 0 {
		// A machine or agent-net itself asked: nobody can read a reply.
		from, typ = agentNetSender, protocol.TypeNotification
	}
	m := protocol.Message{ID: protocol.NewID("m"), ConversationID: protocol.NewID("c"), From: from, To: chief, Type: typ,
		Body: ownerItemBody(db, item, typ == protocol.TypeRequest), IdempotencyKey: ownerItemKeyPrefix + protocol.NewID("k"),
		CreatedAt: now, ExpiresAt: now.Add(protocol.DefaultTTL), State: protocol.StateStored}
	if _, err := db.Exec(`INSERT INTO messages(id,conversation_id,parent_id,sender,recipient,type,body,hop,idem_key,created_at,expires_at,state)
		VALUES(?,?,?,?,?,?,?,0,?,?,?,?)`, m.ID, m.ConversationID, m.ParentID, m.From, m.To, m.Type, m.Body,
		m.IdempotencyKey, m.CreatedAt.UnixNano(), m.ExpiresAt.UnixNano(), m.State); err != nil {
		return item, err
	}
	s.logOwnerItem("desk.to_chief", item, "message", m.ID)
	item.Status, item.NextWho, item.MessageID, item.CreatedAt = ownerItemSentToChief, chief, m.ID, now
	return item, nil
}

// ownerItemBody is the request the chief reads: who asks, what, why only the
// owner can decide, and the asker's recommendation.
func ownerItemBody(q askDB, item protocol.InboxItem, canReply bool) string {
	asker := sessionLabel(q, item.SessionID)
	if name := strings.TrimSpace(item.AskerName); name != "" && name != asker {
		asker = name + " (" + asker + ")"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%s needs the owner (%s):\n%s\n", asker, item.Kind, strings.TrimSpace(item.Text))
	if why := strings.TrimSpace(item.WhyYou); why != "" {
		fmt.Fprintf(&b, "Why only the owner: %s\n", why)
	}
	if rec := strings.TrimSpace(item.Recommendation); rec != "" {
		fmt.Fprintf(&b, "Recommendation: %s\n", rec)
	}
	if len(item.Options) > 0 {
		fmt.Fprintf(&b, "Options: %s\n", strings.Join(item.Options, " / "))
	}
	if d := strings.TrimSpace(item.Detail); d != "" && d != strings.TrimSpace(item.Text) {
		fmt.Fprintf(&b, "Detail: %s\n", d)
	}
	if canReply {
		b.WriteString("Answer what you can. Ask the owner in your chat only what only he can decide, then reply to this message with his answer.")
	} else {
		b.WriteString("agent-net sent this as a notice. Ask the owner in your chat only what only he can decide.")
	}
	body := b.String()
	if len(body) > protocol.MaxBodyBytes {
		body = strings.ToValidUTF8(body[:protocol.MaxBodyBytes], "")
	}
	return body
}

// logOwnerItem writes the log line of an owner item. It never logs the text
// of an agent's ask.
func (s *Store) logOwnerItem(event string, item protocol.InboxItem, kv ...any) {
	if s.logf == nil {
		return
	}
	args := []any{"kind", item.Kind, "from_kind", item.FromKind, "session", item.SessionID}
	if item.SourceRef != "" {
		args = append(args, "ref", item.SourceRef)
	}
	s.logf(event, append(args, kv...)...)
}
