package gateway

import (
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// applyPending derives current waits from retained messages. Activity clears
// the sender's wait, but does not discharge the recipient's obligation to reply.
// Deriving this on read also clears stale waits retained by older versions.
func (s *Store) applyPending(agents []protocol.AgentView) error {
	rows, err := s.db.Query(`SELECT m.id, m.sender, m.recipient, m.created_at,
 COALESCE(NULLIF(sp.name,''), NULLIF(ss.name,''), m.sender), COALESCE(NULLIF(rp.name,''), NULLIF(rs.name,''), m.recipient),
 MAX(ss.last_active, ss.harness_activity) <= m.created_at
 AND NOT EXISTS (SELECT 1 FROM messages sent WHERE sent.sender=m.sender AND sent.rowid>m.rowid)
 FROM messages m JOIN sessions ss ON ss.id=m.sender LEFT JOIN profiles sp ON sp.session_id=m.sender
 JOIN sessions rs ON rs.id=m.recipient LEFT JOIN profiles rp ON rp.session_id=m.recipient
 WHERE m.type='request' AND m.created_at >= ? AND m.expires_at > ?
 AND ss.status<>'ended' AND ss.archived_at=0 AND rs.status<>'ended' AND rs.archived_at=0
 AND NOT EXISTS (SELECT 1 FROM messages reply WHERE reply.type='reply' AND reply.rowid>m.rowid
 AND (reply.conversation_id=m.conversation_id OR (reply.sender=m.recipient AND reply.recipient=m.sender)))
 ORDER BY m.created_at, m.id`, s.now().Add(-24*time.Hour).UnixNano(), s.now().UnixNano())
	if err != nil {
		return err
	}
	defer rows.Close()
	byID := make(map[string]int, len(agents))
	for i := range agents {
		byID[agents[i].SessionID] = i
	}
	for rows.Next() {
		var id, from, to, fromName, toName string
		var at int64
		var waiting bool
		if err := rows.Scan(&id, &from, &to, &at, &fromName, &toName, &waiting); err != nil {
			return err
		}
		if i, ok := byID[from]; ok && waiting {
			agents[i].Pending = append(agents[i].Pending, protocol.PendingRequest{MessageID: id, PeerID: to, PeerName: toName, Since: at / int64(time.Second), Outgoing: true})
		}
		if i, ok := byID[to]; ok {
			agents[i].Pending = append(agents[i].Pending, protocol.PendingRequest{MessageID: id, PeerID: from, PeerName: fromName, Since: at / int64(time.Second)})
		}
	}
	return rows.Err()
}

type pendingEpisode struct {
	since      time.Time
	lastActive int64
	counted    bool
}

// samplePendingStalls counts each observed idle period once. It deliberately
// starts the clock at first observation, since idle start is not stored.
func (s *Store) samplePendingStalls() error {
	agents, err := s.Agents(protocol.QueryAgents{OnlineOnly: true})
	if err != nil {
		return err
	}
	now := s.now()
	s.pendingMu.Lock()
	defer s.pendingMu.Unlock()
	if s.pendingEpisodes == nil {
		s.pendingEpisodes = make(map[string]pendingEpisode)
	}
	seen := make(map[string]bool)
	for _, a := range agents {
		if a.Status != "idle" || len(a.Pending) == 0 {
			continue
		}
		seen[a.SessionID] = true
		e, ok := s.pendingEpisodes[a.SessionID]
		if !ok || e.lastActive != a.LastActive {
			e = pendingEpisode{since: now, lastActive: a.LastActive}
		}
		if !e.counted && now.Sub(e.since) > 20*time.Minute {
			e.counted = true
			s.pendingStalls++
		}
		s.pendingEpisodes[a.SessionID] = e
	}
	for id := range s.pendingEpisodes {
		if !seen[id] {
			delete(s.pendingEpisodes, id)
		}
	}
	return nil
}
