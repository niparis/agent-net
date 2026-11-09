package gateway

import (
	"database/sql"
	"errors"
	"strings"

	"github.com/niparis/agent-net/internal/protocol"
	"github.com/niparis/agent-net/internal/redact"
)

// The owner judgment store keeps one answer row for each ask the owner
// decided (kind answer, ref the ask ID). Every path that sets an ask to
// answered, approved or refused, or that the owner dismisses, calls
// recordOwnerAnswer in the transaction that closes the ask. The row says what
// was decided in plain words. The ask itself keeps its answer code.

// askWordsLimit is how much of an ask a decision line quotes.
const askWordsLimit = 200

// ownerAnswerWords says what an answer meant, in the owner's words where he
// typed them and in plain words where he pressed a button.
func (s *Store) ownerAnswerWords(q askDB, item protocol.InboxItem, answer string) string {
	answer = strings.TrimSpace(answer)
	first := askFirstLine(item.Text, askWordsLimit)
	switch {
	case answer == "dismiss" || answer == "seen" || answer == "dismissed_by_owner":
		return "dismissed: no decision"
	case item.Kind == protocol.AskKindRejection || item.Kind == protocol.AskKindEscalation:
		if words, ok := s.outcomeWords(q, item, answer); ok {
			return words
		}
	case item.Kind == protocol.AskKindDraft && (answer == "accept" || answer == "reject"):
		if answer == "accept" {
			return "accepted: " + first
		}
		return "rejected: " + first
	case answer == "approve":
		return "approved: " + first
	case strings.HasPrefix(answer, "approve {"):
		return "approved: " + first + " (fields " + strings.TrimSpace(strings.TrimPrefix(answer, "approve")) + ")"
	case answer == "refuse":
		return "refused: " + first
	case answer == protocol.LoopAnswerResume || answer == protocol.LoopAnswerStop:
		var ref string
		_ = q.QueryRow(`SELECT source_ref FROM inbox_items WHERE id=?`, item.ID).Scan(&ref)
		if strings.HasPrefix(ref, "loop:") {
			if answer == protocol.LoopAnswerResume {
				return "resumed the loop: " + first
			}
			return "stopped the loop: " + first
		}
	}
	return answer
}

// outcomeWords says what the owner decided about a rejection or an escalation
// of a request between two agents: what was refused and why, and for an
// overrule what request went through.
func (s *Store) outcomeWords(q askDB, item protocol.InboxItem, answer string) (string, bool) {
	if answer != "overrule" && answer != "let_stand" {
		return "", false
	}
	var outcome, reason, body string
	err := q.QueryRow(`SELECT o.outcome,o.reason,m.body FROM graph_outcomes o JOIN messages m ON m.id=o.message_id WHERE o.reply_id=?`, item.MessageID).Scan(&outcome, &reason, &body)
	if err != nil {
		return "", false
	}
	what := "rejection"
	if outcome == "escalate" {
		what = "escalation"
	}
	why := ""
	if r := strings.TrimSpace(reason); r != "" {
		why = " (" + r + ")"
	}
	if answer == "let_stand" {
		return "let the " + what + " stand" + why, true
	}
	return "overruled the " + what + why + ": the request went through as asked: " + strings.TrimSpace(body), true
}

// recordOwnerAnswer writes the answer judgment of one ask from the answer the
// owner gave (or the CTO gave for him).
func (s *Store) recordOwnerAnswer(q askDB, item protocol.InboxItem, answer string) error {
	return s.recordOwnerWords(q, item, s.ownerAnswerWords(q, item, answer))
}

// recordOwnerWords writes the answer judgment of one ask with the words of the
// decision. An ask has one row: a second answer updates it.
func (s *Store) recordOwnerWords(q askDB, item protocol.InboxItem, words string) error {
	savedAt := s.now().Unix()
	if !item.AnsweredAt.IsZero() {
		savedAt = item.AnsweredAt.Unix()
	}
	return upsertAnswerJudgment(q, s.askArea(q, item), item, words, savedAt, "so")
}

// upsertAnswerJudgment inserts the answer row of an ask, or updates it when
// the words or the area changed. The triggers of standing_orders keep the
// search index and the history in step.
func upsertAnswerJudgment(q askDB, area string, item protocol.InboxItem, words string, savedAt int64, idPrefix string) error {
	text := "Q: " + strings.TrimSpace(item.Text) + " / A: " + strings.TrimSpace(words)
	var id, oldText string
	err := q.QueryRow(`SELECT id,text FROM standing_orders WHERE kind=? AND ref=? ORDER BY rowid LIMIT 1`, protocol.StandingKindAnswer, item.ID).Scan(&id, &oldText)
	if errors.Is(err, sql.ErrNoRows) {
		_, err = q.Exec(`INSERT INTO standing_orders(id,text,saved_at,source,tags,pinned,updated_at,kind,area,ref) VALUES(?,?,?,?,?,?,?,?,?,?)`,
			protocol.NewID(idPrefix), text, savedAt, protocol.StandingSourceOwner, "[]", 0, 0, protocol.StandingKindAnswer, area, item.ID)
		return err
	}
	if err != nil {
		return err
	}
	if oldText == text {
		return nil
	}
	_, err = q.Exec(`UPDATE standing_orders SET text=?,area=?,saved_at=?,updated_at=? WHERE id=?`, text, area, savedAt, savedAt, id)
	return err
}

// answerBackfillLimit is how many of the newest answered asks one backfill reads.
const answerBackfillLimit = 500

// BackfillAnswerJudgments gives every answered ask its judgment row and puts a
// decision in plain words where an older row holds a bare code. The gateway
// runs it at start. It is safe to run again: a row that already says the same
// is left alone, and a mark of the same ask is not an answer.
func (s *Store) BackfillAnswerJudgments() error {
	items, err := scanInboxRows(s.db.Query(`SELECT `+inboxColumns+` FROM inbox_items WHERE status IN ('answered','approved','refused') AND answer<>''
		ORDER BY COALESCE(NULLIF(answered_at,0),created_at) DESC LIMIT ?`, answerBackfillLimit))
	if err != nil {
		return err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, item := range items {
		savedAt := item.AnsweredAt.Unix()
		if item.AnsweredAt.IsZero() {
			savedAt = item.CreatedAt.Unix()
		}
		if err := upsertAnswerJudgment(tx, s.askArea(tx, item), item, s.ownerAnswerWords(tx, item, item.Answer), savedAt, "so_b"); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// closeAnsweredItem sets an ask to its answered status and writes its
// judgment, in one transaction. It is for the paths that decide a charter
// draft or a loop ask, which run their own work before the ask closes. The
// store has one connection: the caller holds no transaction.
func (s *Store) closeAnsweredItem(item protocol.InboxItem, answer string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`UPDATE inbox_items SET status=?,answer=?,answered_at=? WHERE id=?`, item.Status, answer, item.AnsweredAt.UnixNano(), item.ID); err != nil {
		return err
	}
	if err := s.recordOwnerAnswer(tx, item, answer); err != nil {
		return err
	}
	return tx.Commit()
}

// scanInboxRows reads every ask of a query and closes the rows, so the caller
// can write on the same connection.
func scanInboxRows(rows *sql.Rows, err error) ([]protocol.InboxItem, error) {
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var items []protocol.InboxItem
	for rows.Next() {
		item, err := scanInbox(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

// ownerRepliedNoText is the decision line when the harness did not send what
// the owner typed.
const ownerRepliedNoText = "replied in the asking session (the text was not captured)"

// closeOwnerRepliedAsks closes the open asks of a session because the owner
// typed in it, and writes each judgment with his text. An ask that his reply
// closes is decided by that reply: the text is the decision.
func (s *Store) closeOwnerRepliedAsks(session, text string) error {
	text = strings.TrimSpace(text)
	if len(text) > protocol.MaxBodyBytes {
		text = text[:protocol.MaxBodyBytes]
	}
	words := redact.SecretValues(text)
	if words == "" {
		words = ownerRepliedNoText
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	cond, args := sessionOwnerAskCond(session, 0, nil)
	asks, err := scanInboxRows(tx.Query(`SELECT `+inboxColumns+` FROM inbox_items WHERE status='open' AND (`+cond+`)`, args...))
	if err != nil {
		return err
	}
	if _, err := s.closeSessionOwnerAsks(tx, session, 0, protocol.AskReasonOwnerReplied); err != nil {
		return err
	}
	for _, ask := range asks {
		if err := s.recordOwnerWords(tx, ask, words); err != nil {
			return err
		}
	}
	return tx.Commit()
}
