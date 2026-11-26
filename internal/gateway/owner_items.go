package gateway

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
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
	// ownerItemAlreadySent is an item the chief already has from the same
	// source with the same text: nothing new was sent.
	ownerItemAlreadySent = "already_sent"
	ownerItemLogged      = "logged"
)

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
	key := ownerItemKey(item)
	var sent string
	err = db.QueryRow(`SELECT id FROM messages WHERE sender=? AND idem_key=?`, from, key).Scan(&sent)
	if err == nil {
		// The minute reconcile raises the same held or waiting item again
		// on every tick: the chief already has it.
		item.Status, item.NextWho, item.MessageID = ownerItemAlreadySent, chief, sent
		return item, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return item, err
	}
	m := protocol.Message{ID: protocol.NewID("m"), ConversationID: protocol.NewID("c"), From: from, To: chief, Type: typ,
		Body: ownerItemBody(db, item, typ == protocol.TypeRequest), IdempotencyKey: key,
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

// ownerItemKey is the idempotency key of the request an owner item sends. An
// item with a source is sent once for each text it has from that source, so a
// held message is sent once and a session's wait once for each thing it waits
// on. An item without a source is sent each time.
func ownerItemKey(item protocol.InboxItem) string {
	if item.SourceRef == "" {
		return ownerItemKeyPrefix + protocol.NewID("k")
	}
	sum := sha256.Sum256([]byte(protocol.NormalizeAskText(item.Text)))
	lo, _ := ownerItemKeyRange(item.SourceRef)
	return lo + hex.EncodeToString(sum[:6])
}

// ownerItemKeyRange is the range of the keys ownerItemKey gives the items of
// one source, whatever their text: lo is their common start, and hi the first
// key after them, so a lookup uses the (sender, idem_key) index.
func ownerItemKeyRange(sourceRef string) (lo, hi string) {
	base := ownerItemKeyPrefix + sourceRef
	return base + ":", base + ";"
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
	// A held message and an @owner step have nobody to answer them but the
	// CTO, so the request ends with how. The end is kept when a long body is
	// cut.
	var end string
	if item.Kind == protocol.AskKindHeld && item.MessageID != "" {
		end = fmt.Sprintf("\nIf the owner agrees, the CTO answers it with held_answer, message_id %s, action a to deliver or r to refuse.", item.MessageID)
	}
	if item.AboutKind == protocol.AskAboutStep {
		if change, _, ref, commit, ok := parseStepAboutID(item.AboutID); ok {
			args := map[string]any{"change": change, "step": ref.Step, "commit": commit,
				"result": protocol.StepResultPass, "owner_words": "<quote the owner's words>"}
			if ref.For != "" {
				args["for"] = ref.For
			}
			if ref.AfterMerge {
				args["after_merge"] = true
			}
			end = "\nThe CTO completes it on the owner's words with " + ctoOpCallText(protocol.OpDeliveryOwnerAnswer, args)
		}
	}
	body := b.String()
	if limit := protocol.MaxBodyBytes - len(end); len(body) > limit {
		body = strings.ToValidUTF8(body[:limit], "")
	}
	return body + end
}

// ctoOpCallText is the exact CTO call a chief request or log line names:
// cto_op with the method and its arguments.
func ctoOpCallText(method string, args map[string]any) string {
	raw, _ := json.Marshal(struct {
		Method string         `json:"method"`
		Args   map[string]any `json:"args"`
	}{method, args})
	return "cto_op " + string(raw)
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
