package gateway

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
	"github.com/niparis/agent-net/internal/redact"
	"github.com/niparis/agent-net/internal/settings"
)

// The chief of staff (decision 0114). One registered session sorts the asks
// that would go to the owner first. An ask it has not sorted in time goes to the
// owner, and nothing is deleted. The chief is a standard agent session: the
// only special code is the routing of asks, the batch message and the sort.

// chiefSessionKey is the owner_settings row that names the chief session.
const chiefSessionKey = "chief_session"

// chiefMigration adds what the chief needs to inbox_items (priority and the
// time its batch message went out) and keeps the sort history. The two
// columns are added in the migrate loop, which checks that they are missing.
const chiefMigration = `-- Chief of staff (decision 0114): the sort history of an ask. prev_* keep the
-- ask as it was, so an undo can put it back.
CREATE TABLE IF NOT EXISTS ask_sorts (
 id INTEGER PRIMARY KEY AUTOINCREMENT,
 ask_id TEXT NOT NULL, at INTEGER NOT NULL, sorter TEXT NOT NULL,
 result TEXT NOT NULL, into_id TEXT NOT NULL DEFAULT '', to_id TEXT NOT NULL DEFAULT '',
 reason TEXT NOT NULL DEFAULT '', priority INTEGER NOT NULL DEFAULT 0, why_you TEXT NOT NULL DEFAULT '',
 prev_next TEXT NOT NULL DEFAULT '', prev_status TEXT NOT NULL DEFAULT '',
 prev_answer TEXT NOT NULL DEFAULT '', prev_answered_at INTEGER NOT NULL DEFAULT 0);
CREATE INDEX IF NOT EXISTS ask_sorts_ask ON ask_sorts(ask_id, id);
`

// chiefAdoptMigration records the time an ask started waiting for the chief
// and whether its creator routed it on purpose. The columns are added in the
// migrate loop, which checks that each one is missing.
const chiefAdoptMigration = `
-- Chief of staff adoption (decision 0114): when an ask started waiting for the
-- chief. Zero means its created_at. An ask opened before the chief existed is
-- adopted now, so its 15 minutes start now, not when its asker wrote it.
-- route_fixed marks an ask whose creator named the next who, so adoption
-- leaves it there.
SELECT 1 /* chief ask adoption time */;
`

// chiefSession is the registered chief session id, or "" when there is none.
func chiefSession(q askDB) (string, error) {
	var id string
	err := q.QueryRow(`SELECT value FROM owner_settings WHERE key=?`, chiefSessionKey).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return id, err
}

// liveChief is the chief session when it is registered, online and not
// archived. Otherwise it is "".
func liveChief(q askDB) (string, error) {
	id, err := chiefSession(q)
	if err != nil || id == "" {
		return "", err
	}
	var n int
	if err := q.QueryRow(`SELECT COUNT(*) FROM sessions WHERE id=? AND online=1 AND archived_at=0`, id).Scan(&n); err != nil || n == 0 {
		return "", err
	}
	return id, nil
}

// SetChief names the chief session. Only the owner or the CTO may do it. An
// empty id clears the role. A new chief takes over the open asks that wait for
// the owner and were never offered to a chief, so asks opened before the chief
// existed are not stranded.
func (s *Store) SetChief(q protocol.ChiefSetRequest, actor string) error {
	if !actorAllowed(actor, "rule") {
		return reject("only the owner or the CTO may set the chief of staff")
	}
	if q.SessionID == "" {
		_, err := s.db.Exec(`DELETE FROM owner_settings WHERE key=?`, chiefSessionKey)
		return err
	}
	var harness, worker string
	var archived int64
	err := s.db.QueryRow(`SELECT harness,worker,archived_at FROM sessions WHERE id=?`, q.SessionID).Scan(&harness, &worker, &archived)
	if errors.Is(err, sql.ErrNoRows) {
		return reject("unknown session %q", q.SessionID)
	}
	if err != nil {
		return err
	}
	if protocol.IsOwner(harness) || harness == "cto" || harness == "bot" || worker != "" || archived != 0 {
		return reject("the chief of staff must be a live agent session, not a worker, the owner, the CTO or a bot")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	now := s.now().UnixNano()
	if _, err = tx.Exec(`INSERT INTO owner_settings(key,value,updated_at) VALUES(?,?,?)
		ON CONFLICT(key) DO UPDATE SET value=excluded.value, updated_at=excluded.updated_at`, chiefSessionKey, q.SessionID, now); err != nil {
		return err
	}
	if _, err = adoptChiefAsks(tx, q.SessionID, now); err != nil {
		return err
	}
	return tx.Commit()
}

// ChiefStatus reads the registered chief and the model it is set to run on.
func (s *Store) ChiefStatus() (protocol.ChiefStatus, error) {
	out := protocol.ChiefStatus{}
	id, err := chiefSession(s.db)
	if err != nil {
		return out, err
	}
	out.SessionID = id
	snap, err := s.SettingsSnapshot()
	if err != nil {
		return out, err
	}
	out.Model = snap.String(protocol.ChiefModelKey, settings.Where{})
	if out.Model == "" {
		out.Model = protocol.DefaultChiefModel
	}
	if id == "" {
		return out, nil
	}
	err = s.db.QueryRow(`SELECT COALESCE(NULLIF(p.name,''),NULLIF(s.name,''),s.title,''), s.online=1 AND s.archived_at=0
		FROM sessions s LEFT JOIN profiles p ON p.session_id=s.id WHERE s.id=?`, id).Scan(&out.Name, &out.Online)
	if errors.Is(err, sql.ErrNoRows) {
		return out, nil
	}
	return out, err
}

// chiefRoute sends a new ask to the chief when one is live. The asker never
// routes to itself, and an ask that already names someone else is left alone.
func chiefRoute(q askDB, item protocol.InboxItem) (string, error) {
	if item.NextWho != "" {
		return item.NextWho, nil
	}
	chief, err := liveChief(q)
	if err != nil || chief == "" || chief == item.SessionID {
		return protocol.AskNextOwner, err
	}
	return protocol.AskNextChief, nil
}

// adoptChiefAsks gives the chief every open ask that still waits for the owner
// and was never offered to a chief. It runs when a chief is set and on the
// start and minute reconcile, so asks opened before the chief existed reach
// it. An ask that was sorted, answered, sent to a session, already offered, or
// routed by its creator on purpose (route_fixed) is left alone. The chief's own
// asks are not routed to itself.
func adoptChiefAsks(q askDB, chief string, now int64) (int, error) {
	if chief == "" {
		return 0, nil
	}
	res, err := q.Exec(`UPDATE inbox_items SET next_who=?, chief_at=?
		WHERE status='open' AND (next_who='' OR next_who=?) AND session_id<>?
		AND route_fixed=0 AND chief_sent_at=0
		AND NOT EXISTS (SELECT 1 FROM ask_sorts r WHERE r.ask_id=inbox_items.id)`,
		protocol.AskNextChief, now, protocol.AskNextOwner, chief)
	if err != nil {
		return 0, err
	}
	n, err := res.RowsAffected()
	return int(n), err
}

// chiefBatch is one message to the chief: the asks that arrived close together.
type chiefBatch struct {
	chief string
	ids   []string
	body  string
}

// chiefBatchDue returns the batch that should go out now, if any. A batch
// waits for a quiet window after its newest ask, and never waits longer than
// ChiefMaxWait after its oldest one.
func (s *Store) chiefBatchDue(now time.Time) (*chiefBatch, error) {
	chief, err := liveChief(s.db)
	if err != nil || chief == "" {
		return nil, err
	}
	rows, err := s.db.Query(`SELECT id,kind,session_id,from_kind,about_kind,about_id,text,recommendation,options,created_at
		FROM inbox_items WHERE status='open' AND next_who=? AND chief_sent_at=0 ORDER BY created_at,id LIMIT ?`, protocol.AskNextChief, protocol.ChiefBatchCap)
	if err != nil {
		return nil, err
	}
	type pending struct {
		id, kind, session, fromKind, aboutKind, aboutID, text, rec, options string
		created                                                             int64
	}
	var list []pending
	for rows.Next() {
		var p pending
		if err := rows.Scan(&p.id, &p.kind, &p.session, &p.fromKind, &p.aboutKind, &p.aboutID, &p.text, &p.rec, &p.options, &p.created); err != nil {
			rows.Close()
			return nil, err
		}
		list = append(list, p)
	}
	err = rows.Err()
	rows.Close()
	if err != nil || len(list) == 0 {
		return nil, err
	}
	nowN := now.UnixNano()
	newest, oldest := list[len(list)-1].created, list[0].created
	if nowN-newest < int64(protocol.ChiefQuietWindow) && nowN-oldest < int64(protocol.ChiefMaxWait) {
		return nil, nil
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Chief of staff: %d ask(s) to sort. Sort each one with agent_ask_sort: close, merge into another open ask, send to a session, or owner (with priority and why_you). Sort only. Never answer for the owner. An ask you leave for 15 minutes goes to the owner.\n", len(list))
	batch := &chiefBatch{chief: chief}
	for _, p := range list {
		batch.ids = append(batch.ids, p.id)
		from := p.fromKind
		if from == "" || from == protocol.AskFromSession {
			from = sessionLabel(s.db, p.session)
		}
		fmt.Fprintf(&b, "\nAsk %s (%s) from %s", p.id, p.kind, from)
		if p.aboutID != "" {
			fmt.Fprintf(&b, ", about %s %s", p.aboutKind, p.aboutID)
		}
		fmt.Fprintf(&b, "\nQuestion: %s", p.text)
		if p.options != "" && p.options != "[]" {
			fmt.Fprintf(&b, "\nOptions: %s", p.options)
		}
		if p.rec != "" {
			fmt.Fprintf(&b, "\nRecommendation: %s", p.rec)
		}
		if related := s.relatedAsks(p.id, p.session, p.aboutID); related != "" {
			fmt.Fprintf(&b, "\nRelated open asks: %s", related)
		}
		b.WriteString("\n")
	}
	if marks := s.recentCorrections(); marks != "" {
		fmt.Fprintf(&b, "\nOwner corrections, newest first (follow them in every sort):\n%s", marks)
	}
	batch.body = strings.TrimRight(b.String(), "\n")
	return batch, nil
}

// relatedAsks names up to five other open asks about the same thing or from the
// same session, so the chief can merge them.
func (s *Store) relatedAsks(id, session, about string) string {
	rows, err := s.db.Query(`SELECT id,text FROM inbox_items WHERE status='open' AND id<>?
		AND ((?<>'' AND session_id=?) OR (?<>'' AND about_id=?)) ORDER BY created_at,id LIMIT 5`, id, session, session, about, about)
	if err != nil {
		return ""
	}
	defer rows.Close()
	var parts []string
	for rows.Next() {
		var rid, text string
		if rows.Scan(&rid, &text) == nil {
			parts = append(parts, rid+" ("+askFirstLine(text, 60)+")")
		}
	}
	return strings.Join(parts, "; ")
}

// storeAgentNetMessage writes a message from agent-net to a session in the
// caller's transaction. The gateway dispatches it after the commit.
//
// A message about one ask carries the ask in its key (chiefAskKeyPrefix), so a
// reply to the message can answer the ask (agentNetReply).
func (s *Store) storeAgentNetMessage(q askDB, to, body, askID string, now time.Time) (protocol.Message, error) {
	key := protocol.NewID("chief")
	if askID != "" {
		key = chiefAskKeyPrefix + askID + ":" + key
	}
	m := protocol.Message{ID: protocol.NewID("m"), ConversationID: protocol.NewID("c"), From: "agent-net", FromName: "agent-net", FromHarness: "agent-net",
		To: to, Type: protocol.TypeRequest, Body: body, IdempotencyKey: key,
		CreatedAt: now, ExpiresAt: now.Add(protocol.DefaultTTL), State: protocol.StateStored}
	_, err := q.Exec(`INSERT INTO messages(id,conversation_id,parent_id,sender,recipient,type,body,hop,idem_key,created_at,expires_at,state)
		VALUES(?,?,?,?,?,?,?,0,?,?,?,?)`, m.ID, m.ConversationID, m.ParentID, m.From, m.To, m.Type, m.Body,
		m.IdempotencyKey, m.CreatedAt.UnixNano(), m.ExpiresAt.UnixNano(), m.State)
	return m, err
}

// storeChiefBatch writes the batch as one message and marks its asks as sent.
func (s *Store) storeChiefBatch(b chiefBatch, now time.Time) (protocol.Message, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return protocol.Message{}, err
	}
	defer tx.Rollback()
	m, err := s.storeAgentNetMessage(tx, b.chief, b.body, "", now)
	if err != nil {
		return m, err
	}
	for _, id := range b.ids {
		if _, err := tx.Exec(`UPDATE inbox_items SET chief_sent_at=? WHERE id=?`, now.UnixNano(), id); err != nil {
			return m, err
		}
	}
	return m, tx.Commit()
}

// flushChief sends the chief's due batch. The gateway calls it every few
// seconds; a batch that is not due changes nothing.
func (g *Gateway) flushChief(ctx context.Context) {
	now := g.store.now()
	b, err := g.store.chiefBatchDue(now)
	if err != nil {
		g.logLine("error", "where", "chief batch", "err", err)
		return
	}
	if b == nil {
		return
	}
	m, err := g.store.storeChiefBatch(*b, now)
	if err != nil {
		g.logLine("error", "where", "chief batch message", "err", err)
		return
	}
	g.dispatch(ctx, m)
}

// reconcileChiefAsks gives the asks back to the owner that the chief has held
// for ChiefTimeout, and every ask waiting on a chief that is not live. It also
// adopts open owner asks that were never offered to a chief, which is how asks
// open at gateway start reach a chief that is already set. Each returned ask
// gets a log line that says why.
func (s *Store) reconcileChiefAsks(now time.Time) error {
	chief, err := liveChief(s.db)
	if err != nil {
		return err
	}
	if _, err := adoptChiefAsks(s.db, chief, now.UnixNano()); err != nil {
		return err
	}
	rows, err := s.db.Query(`SELECT id,kind,text,about_kind,about_id,created_at,chief_at FROM inbox_items WHERE status='open' AND next_who=?`, protocol.AskNextChief)
	if err != nil {
		return err
	}
	type held struct {
		id, text, aboutKind, aboutID string
		created, chiefAt             int64
	}
	var late []held
	cutoff := now.Add(-protocol.ChiefTimeout).UnixNano()
	for rows.Next() {
		var h held
		var kind string
		if err := rows.Scan(&h.id, &kind, &h.text, &h.aboutKind, &h.aboutID, &h.created, &h.chiefAt); err != nil {
			rows.Close()
			return err
		}
		anchor := h.created
		if h.chiefAt != 0 {
			anchor = h.chiefAt
		}
		if chief == "" || anchor < cutoff {
			late = append(late, h)
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, h := range late {
		why := "The chief of staff did not sort it in 15 minutes, so it is with you: "
		if chief == "" {
			why = "The chief of staff is offline, so it is with you: "
		}
		if _, err := s.db.Exec(`UPDATE inbox_items SET next_who=? WHERE id=? AND next_who=?`, protocol.AskNextOwner, h.id, protocol.AskNextChief); err != nil {
			return err
		}
		if err := s.deskLog(s.db, "gateway", h.aboutKind, h.aboutID, why+h.text, "", nil); err != nil {
			return err
		}
	}
	return nil
}

// askSortRow is the ask a sort reads and changes.
type askSortRow struct {
	text, kind, session, aboutKind, aboutID, next, status, answer string
	answeredAt                                                    int64
}

// waitingChiefAsks lists the open asks that wait for the chief, oldest first.
func (s *Store) waitingChiefAsks(q askDB, now time.Time) ([]protocol.AskWaiting, error) {
	rows, err := q.Query(`SELECT id,kind,session_id,from_kind,text,recommendation,created_at
		FROM inbox_items WHERE status='open' AND next_who=? ORDER BY created_at,id`,
		protocol.AskNextChief)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []protocol.AskWaiting{}
	for rows.Next() {
		var w protocol.AskWaiting
		var session, fromKind string
		var createdN int64
		if err := rows.Scan(&w.Ask, &w.Kind, &session, &fromKind, &w.Text, &w.Recommendation, &createdN); err != nil {
			return nil, err
		}
		w.From = fromKind
		if w.From == "" || w.From == protocol.AskFromSession {
			w.From = sessionLabel(q, session)
		}
		w.Age = askAge(now, time.Unix(0, createdN))
		out = append(out, w)
	}
	return out, rows.Err()
}

// askSortStats counts one sorter's sorts by result in one grouped query. Since
// and until are Unix seconds, fromID and toID sort ids; zero leaves a bound
// open. FirstID and LastID cover every row that matched.
func askSortStats(q askDB, sorter string, since, until, fromID, toID int64) (protocol.AskSortStats, error) {
	out := protocol.AskSortStats{Sorter: sorter, Counts: map[string]int{}}
	query := `SELECT result, COUNT(*), MIN(id), MAX(id) FROM ask_sorts WHERE sorter=?`
	args := []any{sorter}
	if since > 0 {
		query += ` AND at>=?`
		args = append(args, since*int64(time.Second))
	}
	if until > 0 {
		query += ` AND at<=?`
		args = append(args, until*int64(time.Second))
	}
	if fromID > 0 {
		query += ` AND id>=?`
		args = append(args, fromID)
	}
	if toID > 0 {
		query += ` AND id<=?`
		args = append(args, toID)
	}
	query += ` GROUP BY result ORDER BY result`
	rows, err := q.Query(query, args...)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var result string
		var n int
		var first, last int64
		if err := rows.Scan(&result, &n, &first, &last); err != nil {
			return out, err
		}
		out.Counts[result] = n
		if out.FirstID == 0 || first < out.FirstID {
			out.FirstID = first
		}
		if last > out.LastID {
			out.LastID = last
		}
	}
	return out, rows.Err()
}

// askAge is how old an ask is, in a short form: 45s, 12m, 3h05m or 2d04h.
func askAge(now, created time.Time) string {
	d := now.Sub(created)
	if d < 0 {
		d = 0
	}
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh%02dm", int(d.Hours()), int(d.Minutes())%60)
	default:
		return fmt.Sprintf("%dd%02dh", int(d.Hours())/24, int(d.Hours())%24)
	}
}

// SortAsk applies one sort of the chief of staff. Only the live chief may
// sort, and only an ask that waits for it. The ask keeps its earlier next,
// status and answer in ask_sorts. It returns the message to send when the ask
// went to a session; the caller dispatches it.
func (s *Store) SortAsk(node string, q protocol.AskSortRequest) (protocol.AskSortResult, protocol.Message, error) {
	var out protocol.AskSortResult
	var none protocol.Message
	if err := q.Validate(); err != nil {
		return out, none, reject("%v", err)
	}
	if q.Detail == "full" {
		return s.readAskFull(node, q.SessionID, q.Ask)
	}
	tx, err := s.db.Begin()
	if err != nil {
		return out, none, err
	}
	defer tx.Rollback()
	chief, err := liveChief(tx)
	if err != nil {
		return out, none, err
	}
	if chief == "" || chief != q.SessionID {
		return out, none, reject("only the chief of staff may sort asks")
	}
	var chiefNode string
	if err := tx.QueryRow(`SELECT node_id FROM sessions WHERE id=?`, chief).Scan(&chiefNode); err != nil || chiefNode != node {
		return out, none, reject("the chief of staff is not on this machine")
	}
	if q.Result == protocol.AskSortList {
		asks, err := s.waitingChiefAsks(tx, s.now())
		if err != nil {
			return out, none, err
		}
		sorter := strings.TrimSpace(q.Sorter)
		if sorter == "" {
			sorter = q.SessionID
		}
		stats, err := askSortStats(tx, sorter, q.Since, q.Until, q.FromID, q.ToID)
		if err != nil {
			return out, none, err
		}
		out = protocol.AskSortResult{Result: protocol.AskSortList, Asks: asks, Stats: &stats}
		return out, none, nil
	}
	var a askSortRow
	err = tx.QueryRow(`SELECT text,kind,session_id,about_kind,about_id,next_who,status,answer,answered_at FROM inbox_items WHERE id=?`, q.Ask).
		Scan(&a.text, &a.kind, &a.session, &a.aboutKind, &a.aboutID, &a.next, &a.status, &a.answer, &a.answeredAt)
	if errors.Is(err, sql.ErrNoRows) {
		return out, none, reject("unknown ask %q", q.Ask)
	}
	if err != nil {
		return out, none, err
	}
	if a.status != "open" || a.next != protocol.AskNextChief {
		return out, none, reject("ask %s is not waiting for the chief of staff", q.Ask)
	}
	now := s.now()
	reason := strings.TrimSpace(q.Reason)
	rec := protocol.AskSort{Ask: q.Ask, At: now, Sorter: chief, Result: q.Result, Into: q.Into, To: q.To, Reason: reason,
		Priority: q.Priority, WhyYou: strings.TrimSpace(q.WhyYou), PrevNext: a.next, PrevStatus: a.status, PrevAnswer: a.answer}
	if a.answeredAt != 0 {
		rec.PrevAnsweredAt = time.Unix(0, a.answeredAt).UTC()
	}
	text := askFirstLine(a.text, 120)
	var logText string
	out = protocol.AskSortResult{Ask: q.Ask, Result: q.Result, Next: protocol.AskNextOwner, State: protocol.AskStateOpen}
	switch q.Result {
	case protocol.AskSortClose, protocol.AskSortMerge:
		if q.Result == protocol.AskSortMerge {
			var into string
			err := tx.QueryRow(`SELECT status FROM inbox_items WHERE id=?`, q.Into).Scan(&into)
			if errors.Is(err, sql.ErrNoRows) || into != "open" || q.Into == q.Ask {
				return out, none, reject("merge needs another open ask as into")
			}
			if err != nil {
				return out, none, err
			}
			note := "merged: " + q.Ask
			if _, err := tx.Exec(`UPDATE inbox_items SET detail=CASE WHEN detail='' THEN ? ELSE detail||char(10)||? END WHERE id=?`, note, note, q.Into); err != nil {
				return out, none, err
			}
			logText = fmt.Sprintf("Chief merged ask into %s: %s", q.Into, text)
		} else {
			logText = "Chief closed ask: " + text
		}
		if _, err := tx.Exec(`UPDATE inbox_items SET status='closed', answer=?, answered_at=? WHERE id=?`, "chief: "+reason, now.UnixNano(), q.Ask); err != nil {
			return out, none, err
		}
		out.Next, out.State = "", protocol.AskClosed
	case protocol.AskSortSend:
		var n int
		if err := tx.QueryRow(`SELECT COUNT(*) FROM sessions WHERE id=? AND archived_at=0 AND id<>?`, q.To, chief).Scan(&n); err != nil {
			return out, none, err
		}
		if n == 0 {
			return out, none, reject("send needs a live session as to")
		}
		if _, err := tx.Exec(`UPDATE inbox_items SET next_who=? WHERE id=?`, q.To, q.Ask); err != nil {
			return out, none, err
		}
		body := fmt.Sprintf("The chief of staff sent you ask %s from %s: %s\nWhy it reached you: %s\nAnswer it yourself only if you can. Anything that needs the owner goes to them with agent_owner_ask.",
			q.Ask, sessionLabel(s.db, a.session), strings.TrimSpace(a.text), reason)
		m, err := s.storeAgentNetMessage(tx, q.To, body, q.Ask, now)
		if err != nil {
			return out, none, err
		}
		out.Next, out.State = q.To, protocol.AskWithSomeone
		logText = fmt.Sprintf("Chief sent ask to %s: %s", sessionLabel(s.db, q.To), text)
		if err := s.commitSort(tx, rec, now, logText, a, "chief"); err != nil {
			return out, none, err
		}
		return out, m, tx.Commit()
	case protocol.AskSortOwner:
		if _, err := tx.Exec(`UPDATE inbox_items SET next_who=?, priority=?, why_you=? WHERE id=?`, protocol.AskNextOwner, q.Priority, rec.WhyYou, q.Ask); err != nil {
			return out, none, err
		}
		logText = fmt.Sprintf("Chief sent ask to you (priority %d): %s", q.Priority, text)
	}
	if err := s.commitSort(tx, rec, now, logText, a, "chief"); err != nil {
		return out, none, err
	}
	return out, none, tx.Commit()
}

// commitSort writes the history row and the log line of one sort.
func (s *Store) commitSort(tx *sql.Tx, rec protocol.AskSort, now time.Time, logText string, a askSortRow, source string) error {
	var answered int64
	if !rec.PrevAnsweredAt.IsZero() {
		answered = rec.PrevAnsweredAt.UnixNano()
	}
	if _, err := tx.Exec(`INSERT INTO ask_sorts(ask_id,at,sorter,result,into_id,to_id,reason,priority,why_you,prev_next,prev_status,prev_answer,prev_answered_at)
		VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?)`, rec.Ask, now.UnixNano(), rec.Sorter, rec.Result, rec.Into, rec.To, rec.Reason, rec.Priority, rec.WhyYou,
		rec.PrevNext, rec.PrevStatus, rec.PrevAnswer, answered); err != nil {
		return err
	}
	return s.deskLog(tx, source, a.aboutKind, a.aboutID, logText, rec.Reason, nil)
}

// AskSortHistory returns the sorts of one ask, oldest first.
func (s *Store) AskSortHistory(askID string) ([]protocol.AskSort, error) {
	rows, err := s.db.Query(`SELECT id,ask_id,at,sorter,result,into_id,to_id,reason,priority,why_you,prev_next,prev_status,prev_answer,prev_answered_at
		FROM ask_sorts WHERE ask_id=? ORDER BY id`, askID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []protocol.AskSort{}
	for rows.Next() {
		var r protocol.AskSort
		var at, answered int64
		if err := rows.Scan(&r.ID, &r.Ask, &at, &r.Sorter, &r.Result, &r.Into, &r.To, &r.Reason, &r.Priority, &r.WhyYou, &r.PrevNext, &r.PrevStatus, &r.PrevAnswer, &answered); err != nil {
			return nil, err
		}
		r.At = time.Unix(0, at).UTC()
		if answered != 0 {
			r.PrevAnsweredAt = time.Unix(0, answered).UTC()
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// chiefCorrectionsMigration keeps the owner's marks of the Desk (decision 0114,
// part 2). A mark is a correction the chief reads in its next batch.
// The marks now live in the owner judgment store (kind correction, area chief).
// This table is no longer written or read; its rows were copied there by the
// last migration (ownerMarksMigration). It stays, because dropping it would
// delete data.
const chiefCorrectionsMigration = `-- The owner's marks on asks (decision 0114, part 2): didnt_need_me or
-- should_have_asked. area is the ask's project, ref the ask id.
CREATE TABLE IF NOT EXISTS chief_corrections (
 id INTEGER PRIMARY KEY AUTOINCREMENT, at INTEGER NOT NULL, mark TEXT NOT NULL,
 text TEXT NOT NULL, area TEXT NOT NULL DEFAULT '', ref TEXT NOT NULL DEFAULT '');
`

// chiefJudgmentArea is the area of the judgments that teach the chief.
const chiefJudgmentArea = "chief"

// ownerMarksMigration copies the marks of chief_corrections into the owner
// judgment store (kind correction, area chief, ref the ask id). Rows go in
// order, so the newest mark has the highest rowid. A row that is already there
// is not copied again, so the statement is safe to replay. The old table is
// kept.
const ownerMarksMigration = `
-- Owner marks move to the judgment store (decision 0114, part 2): one memory.
INSERT INTO standing_orders(id,text,saved_at,source,tags,pinned,updated_at,kind,area,ref)
SELECT 'so_m_' || lower(hex(randomblob(8))), c.text, c.at / 1000000000, 'owner', '[]', 0, 0, 'correction', 'chief', c.ref
FROM chief_corrections c
WHERE NOT EXISTS (SELECT 1 FROM standing_orders o WHERE o.kind='correction' AND o.area='chief' AND o.ref=c.ref AND o.text=c.text AND o.saved_at=c.at / 1000000000)
ORDER BY c.id;
`

// chiefCorrectionsInBatch is how many of the newest marks a batch carries.
const chiefCorrectionsInBatch = 5

// UndoAsk puts an ask back the way it was before the newest sort of the chief
// (decision 0114, part 2). It refuses when the ask changed since that sort, or
// when the newest sort is already undone. The chief's hold is not put back: the
// chief got its one batch, so the ask goes to the owner. Priority and why_you
// are cleared, because only a sort sets them. Nothing is deleted.
func (s *Store) UndoAsk(id, actor string) (protocol.AskSortResult, error) {
	var out protocol.AskSortResult
	if !actorAllowed(actor, "rule") {
		return out, reject("only the owner or the CTO may undo a sort")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	var last protocol.AskSort
	var at, answered int64
	err = tx.QueryRow(`SELECT id,at,sorter,result,into_id,to_id,reason,priority,why_you,prev_next,prev_status,prev_answer,prev_answered_at
		FROM ask_sorts WHERE ask_id=? ORDER BY id DESC LIMIT 1`, id).
		Scan(&last.ID, &at, &last.Sorter, &last.Result, &last.Into, &last.To, &last.Reason, &last.Priority, &last.WhyYou,
			&last.PrevNext, &last.PrevStatus, &last.PrevAnswer, &answered)
	if errors.Is(err, sql.ErrNoRows) {
		return out, reject("ask %s has no chief sort to undo", id)
	}
	if err != nil {
		return out, err
	}
	if last.Result == protocol.AskSortUndo {
		return out, reject("the sort of ask %s is already undone", id)
	}
	var next, status, answer, text, aboutKind, aboutID string
	var nowAnswered int64
	var priority int
	var whyYou string
	err = tx.QueryRow(`SELECT next_who,status,answer,answered_at,priority,why_you,text,about_kind,about_id FROM inbox_items WHERE id=?`, id).
		Scan(&next, &status, &answer, &nowAnswered, &priority, &whyYou, &text, &aboutKind, &aboutID)
	if err != nil {
		return out, err
	}
	// The state the sort left behind. Any other state means the ask moved on.
	changed := false
	switch last.Result {
	case protocol.AskSortClose, protocol.AskSortMerge:
		changed = status != "closed" || nowAnswered != at || answer != "chief: "+last.Reason
	case protocol.AskSortSend:
		changed = next != last.To || status != "open"
	case protocol.AskSortOwner:
		changed = next != protocol.AskNextOwner || priority != last.Priority || whyYou != last.WhyYou
	}
	if changed {
		return out, reject("ask %s changed since the chief sorted it, so it cannot be undone", id)
	}
	now := s.now()
	restored := last.PrevNext
	if restored == "" || restored == protocol.AskNextChief {
		restored = protocol.AskNextOwner
	}
	if _, err := tx.Exec(`UPDATE inbox_items SET next_who=?, status=?, answer=?, answered_at=?, priority=0, why_you='' WHERE id=?`,
		restored, last.PrevStatus, last.PrevAnswer, answered, id); err != nil {
		return out, err
	}
	if last.Result == protocol.AskSortMerge && last.Into != "" {
		if err := removeMergedNote(tx, last.Into, id); err != nil {
			return out, err
		}
	}
	rec := protocol.AskSort{Ask: id, At: now, Sorter: actor, Result: protocol.AskSortUndo, PrevNext: next, PrevStatus: status, PrevAnswer: answer}
	if nowAnswered != 0 {
		rec.PrevAnsweredAt = time.Unix(0, nowAnswered).UTC()
	}
	logText := "Undone chief sort of ask " + id + ": " + askFirstLine(text, 120)
	if err := s.commitSort(tx, rec, now, logText, askSortRow{text: text, aboutKind: aboutKind, aboutID: aboutID}, deskSource(actor)); err != nil {
		return out, err
	}
	out = protocol.AskSortResult{Ask: id, Result: protocol.AskSortUndo, Next: restored, State: protocol.AskStateOpen}
	return out, tx.Commit()
}

// removeMergedNote takes the "merged: <ask>" line that a merge wrote into the
// ask it merged into.
func removeMergedNote(tx *sql.Tx, into, ask string) error {
	var detail string
	if err := tx.QueryRow(`SELECT detail FROM inbox_items WHERE id=?`, into).Scan(&detail); err != nil {
		return err
	}
	var keep []string
	for _, line := range strings.Split(detail, "\n") {
		if line != "merged: "+ask {
			keep = append(keep, line)
		}
	}
	_, err := tx.Exec(`UPDATE inbox_items SET detail=? WHERE id=?`, strings.Join(keep, "\n"), into)
	return err
}

// MarkAsk stores the owner's mark on one ask in the owner judgment store, as a
// correction that the chief reads in its next batch (decision 0114, part 2).
// The judgment has kind correction, area chief and the ask id as ref. The text
// says what the owner marked and carries the ask's first line.
func (s *Store) MarkAsk(q protocol.AskMarkRequest, actor string) error {
	if !actorAllowed(actor, "rule") {
		return reject("only the owner or the CTO may mark an ask")
	}
	if err := q.Validate(); err != nil {
		return reject("%v", err)
	}
	var text, aboutKind, aboutID string
	err := s.db.QueryRow(`SELECT text,about_kind,about_id FROM inbox_items WHERE id=?`, q.Ask).Scan(&text, &aboutKind, &aboutID)
	if errors.Is(err, sql.ErrNoRows) {
		return reject("unknown ask %q", q.Ask)
	}
	if err != nil {
		return err
	}
	why := strings.TrimSpace(q.Why)
	var sentence string
	if q.Mark == protocol.MarkDidntNeedMe {
		sentence = fmt.Sprintf("The owner did not need ask %s (%s). Sort asks like it yourself.", q.Ask, askFirstLine(text, 120))
	} else {
		sentence = fmt.Sprintf("The owner should have been asked about ask %s (%s). Send asks like it to him.", q.Ask, askFirstLine(text, 120))
	}
	if why != "" {
		sentence += " Why: " + why
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := addJudgment(tx, s.now(), protocol.JudgmentAddRequest{
		Kind: protocol.StandingKindCorrection, Area: chiefJudgmentArea, Ref: q.Ask, Text: sentence}); err != nil {
		return err
	}
	logText := "Owner marked ask " + q.Ask + ": " + markWords(q.Mark) + ". " + askFirstLine(text, 120)
	if err := s.deskLog(tx, deskSource(actor), aboutKind, aboutID, logText, why, nil); err != nil {
		return err
	}
	return tx.Commit()
}

// deskSource is the log source of an owner action: the CTO acts for the owner
// and its lines say so.
func deskSource(actor string) string {
	if actor == "cto" {
		return "cto"
	}
	return "owner"
}

// markWords names a mark in the owner's words.
func markWords(mark string) string {
	if mark == protocol.MarkDidntNeedMe {
		return "didn't need me"
	}
	return "should have asked me"
}

// readAskFull reads one ask for the live chief. It changes nothing. Only the
// chief on this machine may read, as with a sort, because an ask can hold what
// a session told the owner.
func (s *Store) readAskFull(node, session, ask string) (protocol.AskSortResult, protocol.Message, error) {
	var out protocol.AskSortResult
	var none protocol.Message
	chief, err := liveChief(s.db)
	if err != nil {
		return out, none, err
	}
	if chief == "" || chief != session {
		return out, none, reject("only the chief of staff may read asks")
	}
	var chiefNode string
	if err := s.db.QueryRow(`SELECT node_id FROM sessions WHERE id=?`, chief).Scan(&chiefNode); err != nil || chiefNode != node {
		return out, none, reject("the chief of staff is not on this machine")
	}
	full, err := s.askFull(ask)
	if errors.Is(err, sql.ErrNoRows) {
		return out, none, reject("unknown ask %q", ask)
	}
	if err != nil {
		return out, none, err
	}
	out = protocol.AskSortResult{Ask: ask, Result: "read", Next: full.Next, State: full.State, Full: &full}
	return out, none, nil
}

// askFull reads the one ask and its payloads.
func (s *Store) askFull(id string) (protocol.AskFull, error) {
	var f protocol.AskFull
	var created, answered int64
	var options string
	err := s.db.QueryRow(`SELECT id,kind,session_id,node,text,recommendation,created_at,status,answer,answered_at,options,from_kind,source_ref,about_kind,about_id,next_who,why_you,detail,priority,change_title
		FROM inbox_items WHERE id=?`, id).
		Scan(&f.ID, &f.Kind, &f.FromSession, &f.Node, &f.Question, &f.Recommendation, &created, &f.Status, &f.Answer, &answered, &options,
			&f.FromKind, &f.SourceRef, &f.AboutKind, &f.AboutID, &f.Next, &f.WhyYou, &f.Detail, &f.Priority, &f.AboutTitle)
	if err != nil {
		return f, err
	}
	if err := json.Unmarshal([]byte(options), &f.Options); err != nil {
		return f, err
	}
	f.CreatedAt = time.Unix(0, created).UTC()
	if answered != 0 {
		f.AnsweredAt = time.Unix(0, answered).UTC()
	}
	f.State = askFullState(f.Status, f.Next)
	f.FromName = sessionLabel(s.db, f.FromSession)
	f.NextName = nextName(s.db, f.Next)
	f.Question = redact.SecretValues(f.Question)
	f.Detail = redact.SecretValues(f.Detail)
	f.Recommendation = redact.SecretValues(f.Recommendation)
	f.Answer = redact.SecretValues(f.Answer)
	f.AboutTitle = redact.SecretValues(f.AboutTitle)
	f.WhyYou = redact.SecretValues(f.WhyYou)
	for i := range f.Options {
		f.Options[i] = redact.SecretValues(f.Options[i])
	}
	sorts, err := s.AskSortHistory(id)
	if err != nil {
		return f, err
	}
	if len(sorts) > 0 {
		for i := range sorts {
			sorts[i].Reason = redact.SecretValues(sorts[i].Reason)
			sorts[i].WhyYou = redact.SecretValues(sorts[i].WhyYou)
		}
		f.Routing = sorts
	}
	if f.Charter, err = s.askFullCharter(f.SourceRef); err != nil {
		return f, err
	}
	if f.Learned, err = s.askFullLearned(f.SourceRef); err != nil {
		return f, err
	}
	return f, nil
}

// askFullState maps an ask's stored status to the Desk state.
func askFullState(status, next string) string {
	switch status {
	case "open":
		if next != "" && next != protocol.AskNextOwner {
			return protocol.AskWithSomeone
		}
		return protocol.AskStateOpen
	case "answered", "approved", "refused":
		return protocol.AskAnswered
	default:
		return protocol.AskClosed
	}
}

// nextName names who acts on the ask now.
func nextName(q interface{ QueryRow(string, ...any) *sql.Row }, next string) string {
	switch next {
	case "":
		return ""
	case protocol.AskNextOwner:
		return "owner"
	case protocol.AskNextChief:
		return "chief of staff"
	default:
		return sessionLabel(q, next)
	}
}

// askFullCharter returns the proposed fields of a draft-charter ask. It reads
// the drafts themselves, so each value is exact and not cut by a detail line.
func (s *Store) askFullCharter(ref string) ([]protocol.CharterDraftField, error) {
	const columns = `SELECT id,level,scope,attr,value,source,status,drafted_by,created_at FROM charter_drafts`
	var rows *sql.Rows
	var err error
	switch {
	case strings.HasPrefix(ref, legacyDraftRefPrefix):
		id, parseErr := sourceRefID(ref, legacyDraftRefPrefix)
		if parseErr != nil {
			return nil, nil
		}
		rows, err = s.db.Query(columns+` WHERE id=?`, id)
	case strings.HasPrefix(ref, draftGroupRefPrefix):
		key := strings.TrimPrefix(ref, draftGroupRefPrefix)
		level, scope, ok := strings.Cut(key, ":")
		if !ok || scope == "" {
			return nil, nil
		}
		cond := `(level='repo' OR level='agent')`
		if level == "team" {
			cond = `level='team'`
		}
		out, err := s.charterRows(columns+` WHERE scope=? AND `+cond+` AND status='pending' ORDER BY id`, scope)
		if err != nil || len(out) > 0 {
			return out, err
		}
		return s.charterRows(columns+` WHERE scope=? AND `+cond+` ORDER BY id`, scope)
	default:
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []protocol.CharterDraftField{}
	for rows.Next() {
		var d protocol.CharterDraftField
		var created int64
		if err := rows.Scan(&d.ID, &d.Level, &d.Scope, &d.Attr, &d.Value, &d.Source, &d.Status, &d.DraftedBy, &created); err != nil {
			return nil, err
		}
		d.CreatedAt = time.Unix(0, created).UTC()
		maskDraftField(&d)
		out = append(out, d)
	}
	return out, rows.Err()
}

// charterRows reads charter draft fields with a query of the same shape.
func (s *Store) charterRows(query, scope string) ([]protocol.CharterDraftField, error) {
	rows, err := s.db.Query(query, scope)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []protocol.CharterDraftField{}
	for rows.Next() {
		var d protocol.CharterDraftField
		var created int64
		if err := rows.Scan(&d.ID, &d.Level, &d.Scope, &d.Attr, &d.Value, &d.Source, &d.Status, &d.DraftedBy, &created); err != nil {
			return nil, err
		}
		d.CreatedAt = time.Unix(0, created).UTC()
		maskDraftField(&d)
		out = append(out, d)
	}
	return out, rows.Err()
}

// askFullLearned returns the learned proposal behind a learned ask.
func (s *Store) askFullLearned(ref string) (*protocol.LearnedSuggestion, error) {
	if !strings.HasPrefix(ref, "learned:") {
		return nil, nil
	}
	id, err := sourceRefID(ref, "learned:")
	if err != nil {
		return nil, nil
	}
	var v protocol.LearnedSuggestion
	var evidence string
	err = s.db.QueryRow(`SELECT id,node,field,edge_kind,kind,phrase,evidence,status,severity,created_at,ready_at,decided_at,
		COALESCE((SELECT origin FROM learned_suggestion_details d WHERE d.suggestion_id=learned_suggestions.id),''),
		COALESCE((SELECT history_id FROM learned_suggestion_details d WHERE d.suggestion_id=learned_suggestions.id),0),
		COALESCE((SELECT raw_phrase FROM learned_phrase_state p WHERE p.suggestion_id=learned_suggestions.id),'')
		FROM learned_suggestions WHERE id=?`, id).
		Scan(&v.ID, &v.Node, &v.Field, &v.EdgeKind, &v.Kind, &v.Phrase, &evidence, &v.Status, &v.Severity, &v.CreatedAt, &v.ReadyAt, &v.DecidedAt, &v.Origin, &v.HistoryID, &v.RawPhrase)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal([]byte(evidence), &v.Evidence); err != nil {
		return nil, err
	}
	if v.Status == "pending" && v.Field != "edge" {
		var old string
		if err := s.db.QueryRow(`SELECT value FROM graph_charter WHERE node=? AND attr=?`, v.Node, v.Field).Scan(&old); err == nil {
			v.OldValue = old
		}
		v.NewValue = learnedValue(v.OldValue, v.Phrase, v.Kind)
	} else if v.Status == "pending" {
		v.NewValue = v.EdgeKind + " -> " + v.Phrase
	}
	maskLearned(&v)
	return &v, nil
}

// maskDraftField masks every text field of a charter draft field. A draft can
// quote a token in any of them, such as its source line, not only in its value.
func maskDraftField(d *protocol.CharterDraftField) {
	d.Level = redact.SecretValues(d.Level)
	d.Scope = redact.SecretValues(d.Scope)
	d.Attr = redact.SecretValues(d.Attr)
	d.Value = redact.SecretValues(d.Value)
	d.Source = redact.SecretValues(d.Source)
	d.Status = redact.SecretValues(d.Status)
	d.DraftedBy = redact.SecretValues(d.DraftedBy)
}

// maskLearned masks every text field of a learned proposal, its evidence
// included, before the proposal leaves the gateway.
func maskLearned(v *protocol.LearnedSuggestion) {
	v.Node = redact.SecretValues(v.Node)
	v.Field = redact.SecretValues(v.Field)
	v.EdgeKind = redact.SecretValues(v.EdgeKind)
	v.Kind = redact.SecretValues(v.Kind)
	v.Phrase = redact.SecretValues(v.Phrase)
	v.Status = redact.SecretValues(v.Status)
	v.Severity = redact.SecretValues(v.Severity)
	v.Origin = redact.SecretValues(v.Origin)
	v.RawPhrase = redact.SecretValues(v.RawPhrase)
	v.OldValue = redact.SecretValues(v.OldValue)
	v.NewValue = redact.SecretValues(v.NewValue)
	v.Level = redact.SecretValues(v.Level)
	v.Scope = redact.SecretValues(v.Scope)
	v.By = redact.SecretValues(v.By)
	for i := range v.Evidence {
		v.Evidence[i].ID = redact.SecretValues(v.Evidence[i].ID)
		v.Evidence[i].Excerpt = redact.SecretValues(v.Evidence[i].Excerpt)
	}
}

// recentCorrections is the text of the newest marks, one line each, for the
// chief's batch. They come from the owner judgment store. It is empty when
// there are none.
func (s *Store) recentCorrections() string {
	found, err := s.FindJudgments(protocol.JudgmentsFindRequest{
		Kind: protocol.StandingKindCorrection, Area: chiefJudgmentArea, Limit: chiefCorrectionsInBatch})
	if err != nil {
		return ""
	}
	var b strings.Builder
	for _, j := range found {
		fmt.Fprintf(&b, "- %s\n", j.Text)
	}
	return b.String()
}

// ChiefSorts returns the sorts of the chief since a time, newest first. An undo
// row is not a row of its own: a sort that the owner undid is marked Undone.
func (s *Store) ChiefSorts(since time.Time) ([]protocol.ChiefSort, error) {
	rows, err := s.db.Query(`SELECT r.id,r.ask_id,r.at,r.sorter,r.result,r.into_id,r.to_id,r.reason,r.priority,r.why_you,
		COALESCE(i.text,''),
		EXISTS (SELECT 1 FROM ask_sorts u WHERE u.ask_id=r.ask_id AND u.id>r.id AND u.result=?)
		FROM ask_sorts r LEFT JOIN inbox_items i ON i.id=r.ask_id
		WHERE r.at>=? AND r.result<>? ORDER BY r.id DESC LIMIT 100`, protocol.AskSortUndo, since.UnixNano(), protocol.AskSortUndo)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []protocol.ChiefSort{}
	for rows.Next() {
		var c protocol.ChiefSort
		var at int64
		var undone bool
		if err := rows.Scan(&c.ID, &c.Ask, &at, &c.Sorter, &c.Result, &c.Into, &c.To, &c.Reason, &c.Priority, &c.WhyYou, &c.Question, &undone); err != nil {
			return nil, err
		}
		c.At = time.Unix(0, at).UTC()
		c.Question = askFirstLine(c.Question, 200)
		c.Undone = undone
		out = append(out, c)
	}
	return out, rows.Err()
}
