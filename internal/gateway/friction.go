package gateway

import (
	"database/sql"
	"errors"
	"sort"
	"strconv"
	"strings"

	"github.com/niparis/agent-net/internal/protocol"
	"github.com/niparis/agent-net/internal/redact"
)

// Friction reports (decision 0170). One row for each thing that slowed an
// agent down. The text is worker-written data: it is redacted before storage
// and quoted on the way out, so a reader does not take it for an instruction.
const frictionMigration = `
-- Friction reports (decision 0170): one row per thing that slowed an agent.
-- digest_id stays empty until a T2 digest carries the row.
CREATE TABLE IF NOT EXISTS friction (
 id INTEGER PRIMARY KEY AUTOINCREMENT,
 project TEXT NOT NULL DEFAULT '',
 session TEXT NOT NULL DEFAULT '',
 change_id TEXT NOT NULL DEFAULT '',
 harness TEXT NOT NULL DEFAULT '',
 model TEXT NOT NULL DEFAULT '',
 area TEXT NOT NULL,
 text TEXT NOT NULL,
 created_at INTEGER NOT NULL,
 digest_id TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS friction_recent ON friction(created_at DESC, id DESC);
CREATE INDEX IF NOT EXISTS friction_project ON friction(project, created_at DESC);
CREATE INDEX IF NOT EXISTS friction_area ON friction(area, created_at DESC);
`

// frictionStatusMigration keeps the read state of each report (decision 0175).
// friction_status_history keeps every mark, oldest first, and the current mark
// of a report is its latest row. A report with no row is new. The table
// friction_status that held the current mark was dropped, as a copy of it.
const frictionStatusMigration = `
-- Friction read status (decision 0175): the current mark of a report.
CREATE TABLE IF NOT EXISTS friction_status (
 friction_id INTEGER PRIMARY KEY,
 status TEXT NOT NULL,
 changed_by TEXT NOT NULL DEFAULT '',
 changed_at INTEGER NOT NULL,
 link TEXT NOT NULL DEFAULT '',
 note TEXT NOT NULL DEFAULT ''
);
-- Every mark a report received, appended and never edited.
CREATE TABLE IF NOT EXISTS friction_status_history (
 id INTEGER PRIMARY KEY AUTOINCREMENT,
 friction_id INTEGER NOT NULL,
 status TEXT NOT NULL,
 changed_by TEXT NOT NULL DEFAULT '',
 changed_at INTEGER NOT NULL,
 link TEXT NOT NULL DEFAULT '',
 note TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS friction_status_history_report ON friction_status_history(friction_id, id);
`

// frictionReadLimit bounds one page. A caller asking for more gets a full page
// and a cursor, never a refusal.
const frictionReadLimit = 200

// frictionDefaultLimit is the page size when the caller gives none.
const frictionDefaultLimit = 20

// frictionSampleLimit bounds the ids a mark reply names. The count is exact.
const frictionSampleLimit = 20

// frictionFrom joins each report to its current mark: its latest history row.
// A report with no mark reads as new.
const frictionFrom = " FROM friction f LEFT JOIN friction_status_history st ON st.id=(SELECT MAX(id) FROM friction_status_history WHERE friction_id=f.id)"

// frictionCols are the columns a read returns, in the order it scans them.
const frictionCols = "f.id,f.project,f.session,f.change_id,f.harness,f.model,f.area,f.text,f.created_at," +
	"COALESCE(st.status,'new'),COALESCE(st.changed_by,''),COALESCE(st.changed_at,0),COALESCE(st.link,''),COALESCE(st.note,'')"

// ReportFriction stores one hand-back's list. It returns how many rows it
// stored; a report with no entries stores nothing and is fine.
func (s *Store) ReportFriction(q protocol.FrictionReportRequest) (protocol.FrictionReportResult, error) {
	var out protocol.FrictionReportResult
	if err := q.Validate(); err != nil {
		return out, reject("%s", err.Error())
	}
	if len(q.Entries) == 0 {
		return out, nil
	}
	at := s.now().Unix()
	tx, err := s.db.Begin()
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	if err := insertFrictionTx(tx, q, at); err != nil {
		return out, err
	}
	if err := tx.Commit(); err != nil {
		return out, err
	}
	out.Stored = len(q.Entries)
	return out, nil
}

// insertFrictionTx writes the entries in the caller's transaction. The text is
// redacted here, at the one place every path stores it.
func insertFrictionTx(tx *sql.Tx, q protocol.FrictionReportRequest, at int64) error {
	for _, f := range q.Entries {
		text := redact.Secrets(protocol.CleanFrictionText(f.Text))
		if _, err := tx.Exec(`INSERT INTO friction(project,session,change_id,harness,model,area,text,created_at)
 VALUES(?,?,?,?,?,?,?,?)`, q.Project, q.SessionID, q.ChangeID, q.Harness, q.Model, f.Area, text, at); err != nil {
			return err
		}
	}
	return nil
}

// frictionFilter is the WHERE clause that a read, its counts and a mark share.
// It filters on the current status, and an empty status filter matches all.
func frictionFilter(project, area, status, text string, since, id int64) (string, []any) {
	where := " WHERE 1=1"
	var args []any
	if project != "" {
		where += " AND f.project=?"
		args = append(args, project)
	}
	if area != "" {
		where += " AND f.area=?"
		args = append(args, area)
	}
	if status != "" {
		where += " AND COALESCE(st.status,'new')=?"
		args = append(args, status)
	}
	if text != "" {
		where += " AND instr(lower(f.text),lower(?))>0"
		args = append(args, text)
	}
	if since > 0 {
		where += " AND f.created_at>=?"
		args = append(args, since)
	}
	if id > 0 {
		where += " AND f.id=?"
		args = append(args, id)
	}
	return where, args
}

// frictionIn is the placeholder list of an IN clause with n values.
func frictionIn(n int) string {
	return strings.TrimSuffix(strings.Repeat("?,", n), ",")
}

// frictionIDsInGroup returns the ids in the group among the reports the WHERE
// clause holds, oldest first. A group is computed from the text, so the rows are
// read and compared here; only the ids are kept.
func (s *Store) frictionIDsInGroup(where string, args []any, group string) ([]int64, error) {
	rows, err := s.db.Query(`SELECT f.id,f.area,f.text`+frictionFrom+where+` ORDER BY f.id`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []int64
	for rows.Next() {
		var id int64
		var area, text string
		if err := rows.Scan(&id, &area, &text); err != nil {
			return nil, err
		}
		if protocol.FrictionGroup(area, text) == group {
			ids = append(ids, id)
		}
	}
	return ids, rows.Err()
}

// FrictionFind reads the stored reports, newest first, one page at a time. The
// counts cover every report the filter holds. Text is returned quoted: it is
// data, not an instruction.
func (s *Store) FrictionFind(q protocol.FrictionQuery) (protocol.FrictionFindResult, error) {
	out := protocol.FrictionFindResult{Entries: []protocol.FrictionView{}, Counts: []protocol.FrictionProjectCount{}}
	if err := q.Validate(); err != nil {
		return out, reject("%s", err.Error())
	}
	limit := q.Limit
	if limit <= 0 {
		limit = frictionDefaultLimit
	}
	if limit > frictionReadLimit {
		limit = frictionReadLimit
	}
	var since int64
	if q.Since != "" {
		at, err := protocol.ParseFrictionSince(q.Since)
		if err != nil {
			return out, reject("%s", err.Error())
		}
		since = at
	}
	where, args := frictionFilter(q.Project, q.Area, q.Status, q.Text, since, q.ID)
	if q.Group != "" {
		ids, err := s.frictionIDsInGroup(where, args, q.Group)
		if err != nil {
			return out, err
		}
		if len(ids) == 0 {
			return out, nil
		}
		where += " AND f.id IN (" + frictionIn(len(ids)) + ")"
		for _, id := range ids {
			args = append(args, id)
		}
	}
	if err := s.frictionCounts(&out, where, args); err != nil {
		return out, err
	}
	page := where
	pageArgs := append([]any(nil), args...)
	if q.Cursor != "" {
		cursor, _ := strconv.ParseInt(q.Cursor, 10, 64)
		page += " AND f.id<?"
		pageArgs = append(pageArgs, cursor)
	}
	// One row more than the page tells whether another page follows.
	rows, err := s.db.Query(`SELECT `+frictionCols+frictionFrom+page+` ORDER BY f.id DESC LIMIT ?`, append(pageArgs, limit+1)...)
	if err != nil {
		return out, err
	}
	more := false
	for rows.Next() {
		if len(out.Entries) == limit {
			more = true
			break
		}
		var v protocol.FrictionView
		var text string
		if err := rows.Scan(&v.ID, &v.Project, &v.Session, &v.Change, &v.Harness, &v.Model, &v.Area, &text, &v.CreatedAt,
			&v.Status, &v.ChangedBy, &v.ChangedAt, &v.Link, &v.Note); err != nil {
			rows.Close()
			return out, err
		}
		v.Group = protocol.FrictionGroup(v.Area, text)
		v.Text = strconv.Quote(text)
		out.Entries = append(out.Entries, v)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return out, err
	}
	if more {
		out.NextCursor = strconv.FormatInt(out.Entries[len(out.Entries)-1].ID, 10)
	}
	if q.ID > 0 && len(out.Entries) == 1 {
		history, err := s.frictionHistory(out.Entries[0].ID)
		if err != nil {
			return out, err
		}
		out.Entries[0].History = history
	}
	return out, nil
}

// frictionCounts fills the totals and the per-project counts of the WHERE
// clause. It reads no page, so a reader can count without paging.
func (s *Store) frictionCounts(out *protocol.FrictionFindResult, where string, args []any) error {
	rows, err := s.db.Query(`SELECT f.project,COUNT(*),SUM(CASE WHEN COALESCE(st.status,'new')='new' THEN 1 ELSE 0 END)`+frictionFrom+where+` GROUP BY f.project`, args...)
	if err != nil {
		return err
	}
	for rows.Next() {
		var c protocol.FrictionProjectCount
		if err := rows.Scan(&c.Project, &c.Count, &c.Unread); err != nil {
			rows.Close()
			return err
		}
		out.Counts = append(out.Counts, c)
		out.Total += c.Count
		out.Unread += c.Unread
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	sort.Slice(out.Counts, func(i, j int) bool {
		if out.Counts[i].Count != out.Counts[j].Count {
			return out.Counts[i].Count > out.Counts[j].Count
		}
		return out.Counts[i].Project < out.Counts[j].Project
	})
	return nil
}

// frictionHistory returns every mark a report received, oldest first.
func (s *Store) frictionHistory(id int64) ([]protocol.FrictionStatusChange, error) {
	rows, err := s.db.Query(`SELECT status,changed_by,changed_at,link,note FROM friction_status_history WHERE friction_id=? ORDER BY id`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []protocol.FrictionStatusChange
	for rows.Next() {
		var c protocol.FrictionStatusChange
		if err := rows.Scan(&c.Status, &c.ChangedBy, &c.ChangedAt, &c.Link, &c.Note); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// MarkFriction sets the read status of the reports the request picks, and
// writes one history row for each. actor is who made the mark: "owner" or
// "cto". A dry run counts the matches and writes nothing. An id that names no
// report matches nothing.
func (s *Store) MarkFriction(actor string, q protocol.FrictionMarkRequest) (protocol.FrictionMarkResult, error) {
	out := protocol.FrictionMarkResult{Status: q.Status, DryRun: q.DryRun, SampleIDs: []int64{}}
	if err := q.Validate(); err != nil {
		return out, reject("%s", err.Error())
	}
	ids, err := s.frictionMarkIDs(q)
	if err != nil {
		return out, err
	}
	out.Matched = len(ids)
	for i, id := range ids {
		if i == frictionSampleLimit {
			break
		}
		out.SampleIDs = append(out.SampleIDs, id)
	}
	if q.DryRun || len(ids) == 0 {
		return out, nil
	}
	at := s.now().Unix()
	link := redact.Secrets(protocol.CleanFrictionText(q.Link))
	note := redact.Secrets(protocol.CleanFrictionText(q.Note))
	tx, err := s.db.Begin()
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	for _, id := range ids {
		if _, err := tx.Exec(`INSERT INTO friction_status_history(friction_id,status,changed_by,changed_at,link,note) VALUES(?,?,?,?,?,?)`,
			id, q.Status, actor, at, link, note); err != nil {
			return out, err
		}
	}
	if err := tx.Commit(); err != nil {
		return out, err
	}
	out.Marked = len(ids)
	return out, nil
}

// MarkFrictionAsLead is the agent path of MarkFriction. Only the pinned lead
// session of an Agent may set the status (decision 0175): any other session is
// refused before anything is written.
func (s *Store) MarkFrictionAsLead(session string, q protocol.FrictionMarkRequest) (protocol.FrictionMarkResult, error) {
	lead, err := s.frictionLeadSession(session)
	if err != nil {
		return protocol.FrictionMarkResult{}, err
	}
	if !lead {
		return protocol.FrictionMarkResult{}, reject("only a lead session can set the status of friction reports")
	}
	return s.MarkFriction(session, q)
}

// frictionLeadSession says whether a session is the pinned lead session of its
// Agent. This is the same rule the lead routing uses (isLeadSessionQ).
func (s *Store) frictionLeadSession(session string) (bool, error) {
	var agent, worker string
	err := s.db.QueryRow(`SELECT COALESCE(agent_id,''),COALESCE(worker,'') FROM sessions WHERE id=?`, session).Scan(&agent, &worker)
	// A worker never leads, even when a pin names its session.
	if errors.Is(err, sql.ErrNoRows) || agent == "" || worker != "" {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	var pin string
	if err := s.db.QueryRow(`SELECT COALESCE(lead_session,'') FROM agents WHERE id=?`, agent).Scan(&pin); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}
		return false, err
	}
	return pin != "" && pin == session, nil
}

// frictionMarkIDs returns the ids a mark picks, oldest first. Ids that name no
// report are dropped.
func (s *Store) frictionMarkIDs(q protocol.FrictionMarkRequest) ([]int64, error) {
	switch {
	case len(q.IDs) > 0:
		args := make([]any, 0, len(q.IDs))
		for _, id := range q.IDs {
			args = append(args, id)
		}
		return s.frictionIDRows(" WHERE f.id IN ("+frictionIn(len(q.IDs))+")", args)
	case q.Group != "":
		return s.frictionIDsInGroup(" WHERE 1=1", nil, q.Group)
	default:
		where, args := frictionFilter(q.Project, q.Area, "", q.Text, 0, 0)
		return s.frictionIDRows(where, args)
	}
}

// frictionIDRows returns the ids of the reports the WHERE clause holds.
func (s *Store) frictionIDRows(where string, args []any) ([]int64, error) {
	rows, err := s.db.Query(`SELECT f.id`+frictionFrom+where+` ORDER BY f.id`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}
