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

// announcedDone tells whether the task a session announced reads as the answer
// "done", the same reading the idle check uses.
func announcedDone(task string) bool {
	kind, _ := parseCheckAnswer(task)
	return kind == "done"
}

// checkSessionStalls runs on the gateway's minute tick. A poll of a harness is
// not progress: only output, a turn boundary or a delivery acknowledgement is.
// One ask covers one activity time: it is not raised again for that time after
// the owner answers it or the chief closes it.
func (s *Store) checkSessionStalls() error {
	if err := s.retireObsoleteNotices(); err != nil {
		return err
	}
	snap, err := s.SettingsSnapshot()
	if err != nil {
		return err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	rows, err := tx.Query(`SELECT s.id,s.harness,s.status,s.online,s.worker,s.waiting,
 COALESCE(NULLIF(s.repo_project,''),p.project,''),s.harness_activity,COALESCE(p.task,''),
 COALESCE((SELECT MIN(m.created_at) FROM messages m WHERE m.recipient=s.id AND
 (m.state IN ('stored','dispatched') OR (m.state='failed' AND EXISTS(SELECT 1 FROM delivery_retries r WHERE r.message_id=m.id)))),0)
 FROM sessions s LEFT JOIN profiles p ON p.session_id=s.id
 WHERE s.harness IN ('claude','codex','opencode','pi')`)
	if err != nil {
		return err
	}
	type candidate struct {
		id, harness, status, worker, waiting, repo, task string
		online                                           int
		active, pending                                  int64
	}
	var sessions []candidate
	for rows.Next() {
		var c candidate
		if err := rows.Scan(&c.id, &c.harness, &c.status, &c.online, &c.worker, &c.waiting, &c.repo, &c.active, &c.task, &c.pending); err != nil {
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
		// An offline session, or one that is idle and done, has nothing to
		// stall. A busy session that stopped making progress still does.
		idleDone := c.status == "idle" || (c.status != "busy" && announcedDone(c.task))
		eligible := c.online != 0 && c.worker == "" && c.waiting == "" && !idleDone && (c.pending > 0 || c.status == "busy")
		baseline := c.active
		if c.status != "busy" && c.pending > baseline {
			baseline = c.pending
		}
		baselineSec := baseline / int64(time.Second)
		// The newest activity time an ask was raised for, in any state: open,
		// dismissed, answered or closed. It is not raised again for that time.
		var asked int64
		if err := tx.QueryRow(`SELECT COALESCE(MAX(CAST(conversation_id AS INTEGER)),0) FROM inbox_items WHERE session_id=? AND message_id=?`, c.id, sessionStallMarker).Scan(&asked); err != nil {
			return err
		}
		var id, saved string
		err := tx.QueryRow(`SELECT id,conversation_id FROM inbox_items WHERE session_id=? AND message_id=? AND status IN ('open','dismissed') ORDER BY created_at DESC LIMIT 1`, c.id, sessionStallMarker).Scan(&id, &saved)
		if err != nil && err != sql.ErrNoRows {
			return err
		}
		old, _ := strconv.ParseInt(saved, 10, 64)
		if id != "" && (!eligible || baselineSec > old) {
			if _, err := tx.Exec(`UPDATE inbox_items SET status='closed',answer='activity_or_end',answered_at=? WHERE id=?`, now.UnixNano(), id); err != nil {
				return err
			}
		}
		if !eligible || baseline <= 0 || baselineSec <= asked {
			continue
		}
		timeout := snap.Duration("sessions.stall_timeout", settings.Where{Repo: c.repo})
		if timeout <= 0 || now.Sub(time.Unix(0, baseline)) <= timeout {
			continue
		}
		_, err = s.createInboxItem(tx, protocol.InboxItem{Kind: "session_stall", SessionID: c.id, Node: c.repo, MessageID: sessionStallMarker,
			ConversationID: strconv.FormatInt(baselineSec, 10),
			Text:           fmt.Sprintf("%s session may be stalled. No harness activity has been observed since %s. Open Sessions and press Enter to look, or restart the session.", c.harness, time.Unix(0, baseline).UTC().Format(time.RFC3339))})
		if err != nil {
			return err
		}
	}
	return tx.Commit()
}
