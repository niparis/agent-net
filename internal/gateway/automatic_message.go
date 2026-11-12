package gateway

import (
	"github.com/niparis/agent-net/internal/protocol"
)

// storeAutomaticMessage is for gateway-generated text only. Public msg.send
// still rejects the reserved sender. Real owner text goes through StoreMessage.
func (s *Store) storeAutomaticMessage(p protocol.MsgSend) (protocol.Message, bool, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return protocol.Message{}, false, err
	}
	defer tx.Rollback()
	if p.IdempotencyKey == "" {
		p.IdempotencyKey = protocol.NewID("k")
	}
	if m, ok, err := s.message(tx, "m.sender='agent-net' AND m.idem_key=?", p.IdempotencyKey); err != nil || ok {
		return m, false, err
	}
	live, exists, err := liveSession(tx, p.Recipient)
	if err != nil {
		return protocol.Message{}, false, err
	}
	if !exists {
		return protocol.Message{}, false, reject("unknown recipient session %q", p.Recipient)
	}
	p.Recipient = live
	if p.WorkerTask {
		var stopped bool
		if err := tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM sessions WHERE id=? AND worker<>'' AND (online=0 OR worker_run_active=0))`, p.Recipient).Scan(&stopped); err != nil {
			return protocol.Message{}, false, err
		}
		if stopped {
			return protocol.Message{}, false, reject("worker %s is offline or has ended; its task cannot be sent", p.Recipient)
		}
	}
	now := s.now().UTC()
	m := protocol.Message{ID: protocol.NewID("m"), ConversationID: protocol.NewID("c"), From: "agent-net", FromName: "agent-net", FromHarness: "agent-net", To: p.Recipient, Type: p.Type, Body: p.Body, IdempotencyKey: p.IdempotencyKey, CreatedAt: now, ExpiresAt: now.Add(protocol.DefaultTTL), State: protocol.StateStored}
	if m.Type == "" {
		m.Type = protocol.TypeNotification
	}
	if p.WorkerTask {
		task := true
		m.WorkerTask = &task
	}
	if _, err = tx.Exec(`INSERT INTO messages(id,conversation_id,parent_id,sender,recipient,type,body,hop,idem_key,created_at,expires_at,state,worker_task) VALUES(?,?,?,?,?,?,?,0,?,?,?,?,?)`, m.ID, m.ConversationID, "", m.From, m.To, m.Type, m.Body, m.IdempotencyKey, m.CreatedAt.UnixNano(), m.ExpiresAt.UnixNano(), m.State, m.WorkerTask); err != nil {
		return protocol.Message{}, false, err
	}
	s.event(tx, "msg.stored", "", m.From, m.ID, "by=agent-net")
	if err = tx.Commit(); err != nil {
		return protocol.Message{}, false, err
	}
	s.logMessage(m)
	return m, true, nil
}
