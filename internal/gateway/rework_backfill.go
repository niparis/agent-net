package gateway

import (
	"database/sql"
	"errors"
	"strings"

	"github.com/niparis/agent-net/internal/protocol"
)

// backfillRework recovers old counters from the messages still retained. The
// marker and counts commit together, so an interrupted start can safely retry.
// Bodies are read only for classification and are never written anywhere.
func (s *Store) backfillRework() error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var marker string
	err = tx.QueryRow(`SELECT value FROM owner_settings WHERE key='rework_backfill_done'`).Scan(&marker)
	if err == nil {
		return nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	type session struct {
		first                         int64
		runs                          []int64
		caller                        string
		seen, handback, partial, idle bool
		cont, fix                     int
	}
	sessions := map[string]*session{}
	rows, err := tx.Query(`SELECT id,session_id,caller FROM worker_runs WHERE session_id IN
  (SELECT session_id FROM worker_runs WHERE counters_known=0 AND session_id<>'') ORDER BY id`)
	if err != nil {
		return err
	}
	for rows.Next() {
		var id int64
		var sid, caller string
		if err := rows.Scan(&id, &sid, &caller); err != nil {
			rows.Close()
			return err
		}
		sess := sessions[sid]
		if sess == nil {
			sess = &session{first: id}
			sessions[sid] = sess
		}
		sess.runs = append(sess.runs, id)
		if sess.caller == "" {
			sess.caller = caller
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	if len(sessions) > 0 {
		// Infer missing callers before replay: the original task may have
		// expired, leaving a hand-back before the first retained request.
		rows, err = tx.Query(`SELECT recipient,sender FROM messages WHERE type=? ORDER BY created_at,rowid`, protocol.TypeRequest)
		if err != nil {
			return err
		}
		for rows.Next() {
			var to, from string
			if err := rows.Scan(&to, &from); err != nil {
				rows.Close()
				return err
			}
			if sess := sessions[to]; sess != nil && sess.caller == "" {
				sess.caller = from
			}
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return err
		}
		rows.Close()
		// One ordered body pass avoids rescanning messages for every worker.
		// rowid preserves insertion order when timestamps are equal.
		rows, err = tx.Query(`SELECT sender,recipient,type,body,created_at FROM messages ORDER BY created_at,rowid`)
		if err != nil {
			return err
		}
		for rows.Next() {
			var from, to, typ, body string
			var at int64
			if err := rows.Scan(&from, &to, &typ, &body, &at); err != nil {
				rows.Close()
				return err
			}
			if sess := sessions[to]; sess != nil && from == sess.caller {
				sess.seen = true
				if typ == protocol.TypeRequest {
					cont, fix := protocol.ReworkCounts(sess.handback, sess.partial, sess.idle)
					sess.cont += cont
					sess.fix += fix
				}
			}
			if sess := sessions[from]; sess != nil && to == sess.caller && (typ == protocol.TypeReply || typ == protocol.TypeNotification) {
				handback, partial, idle, ok := storedReworkNotice(body)
				if ok {
					sess.handback, sess.partial, sess.idle = handback, partial, idle
				}
			}
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return err
		}
		rows.Close()
	}
	update, err := tx.Prepare(`UPDATE worker_runs SET continue_count=?,fix_count=?,counters_known=1,
		caller=CASE WHEN id=? THEN ? ELSE caller END WHERE id=?`)
	if err != nil {
		return err
	}
	defer update.Close()
	for _, sess := range sessions {
		if !sess.seen {
			continue
		}
		// Replace all rows, including any newer live row, to avoid counting
		// messages twice. Address rows by primary key so many sessions do
		// not require a full worker_runs scan for every update.
		for _, id := range sess.runs {
			cont, fix := 0, 0
			if id == sess.first {
				cont, fix = sess.cont, sess.fix
			}
			if _, err := update.Exec(cont, fix, sess.first, sess.caller, id); err != nil {
				return err
			}
		}
	}
	if _, err := tx.Exec(`INSERT INTO owner_settings(key,value,updated_at) VALUES ('rework_backfill_done','1',?)`, s.now().UnixNano()); err != nil {
		return err
	}
	return tx.Commit()
}

// storedReworkNotice reads only the daemon's notice header and the explicit
// completion line of its report. Started and queued notices do not hand back.
func storedReworkNotice(body string) (handback, partial, idle, ok bool) {
	const prefix = "[agent-net worker notice]\nstatus: "
	if !strings.HasPrefix(body, prefix) {
		return
	}
	status, _, _ := strings.Cut(strings.TrimPrefix(body, prefix), "\n")
	switch status {
	case "idle without hand-back":
		return false, false, true, true
	case "partial":
		return true, true, false, true
	case protocol.StatusDone, protocol.StatusFailed, protocol.StatusTimedOut:
		if _, report, found := strings.Cut(body, protocol.ReportMarker+"\n"); found {
			report, _, _ = strings.Cut(report, "\n"+protocol.ChecklistMarker)
			complete, _ := protocol.WorkerReportComplete(report)
			partial = !complete
		}
		return true, partial, false, true
	}
	return
}
