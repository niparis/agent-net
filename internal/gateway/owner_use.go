package gateway

import (
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Owner use is a daily-halving score, bounded so it cannot overflow. The
// stored score is as of owner_used_at; reads decay it to the current day.
func decayOwnerUses(count int, used, now int64) int {
	if used <= 0 || now <= used {
		return count
	}
	days := (now - used) / int64((24*time.Hour)/time.Second)
	if days >= 30 {
		return 0
	}
	return count >> uint(days)
}

func recordOwnerUse(q interface {
	Exec(string, ...any) (sql.Result, error)
}, id string, now time.Time) error {
	r, err := q.Exec(`UPDATE sessions SET owner_uses=MIN(1000000, (owner_uses >> MIN(30, MAX(0, (? - owner_used_at) / ?))) + 1), owner_used_at=? WHERE id=?`, now.UnixNano(), int64(24*time.Hour), now.UnixNano(), id)
	if err != nil {
		return err
	}
	n, err := r.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return reject("unknown session")
	}
	return nil
}

func (s *Store) RecordOwnerUse(id string) error { return recordOwnerUse(s.db, id, s.now()) }
func (s *Store) SetSessionPin(id string, pinned bool) error {
	stamp := int64(0)
	if pinned {
		stamp = s.now().UnixNano()
	}
	r, err := s.db.Exec(`UPDATE sessions SET pinned_at=CASE WHEN ?<>0 AND pinned_at<>0 THEN pinned_at ELSE ? END WHERE id=? AND archived_at=0`, stamp, stamp, id)
	if err != nil {
		return err
	}
	n, err := r.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return reject("unknown session")
	}
	return nil
}

func tempSessionFolder(folder string) bool {
	if !filepath.IsAbs(folder) {
		return false
	}
	folder = filepath.Clean(folder)
	for _, base := range []string{os.TempDir(), "/tmp", "/var/folders", "/private/tmp", "/private/var/folders"} {
		base = filepath.Clean(base)
		if base == "/" || base == "." {
			continue
		}
		if folder == base || strings.HasPrefix(folder, base+string(filepath.Separator)) {
			return true
		}
	}
	return false
}

// AutoArchiveSessions makes the selection and updates under the same lock.
func (s *Store) AutoArchiveSessions() ([]string, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	now := s.now()
	cutoff := now.Add(-7 * 24 * time.Hour).UnixNano()
	rows, err := tx.Query(`SELECT s.id,s.cwd,MAX(s.last_active,s.last_seen,s.created_at) FROM sessions s WHERE s.archived_at=0 AND s.online=0 AND s.pinned_at=0 AND s.owner_used_at<? AND s.harness NOT IN ('owner','cto','bot') AND s.id<>COALESCE((SELECT value FROM owner_settings WHERE key='cto_session'),'') AND NOT EXISTS(SELECT 1 FROM project_mergers WHERE session_id=s.id) ORDER BY s.id`, cutoff)
	if err != nil {
		return nil, err
	}
	var ids []string
	for rows.Next() {
		var id, cwd string
		var active int64
		if err = rows.Scan(&id, &cwd, &active); err != nil {
			rows.Close()
			return nil, err
		}
		if tempSessionFolder(cwd) || active < cutoff {
			ids = append(ids, id)
		}
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()
	for _, id := range ids {
		if _, err = tx.Exec(`UPDATE sessions SET archived_at=? WHERE id=?`, now.UnixNano(), id); err != nil {
			return nil, err
		}
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return ids, nil
}
