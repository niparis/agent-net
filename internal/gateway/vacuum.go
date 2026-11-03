package gateway

import (
	"os"
	"time"
)

const (
	// vacuumEvery is how often the gateway may run a VACUUM. The hourly
	// maintenance calls MaybeVacuum, but a vacuum rewrites the whole file, so
	// it waits a week between runs.
	vacuumEvery = 7 * 24 * time.Hour
	// vacuumFreePercent is the share of free pages above which a vacuum is
	// worth the rewrite.
	vacuumFreePercent = 20
)

// MaybeVacuum runs VACUUM when more than a fifth of gateway.db is free pages
// and the last vacuum is at least a week old. It returns the file size before
// and after, and whether it ran. It never deletes rows.
func (s *Store) MaybeVacuum(now time.Time) (before, after int64, ran bool, err error) {
	s.vacuumMu.Lock()
	defer s.vacuumMu.Unlock()
	if !s.lastVacuum.IsZero() && now.Sub(s.lastVacuum) < vacuumEvery {
		return 0, 0, false, nil
	}
	var pageCount, freePages int64
	if err = s.db.QueryRow(`PRAGMA page_count`).Scan(&pageCount); err != nil {
		return 0, 0, false, err
	}
	if err = s.db.QueryRow(`PRAGMA freelist_count`).Scan(&freePages); err != nil {
		return 0, 0, false, err
	}
	if pageCount <= 0 || freePages*100 <= pageCount*vacuumFreePercent {
		return 0, 0, false, nil
	}
	// Fold the write-ahead log into the database first, so the sizes below
	// describe the file on disk.
	s.checkpoint()
	before = fileSize(s.path)
	if _, err = s.db.Exec(`VACUUM`); err != nil {
		return before, before, false, err
	}
	s.checkpoint()
	after = fileSize(s.path)
	s.lastVacuum = now
	return before, after, true, nil
}

// checkpoint folds the write-ahead log into the database. It is best effort:
// a busy database simply keeps its log until the next call.
func (s *Store) checkpoint() {
	s.db.Exec(`PRAGMA wal_checkpoint(TRUNCATE)`)
}

func fileSize(path string) int64 {
	info, err := os.Stat(path)
	if err != nil {
		return 0
	}
	return info.Size()
}
