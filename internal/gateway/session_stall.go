package gateway

import (
	"database/sql"
	"fmt"
	"strconv"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
	"github.com/niparis/agent-net/internal/settings"
)

const sessionStallMarker = "session-stall"

func busyInitialActivity(status string, now int64) int64 {
	if status == "busy" {
		return now
	}
	return 0
}

// checkSessionStalls runs on the gateway's minute tick. A poll of a harness is
// not progress: only output, a turn boundary or a delivery acknowledgement is.
func (s *Store) checkSessionStalls() error {
	snap, err := s.SettingsSnapshot()
	if err != nil {
		return err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	rows, err := tx.Query(`SELECT s.id,s.harness,s.status,s.online,s.harness_lost,s.worker,s.waiting,
 COALESCE(NULLIF(s.repo_project,''),p.project,''),s.harness_activity,
 COALESCE((SELECT MIN(m.created_at) FROM messages m WHERE m.recipient=s.id AND
 (m.state IN ('stored','dispatched') OR (m.state='failed' AND EXISTS(SELECT 1 FROM delivery_retries r WHERE r.message_id=m.id)))),0)
 FROM sessions s LEFT JOIN profiles p ON p.session_id=s.id
 WHERE s.harness IN ('claude','codex','opencode','pi')`)
	if err != nil {
		return err
	}
	type candidate struct {
		id, harness, status, worker, waiting, repo string
		online, lost                               int
		active, pending                            int64
	}
	var sessions []candidate
	for rows.Next() {
		var c candidate
		if err := rows.Scan(&c.id, &c.harness, &c.status, &c.online, &c.lost, &c.worker, &c.waiting, &c.repo, &c.active, &c.pending); err != nil {
			rows.Close()
			return err
		}
		sessions = append(sessions, c)
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	now := s.now()
	for _, c := range sessions {
		var id, saved string
		err := tx.QueryRow(`SELECT id,conversation_id FROM inbox_items WHERE session_id=? AND message_id=? AND status IN ('open','dismissed') ORDER BY created_at DESC LIMIT 1`, c.id, sessionStallMarker).Scan(&id, &saved)
		if err != nil && err != sql.ErrNoRows {
			return err
		}
		baseline := c.active
		if c.status != "busy" && c.pending > baseline {
			baseline = c.pending
		}
		eligible := (c.online != 0 || c.lost != 0) && c.worker == "" && c.waiting == "" && (c.pending > 0 || c.status == "busy")
		old, _ := strconv.ParseInt(saved, 10, 64)
		if id != "" && (!eligible || c.active/int64(time.Second) > old) {
			if _, err := tx.Exec(`UPDATE inbox_items SET status='closed',answer='activity_or_end',answered_at=? WHERE id=?`, now.UnixNano(), id); err != nil {
				return err
			}
			id = ""
		}
		if !eligible || id != "" || baseline <= 0 {
			continue
		}
		timeout := snap.Duration("sessions.stall_timeout", settings.Where{Repo: c.repo})
		if timeout <= 0 || now.Sub(time.Unix(0, baseline)) <= timeout {
			continue
		}
		_, err = s.createInboxItem(tx, protocol.InboxItem{Kind: "session_stall", SessionID: c.id, Node: c.repo, MessageID: sessionStallMarker,
			ConversationID: strconv.FormatInt(baseline/int64(time.Second), 10),
			Text:           fmt.Sprintf("%s session may be stalled. No harness activity has been observed since %s. Open Sessions and press Enter to look, or restart the session.", c.harness, time.Unix(0, baseline).UTC().Format(time.RFC3339))})
		if err != nil {
			return err
		}
	}
	return tx.Commit()
}
