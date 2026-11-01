package daemon

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/niparis/agent-net/internal/protocol"
)

func (d *Daemon) ctoSessionID() string {
	var id string
	_ = d.st.db.QueryRow(`SELECT session_id FROM cto_network WHERE id=1`).Scan(&id)
	return id
}

// Called when the owner starts a CTO turn, never from the CTO tool path.
// The gateway, not a native conversation or the owner's chosen title, owns ID.
func (d *Daemon) registerCTOSession(ctx context.Context) error {
	raw, err := d.ctoAttributedCall(ctx, "cto.session.register", map[string]string{"owner": d.ownerSessionID()})
	if err != nil {
		return err
	}
	var result struct {
		SessionID string `json:"session_id"`
	}
	if json.Unmarshal(raw, &result) != nil || result.SessionID == "" {
		return errors.New("gateway did not register the CTO network session")
	}
	_, err = d.st.db.Exec(`INSERT INTO cto_network(id,session_id) VALUES(1,?) ON CONFLICT(id) DO UPDATE SET session_id=excluded.session_id`, result.SessionID)
	return err
}

// Build a plain peer message. Owner attribution is never read from arguments.
func (d *Daemon) ctoMessage(ctx context.Context, action string, raw json.RawMessage) (protocol.MsgSend, error) {
	var q struct {
		To        string `json:"to"`
		Body      string `json:"body"`
		MessageID string `json:"message_id"`
	}
	var out protocol.MsgSend
	if json.Unmarshal(raw, &q) != nil || q.Body == "" {
		return out, errors.New("message needs body")
	}
	out = protocol.MsgSend{Sender: d.ctoSessionID(), Body: q.Body, Type: protocol.TypeRequest}
	if out.Sender == "" {
		return out, errors.New("CTO network session is not registered; start the CTO from the owner interface")
	}
	switch action {
	case "send":
		if q.To == "" {
			return out, errors.New("send needs to and body")
		}
		to, err := d.resolveTo(ctx, q.To)
		out.Recipient = to
		return out, err
	case "reply":
		if q.MessageID == "" {
			return out, errors.New("reply needs message_id and body")
		}
		data, err := d.call(ctx, "query.message", protocol.QueryMessage{MessageID: q.MessageID})
		if err != nil {
			return out, err
		}
		var parent protocol.MessageResult
		if err := json.Unmarshal(data, &parent); err != nil {
			return out, err
		}
		if parent.Message.To != out.Sender {
			return out, errors.New("message was not sent to the CTO")
		}
		out.Recipient, out.ParentID, out.Type = parent.Message.From, q.MessageID, protocol.TypeReply
		return out, nil
	}
	return out, errors.New("message needs action send or reply")
}
