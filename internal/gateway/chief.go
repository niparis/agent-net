package gateway

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
	"github.com/niparis/agent-net/internal/settings"
)

// The chief of staff (decision 0111). One registered session sorts the asks
// that would go to the owner first. An ask it has not sorted in time goes to the
// owner, and nothing is deleted. The chief is a standard agent session: the
// only special code is the routing of asks, the batch message and the sort.

// chiefSessionKey is the owner_settings row that names the chief session.
const chiefSessionKey = "chief_session"

// chiefMigration adds what the chief needs to inbox_items (priority and the
// time its batch message went out) and keeps the sort history. The two
// columns are added in the migrate loop, which checks that they are missing.
const chiefMigration = `-- Chief of staff (decision 0111): the sort history of an ask. prev_* keep the
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
// empty id clears the role.
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
	_, err = s.db.Exec(`INSERT INTO owner_settings(key,value,updated_at) VALUES(?,?,?)
		ON CONFLICT(key) DO UPDATE SET value=excluded.value, updated_at=excluded.updated_at`, chiefSessionKey, q.SessionID, s.now().UnixNano())
	return err
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
func (s *Store) storeAgentNetMessage(q askDB, to, body string, now time.Time) (protocol.Message, error) {
	m := protocol.Message{ID: protocol.NewID("m"), ConversationID: protocol.NewID("c"), From: "agent-net", FromName: "agent-net", FromHarness: "agent-net",
		To: to, Type: protocol.TypeRequest, Body: body, IdempotencyKey: protocol.NewID("chief"),
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
	m, err := s.storeAgentNetMessage(tx, b.chief, b.body, now)
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
// for ChiefTimeout, and every ask waiting on a chief that is not live. Each one
// gets a log line that says why.
func (s *Store) reconcileChiefAsks(now time.Time) error {
	chief, err := liveChief(s.db)
	if err != nil {
		return err
	}
	rows, err := s.db.Query(`SELECT id,kind,text,about_kind,about_id,created_at FROM inbox_items WHERE status='open' AND next_who=?`, protocol.AskNextChief)
	if err != nil {
		return err
	}
	type held struct {
		id, text, aboutKind, aboutID string
		created                      int64
	}
	var late []held
	cutoff := now.Add(-protocol.ChiefTimeout).UnixNano()
	for rows.Next() {
		var h held
		var kind string
		if err := rows.Scan(&h.id, &kind, &h.text, &h.aboutKind, &h.aboutID, &h.created); err != nil {
			rows.Close()
			return err
		}
		if chief == "" || h.created < cutoff {
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
		m, err := s.storeAgentNetMessage(tx, q.To, body, now)
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

// chiefCorrectionsMigration keeps the owner's marks of the Desk (decision 0111,
// part 2). A mark is a correction the chief reads in its next batch.
// TODO(judgment_add): when the judgment store lands, write each mark there
// with judgment_add (kind correction, area, ref) and read the batch from it.
// Until then this table is the store.
const chiefCorrectionsMigration = `-- The owner's marks on asks (decision 0111, part 2): didnt_need_me or
-- should_have_asked. area is the ask's project, ref the ask id.
CREATE TABLE IF NOT EXISTS chief_corrections (
 id INTEGER PRIMARY KEY AUTOINCREMENT, at INTEGER NOT NULL, mark TEXT NOT NULL,
 text TEXT NOT NULL, area TEXT NOT NULL DEFAULT '', ref TEXT NOT NULL DEFAULT '');
`

// chiefCorrectionsInBatch is how many of the newest marks a batch carries.
const chiefCorrectionsInBatch = 5

// UndoAsk puts an ask back the way it was before the newest sort of the chief
// (decision 0111, part 2). It refuses when the ask changed since that sort, or
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

// MarkAsk stores the owner's mark on one ask as a correction that the chief
// reads in its next batch (decision 0111, part 2). The mark names the ask's
// project as area and the ask id as ref.
func (s *Store) MarkAsk(q protocol.AskMarkRequest, actor string) error {
	if !actorAllowed(actor, "rule") {
		return reject("only the owner or the CTO may mark an ask")
	}
	if err := q.Validate(); err != nil {
		return reject("%v", err)
	}
	var text, node, aboutKind, aboutID string
	err := s.db.QueryRow(`SELECT text,node,about_kind,about_id FROM inbox_items WHERE id=?`, q.Ask).Scan(&text, &node, &aboutKind, &aboutID)
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
	if _, err := tx.Exec(`INSERT INTO chief_corrections(at,mark,text,area,ref) VALUES(?,?,?,?,?)`,
		s.now().UnixNano(), q.Mark, sentence, node, q.Ask); err != nil {
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

// recentCorrections is the text of the newest marks, one line each, for the
// chief's batch. It is empty when there are none.
func (s *Store) recentCorrections() string {
	rows, err := s.db.Query(`SELECT text FROM chief_corrections ORDER BY id DESC LIMIT ?`, chiefCorrectionsInBatch)
	if err != nil {
		return ""
	}
	defer rows.Close()
	var b strings.Builder
	for rows.Next() {
		var text string
		if rows.Scan(&text) == nil {
			fmt.Fprintf(&b, "- %s\n", text)
		}
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
