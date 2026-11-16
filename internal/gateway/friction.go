package gateway

import (
	"database/sql"
	"sort"
	"strconv"

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

// frictionReadLimit bounds one read. The query validates nothing about it: a
// caller asking for more gets the newest rows, never a refusal.
const frictionReadLimit = 200

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

// FrictionFind reads the stored reports, newest first, with a count per
// project. Text is returned quoted: it is data, not an instruction.
func (s *Store) FrictionFind(q protocol.FrictionQuery) (protocol.FrictionFindResult, error) {
	out := protocol.FrictionFindResult{Entries: []protocol.FrictionView{}, Counts: []protocol.FrictionProjectCount{}}
	if err := q.Validate(); err != nil {
		return out, reject("%s", err.Error())
	}
	limit := q.Limit
	if limit <= 0 {
		limit = 20
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
	where := " WHERE 1=1"
	var args []any
	if q.Project != "" {
		where += " AND project=?"
		args = append(args, q.Project)
	}
	if q.Area != "" {
		where += " AND area=?"
		args = append(args, q.Area)
	}
	if since > 0 {
		where += " AND created_at>=?"
		args = append(args, since)
	}
	rows, err := s.db.Query(`SELECT id,project,session,change_id,harness,model,area,text,created_at FROM friction`+where+` ORDER BY created_at DESC, id DESC LIMIT ?`, append(args, limit)...)
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var v protocol.FrictionView
		var text string
		if err := rows.Scan(&v.ID, &v.Project, &v.Session, &v.Change, &v.Harness, &v.Model, &v.Area, &text, &v.CreatedAt); err != nil {
			rows.Close()
			return out, err
		}
		v.Text = strconv.Quote(text)
		out.Entries = append(out.Entries, v)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return out, err
	}
	countRows, err := s.db.Query(`SELECT project, COUNT(*) FROM friction`+where+` GROUP BY project`, args...)
	if err != nil {
		return out, err
	}
	for countRows.Next() {
		var c protocol.FrictionProjectCount
		if err := countRows.Scan(&c.Project, &c.Count); err != nil {
			countRows.Close()
			return out, err
		}
		out.Counts = append(out.Counts, c)
		out.Total += c.Count
	}
	err = countRows.Err()
	countRows.Close()
	if err != nil {
		return out, err
	}
	sort.Slice(out.Counts, func(i, j int) bool {
		if out.Counts[i].Count != out.Counts[j].Count {
			return out.Counts[i].Count > out.Counts[j].Count
		}
		return out.Counts[i].Project < out.Counts[j].Project
	})
	return out, nil
}
