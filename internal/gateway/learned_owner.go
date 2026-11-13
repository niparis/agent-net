package gateway

import (
	"database/sql"
	"strings"

	"github.com/niparis/agent-net/internal/protocol"
)

// learnedListTables are the tables OwnerLearned reads: the suggestions and their
// details, phrase state and views, the history of a change (graph_history), the
// graph charter and the drafts. The drafts' own-value check (draftOwnValue) reads
// the agent and team charters, and the drafts' names (draftName) read the agents
// and the teams. Keep it equal to those queries (see readThrough).
var learnedListTables = []string{"learned_suggestions", "learned_suggestion_details", "learned_phrase_state",
	"learned_views", "graph_history", "graph_charter", "charter_drafts", "agent_charter", "charter_values", "teams", "agents"}

// ownerLearnedRows is what the owner's list reads from the database: every
// suggestion, ready or not, and the pending drafts. OwnerLearned applies the
// ready time to the suggestions on each call, so the rows need no clock.
type ownerLearnedRows struct {
	items  []protocol.LearnedSuggestion
	drafts []protocol.LearnedSuggestion
}

// OwnerLearned returns ready major suggestions and recorded changes. The rows
// are kept until one of learnedListTables is written (see readThrough). The ready
// time is checked on every call, so a suggestion shows as soon as its time has
// passed, with no write. Each caller gets a copy.
func (s *Store) OwnerLearned() ([]protocol.LearnedSuggestion, error) {
	v, err := s.readThrough("owner_learned", learnedListTables, false, func() (any, error) { return s.ownerLearnedRead() })
	if err != nil {
		return nil, err
	}
	rows := v.(ownerLearnedRows)
	now := s.now().UnixNano()
	out := make([]protocol.LearnedSuggestion, 0, len(rows.items)+len(rows.drafts))
	for _, item := range rows.items {
		if item.Status == "pending" && (item.Severity != "major" || item.ReadyAt > now) {
			continue
		}
		out = append(out, copyOf(item))
	}
	return append(out, copyOf(rows.drafts)...), nil
}

func (s *Store) ownerLearnedRead() (ownerLearnedRows, error) {
	all, err := s.LearnedSuggestions(false)
	if err != nil {
		return ownerLearnedRows{}, err
	}
	items := make([]protocol.LearnedSuggestion, 0, len(all))
	for _, v := range all {
		var seen int64
		err = s.db.QueryRow(`SELECT last_seen_id FROM learned_views WHERE node=?`, v.Node).Scan(&seen)
		if err != nil && err != sql.ErrNoRows {
			return ownerLearnedRows{}, err
		}
		if v.HistoryID != 0 {
			var actor string
			err = s.db.QueryRow(`SELECT actor,new_value FROM graph_history WHERE id=?`, v.HistoryID).Scan(&actor, &v.NewValue)
			if err != nil && err != sql.ErrNoRows {
				return ownerLearnedRows{}, err
			}
			v.Unseen = v.Status == "applied" && actor == "learned" && v.HistoryID > seen
			v.By = actor
		}
		if v.Status == "pending" && v.Field != "edge" {
			err = s.db.QueryRow(`SELECT value FROM graph_charter WHERE node=? AND attr=?`, v.Node, v.Field).Scan(&v.OldValue)
			if err != nil && err != sql.ErrNoRows {
				return ownerLearnedRows{}, err
			}
			v.NewValue = learnedValue(v.OldValue, v.Phrase, v.Kind)
		} else if v.Status == "pending" {
			v.NewValue = v.EdgeKind + " -> " + v.Phrase
		}
		items = append(items, v)
	}
	drafts, err := s.ownerDrafts()
	if err != nil {
		return ownerLearnedRows{}, err
	}
	return ownerLearnedRows{items: items, drafts: drafts}, nil
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
	return s.decideLearned(id, action, "owner")
}

// decideLearned is DecideLearned for the owner or the CTO. The actor is
// recorded in the graph history of the change.
func (s *Store) decideLearned(id int64, action, actor string) error {
	if action != "accept" && action != "reject" {
		return reject("action must be accept or reject")
	}
	if id < 0 {
		// A charter draft (decision 0088) shares the owner's learned list.
		return s.DecideCharterDraft(-id, action, actor)
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var settingsRevisionBefore int64
	if err = tx.QueryRow(`SELECT revision FROM setting_revision WHERE id=1`).Scan(&settingsRevisionBefore); err != nil {
		return err
	}

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
			err = setEdgeTx(tx, protocol.Edge{From: v.Node, To: v.Phrase, Kind: v.EdgeKind}, actor, now)
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
		err = setCharterTx(tx, v.Node, v.Field, learnedValue(old, v.Phrase, v.Kind), actor, now)
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
	var settingsRevisionAfter int64
	if err = tx.QueryRow(`SELECT revision FROM setting_revision WHERE id=1`).Scan(&settingsRevisionAfter); err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	if settingsRevisionAfter != settingsRevisionBefore {
		s.settingsNotify()
	}
	return nil
}

// carryOutLearned settles a learned suggestion whose ask was answered in free
// text, as the CTO does when it made the change itself. A refusal rejects it;
// anything else says the change is carried out, so it is marked applied. The
// change is not made again. A suggestion that is no longer pending is left
// alone.
func (s *Store) carryOutLearned(id int64, answer string) error {
	status := "applied"
	if learnedRefusal(answer) {
		status = "rejected"
	}
	_, err := s.db.Exec(`UPDATE learned_suggestions SET status=?,decided_at=? WHERE id=? AND status='pending'`, status, s.now().UnixNano(), id)
	return err
}

// learnedRefusal says whether a free-text answer declines the suggestion.
func learnedRefusal(answer string) bool {
	first := strings.ToLower(strings.TrimSpace(answer))
	for _, w := range []string{"no", "not", "don't", "dont", "do not", "reject", "decline", "refuse", "never"} {
		if first == w || strings.HasPrefix(first, w+" ") || strings.HasPrefix(first, w+",") || strings.HasPrefix(first, w+":") || strings.HasPrefix(first, w+".") {
			return true
		}
	}
	return false
}

// settleLearnedDone marks a pending major suggestion as applied when its change
// already exists: the edge is in the graph, or the charter value already holds
// the phrase (or no longer holds it, for a removal). Whoever made the change,
// the owner, the CTO or a session, there is nothing left to ask.
func (s *Store) settleLearnedDone() error {
	rows, err := s.db.Query(`SELECT id,node,field,edge_kind,kind,phrase FROM learned_suggestions WHERE status='pending' AND severity='major' AND ready_at<=?`, s.now().UnixNano())
	if err != nil {
		return err
	}
	var pending []LearnedSuggestion
	for rows.Next() {
		var v LearnedSuggestion
		if err = rows.Scan(&v.ID, &v.Node, &v.Field, &v.EdgeKind, &v.Kind, &v.Phrase); err != nil {
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
		done := false
		if v.Field == "edge" {
			if v.Kind == "add" {
				err = s.db.QueryRow(`SELECT EXISTS(SELECT 1 FROM graph_edges WHERE source=? AND target=? AND kind=?)`, v.Node, v.Phrase, v.EdgeKind).Scan(&done)
			}
		} else {
			var old string
			err = s.db.QueryRow(`SELECT value FROM graph_charter WHERE node=? AND attr=?`, v.Node, v.Field).Scan(&old)
			if err == sql.ErrNoRows {
				err = nil
			}
			if err == nil {
				has := false
				for _, item := range strings.Split(old, ";") {
					if strings.EqualFold(strings.TrimSpace(item), strings.TrimSpace(v.Phrase)) {
						has = true
					}
				}
				done = has == (v.Kind == "add")
			}
		}
		if err != nil {
			return err
		}
		if !done {
			continue
		}
		if _, err = s.db.Exec(`UPDATE learned_suggestions SET status='applied',decided_at=? WHERE id=? AND status='pending'`, s.now().UnixNano(), v.ID); err != nil {
			return err
		}
	}
	return nil
}
