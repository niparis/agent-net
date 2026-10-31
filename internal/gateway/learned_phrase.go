package gateway

import (
	"context"
	"strings"
	"time"
)

// phraseLimiter keeps the chooser's hourly and window limits across restarts.
type phraseLimiter struct{ s *Store }

func (l phraseLimiter) Reserve(now time.Time) (bool, error) {
	tx, err := l.s.db.Begin()
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	at := now.UnixNano()
	if _, err = tx.Exec(`DELETE FROM learned_phrase_runs WHERE at<=?`, at-int64(time.Hour)); err != nil {
		return false, err
	}
	var count int
	var last int64
	if err = tx.QueryRow(`SELECT COUNT(*),COALESCE(MAX(at),0) FROM learned_phrase_runs WHERE at<=?`, at).Scan(&count, &last); err != nil {
		return false, err
	}
	if count >= 6 || (count > 0 && at-last < int64(2*time.Minute)) {
		return false, tx.Commit()
	}
	if _, err = tx.Exec(`INSERT INTO learned_phrase_runs(at) VALUES(?)`, at); err != nil {
		return false, err
	}
	return true, tx.Commit()
}

type phraseCandidate struct {
	id         int64
	field, raw string
}

func (s *Store) phraseReady(ctx context.Context) error {
	if s.phraseCleaner == nil {
		return nil
	}
	// New ready items that arrive during an active window wait for the next
	// batch. Hitting the separate hourly cap still falls back to raw text.
	var inWindow int
	now := s.now().UnixNano()
	if err := s.db.QueryRow(`SELECT EXISTS(SELECT 1 FROM learned_phrase_runs WHERE at>? AND at<=?)`, now-int64(2*time.Minute), now).Scan(&inWindow); err != nil {
		return err
	}
	if inWindow != 0 {
		return nil
	}
	rows, err := s.db.Query(`SELECT l.id,l.field,l.phrase FROM learned_suggestions l JOIN learned_suggestion_details d ON d.suggestion_id=l.id LEFT JOIN learned_phrase_state p ON p.suggestion_id=l.id WHERE l.status='pending' AND l.ready_at<=? AND d.origin='owner' AND l.field IN ('accepts','needs','consumers') AND p.suggestion_id IS NULL ORDER BY l.id`, s.now().UnixNano())
	if err != nil {
		return err
	}
	var items []phraseCandidate
	var inputs []string
	for rows.Next() {
		var p phraseCandidate
		if err = rows.Scan(&p.id, &p.field, &p.raw); err != nil {
			rows.Close()
			return err
		}
		items = append(items, p)
		inputs = append(inputs, p.raw)
	}
	err = rows.Err()
	rows.Close()
	if err != nil || len(items) == 0 {
		return err
	}
	cleaned := s.phraseCleaner.Clean(ctx, s.now(), inputs)
	// Do not commit fallback while the daemon is shutting down; retry after restart.
	if ctx.Err() != nil {
		return ctx.Err()
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, p := range items {
		value := cleaned[p.raw]
		if value == "" {
			value = p.raw
		}
		// A needs item names its dependency before "for". Preserve that name.
		if p.field == "needs" {
			if target, _, ok := strings.Cut(p.raw, " for "); ok && !strings.HasPrefix(value, target+" for ") {
				value = p.raw
			}
		}
		var rejected, duplicate int
		if err = tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM learned_suggestions l LEFT JOIN learned_phrase_state p ON p.suggestion_id=l.id WHERE l.node=(SELECT node FROM learned_suggestions WHERE id=?) AND l.field=? AND l.status='rejected' AND (l.phrase=? COLLATE NOCASE OR p.raw_phrase=? COLLATE NOCASE))`, p.id, p.field, value, p.raw).Scan(&rejected); err != nil {
			return err
		}
		if rejected != 0 {
			if _, err = tx.Exec(`UPDATE learned_suggestions SET status='rejected',decided_at=? WHERE id=? AND status='pending'`, s.now().UnixNano(), p.id); err != nil {
				return err
			}
			continue
		}
		if err = tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM learned_suggestions WHERE id<>? AND node=(SELECT node FROM learned_suggestions WHERE id=?) AND field=? AND phrase=? COLLATE NOCASE AND status='pending')`, p.id, p.id, p.field, value).Scan(&duplicate); err != nil {
			return err
		}
		if duplicate != 0 {
			value = p.raw
		}
		result, e := tx.Exec(`UPDATE learned_suggestions SET phrase=? WHERE id=? AND status='pending' AND phrase=?`, value, p.id, p.raw)
		if e != nil {
			return e
		}
		n, e := result.RowsAffected()
		if e != nil {
			return e
		}
		if n == 0 {
			continue
		}
		if _, err = tx.Exec(`INSERT OR IGNORE INTO learned_phrase_state(suggestion_id,raw_phrase) VALUES(?,?)`, p.id, p.raw); err != nil {
			return err
		}
	}
	return tx.Commit()
}
