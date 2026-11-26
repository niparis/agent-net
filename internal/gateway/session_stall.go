package gateway

import (
	"fmt"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
	"github.com/niparis/agent-net/internal/settings"
)

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

// sessionWaitsForOthersSQL is true for a session that waits for work it handed
// to others: a worker it started is online, or another session has claimed an
// open step task of a change it submitted. It has no output while it waits, and
// 20 minutes or more of that is normal (decision of 2026-11-12).
const sessionWaitsForOthersSQL = `(EXISTS(SELECT 1 FROM sessions w WHERE w.online=1 AND w.worker<>'' AND json_extract(w.worker,'$.owner')=s.id)
 OR EXISTS(SELECT 1 FROM delivery_step_tasks t JOIN delivery_changes c ON c.id=t.change_id AND c.revision=t.revision
 WHERE c.caller=s.id AND c.state='open' AND t.claimed_by<>'' AND t.claimed_by<>s.id))`

// checkSessionStalls runs on the gateway's minute tick. A poll of a harness is
// not progress: only output, a turn boundary or a delivery acknowledgement is.
// The mark lives on the session row (decision 0196): no item waits for the
// owner, so the mark may not come from one. It clears on the next activity or
// when the session is no longer eligible, and the log line of a stall is
// raised once for the activity time it started from.
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
 COALESCE(NULLIF(s.repo_project,''),s.announced_project),s.harness_activity,s.task,
 COALESCE((SELECT MIN(m.created_at) FROM messages m WHERE m.recipient=s.id AND
 (m.state IN ('stored','dispatched') OR (m.state='failed' AND EXISTS(SELECT 1 FROM delivery_retries r WHERE r.message_id=m.id)))),0),
 s.stalled_since,s.stall_asked_for,s.stall_asked_at,
 ` + sessionWaitsForOthersSQL + `
 FROM sessions s
 WHERE s.harness IN ('claude','codex','opencode','pi')`)
	if err != nil {
		return err
	}
	type candidate struct {
		id, harness, status, worker, waiting, repo, task string
		online                                           int
		active, pending                                  int64
		stalledSince, askedFor, askedAt                  int64
		waitsForOthers                                   bool
	}
	var sessions []candidate
	for rows.Next() {
		var c candidate
		if err := rows.Scan(&c.id, &c.harness, &c.status, &c.online, &c.worker, &c.waiting, &c.repo, &c.active, &c.task, &c.pending,
			&c.stalledSince, &c.askedFor, &c.askedAt, &c.waitsForOthers); err != nil {
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
		// An offline session, or one that is not busy and has announced done,
		// has nothing to stall. A busy session that stopped making progress
		// still does, and so does an idle session with a stuck delivery.
		idleDone := c.status != "busy" && announcedDone(c.task)
		eligible := c.online != 0 && c.worker == "" && c.waiting == "" && !c.waitsForOthers && !idleDone && (c.pending > 0 || c.status == "busy")
		baseline := c.active
		if c.status != "busy" && c.pending > baseline {
			baseline = c.pending
		}
		baselineSec := baseline / int64(time.Second)
		// The mark is the activity time the stall started from. Newer activity
		// or a session that can no longer stall ends it. An end this sweep made
		// lets a new stall for that newer activity start no wait: it ended when
		// that activity came.
		closedNow := false
		if c.stalledSince > 0 && (!eligible || baselineSec > c.stalledSince) {
			closedNow = true
			if _, err := tx.Exec(`UPDATE sessions SET stalled_since=0 WHERE id=?`, c.id); err != nil {
				return err
			}
			c.stalledSince = 0
		}
		// A stall is not raised again for an activity time it was raised for.
		if !eligible || baseline <= 0 || baselineSec <= c.askedFor {
			continue
		}
		timeout := snap.Duration("sessions.stall_timeout", settings.Where{Repo: c.repo})
		if timeout <= 0 || now.Sub(time.Unix(0, baseline)) <= timeout {
			continue
		}
		// One mark at a time, and none within the stall window after the last
		// one ended. Activity that the harness reports late can make a session
		// look stalled again at once, and the marks then came about every hour.
		if c.stalledSince > 0 || (!closedNow && c.askedAt > 0 && now.Sub(time.Unix(0, c.askedAt)) < timeout) {
			continue
		}
		s.logOwnerItem("desk.dropped", protocol.InboxItem{Kind: protocol.AskKindStall, SessionID: c.id, Node: c.repo},
			"text", askFirstLine(fmt.Sprintf("%s session may be stalled. No harness activity has been observed since %s. Open Sessions and press Enter to look, or restart the session.", c.harness, time.Unix(0, baseline).UTC().Format(time.RFC3339)), 160))
		if _, err := tx.Exec(`UPDATE sessions SET stalled_since=?,stall_asked_for=?,stall_asked_at=? WHERE id=?`,
			baselineSec, baselineSec, now.UnixNano(), c.id); err != nil {
			return err
		}
	}
	return tx.Commit()
}
