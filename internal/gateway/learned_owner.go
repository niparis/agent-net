package gateway

import (
	"database/sql"
	"strings"

	"github.com/niparis/agent-net/internal/protocol"
)

// OwnerLearned returns ready major suggestions and recorded changes.
func (s *Store) OwnerLearned() ([]protocol.LearnedSuggestion, error) {
	all, err := s.LearnedSuggestions(false)
	if err != nil {
		return nil, err
	}
	out := make([]protocol.LearnedSuggestion, 0, len(all))
	for _, v := range all {
		if v.Status == "pending" && (v.Severity != "major" || v.ReadyAt > s.now().UnixNano()) {
			continue
		}
		var seen int64
		err = s.db.QueryRow(`SELECT last_seen_id FROM learned_views WHERE node=?`, v.Node).Scan(&seen)
		if err != nil && err != sql.ErrNoRows {
			return nil, err
		}
		if v.HistoryID != 0 {
			var actor string
			err = s.db.QueryRow(`SELECT actor,new_value FROM graph_history WHERE id=?`, v.HistoryID).Scan(&actor, &v.NewValue)
			if err != nil && err != sql.ErrNoRows {
				return nil, err
			}
			v.Unseen = v.Status == "applied" && actor == "learned" && v.HistoryID > seen
			v.By = actor
		}
		if v.Status == "pending" && v.Field != "edge" {
			err = s.db.QueryRow(`SELECT value FROM graph_charter WHERE node=? AND attr=?`, v.Node, v.Field).Scan(&v.OldValue)
			if err != nil && err != sql.ErrNoRows {
				return nil, err
			}
			v.NewValue = learnedValue(v.OldValue, v.Phrase, v.Kind)
		} else if v.Status == "pending" {
			v.NewValue = v.EdgeKind + " -> " + v.Phrase
		}
		out = append(out, v)
	}
	return out, nil
}
func learnedValue(old, phrase, kind string) string {
	var items []string
	found := false
	for _, item := range strings.Split(old, ";") {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		if strings.EqualFold(item, strings.TrimSpace(phrase)) {
			found = true
			if kind == "remove" {
				continue
			}
		}
		items = append(items, item)
	}
	if kind == "add" && !found {
		items = append(items, phrase)
	}
	return strings.Join(items, "; ")
}

// MarkLearnedViewed advances a per-node watermark without skipping later work.
func (s *Store) MarkLearnedViewed(node string, id int64) error {
	if node == "" || id <= 0 {
		return reject("node and learned change required")
	}
	var historyID int64
	if err := s.db.QueryRow(`SELECT d.history_id FROM learned_suggestions l JOIN learned_suggestion_details d ON d.suggestion_id=l.id JOIN graph_history h ON h.id=d.history_id WHERE l.id=? AND l.node=? AND l.status='applied' AND h.actor='learned'`, id, node).Scan(&historyID); err == sql.ErrNoRows {
		return reject("unknown learned change")
	} else if err != nil {
		return err
	}
	_, err := s.db.Exec(`INSERT INTO learned_views(node,last_seen_id) VALUES(?,?) ON CONFLICT(node) DO UPDATE SET last_seen_id=MAX(last_seen_id,excluded.last_seen_id)`, node, historyID)
	return err
}

// DecideLearned applies a reviewed major suggestion or records its rejection.
func (s *Store) DecideLearned(id int64, action string) error {
	if action != "accept" && action != "reject" {
		return reject("action must be accept or reject")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var v protocol.LearnedSuggestion
	if err = tx.QueryRow(`SELECT node,field,edge_kind,kind,phrase FROM learned_suggestions WHERE id=? AND status='pending' AND severity='major' AND ready_at<=?`, id, s.now().UnixNano()).Scan(&v.Node, &v.Field, &v.EdgeKind, &v.Kind, &v.Phrase); err == sql.ErrNoRows {
		return reject("ready major suggestion not found")
	} else if err != nil {
		return err
	}
	now := s.now().UnixNano()
	if action == "reject" {
		if _, err = tx.Exec(`UPDATE learned_suggestions SET status='rejected',decided_at=? WHERE status='pending' AND node=? AND field=? AND phrase=? COLLATE NOCASE`, now, v.Node, v.Field, v.Phrase); err != nil {
			return err
		}
		return tx.Commit()
	}
	var before, after int64
	if err = tx.QueryRow(`SELECT COALESCE(MAX(id),0) FROM graph_history`).Scan(&before); err != nil {
		return err
	}
	if v.Field == "edge" {
		if v.Kind != "add" || v.EdgeKind != "depends" {
			return reject("unsupported edge suggestion")
		}
		var exists int
		if err = tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM graph_edges WHERE source=? AND target=? AND kind=?)`, v.Node, v.Phrase, v.EdgeKind).Scan(&exists); err != nil {
			return err
		}
		if exists == 0 {
			err = setEdgeTx(tx, protocol.Edge{From: v.Node, To: v.Phrase, Kind: v.EdgeKind}, "owner", now)
		}
	} else {
		if v.Kind != "add" && v.Kind != "remove" {
			return reject("unsupported suggestion")
		}
		var old string
		err = tx.QueryRow(`SELECT value FROM graph_charter WHERE node=? AND attr=?`, v.Node, v.Field).Scan(&old)
		if err == sql.ErrNoRows {
			err = nil
		}
		if err != nil {
			return err
		}
		err = setCharterTx(tx, v.Node, v.Field, learnedValue(old, v.Phrase, v.Kind), "owner", now)
	}
	if err != nil {
		return err
	}
	if err = tx.QueryRow(`SELECT COALESCE(MAX(id),0) FROM graph_history`).Scan(&after); err != nil {
		return err
	}
	if after == before {
		after = 0
	}
	if _, err = tx.Exec(`INSERT INTO learned_suggestion_details(suggestion_id,history_id) VALUES(?,?) ON CONFLICT(suggestion_id) DO UPDATE SET history_id=excluded.history_id`, id, after); err != nil {
		return err
	}
	if _, err = tx.Exec(`UPDATE learned_suggestions SET status='applied',decided_at=? WHERE id=?`, now, id); err != nil {
		return err
	}
	return tx.Commit()
}
