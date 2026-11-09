package gateway

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
	"github.com/niparis/agent-net/internal/redact"
)

// StandingOrders lists the owner's standing orders and rules, oldest first.
func (s *Store) StandingOrders() ([]protocol.StandingOrder, error) {
	rows, err := s.db.Query(`SELECT id,text,saved_at,source,tags,pinned,updated_at,kind,area,ref FROM standing_orders WHERE kind IN ('order','rule') ORDER BY saved_at, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []protocol.StandingOrder{}
	for rows.Next() {
		var o protocol.StandingOrder
		var tags string
		if err := rows.Scan(&o.ID, &o.Text, &o.SavedAt, &o.Source, &tags, &o.Pinned, &o.UpdatedAt, &o.Kind, &o.Area, &o.Ref); err != nil {
			return nil, err
		}
		json.Unmarshal([]byte(tags), &o.Tags)
		out = append(out, o)
	}
	return out, rows.Err()
}

// SaveStandingOrder adds an order. Restore puts back a removed one with its
// old ID, time and source.
func (s *Store) SaveStandingOrder(q protocol.StandingOrdersRequest) (protocol.StandingOrder, error) {
	o := protocol.StandingOrder{
		ID: protocol.NewID("so"), SavedAt: time.Now().Unix(), Source: q.Source, Tags: q.Tags,
		Kind: firstNonEmpty(q.Kind, protocol.StandingKindOrder), Area: q.Area, Ref: q.Ref,
	}
	if q.Pinned != nil {
		o.Pinned = *q.Pinned
	}
	if q.Restore != nil {
		o = *q.Restore
	}
	if o.Kind == "" {
		o.Kind = protocol.StandingKindOrder
	}
	text, err := protocol.CleanStandingText(firstNonEmpty(o.Text, q.Text))
	if err != nil {
		return o, err
	}
	o.Text = text
	if o.Source != protocol.StandingSourceCTO {
		o.Source = protocol.StandingSourceOwner
	}
	tags, _ := json.Marshal(o.Tags)
	_, err = s.db.Exec(`INSERT INTO standing_orders(id,text,saved_at,source,tags,pinned,updated_at,kind,area,ref) VALUES(?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(id) DO UPDATE SET text=excluded.text,saved_at=excluded.saved_at,source=excluded.source,tags=excluded.tags,pinned=excluded.pinned,updated_at=excluded.updated_at,kind=excluded.kind,area=excluded.area,ref=excluded.ref`,
		o.ID, o.Text, o.SavedAt, o.Source, string(tags), o.Pinned, o.UpdatedAt, o.Kind, o.Area, o.Ref)
	return o, err
}

// RemoveStandingOrder deletes one order and returns it.
func (s *Store) RemoveStandingOrder(id string) (*protocol.StandingOrder, error) {
	var o protocol.StandingOrder
	var tags string
	err := s.db.QueryRow(`SELECT id,text,saved_at,source,tags,pinned,updated_at,kind,area,ref FROM standing_orders WHERE id=?`, id).Scan(&o.ID, &o.Text, &o.SavedAt, &o.Source, &tags, &o.Pinned, &o.UpdatedAt, &o.Kind, &o.Area, &o.Ref)
	if err != nil {
		return nil, errors.New("no standing order " + id)
	}
	json.Unmarshal([]byte(tags), &o.Tags)
	if _, err := s.db.Exec(`DELETE FROM standing_orders WHERE id=?`, id); err != nil {
		return nil, err
	}
	return &o, nil
}

func (s *Store) UpdateStandingOrder(q protocol.StandingOrdersRequest) (protocol.StandingOrder, error) {
	var o protocol.StandingOrder
	var tags string
	err := s.db.QueryRow(`SELECT id,text,saved_at,source,tags,pinned,updated_at,kind,area,ref FROM standing_orders WHERE id=?`, q.ID).Scan(&o.ID, &o.Text, &o.SavedAt, &o.Source, &tags, &o.Pinned, &o.UpdatedAt, &o.Kind, &o.Area, &o.Ref)
	if err != nil {
		return protocol.StandingOrder{}, errors.New("no standing order " + q.ID)
	}
	json.Unmarshal([]byte(tags), &o.Tags)
	if q.Text != "" {
		o.Text, err = protocol.CleanStandingText(q.Text)
		if err != nil {
			return o, err
		}
	}
	if q.Tags != nil {
		o.Tags = q.Tags
	}
	if q.Pinned != nil {
		o.Pinned = *q.Pinned
	}
	if q.Kind != "" {
		o.Kind = q.Kind
	}
	if q.Area != "" {
		o.Area = q.Area
	}
	if q.Ref != "" {
		o.Ref = q.Ref
	}
	o.UpdatedAt = time.Now().Unix()
	return s.SaveStandingOrder(protocol.StandingOrdersRequest{Restore: &o})
}

func (s *Store) SearchStandingOrders(q protocol.StandingOrdersRequest) ([]protocol.StandingOrder, error) {
	rows, err := s.db.Query(`SELECT o.id,o.text,o.saved_at,o.source,o.tags,o.pinned,o.updated_at,snippet(standing_orders_fts,0,'[',']','...',24),o.kind,o.area,o.ref
 FROM standing_orders_fts JOIN standing_orders o ON o.rowid=standing_orders_fts.rowid
 WHERE standing_orders_fts MATCH ? AND (?='' OR EXISTS(SELECT 1 FROM json_each(o.tags) WHERE value=?))
 AND (?='' OR o.source=?) AND o.saved_at>=? AND o.kind IN ('order','rule') ORDER BY bm25(standing_orders_fts),o.id LIMIT 20`, q.Query, q.Tag, q.Tag, q.Source, q.Source, q.SavedSince)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []protocol.StandingOrder{}
	for rows.Next() {
		var o protocol.StandingOrder
		var tags string
		if err := rows.Scan(&o.ID, &o.Text, &o.SavedAt, &o.Source, &tags, &o.Pinned, &o.UpdatedAt, &o.Snippet, &o.Kind, &o.Area, &o.Ref); err != nil {
			return nil, err
		}
		json.Unmarshal([]byte(tags), &o.Tags)
		out = append(out, o)
	}
	return out, rows.Err()
}

func (s *Store) StandingOrderHistory(id string) ([]protocol.StandingOrder, error) {
	rows, err := s.db.Query(`SELECT order_id,text,tags,changed_at,kind,area,ref FROM standing_order_history WHERE order_id=? ORDER BY rowid`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []protocol.StandingOrder{}
	for rows.Next() {
		var o protocol.StandingOrder
		var tags string
		if err := rows.Scan(&o.ID, &o.Text, &tags, &o.UpdatedAt, &o.Kind, &o.Area, &o.Ref); err != nil {
			return nil, err
		}
		json.Unmarshal([]byte(tags), &o.Tags)
		out = append(out, o)
	}
	return out, rows.Err()
}

// AddJudgment adds a correction or rule to the owner judgment store.
func (s *Store) AddJudgment(q protocol.JudgmentAddRequest) (protocol.StandingOrder, error) {
	return addJudgment(s.db, time.Now(), q)
}

// addJudgment is the one write path of corrections and rules. Owner marks on
// asks use it inside their own transaction.
func addJudgment(db execer, now time.Time, q protocol.JudgmentAddRequest) (protocol.StandingOrder, error) {
	if q.Kind != protocol.StandingKindCorrection && q.Kind != protocol.StandingKindRule {
		return protocol.StandingOrder{}, errors.New("judgment kind must be correction or rule")
	}
	text, err := protocol.CleanStandingText(q.Text)
	if err != nil {
		return protocol.StandingOrder{}, err
	}
	o := protocol.StandingOrder{
		ID:        protocol.NewID("so"),
		Text:      text,
		SavedAt:   now.Unix(),
		Source:    protocol.StandingSourceOwner,
		Kind:      q.Kind,
		Area:      q.Area,
		Ref:       q.Ref,
		Tags:      []string{},
		Pinned:    false,
		UpdatedAt: 0,
	}
	_, err = db.Exec(`INSERT INTO standing_orders(id,text,saved_at,source,tags,pinned,updated_at,kind,area,ref) VALUES(?,?,?,?,?,?,?,?,?,?)`,
		o.ID, o.Text, o.SavedAt, o.Source, "[]", 0, 0, o.Kind, o.Area, o.Ref)
	return o, err
}

// FindJudgments searches the owner judgment store: ranked rows with snippets
// when query is provided, or the newest first when no query is given. With
// Marks it returns only the owner's Desk marks and adds the ask, its whole
// question and what the chief did with it to each row.
func (s *Store) FindJudgments(q protocol.JudgmentsFindRequest) ([]protocol.StandingOrder, error) {
	limit := q.Limit
	if limit <= 0 {
		limit = 20
	} else if limit > 100 {
		limit = 100
	}
	offset := q.Offset
	if offset < 0 {
		offset = 0
	}
	kind, area := q.Kind, q.Area
	if q.Marks {
		kind, area = protocol.StandingKindCorrection, chiefJudgmentArea
	}

	var rows *sql.Rows
	var err error
	if strings.TrimSpace(q.Query) != "" {
		rows, err = s.db.Query(`SELECT o.id,o.text,o.saved_at,o.source,o.tags,o.pinned,o.updated_at,snippet(standing_orders_fts,0,'[',']','...',24),o.kind,o.area,o.ref
 FROM standing_orders_fts JOIN standing_orders o ON o.rowid=standing_orders_fts.rowid
 WHERE standing_orders_fts MATCH ?
   AND (?='' OR o.area=?)
   AND (?='' OR o.kind=?)
   AND (?='' OR o.ref=?)
 ORDER BY bm25(standing_orders_fts), o.id LIMIT ? OFFSET ?`, q.Query, area, area, kind, kind, q.Ref, q.Ref, limit, offset)
	} else {
		rows, err = s.db.Query(`SELECT o.id,o.text,o.saved_at,o.source,o.tags,o.pinned,o.updated_at,'' AS snippet,o.kind,o.area,o.ref
 FROM standing_orders o
 WHERE (?='' OR o.area=?)
   AND (?='' OR o.kind=?)
   AND (?='' OR o.ref=?)
 ORDER BY o.pinned DESC, o.saved_at DESC, o.rowid DESC LIMIT ? OFFSET ?`, area, area, kind, kind, q.Ref, q.Ref, limit, offset)
	}
	if err != nil {
		return nil, err
	}
	out := []protocol.StandingOrder{}
	for rows.Next() {
		var o protocol.StandingOrder
		var tags string
		if err := rows.Scan(&o.ID, &o.Text, &o.SavedAt, &o.Source, &tags, &o.Pinned, &o.UpdatedAt, &o.Snippet, &o.Kind, &o.Area, &o.Ref); err != nil {
			rows.Close()
			return nil, err
		}
		json.Unmarshal([]byte(tags), &o.Tags)
		out = append(out, o)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	if q.Marks {
		for i := range out {
			if err := s.addMarkAsk(&out[i]); err != nil {
				return nil, err
			}
		}
	}
	return out, nil
}

// addMarkAsk puts the ask on a mark row: its whole question, the mark and the
// chief's latest disposition.
func (s *Store) addMarkAsk(o *protocol.StandingOrder) error {
	o.Ask = o.Ref
	o.Mark = markFromText(o.Text)
	if o.Mark != "" {
		o.MarkText = markWords(o.Mark)
	}
	if o.Ask != "" {
		var text string
		err := s.db.QueryRow(`SELECT text FROM inbox_items WHERE id=?`, o.Ask).Scan(&text)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		o.Question = redact.SecretValues(text)
		if o.Disposition, err = s.askDisposition(o.Ask); err != nil {
			return err
		}
	}
	return nil
}

// markFromText names the mark that MarkAsk wrote into the judgment text. It is
// empty for an older row that does not carry one.
func markFromText(text string) string {
	switch {
	case strings.HasPrefix(text, "The owner did not need ask "):
		return protocol.MarkDidntNeedMe
	case strings.HasPrefix(text, "The owner should have been asked about ask "):
		return protocol.MarkShouldHaveAsked
	}
	return ""
}

// askDisposition is the chief's latest sort of one ask in one line.
func (s *Store) askDisposition(id string) (string, error) {
	var result, into, to, reason, why string
	var priority int
	err := s.db.QueryRow(`SELECT result,into_id,to_id,reason,priority,why_you FROM ask_sorts WHERE ask_id=? ORDER BY id DESC LIMIT 1`, id).
		Scan(&result, &into, &to, &reason, &priority, &why)
	if errors.Is(err, sql.ErrNoRows) {
		return "not sorted", nil
	}
	if err != nil {
		return "", err
	}
	line := ""
	switch result {
	case protocol.AskSortClose:
		line = "closed: " + reason
	case protocol.AskSortMerge:
		line = "merged into " + into + ": " + reason
	case protocol.AskSortSend:
		line = "sent to " + nextName(s.db, to) + ": " + reason
	case protocol.AskSortOwner:
		line = "sent to the owner"
		if priority > 0 {
			line += fmt.Sprintf(" (priority %d)", priority)
		}
		if why != "" {
			line += ": " + why
		}
	case protocol.AskSortUndo:
		line = "undone: " + reason
	default:
		line = result + ": " + reason
	}
	return redact.SecretValues(line), nil
}

// BackfillAnswerJudgments backfills answered owner asks into standing_orders as kind=answer (newest 500).
func (s *Store) BackfillAnswerJudgments() error {
	_, err := s.db.Exec(`INSERT INTO standing_orders(id,text,saved_at,source,tags,pinned,updated_at,kind,area,ref)
SELECT
 'so_b_' || lower(hex(randomblob(8))),
 'Q: ' || trim(i.text) || ' / A: ' || trim(i.answer),
 COALESCE(NULLIF(i.answered_at / 1000000000, 0), i.created_at / 1000000000, unixepoch()),
 'owner',
 '[]',
 0,
 0,
 'answer',
 COALESCE(
   NULLIF((SELECT c.project FROM delivery_changes c WHERE c.id=i.about_id AND i.about_kind='change'), ''),
   NULLIF((SELECT c.project FROM delivery_step_inbox d JOIN delivery_changes c ON c.id=d.change_id WHERE d.item_id=i.id), ''),
   CASE WHEN i.about_kind IN ('project', 'repo', 'team') THEN NULLIF(i.about_id, '') ELSE NULL END,
   NULLIF((SELECT p.project FROM profiles p WHERE p.session_id=i.session_id), ''),
   NULLIF((SELECT s.repo_project FROM sessions s WHERE s.id=i.session_id), ''),
   NULLIF((SELECT s.repo_id FROM sessions s WHERE s.id=i.session_id), ''),
   NULLIF(i.node, ''),
   ''
 ),
 i.id
FROM inbox_items i
WHERE i.status IN ('answered', 'approved', 'refused') AND i.answer<>''
  AND NOT EXISTS (SELECT 1 FROM standing_orders WHERE ref=i.id)
ORDER BY COALESCE(NULLIF(i.answered_at, 0), i.created_at) DESC
LIMIT 500;`)
	if err != nil {
		return err
	}
	_, _ = s.db.Exec(`INSERT INTO standing_orders_fts(standing_orders_fts) VALUES('rebuild')`)
	return nil
}
