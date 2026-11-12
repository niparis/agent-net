package gateway

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"

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

// StandingNotes lists the gateway notes, newest first, with the stale flag.
func (s *Store) StandingNotes() ([]protocol.StandingOrder, error) {
	rows, err := s.db.Query(`SELECT ` + noteColumns + ` FROM standing_orders WHERE name<>'' ORDER BY saved_at DESC, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []protocol.StandingOrder{}
	now := time.Now()
	for rows.Next() {
		o, err := scanNote(rows)
		if err != nil {
			return nil, err
		}
		o.Stale = noteStale(o, now)
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
	// Only the undo of a removed note keeps the agent source.
	if o.Source != protocol.StandingSourceCTO && !(q.Restore != nil && o.Source == protocol.StandingSourceAgent) {
		o.Source = protocol.StandingSourceOwner
	}
	if o.Name == "" {
		o.Scope, o.ScopeRef = protocol.NoteScopeOwner, ""
	}
	if o.Scope == "" {
		o.Scope = protocol.NoteScopeOwner
	}
	tags, _ := json.Marshal(o.Tags)
	_, err = s.db.Exec(`INSERT INTO standing_orders(id,text,saved_at,source,tags,pinned,updated_at,kind,area,ref,scope,scope_ref,name,description,used_at,uses) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(id) DO UPDATE SET text=excluded.text,saved_at=excluded.saved_at,source=excluded.source,tags=excluded.tags,pinned=excluded.pinned,updated_at=excluded.updated_at,kind=excluded.kind,area=excluded.area,ref=excluded.ref,scope=excluded.scope,scope_ref=excluded.scope_ref,name=excluded.name,description=excluded.description,used_at=excluded.used_at,uses=excluded.uses`,
		o.ID, o.Text, o.SavedAt, o.Source, string(tags), o.Pinned, o.UpdatedAt, o.Kind, o.Area, o.Ref, o.Scope, o.ScopeRef, o.Name, o.Description, o.UsedAt, o.Uses)
	return o, err
}

// RemoveStandingOrder deletes one order and returns it.
func (s *Store) RemoveStandingOrder(id string) (*protocol.StandingOrder, error) {
	o, err := scanNote(s.db.QueryRow(`SELECT `+noteColumns+` FROM standing_orders WHERE id=?`, id))
	if err != nil {
		return nil, errors.New("no standing order " + id)
	}
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
	if ftsMatch(q.Query) == "" {
		return []protocol.StandingOrder{}, nil
	}
	rows, err := s.db.Query(`SELECT o.id,o.text,o.saved_at,o.source,o.tags,o.pinned,o.updated_at,snippet(standing_orders_fts,0,'[',']','...',24),o.kind,o.area,o.ref
 FROM standing_orders_fts JOIN standing_orders o ON o.rowid=standing_orders_fts.rowid
 WHERE standing_orders_fts MATCH ? AND (?='' OR EXISTS(SELECT 1 FROM json_each(o.tags) WHERE value=?))
 AND (?='' OR o.source=?) AND o.saved_at>=? AND o.kind IN ('order','rule') ORDER BY bm25(standing_orders_fts),o.id LIMIT 20`, ftsMatch(q.Query), q.Tag, q.Tag, q.Source, q.Source, q.SavedSince)
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

	// Gateway notes are judgments too, but an agent's note is not the owner's
	// past decision: they come only when the kind names a note kind or the
	// caller asks for the stale ones.
	notes, stale := 0, 0
	switch kind {
	case protocol.StandingKindFeedback, protocol.StandingKindFact, protocol.StandingKindReference:
		notes = 1
	}
	var cutoff int64
	if q.Stale {
		notes, stale = 1, 1
		cutoff = time.Now().Add(-time.Duration(protocol.NotesStaleDays) * 24 * time.Hour).Unix()
	}
	const cols = `o.id,o.text,o.saved_at,o.source,o.tags,o.pinned,o.updated_at,%s,o.kind,o.area,o.ref,o.scope,o.scope_ref,o.name,o.description,o.used_at,o.uses`
	const noteFilter = ` AND (o.name='' OR ?=1) AND (?=0 OR (o.name<>'' AND MAX(o.used_at,o.saved_at,o.updated_at)<?))`
	var rows *sql.Rows
	var err error
	match := ftsMatch(q.Query)
	if match != "" {
		rows, err = s.db.Query(fmt.Sprintf(`SELECT `+cols, `snippet(standing_orders_fts,0,'[',']','...',24)`)+`
 FROM standing_orders_fts JOIN standing_orders o ON o.rowid=standing_orders_fts.rowid
 WHERE standing_orders_fts MATCH ?
   AND (?='' OR o.area=?)
   AND (?='' OR o.kind=?)
   AND (?='' OR o.ref=?)`+noteFilter+`
 ORDER BY bm25(standing_orders_fts), o.id LIMIT ? OFFSET ?`, match, area, area, kind, kind, q.Ref, q.Ref, notes, stale, cutoff, limit, offset)
	} else {
		rows, err = s.db.Query(fmt.Sprintf(`SELECT `+cols, `''`)+`
 FROM standing_orders o
 WHERE (?='' OR o.area=?)
   AND (?='' OR o.kind=?)
   AND (?='' OR o.ref=?)`+noteFilter+`
 ORDER BY o.pinned DESC, o.saved_at DESC, o.rowid DESC LIMIT ? OFFSET ?`, area, area, kind, kind, q.Ref, q.Ref, notes, stale, cutoff, limit, offset)
	}
	if err != nil {
		return nil, err
	}
	out := []protocol.StandingOrder{}
	for rows.Next() {
		var o protocol.StandingOrder
		var tags string
		if err := rows.Scan(&o.ID, &o.Text, &o.SavedAt, &o.Source, &tags, &o.Pinned, &o.UpdatedAt, &o.Snippet, &o.Kind, &o.Area, &o.Ref, &o.Scope, &o.ScopeRef, &o.Name, &o.Description, &o.UsedAt, &o.Uses); err != nil {
			rows.Close()
			return nil, err
		}
		json.Unmarshal([]byte(tags), &o.Tags)
		o.Stale = noteStale(o, time.Now())
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

// MarksStored counts the owner's Desk marks in the judgment store.
func (s *Store) MarksStored() (int, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM standing_orders WHERE kind=? AND area=?`, protocol.StandingKindCorrection, chiefJudgmentArea).Scan(&n)
	return n, err
}

// marksNote explains an empty marks result.
func marksNote(stored, offset int) string {
	if stored == 0 {
		return "0 owner marks are stored: the owner has not marked an ask yet."
	}
	return fmt.Sprintf("%d owner marks are stored, none at offset %d.", stored, offset)
}

// ftsMatch turns a search the way a person types it into an FTS5 MATCH
// expression. Every term becomes a quoted string, so a hyphen, a colon or a
// stray quote is text and not FTS5 syntax. Terms join with AND. An upper-case
// OR between two terms stays OR, a "quoted run" stays one phrase, and a
// trailing * on a term stays a prefix match.
func ftsMatch(query string) string {
	var parts []string
	rs := []rune(query)
	for i := 0; i < len(rs); {
		if unicode.IsSpace(rs[i]) {
			i++
			continue
		}
		var term string
		quoted := rs[i] == '"'
		if quoted {
			j := i + 1
			for j < len(rs) && rs[j] != '"' {
				j++
			}
			term = string(rs[i+1 : j])
			i = j
			if i < len(rs) {
				i++
			}
		} else {
			j := i
			for j < len(rs) && !unicode.IsSpace(rs[j]) {
				j++
			}
			term = string(rs[i:j])
			i = j
		}
		prefix := false
		if quoted {
			if i < len(rs) && rs[i] == '*' {
				prefix = true
				i++
			}
		} else {
			if term == "OR" {
				parts = append(parts, "OR")
				continue
			}
			if strings.HasSuffix(term, "*") && len(term) > 1 {
				prefix = true
				term = strings.TrimRight(term, "*")
			}
		}
		if strings.TrimSpace(term) == "" {
			continue
		}
		part := `"` + strings.ReplaceAll(term, `"`, `""`) + `"`
		if prefix {
			part += "*"
		}
		parts = append(parts, part)
	}
	// An OR needs a term on both sides.
	var out []string
	for i, p := range parts {
		if p == "OR" && (len(out) == 0 || out[len(out)-1] == "OR" || i == len(parts)-1) {
			continue
		}
		out = append(out, p)
	}
	return strings.Join(out, " ")
}
