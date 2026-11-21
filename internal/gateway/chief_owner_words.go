package gateway

import (
	"database/sql"
	"errors"

	"github.com/niparis/agent-net/internal/protocol"
)

// ownerWordsCheck checks the message a close cites as the owner's words. The
// owner holds an ask the chief cannot settle on its own: he answered it in
// another chat, so the chief closes it on his word. The message must come from
// his own session, or from the CTO on his instructions. The gateway stores the
// CTO's owner_approved flag only from the registered CTO session, so the flag
// is trusted here. The message must also be sent after the ask opened. The
// body is never read: text in a body cannot make the owner's word.
func ownerWordsCheck(q askDB, ask, message string) error {
	var fromHarness string
	var ownerApproved, fromCTO int
	var sent, opened int64
	err := q.QueryRow(`SELECT s.harness, m.owner_approved, m.sender_cto, m.created_at, i.created_at
		FROM messages m JOIN sessions s ON s.id=m.sender JOIN inbox_items i ON i.id=?
		WHERE m.id=?`, ask, message).Scan(&fromHarness, &ownerApproved, &fromCTO, &sent, &opened)
	if errors.Is(err, sql.ErrNoRows) {
		return reject("unknown message %q: owner_words_message names a message from the owner or from the CTO on his instructions", message)
	}
	if err != nil {
		return err
	}
	ownOwner := fromHarness == protocol.OwnerHarness
	// "cto" is the harness of the CTO session, as the gateway names it elsewhere.
	onInstruction := fromHarness == "cto" && fromCTO != 0 && ownerApproved != 0
	if !ownOwner && !onInstruction {
		return reject("message %s is not the owner's words: it must come from his session, or from the CTO on his instructions", message)
	}
	if sent <= opened {
		return reject("message %s was sent before ask %s was opened, so it was not the owner's answer to it", message, ask)
	}
	return nil
}

// closeAnswer is the answer an ask keeps when a sort closes it. A close that
// rests on the owner's words names the message that carries them.
func closeAnswer(sorter, reason, ownerMessage string) string {
	if ownerMessage == "" {
		return sorter + ": " + reason
	}
	return sorter + ": " + reason + " (on the owner's words in " + ownerMessage + ")"
}
