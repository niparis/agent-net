package gateway

import (
	"path"
	"slices"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// ArchivedSessions is owner-only recovery data. Discovery reads exclude it.
func (s *Store) ArchivedSessions() (protocol.SessionsArchiveResult, error) {
	return s.ArchiveSessions(protocol.SessionsArchiveRequest{Restore: true, DryRun: true})
}

// ArchiveSessions keeps rows and foreign references intact. The single write
// transaction checks both preview membership and online state before updating.
func (s *Store) ArchiveSessions(q protocol.SessionsArchiveRequest) (protocol.SessionsArchiveResult, error) {
	out := protocol.SessionsArchiveResult{Sessions: []protocol.SessionArchiveEntry{}}
	for _, pattern := range []string{q.Name, q.Folder} {
		if _, err := path.Match(pattern, ""); err != nil {
			return out, reject("invalid glob: %v", err)
		}
	}
	var before time.Time
	if q.CreatedBefore != "" {
		var err error
		before, err = time.Parse(time.RFC3339, q.CreatedBefore)
		if err != nil {
			before, err = time.Parse("2006-01-02", q.CreatedBefore)
		}
		if err != nil {
			return out, reject("created_before must be YYYY-MM-DD or RFC3339")
		}
	}
	tx, err := s.db.Begin()
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	where := "s.archived_at=0"
	if q.Restore {
		where = "s.archived_at<>0"
	}
	var args []any
	if q.Project != nil {
		where += ` AND CASE WHEN s.no_project<>0 THEN '' ELSE COALESCE(NULLIF(s.repo_project,''),p.project,'') END=?`
		args = append(args, *q.Project)
	}
	rows, err := tx.Query(agentSelect+` WHERE `+where+` AND (n.id IS NOT NULL OR s.harness='bot') ORDER BY s.id`, args...)
	if err != nil {
		return out, err
	}
	ids := []string{}
	for rows.Next() {
		a, e := scanAgent(rows)
		if e != nil {
			rows.Close()
			return out, e
		}
		if len(q.IDs) > 0 && !slices.Contains(q.IDs, a.SessionID) {
			continue
		}
		if protocol.IsOwner(a.Harness) {
			continue
		}
		if a.Online {
			if slices.Contains(q.IDs, a.SessionID) {
				rows.Close()
				return out, reject("session %s is online", a.SessionID)
			}
			continue
		}
		if q.Name != "" {
			match, _ := path.Match(q.Name, a.Profile.Name)
			if !match {
				continue
			}
		}
		if q.Folder != "" {
			match, _ := path.Match(q.Folder, a.Cwd)
			if !match {
				continue
			}
		}
		if q.Harness != "" && a.Harness != q.Harness {
			continue
		}
		if !before.IsZero() && (a.FirstSeen == 0 || a.FirstSeen >= before.Unix()) {
			continue
		}
		// Offline alone does not mean a worker has ended. Running or queued runs
		// are excluded below using their durable run records.
		if q.EndedWorkers && a.Worker == nil {
			continue
		}
		ids = append(ids, a.SessionID)
		out.Sessions = append(out.Sessions, protocol.SessionArchiveEntry{SessionID: a.SessionID, Name: a.Profile.Name, Folder: a.Cwd, Harness: a.Harness})
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return out, err
	}
	if q.EndedWorkers {
		kept := out.Sessions[:0]
		ids = ids[:0]
		for _, a := range out.Sessions {
			var ended bool
			err = tx.QueryRow(`SELECT worker_run_active=0 AND EXISTS(SELECT 1 FROM worker_runs WHERE session_id=sessions.id AND ended_at>0) FROM sessions WHERE id=?`, a.SessionID).Scan(&ended)
			if err != nil {
				return out, err
			}
			if ended {
				kept = append(kept, a)
				ids = append(ids, a.SessionID)
			}
		}
		out.Sessions = kept
	}
	out.Count = len(out.Sessions)
	if q.Expected != nil {
		expected := slices.Clone(q.Expected)
		slices.Sort(expected)
		expected = slices.Compact(expected)
		if !slices.Equal(expected, ids) {
			return out, reject("selection changed; preview again")
		}
	}
	if q.DryRun {
		return out, nil
	}
	// ID-based confirmation must not silently ignore a session that came online,
	// disappeared, or was already archived since the preview.
	if len(q.IDs) > 0 && len(ids) != len(slices.Compact(slices.Sorted(slices.Values(q.IDs)))) {
		return out, reject("selection changed; preview again")
	}
	stamp := s.now().UnixNano()
	if q.Restore {
		stamp = 0
	}
	for _, id := range ids {
		if _, err = tx.Exec(`UPDATE sessions SET archived_at=? WHERE id=?`, stamp, id); err != nil {
			return out, err
		}
	}
	if err = tx.Commit(); err != nil {
		return out, err
	}
	return out, nil
}
