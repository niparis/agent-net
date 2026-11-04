package gateway

import (
	"database/sql"
	"errors"
	"strings"

	"github.com/niparis/agent-net/internal/protocol"
)

// backfillTaskRework links retained continuation briefs and replays only the
// new mid-run counter. Existing continue and fix counts are left intact. The
// marker and all changes share a transaction so startup can safely retry.
func (s *Store) backfillTaskRework() error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var marker string
	err = tx.QueryRow(`SELECT value FROM owner_settings WHERE key='task_rework_backfill_done'`).Scan(&marker)
	if err == nil {
		return nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	type run struct {
		first           int64
		ids             []int64
		caller, outcome string
		outcomes        []struct {
			ended   int64
			outcome string
		}
		seen, notice bool
		mid          int
	}
	runs := map[string]*run{}
	rows, err := tx.Query(`SELECT id,session_id,caller,outcome,ended_at FROM worker_runs WHERE session_id<>'' ORDER BY id`)
	if err != nil {
		return err
	}
	for rows.Next() {
		var id, ended int64
		var sid, caller, outcome string
		if err := rows.Scan(&id, &sid, &caller, &outcome, &ended); err != nil {
			rows.Close()
			return err
		}
		r := runs[sid]
		if r == nil {
			r = &run{first: id}
			runs[sid] = r
		}
		r.ids = append(r.ids, id)
		if r.caller == "" {
			r.caller = caller
		}
		r.outcome = outcome
		r.outcomes = append(r.outcomes, struct {
			ended   int64
			outcome string
		}{ended, outcome})
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	type brief struct {
		at                      int64
		sender, recipient, task string
	}
	var briefs []brief
	links := map[string]string{}
	linkAt := map[string]int64{}
	rows, err = tx.Query(`SELECT created_at,sender,recipient,type,body,worker_task FROM messages ORDER BY created_at,rowid`)
	if err != nil {
		return err
	}
	for rows.Next() {
		var at int64
		var from, to, typ, body string
		var workerTask sql.NullBool
		if err := rows.Scan(&at, &from, &to, &typ, &body, &workerTask); err != nil {
			rows.Close()
			return err
		}
		if r := runs[to]; r != nil && typ == protocol.TypeRequest {
			if r.caller == "" {
				r.caller = from
			}
			if from == r.caller {
				if r.seen && !r.notice {
					r.mid++
				}
				r.seen = true
				r.notice = false
			}
		}
		if r := runs[from]; r != nil && to == r.caller && (typ == protocol.TypeReply || typ == protocol.TypeNotification) {
			_, _, _, ok := storedReworkNotice(body)
			if ok {
				r.notice = true
			}
		}
		if typ != protocol.TypeRequest || !protocol.IsWorkerTask(protocol.Message{Type: typ, Body: body, WorkerTask: nullableWorkerTask(workerTask)}) {
			continue
		}
		_, task, ok := strings.Cut(body, "\nTask:\n")
		if !ok {
			continue
		}
		const head = "You continue the work of an earlier worker that ended ("
		if strings.HasPrefix(task, head) {
			_, core, found := strings.Cut(task, "\n\n")
			if found {
				core, _, _ = strings.Cut(core, "\n\nAlso: ")
				core = strings.TrimSpace(core)
				// Match characters rather than bytes, without cutting UTF-8.
				if chars := []rune(core); len(chars) > 300 {
					core = string(chars[:300])
				}
				if core != "" {
					for i := len(briefs) - 1; i >= 0; i-- {
						b := briefs[i]
						if b.at < at && b.sender == from && b.recipient != to && strings.Contains(b.task, core) {
							links[to] = b.recipient
							linkAt[to] = at
							break
						}
					}
				}
			}
		}
		briefs = append(briefs, brief{at, from, to, task})
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	for sid, r := range runs {
		for _, id := range r.ids {
			mid := 0
			if id == r.first {
				mid = r.mid
			}
			if _, err := tx.Exec(`UPDATE worker_runs SET midrun_count=? WHERE id=?`, mid, id); err != nil {
				return err
			}
		}
		prev, ok := links[sid]
		if !ok {
			continue
		}
		root := prev
		seen := map[string]bool{sid: true}
		for links[root] != "" && !seen[root] {
			seen[root] = true
			root = links[root]
		}
		if seen[root] {
			continue
		}
		for _, id := range r.ids {
			if _, err := tx.Exec(`UPDATE worker_runs SET task_id=? WHERE id=?`, root, id); err != nil {
				return err
			}
		}
		cont, fix := 0, 1
		if old := runs[prev]; old != nil {
			outcome := old.outcome
			var latest int64
			for _, run := range old.outcomes {
				if run.ended*1_000_000_000 <= linkAt[sid] && run.ended >= latest {
					latest, outcome = run.ended, run.outcome
				}
			}
			if outcome == protocol.OutcomePartial {
				cont, fix = 1, 0
			}
		}
		if _, err := tx.Exec(`UPDATE worker_runs SET continue_count=continue_count+?,fix_count=fix_count+? WHERE id=?`, cont, fix, r.first); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(`INSERT INTO owner_settings(key,value,updated_at) VALUES('task_rework_backfill_done','1',?)`, s.now().UnixNano()); err != nil {
		return err
	}
	return tx.Commit()
}

func nullableWorkerTask(v sql.NullBool) *bool {
	if !v.Valid {
		return nil
	}
	return &v.Bool
}
