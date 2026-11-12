package gateway

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// Asks and the Desk (decision 0090). inbox_items is the one store of asks.
// This file reads them as one list, closes them by themselves when their
// reason goes away, and keeps the event log.

// askDB is what both *sql.DB and *sql.Tx offer. An ask is written in the same
// transaction as the change that makes it.
type askDB interface {
	Exec(string, ...any) (sql.Result, error)
	Query(string, ...any) (*sql.Rows, error)
	QueryRow(string, ...any) *sql.Row
}

const (
	deskLogKeep    = 2000
	deskLogDefault = 100
	deskLogMax     = 500
	// gatewayAsker is the session an ask carries when no session asked.
	gatewayAsker = "agent-net"
)

// askFirstLine is the first line of s, cut for one row of the Desk or the log.
func askFirstLine(s string, max int) string {
	s, _, _ = strings.Cut(strings.TrimSpace(s), "\n")
	s = strings.TrimSpace(protocol.CleanWaitingText(s))
	if r := []rune(s); len(r) > max {
		s = string(r[:max-1]) + "…"
	}
	return s
}

// deskLog adds one line to the event log. Options carry what undoes the event.
func (s *Store) deskLog(q askDB, source, aboutKind, aboutID, text, detail string, options []string) error {
	text = askFirstLine(text, 300)
	if text == "" {
		return nil
	}
	if options == nil {
		options = []string{}
	}
	raw, err := json.Marshal(options)
	if err != nil {
		return err
	}
	res, err := q.Exec(`INSERT INTO desk_log(at,source,about_kind,about_id,text,detail,options) VALUES(?,?,?,?,?,?,?)`,
		s.now().UnixNano(), source, aboutKind, aboutID, text, detail, string(raw))
	if err != nil {
		return err
	}
	// Keep the log short: a few hundred inserts at most between two prunes.
	if id, err := res.LastInsertId(); err == nil && id%200 == 0 {
		_, err = q.Exec(`DELETE FROM desk_log WHERE id<=?`, id-deskLogKeep)
		return err
	}
	return nil
}

// AddDeskNote adds one line to the event log from a machine's daemon. It is a
// report, not an ask: nothing waits for an answer.
func (s *Store) AddDeskNote(nodeID, text, detail string) error {
	return s.deskLog(s.db, "daemon", "node", nodeID, text, detail, nil)
}

// DeskLog returns the newest log lines, newest first.
func (s *Store) DeskLog(limit int) ([]protocol.DeskLogEntry, error) {
	if limit <= 0 {
		limit = deskLogDefault
	}
	if limit > deskLogMax {
		limit = deskLogMax
	}
	rows, err := s.db.Query(`SELECT id,at,source,about_kind,about_id,text,detail,options FROM desk_log ORDER BY id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []protocol.DeskLogEntry{}
	for rows.Next() {
		var e protocol.DeskLogEntry
		var at int64
		var options string
		if err := rows.Scan(&e.ID, &at, &e.Source, &e.AboutKind, &e.AboutID, &e.Text, &e.Detail, &options); err != nil {
			return nil, err
		}
		e.At = time.Unix(0, at).UTC()
		if err := json.Unmarshal([]byte(options), &e.Options); err != nil {
			return nil, err
		}
		if len(e.Options) == 0 {
			e.Options = nil
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

const askSelect = `SELECT i.id,i.kind,i.session_id,i.node,i.text,i.recommendation,i.message_id,i.conversation_id,i.created_at,i.status,i.answer,i.answered_at,i.options,
 COALESCE(NULLIF(i.asker_name,''), CASE WHEN s.archived_at=0 THEN COALESCE(NULLIF(p.name,''),NULLIF(s.name,''),s.title,'') ELSE '' END, ''),
 i.change_title,i.from_kind,i.source_ref,i.about_kind,i.about_id,i.next_who,i.why_you,i.detail,i.priority,i.owner,i.state,i.category,
 COALESCE(d.change_id,''),COALESCE(d.step,''),COALESCE(d.for_step,''),COALESCE(d.after_merge,0),COALESCE(d.commit_hash,''),COALESCE(c.project,''),COALESCE(c.title,'')
 FROM inbox_items i LEFT JOIN sessions s ON s.id=i.session_id LEFT JOIN profiles p ON p.session_id=i.session_id
 LEFT JOIN delivery_step_inbox d ON d.item_id=i.id LEFT JOIN delivery_changes c ON c.id=d.change_id`

// ListAsks returns every open ask, oldest first, in one query. The Desk reads
// this one list instead of building rows from the Inbox, held requests,
// outcomes, learned suggestions and sessions.
func (s *Store) ListAsks() ([]protocol.Ask, error) {
	rows, err := s.db.Query(askSelect + ` WHERE i.status='open' ORDER BY i.created_at,i.id`)
	if err != nil {
		return nil, err
	}
	type step struct {
		project string
		ask     int
	}
	var steps []step
	asks := []protocol.Ask{}
	for rows.Next() {
		var a protocol.Ask
		var created, answered int64
		var options, status, changeID, changeTitle, project string
		var after int
		var d protocol.InboxDelivery
		if err := rows.Scan(&a.ID, &a.Kind, &a.FromSession, &a.Node, &a.Question, &a.Recommendation, &a.MessageID, &a.ConversationID, &created, &status, &a.Answer, &answered, &options,
			&a.From, &changeTitle, &a.FromKind, &a.Ref, &a.AboutKind, &a.AboutID, &a.Next, &a.WhyYou, &a.Detail, &a.Priority, &a.Owner, &a.DeskState, &a.Category,
			&changeID, &d.Step, &d.For, &after, &d.Commit, &project, &a.AboutTitle); err != nil {
			rows.Close()
			return nil, err
		}
		if err := json.Unmarshal([]byte(options), &a.Options); err != nil {
			rows.Close()
			return nil, err
		}
		a.CreatedAt = time.Unix(0, created).UTC()
		if answered != 0 {
			a.ClosedAt = time.Unix(0, answered).UTC()
		}
		a.State = protocol.AskStateOpen
		if a.Next != "" && a.Next != protocol.AskNextOwner {
			a.State = protocol.AskWithSomeone
		}
		if a.FromKind == "" {
			a.FromKind = protocol.AskFromSession
		}
		if a.FromKind == protocol.AskFromSession && (a.From == "" || strings.HasPrefix(a.From, "s_")) {
			a.From = "an agent"
		}
		if changeID != "" {
			d.ChangeID, d.AfterMerge = changeID, after != 0
			a.Delivery = &d
			a.AboutKind, a.AboutID = protocol.AskAboutChange, changeID
			if a.AboutTitle == "" {
				a.AboutTitle = changeTitle
			}
			steps = append(steps, step{project, len(asks)})
		}
		if a.AboutTitle == "" {
			a.AboutTitle = changeTitle
		}
		asks = append(asks, a)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	pipelines := map[string]protocol.Pipeline{}
	for _, st := range steps {
		p, ok := pipelines[st.project]
		if !ok {
			if p, err = s.deliveryPipeline(st.project); err != nil {
				return nil, err
			}
			pipelines[st.project] = p
		}
		asks[st.ask].Delivery.ReportFields = p.ReportFields(asks[st.ask].Delivery.StepRef)
	}
	return asks, nil
}

// closeAsks closes every open ask that matches cond, keeps the reason in the
// answer, and writes one log line for each. cond is a SQL condition on
// inbox_items that this package writes; it never holds user text.
func (s *Store) closeAsks(q askDB, cond string, args []any, reason string) (int, error) {
	rows, err := q.Query(`SELECT id,kind,text,about_kind,about_id FROM inbox_items WHERE status='open' AND (`+cond+`)`, args...)
	if err != nil {
		return 0, err
	}
	type closed struct{ id, kind, text, aboutKind, aboutID string }
	var list []closed
	for rows.Next() {
		var c closed
		if err := rows.Scan(&c.id, &c.kind, &c.text, &c.aboutKind, &c.aboutID); err != nil {
			rows.Close()
			return 0, err
		}
		list = append(list, c)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return 0, err
	}
	now := s.now().UnixNano()
	for _, c := range list {
		if _, err := q.Exec(`UPDATE inbox_items SET status='closed',answer=?,answered_at=? WHERE id=? AND status='open'`, reason, now, c.id); err != nil {
			return 0, err
		}
		// A source that was decided by a person is not automation: only the
		// closes that the gateway does by itself go to the log.
		if reason == protocol.AskReasonResolved {
			continue
		}
		if err := s.deskLog(q, "desk", c.aboutKind, c.aboutID, fmt.Sprintf("Closed %s ask (%s): %s", c.kind, strings.ReplaceAll(reason, "_", " "), askFirstLine(c.text, 120)), "", nil); err != nil {
			return 0, err
		}
	}
	return len(list), nil
}

// closeAskRef closes the open ask of one source key.
func (s *Store) closeAskRef(q askDB, ref, reason string) error {
	if ref == "" {
		return nil
	}
	_, err := s.closeAsks(q, `source_ref=?`, []any{ref}, reason)
	return err
}

// askKindsOfSession are the asks that a session itself raises and that lose
// their meaning when the session ends. A rejection or a held request is work
// between two sessions and stays until the owner decides it.
const askKindsOfSession = `'question','approval','feature_idea','waiting','session_stall','check'`

// reconcileEndedSessionAsks closes the asks of archived sessions.
func (s *Store) reconcileEndedSessionAsks(q askDB) (int, error) {
	return s.closeAsks(q, `from_kind IN ('','session') AND kind IN (`+askKindsOfSession+`)
		AND EXISTS (SELECT 1 FROM sessions x WHERE x.id=inbox_items.session_id AND x.archived_at<>0)`, nil, protocol.AskReasonSessionEnded)
}

// closeSessionOwnerAsks closes the open questions, approvals and turn-end
// notes that one session raised itself. The owner typed in that session, or
// the session went on to new work: the reason of the ask is gone. Nothing is
// deleted. before limits the close to asks made before that time (UnixNano);
// zero means all.
func (s *Store) closeSessionOwnerAsks(q askDB, session string, before int64, reason string, kinds ...string) (int, error) {
	cond, args := sessionOwnerAskCond(session, before, kinds)
	return s.closeAsks(q, cond, args, reason)
}

// sessionOwnerAskCond selects the open asks that closeSessionOwnerAsks closes.
func sessionOwnerAskCond(session string, before int64, kinds []string) (string, []any) {
	if len(kinds) == 0 {
		kinds = []string{protocol.AskKindQuestion, protocol.AskKindApproval, protocol.AskKindCheck}
	}
	cond := `session_id=? AND from_kind IN ('','session') AND kind IN ('` + strings.Join(kinds, `','`) + `')`
	args := []any{session}
	if before > 0 {
		cond += ` AND created_at<?`
		args = append(args, before)
	}
	return cond, args
}

// movedOnCleanupKey marks the one-time cleanup in owner_settings.
const movedOnCleanupKey = "asks_moved_on_cleanup_v1"

// movedOnGap is how long after an ask the session's task must have started
// for the cleanup to call the ask left behind. A task line of the same turn
// as the ask is never that far after it.
const movedOnGap = 10 * time.Minute

// movedOnCond selects the open approvals and check items that a session
// raised and then left: its task started more than movedOnGap after the item.
// Questions are not part of it: one that the session left may still be a real
// question to the owner, and questions close by the live rules (a typed
// prompt, a dismissal, a later announce). A session may hold two different
// approvals at once, so a newer item never closes an older one; a replaced
// item already has its own status.
const movedOnCond = `from_kind IN ('','session') AND kind IN ('approval','check')
	AND EXISTS (SELECT 1 FROM profiles p WHERE p.session_id=inbox_items.session_id AND p.task_started > inbox_items.created_at + ?)`

// reconcileMovedOnOnce closes the asks that sessions left behind before the
// close-on-activity rules existed, once. It closes and never deletes. The
// marker in owner_settings keeps it from running again.
func (s *Store) reconcileMovedOnOnce() (int, error) {
	var done string
	err := s.db.QueryRow(`SELECT value FROM owner_settings WHERE key=?`, movedOnCleanupKey).Scan(&done)
	if err == nil {
		return 0, nil
	}
	if err != sql.ErrNoRows {
		return 0, err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	n, err := s.closeAsks(tx, movedOnCond, []any{int64(movedOnGap)}, protocol.AskReasonMovedOnClean)
	if err != nil {
		return 0, err
	}
	if _, err := tx.Exec(`INSERT INTO owner_settings(key,value,updated_at) VALUES(?,?,?)`, movedOnCleanupKey, strconv.Itoa(n), s.now().UnixNano()); err != nil {
		return 0, err
	}
	return n, tx.Commit()
}

// escalationAnsweredCond selects the open escalations whose request was
// answered later: the session that escalated sent a plain reply (not another
// outcome) in the same conversation after the escalation. The escalation item
// holds the id of the escalating reply, and graph_outcomes links that reply
// to the request.
const escalationAnsweredCond = `kind='escalation' AND from_kind IN ('','session') AND EXISTS (
	SELECT 1 FROM graph_outcomes o JOIN messages req ON req.id=o.message_id
	JOIN messages later ON later.conversation_id=req.conversation_id AND later.sender=inbox_items.session_id
		AND later.type IN ` + answeredTypesSQL + ` AND later.created_at > inbox_items.created_at
	WHERE o.reply_id=inbox_items.message_id
		AND NOT EXISTS (SELECT 1 FROM graph_outcomes x WHERE x.reply_id=later.id))`

// reconcileEscalationAsks closes the escalations of requests that were
// answered after the escalation, and marks the outcome answered so the old
// outcome inbox drops it too.
func (s *Store) reconcileEscalationAsks(q askDB) (int, error) {
	if _, err := q.Exec(`UPDATE graph_outcomes SET resolution='answered' WHERE resolution='' AND reply_id IN
		(SELECT message_id FROM inbox_items WHERE status='open' AND (` + escalationAnsweredCond + `))`); err != nil {
		return 0, err
	}
	return s.closeAsks(q, escalationAnsweredCond, nil, protocol.AskReasonAnswered)
}

// reconcileChangeAsks closes the asks about a change that is done or stopped.
func (s *Store) reconcileChangeAsks(q askDB) (int, error) {
	return s.closeAsks(q, `about_kind='change' AND EXISTS (SELECT 1 FROM delivery_changes c WHERE c.id=inbox_items.about_id AND c.state IN ('done','stopped'))`, nil, protocol.AskReasonChangeDone)
}

// closeChangeAsks closes every open ask about one change.
func (s *Store) closeChangeAsks(q askDB, changeID, reason string) error {
	_, err := s.closeAsks(q, `about_kind='change' AND about_id=?`, []any{changeID}, reason)
	return err
}

// reconcileHeldAsks opens an ask for every held request and closes the asks of
// requests that are no longer held or have expired.
func (s *Store) reconcileHeldAsks(q askDB) error {
	now := s.now().UnixNano()
	if _, err := s.closeAsks(q, `kind='held' AND NOT EXISTS (SELECT 1 FROM messages m WHERE m.id=substr(inbox_items.source_ref,6) AND m.state='held' AND m.expires_at>?)`, []any{now}, protocol.AskReasonExpired); err != nil {
		return err
	}
	rows, err := q.Query(`SELECT m.id,m.sender,m.recipient,m.body FROM messages m WHERE m.state='held' AND m.hold_kind='' AND m.expires_at>?
		AND NOT EXISTS (SELECT 1 FROM inbox_items i WHERE i.source_ref='held:'||m.id AND i.status='open')`, now)
	if err != nil {
		return err
	}
	type held struct{ id, from, to, body string }
	var list []held
	for rows.Next() {
		var h held
		if err := rows.Scan(&h.id, &h.from, &h.to, &h.body); err != nil {
			rows.Close()
			return err
		}
		list = append(list, h)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, h := range list {
		if err := s.openHeldAsk(q, h.id, h.from, h.to, h.body); err != nil {
			return err
		}
	}
	return nil
}

// openHeldAsk is the ask of one held request: from the sender, about the
// session it wants to reach.
func (s *Store) openHeldAsk(q askDB, id, from, to, body string) error {
	_, err := s.createInboxItem(q, protocol.InboxItem{
		Kind: protocol.AskKindHeld, SessionID: from, SourceRef: "held:" + id, MessageID: id,
		AboutKind: protocol.AskAboutSession, AboutID: to,
		Text: askFirstLine(body, 300), Detail: body, WhyYou: "A rule says to ask you before this request goes through.",
	})
	return err
}

// closeHeldAsk closes the ask of a held request the owner decided or that expired.
func (s *Store) closeHeldAsk(q askDB, id, reason string) error {
	return s.closeAskRef(q, "held:"+id, reason)
}

// syncWaitingAsk keeps the ask of a session that waits for its user. waiting
// is the stored JSON of the session's wait; empty means it does not wait.
func (s *Store) syncWaitingAsk(q askDB, session, waiting string) error {
	ref := "waiting:" + session
	if waiting == "" {
		return s.closeAskRef(q, ref, protocol.AskReasonResolved)
	}
	var w protocol.Waiting
	// An explicit wait comes from agent_owner_ask, which has its own ask.
	if json.Unmarshal([]byte(waiting), &w) != nil || strings.TrimSpace(w.Text) == "" || w.Explicit {
		return s.closeAskRef(q, ref, protocol.AskReasonResolved)
	}
	var harness, worker string
	if err := q.QueryRow(`SELECT harness,worker FROM sessions WHERE id=?`, session).Scan(&harness, &worker); err != nil {
		if err == sql.ErrNoRows {
			return nil
		}
		return err
	}
	// The owner's own sessions and workers (which a lead answers) are not asks.
	if protocol.IsOwner(harness) || worker != "" {
		return nil
	}
	// A short wait for the owner who works in the session makes no ask
	// (decision 0150): the minute reconcile opens it once the wait is old
	// enough. When he is away, the question goes to the chief at once. The
	// gateway stamps every stored wait with its start.
	if w.Since > 0 && s.now().Sub(time.Unix(w.Since, 0)) < waitingAskDebounce {
		present, err := s.ownerInSession(q, session)
		if err != nil {
			return err
		}
		if present {
			var open int
			if err := q.QueryRow(`SELECT COUNT(*) FROM inbox_items WHERE source_ref=? AND status='open'`, ref).Scan(&open); err != nil {
				return err
			}
			if open == 0 {
				return nil
			}
		}
	}
	// A wait on the session's own lead is an ordinary wait inside the team: the
	// lead hears of it (blockedMemberNotice), the owner has no ask (decision
	// 0126). A wait a harness event reported (a permission prompt) is the
	// owner's, whatever its text says.
	if !w.Blocking {
		if _, named, err := s.waitNamesLead(q, session, w.Text); err != nil {
			return err
		} else if named {
			return s.closeAskRef(q, ref, protocol.AskReasonResolved)
		}
	}
	_, err := s.createInboxItem(q, protocol.InboxItem{
		Kind: protocol.AskKindWaiting, SessionID: session, SourceRef: ref,
		AboutKind: protocol.AskAboutSession, AboutID: session,
		Text: w.Text, Options: w.Options, Detail: w.Kind,
	})
	return err
}

// waitingAskDebounce is how long a session waits for its user before the
// wait becomes an ask.
const waitingAskDebounce = 2 * time.Minute

// reconcileWaitingAsks opens the asks of the waits that outlived the
// debounce. Only sessions that wait and have no open ask are read.
func (s *Store) reconcileWaitingAsks() error {
	rows, err := s.db.Query(`SELECT id,waiting FROM sessions WHERE waiting<>'' AND archived_at=0
		AND NOT EXISTS (SELECT 1 FROM inbox_items i WHERE i.source_ref='waiting:'||sessions.id AND i.status='open')`)
	if err != nil {
		return err
	}
	type wait struct{ id, waiting string }
	var list []wait
	for rows.Next() {
		var w wait
		if err := rows.Scan(&w.id, &w.waiting); err != nil {
			rows.Close()
			return err
		}
		list = append(list, w)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, w := range list {
		if err := s.syncWaitingAsk(s.db, w.id, w.waiting); err != nil {
			return err
		}
	}
	return nil
}

// reconcileLearnedAsks opens an ask for every ready major learned suggestion
// and every pending charter draft, and closes the asks of those the owner or
// the CTO decided. It reads the same two lists the owner saw before, so the
// same suggestions show up.
func (s *Store) reconcileLearnedAsks() error {
	// PendingCharterDrafts first: it marks the drafts that got a value some
	// other way as superseded.
	drafts, err := s.PendingCharterDrafts()
	if err != nil {
		return err
	}
	if _, err := s.closeAsks(s.db, `kind='learned' AND source_ref LIKE 'learned:%' AND NOT EXISTS (SELECT 1 FROM learned_suggestions l WHERE l.id=CAST(substr(inbox_items.source_ref,9) AS INTEGER) AND l.status='pending')`, nil, protocol.AskReasonResolved); err != nil {
		return err
	}
	// An ask that went to a team lead closes when the node has no pending suggestion left.
	if _, err := s.closeAsks(s.db, `kind='learned' AND source_ref LIKE ? AND NOT EXISTS (SELECT 1 FROM learned_suggestions l WHERE l.node=inbox_items.about_id AND l.status='pending')`, []any{leadAskRefPrefix + "%"}, protocol.AskReasonResolved); err != nil {
		return err
	}
	open := map[string]bool{}
	rows, err := s.db.Query(`SELECT source_ref FROM inbox_items WHERE status='open' AND kind='learned'`)
	if err != nil {
		return err
	}
	for rows.Next() {
		var ref string
		if err := rows.Scan(&ref); err != nil {
			rows.Close()
			return err
		}
		open[ref] = true
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	ready, err := s.LearnedSuggestions(true)
	if err != nil {
		return err
	}
	for _, v := range ready {
		ref := fmt.Sprintf("learned:%d", v.ID)
		if v.Severity != "major" || open[ref] {
			continue
		}
		item := protocol.InboxItem{
			Kind: protocol.AskKindLearned, SessionID: gatewayAsker, FromKind: protocol.AskFromGateway, Node: v.Node, AskerName: "learned",
			SourceRef: ref, AboutKind: protocol.AskAboutProject, AboutID: v.Node,
			Text: learnedAskText(v), Detail: learnedAskBecause(v),
			Options: []string{"accept", "reject"},
		}
		// An edge suggestion for a team member goes to the team lead, once.
		if v.Field == "edge" {
			sent, err := s.sendLearnedToLead(v, item)
			if err != nil {
				return err
			}
			if sent {
				continue
			}
		}
		if _, err := s.createInboxItem(s.db, item); err != nil {
			return err
		}
	}
	return s.reconcileDraftAsks(drafts)
}

// sendLearnedToLead sends the ask of a learned suggestion to the lead of the
// node's team when the team asks to the lead. It is false when the ask is the
// owner's, and also false when the lead was already asked about this
// suggestion and the ask is still there or was answered: then there is nothing
// to open for the owner either, so it reports true.
func (s *Store) sendLearnedToLead(v protocol.LearnedSuggestion, item protocol.InboxItem) (bool, error) {
	var seen int
	if err := s.db.QueryRow(`SELECT EXISTS(SELECT 1 FROM inbox_items WHERE kind='learned' AND about_id=? AND text=? AND status<>'closed' AND source_ref LIKE ?)`,
		v.Node, item.Text, leadAskRefPrefix+"%").Scan(&seen); err != nil {
		return false, err
	}
	if seen != 0 {
		return true, nil
	}
	item.Options = nil
	return s.redirectNodeAsk(v.Node, "", item)
}

// reconcileAsks brings every derived ask in line with its source. The gateway
// runs it on its minute tick and at start. Each source also acts at the moment
// it changes, so this is the safety net and the only path for time-based
// changes such as a held request that expires.
func (s *Store) reconcileAsks() error {
	if err := s.reconcileAppConsentAsks(); err != nil {
		return err
	}
	if err := s.reconcileHeldAsks(s.db); err != nil {
		return fmt.Errorf("held asks: %w", err)
	}
	if err := s.reconcileLearnedAsks(); err != nil {
		return fmt.Errorf("learned asks: %w", err)
	}
	if _, err := s.reconcileEndedSessionAsks(s.db); err != nil {
		return fmt.Errorf("ended session asks: %w", err)
	}
	if _, err := s.reconcileChangeAsks(s.db); err != nil {
		return fmt.Errorf("change asks: %w", err)
	}
	if err := s.reconcileChiefAsks(s.now()); err != nil {
		return fmt.Errorf("chief asks: %w", err)
	}
	if _, err := s.reconcileEscalationAsks(s.db); err != nil {
		return fmt.Errorf("escalation asks: %w", err)
	}
	if _, err := s.reconcileMovedOnOnce(); err != nil {
		return fmt.Errorf("moved on cleanup: %w", err)
	}
	if err := s.reconcileWaitingAsks(); err != nil {
		return fmt.Errorf("waiting asks: %w", err)
	}
	return nil
}

// OpenExternalAsk opens or refreshes an ask that a source outside the gateway
// owns, such as a CTO proposal on a machine. The ref makes it safe to repeat.
func (s *Store) OpenExternalAsk(node string, a protocol.AskOpen) (protocol.Ask, error) {
	switch a.Kind {
	case protocol.AskKindProposal, protocol.AskKindCheck:
	case protocol.AskKindSignIn:
		// The ref is the provider key, never a command. An unknown provider
		// has no allowlisted login, so it cannot open a pane.
		if _, ok := protocol.SignInProvider(a.Ref); !ok {
			return protocol.Ask{}, reject("unknown sign-in provider")
		}
	default:
		return protocol.Ask{}, reject("invalid ask kind")
	}
	if strings.TrimSpace(a.Ref) == "" || len(a.Ref) > 200 {
		return protocol.Ask{}, reject("ask ref required")
	}
	ref := node + ":" + a.Ref
	fromKind := protocol.AskFromCTO
	if a.Kind == protocol.AskKindCheck || a.Kind == protocol.AskKindSignIn {
		fromKind = protocol.AskFromGateway
	}
	item, err := s.createInboxItem(s.db, protocol.InboxItem{
		Kind: a.Kind, SessionID: node, Node: node, FromKind: fromKind, AskerName: a.From, SourceRef: a.Kind + ":" + ref,
		AboutKind: a.AboutKind, AboutID: a.AboutID, Text: a.Question, Options: a.Options, Recommendation: a.Recommendation, Detail: a.Detail,
	})
	if err != nil {
		return protocol.Ask{}, err
	}
	return protocol.Ask{ID: item.ID, Kind: item.Kind, Ref: item.SourceRef, Question: item.Text, State: protocol.AskStateOpen, Next: protocol.AskNextOwner, CreatedAt: item.CreatedAt}, nil
}

// CloseExternalAsk closes the ask that OpenExternalAsk opened for a key.
func (s *Store) CloseExternalAsk(node string, c protocol.AskClose) error {
	if c.Reason == "" {
		c.Reason = protocol.AskReasonResolved
	}
	for _, kind := range []string{protocol.AskKindProposal, protocol.AskKindCheck, protocol.AskKindSignIn} {
		if err := s.closeAskRef(s.db, kind+":"+node+":"+c.Ref, c.Reason); err != nil {
			return err
		}
	}
	return nil
}

// stepAskRef is the source key of the ask for one step of one revision.
func stepAskRef(c protocol.Change, ref protocol.StepRef) string {
	return fmt.Sprintf("step:%s:%d:%s:%s:%t", c.ID, c.Revision, ref.Step, ref.For, ref.AfterMerge)
}

// syncStepAsk follows a step report. A needs_owner report is an ask about the
// change; any later report for the same step answers it. The approve step has
// its own written-approval ask and is left to that path.
func (s *Store) syncStepAsk(q askDB, c protocol.Change, e protocol.Evidence) error {
	ref := stepAskRef(c, e.StepRef)
	if e.Result != string(protocol.StepResultNeedsOwner) || e.Step == protocol.StepApprove {
		return s.closeAskRef(q, ref, protocol.AskReasonResolved)
	}
	question := askFirstLine(e.Note, 300)
	if question == "" {
		question = fmt.Sprintf("%s needs you for change %s", e.StepRef.String(), c.ID)
	}
	_, err := s.createInboxItem(q, protocol.InboxItem{
		Kind: protocol.AskKindStep, SessionID: e.By, Node: c.Project, ChangeTitle: c.Title, SourceRef: ref,
		AboutKind: protocol.AskAboutChange, AboutID: c.ID, Text: question, Detail: e.Note,
		WhyYou: "The step reported that only you can decide it.",
	})
	return err
}

// SyncExternalAsks makes the open asks of one kind from one machine equal to
// the list. It is the repair path for OpenExternalAsk and CloseExternalAsk.
func (s *Store) SyncExternalAsks(node string, sync protocol.AskSync) error {
	if len(sync.Asks) > 200 {
		return reject("too many asks")
	}
	keep := map[string]bool{}
	for _, a := range sync.Asks {
		a.Kind = sync.Kind
		if _, err := s.OpenExternalAsk(node, a); err != nil {
			return err
		}
		keep[sync.Kind+":"+node+":"+a.Ref] = true
	}
	rows, err := s.db.Query(`SELECT source_ref FROM inbox_items WHERE status='open' AND kind=? AND session_id=? AND source_ref LIKE ?`, sync.Kind, node, sync.Kind+":"+node+":%")
	if err != nil {
		return err
	}
	var stale []string
	for rows.Next() {
		var ref string
		if err := rows.Scan(&ref); err != nil {
			rows.Close()
			return err
		}
		if !keep[ref] {
			stale = append(stale, ref)
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, ref := range stale {
		if err := s.closeAskRef(s.db, ref, protocol.AskReasonResolved); err != nil {
			return err
		}
	}
	return nil
}

// SettleDeskLog marks a log line as done once its change was undone: the undo
// options go, and the text says so. A second call changes nothing.
func (s *Store) SettleDeskLog(id int64) error {
	_, err := s.db.Exec(`UPDATE desk_log SET options='[]', text='Undone: '||text WHERE id=? AND options<>'[]'`, id)
	return err
}

// askPlainField names a charter field in the owner's words.
func askPlainField(field string) string {
	switch field {
	case "refuses":
		return "what it refuses"
	case "accepts":
		return "what it accepts"
	case "feature_requests":
		return "what it wants"
	case "":
		return "its charter"
	}
	return "its " + strings.ReplaceAll(field, "_", " ")
}

// learnedAskText says what a learned suggestion would change, without the
// node name: the ask is about the node already.
func learnedAskText(v protocol.LearnedSuggestion) string {
	phrase := v.Phrase
	if v.Origin == "owner" && v.RawPhrase != "" {
		phrase = v.RawPhrase
	}
	phrase = askFirstLine(phrase, 120)
	if v.Field == "edge" {
		verb := "add"
		if v.Kind == "remove" {
			verb = "remove"
		}
		return fmt.Sprintf("%s a %s edge to %s", verb, askFirstLine(v.EdgeKind, 40), phrase)
	}
	field := askPlainField(v.Field)
	switch v.Kind {
	case "remove":
		return fmt.Sprintf("remove \"%s\" from %s", phrase, field)
	case "narrow":
		return fmt.Sprintf("change %s to \"%s\"", field, phrase)
	}
	return fmt.Sprintf("add \"%s\" to %s", phrase, field)
}

// learnedAskBecause is the evidence of a learned suggestion, one line each.
func learnedAskBecause(v protocol.LearnedSuggestion) string {
	var lines []string
	for _, e := range v.Evidence {
		if e.Excerpt != "" {
			lines = append(lines, "Because: "+askFirstLine(e.Excerpt, 200))
		}
	}
	return strings.Join(lines, "\n")
}
