package gateway

import (
	"database/sql"
	"regexp"
	"strings"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

var learnedRestriction = regexp.MustCompile(`(?i)\b(only|exclusively|solely|except|excluding|unless|without|never|cannot|can't|restricted|restrict|prohibited|refuse|must|ask)\b`)

// learnedClass defaults to major. Only the explicitly allowed additions can
// be applied without an owner decision.
func learnedClass(v LearnedSuggestion) string {
	if v.Kind != "add" {
		return "major"
	}
	if v.Field != "edge" && learnedRestriction.MatchString(v.Phrase) {
		return "major"
	}
	switch v.Field {
	case "accepts":
		if v.Origin == "owner" {
			return "minor"
		}
	case "needs", "consumers":
		return "minor"
	case "edge":
		if v.EdgeKind == "depends" {
			return "minor"
		}
	}
	return "major"
}

// RejectLearnedSuggestion records an owner decision. Rejected phrases are not
// proposed again, even when new evidence arrives.
func (s *Store) RejectLearnedSuggestion(id int64) error {
	res, err := s.db.Exec(`UPDATE learned_suggestions SET status='rejected',decided_at=? WHERE status='pending' AND EXISTS (SELECT 1 FROM learned_suggestions selected WHERE selected.id=? AND selected.status='pending' AND selected.node=learned_suggestions.node AND selected.field=learned_suggestions.field AND selected.phrase=learned_suggestions.phrase COLLATE NOCASE)`, s.now().UnixNano(), id)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return reject("suggestion is not pending")
	}
	return nil
}

// ProcessLearned classifies ready suggestions and applies eligible additions.
// Each graph change, its history and the suggestion status commit together.
func (s *Store) ProcessLearned() error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	now := s.now().UnixNano()
	rows, err := tx.Query(`SELECT l.id,l.node,l.field,l.edge_kind,l.kind,l.phrase,l.severity,COALESCE(d.origin,'') FROM learned_suggestions l LEFT JOIN learned_suggestion_details d ON d.suggestion_id=l.id WHERE l.status='pending' AND l.ready_at<=? ORDER BY l.id`, now)
	if err != nil {
		return err
	}
	var pending []LearnedSuggestion
	for rows.Next() {
		var v LearnedSuggestion
		if err = rows.Scan(&v.ID, &v.Node, &v.Field, &v.EdgeKind, &v.Kind, &v.Phrase, &v.Severity, &v.Origin); err != nil {
			rows.Close()
			return err
		}
		pending = append(pending, v)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, v := range pending {
		class := learnedClass(v)
		// A major classification is sticky, including a previous cap promotion.
		if v.Severity == "major" {
			class = "major"
		}
		var maturity string
		err = tx.QueryRow(`SELECT value FROM graph_charter WHERE node=? AND attr='maturity'`, v.Node).Scan(&maturity)
		if err != nil && err != sql.ErrNoRows {
			return err
		}
		if maturity == "critical" || strings.HasPrefix(maturity, "critical - ") {
			class = "major"
		}
		if v.Field == "edge" {
			maturity = ""
			err = tx.QueryRow(`SELECT value FROM graph_charter WHERE node=? AND attr='maturity'`, v.Phrase).Scan(&maturity)
			if err != nil && err != sql.ErrNoRows {
				return err
			}
			if maturity == "critical" || strings.HasPrefix(maturity, "critical - ") {
				class = "major"
			}
		}
		if class == "minor" {
			var count int
			if err = tx.QueryRow(`SELECT COUNT(*) FROM graph_history WHERE node=? AND actor='learned' AND at>? AND at<=?`, v.Node, now-int64(24*time.Hour), now).Scan(&count); err != nil {
				return err
			}
			if count >= 3 {
				class = "major"
			}
		}
		if class == "major" {
			if _, err = tx.Exec(`UPDATE learned_suggestions SET severity='major' WHERE id=?`, v.ID); err != nil {
				return err
			}
			continue
		}
		var before, after int64
		if err = tx.QueryRow(`SELECT COALESCE(MAX(id),0) FROM graph_history`).Scan(&before); err != nil {
			return err
		}
		if v.Field == "edge" {
			// Never overwrite an existing request rule when adding an edge.
			var exists int
			if err = tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM graph_edges WHERE source=? AND target=? AND kind='depends')`, v.Node, v.Phrase).Scan(&exists); err != nil {
				return err
			}
			if exists == 0 {
				err = setEdgeTx(tx, protocol.Edge{From: v.Node, To: v.Phrase, Kind: "depends"}, "learned", now)
			}
		} else {
			var old string
			err = tx.QueryRow(`SELECT value FROM graph_charter WHERE node=? AND attr=?`, v.Node, v.Field).Scan(&old)
			if err == sql.ErrNoRows {
				err = nil
			}
			if err != nil {
				return err
			}
			value := old
			found := false
			for _, item := range strings.Split(old, ";") {
				if strings.EqualFold(strings.TrimSpace(item), strings.TrimSpace(v.Phrase)) {
					found = true
					break
				}
			}
			if !found {
				if strings.TrimSpace(old) != "" {
					value = strings.TrimSpace(old) + "; " + v.Phrase
				} else {
					value = v.Phrase
				}
			}
			clean, e := protocol.CleanCharterValue(v.Field, value)
			if e != nil || clean != value || strings.TrimSpace(v.Phrase) == "" {
				if _, err = tx.Exec(`UPDATE learned_suggestions SET severity='major' WHERE id=?`, v.ID); err != nil {
					return err
				}
				continue
			}
			err = setCharterTx(tx, v.Node, v.Field, value, "learned", now)
		}
		if err != nil {
			if _, ok := err.(Rejection); ok {
				if _, err = tx.Exec(`UPDATE learned_suggestions SET severity='major' WHERE id=?`, v.ID); err != nil {
					return err
				}
				continue
			}
			return err
		}
		if err = tx.QueryRow(`SELECT COALESCE(MAX(id),0) FROM graph_history`).Scan(&after); err != nil {
			return err
		}
		if before == after {
			after = 0
		}
		if _, err = tx.Exec(`INSERT INTO learned_suggestion_details(suggestion_id,history_id) VALUES(?,?) ON CONFLICT(suggestion_id) DO UPDATE SET history_id=excluded.history_id`, v.ID, after); err != nil {
			return err
		}
		if _, err = tx.Exec(`UPDATE learned_suggestions SET status='applied',severity='minor',decided_at=? WHERE id=?`, now, v.ID); err != nil {
			return err
		}
	}
	return tx.Commit()
}
