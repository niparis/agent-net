package gateway

import (
	"fmt"
	"html"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

const reminderGrace = 2 * time.Minute

// The request is still public, open and contactable. Use the same specific
// edge before wildcard ordering as RequestRule. A held request is not work B
// has received. Never send into an owner or CTO conversation. Public asks
// from the CTO may still remind the peer that owes an answer.
const remindableRequest = ` FROM messages q
 JOIN sessions src ON src.id=q.sender JOIN sessions dst ON dst.id=q.recipient
 LEFT JOIN profiles sp ON sp.session_id=src.id LEFT JOIN profiles dp ON dp.session_id=dst.id
 WHERE q.type='request' AND q.state IN ('stored','dispatched','delivered')
 AND q.created_at>=? AND q.created_at<? AND q.expires_at>?
 AND dst.status='idle' AND dst.last_active>0 AND dst.last_active<?
 AND src.harness<>'owner' AND dst.harness NOT IN ('owner','cto')
 AND NOT EXISTS (SELECT 1 FROM owner_settings c WHERE c.key='cto_session' AND c.value=q.recipient)
 AND NOT EXISTS (SELECT 1 FROM messages r WHERE r.conversation_id=q.conversation_id AND r.type='reply' AND r.created_at>=q.created_at)
 AND COALESCE((SELECT e.rule FROM graph_edges e
 WHERE e.target=COALESCE(NULLIF(dst.repo_project,''),dp.project,'')
 AND e.target<>'' AND (e.source=COALESCE(NULLIF(src.repo_project,''),sp.project,'') OR e.source='*')
 ORDER BY CASE WHEN e.source='*' THEN 1 ELSE 0 END,e.kind LIMIT 1),'normal') NOT IN ('never','ask')`

func (s *Store) reminderArgs() []any {
	now := s.now()
	return []any{now.Add(-24 * time.Hour).UnixNano(), now.Add(-reminderGrace).UnixNano(), now.UnixNano(), now.Add(-reminderGrace).UnixNano()}
}

// reminderEligible is also checked at dispatch, since a queued notification
// must not wake a busy session, cross a changed edge, or outlive its request.
func (s *Store) reminderEligible(id string) (bool, error) {
	var n int
	err := s.db.QueryRow("SELECT COUNT(*)"+remindableRequest+" AND q.id=? AND dst.online=1", append(s.reminderArgs(), id)...).Scan(&n)
	return n == 1, err
}

// remindPending stores the notification and the sole durable flag atomically.
// The normal message queue supplies delivery and its existing audit history.
func (s *Store) remindPending() ([]protocol.Message, error) {
	s.reminderMu.Lock()
	defer s.reminderMu.Unlock()
	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	rows, err := tx.Query(`SELECT q.id,q.conversation_id,q.recipient,q.created_at,dst.online,
 COALESCE(NULLIF(sp.name,''),NULLIF(src.name,''),q.sender)`+remindableRequest+" AND q.reminded=0", s.reminderArgs()...)
	if err != nil {
		return nil, err
	}
	type candidate struct {
		id, conv, to, name string
		at                 int64
		online             bool
	}
	var candidates []candidate
	for rows.Next() {
		var c candidate
		if err = rows.Scan(&c.id, &c.conv, &c.to, &c.at, &c.online, &c.name); err != nil {
			rows.Close()
			return nil, err
		}
		candidates = append(candidates, c)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	var out []protocol.Message
	for _, c := range candidates {
		if !c.online {
			if s.reminderOffline == nil {
				s.reminderOffline = map[string]bool{}
			}
			s.reminderOffline[c.id] = true
			continue
		}
		now := s.now()
		m := protocol.Message{ID: protocol.NewID("m"), ConversationID: c.conv, ParentID: c.id,
			From: "agent-net", FromName: "agent-net", FromHarness: "agent-net", To: c.to, Type: protocol.TypeNotification,
			Body:           fmt.Sprintf("[agent-net] reminder: %s is waiting for your answer to %s (sent %s ago). Answer with agent_reply, or say you cannot.", protocol.NeutralizeMarkers(html.EscapeString(protocol.CleanWaitingText(c.name))), c.id, now.Sub(time.Unix(0, c.at)).Truncate(time.Second)),
			IdempotencyKey: "reminder:" + c.id, CreatedAt: now.UTC(), ExpiresAt: now.Add(protocol.DefaultTTL).UTC(), State: protocol.StateStored}
		if _, err = tx.Exec(`INSERT INTO messages(id,conversation_id,parent_id,sender,recipient,type,body,hop,idem_key,created_at,expires_at,state)
   VALUES(?,?,?,?,?,?,?,0,?,?,?,?)`, m.ID, m.ConversationID, m.ParentID, m.From, m.To, m.Type, m.Body, m.IdempotencyKey, m.CreatedAt.UnixNano(), m.ExpiresAt.UnixNano(), m.State); err != nil {
			return nil, err
		}
		if _, err = tx.Exec("UPDATE messages SET reminded=1 WHERE id=?", c.id); err != nil {
			return nil, err
		}
		s.event(tx, "msg.stored", "", m.From, m.ID, "type=notification by=agent-net reminder")
		out = append(out, m)
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	for _, m := range out {
		s.logMessage(m)
	}
	return out, nil
}

func (s *Store) reminderCounts(c *protocol.SessionCounts) error {
	s.reminderMu.Lock()
	c.PendingRemindersOffline = len(s.reminderOffline)
	s.reminderMu.Unlock()
	return s.db.QueryRow(`SELECT COUNT(*),COALESCE(SUM(EXISTS(
 SELECT 1 FROM messages r WHERE r.conversation_id=q.conversation_id AND r.type='reply'
 AND r.sender=q.recipient AND r.recipient=q.sender AND r.created_at>=n.created_at AND r.created_at<=n.created_at+?
 )),0) FROM messages n JOIN messages q ON q.id=n.parent_id WHERE n.sender='agent-net'`, int64(30*time.Minute)).Scan(&c.PendingReminders, &c.PendingAnsweredAfterReminder)
}
