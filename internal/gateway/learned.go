package gateway

import (
	"database/sql"
	"encoding/json"
	"regexp"
	"strings"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// Learned suggestion types are shared with the owner interfaces.
type LearnedEvidence = protocol.LearnedEvidence
type LearnedSuggestion = protocol.LearnedSuggestion

// LearnedSuggestions returns pending and decided candidates, optionally limited
// to pending candidates whose node has been quiet for two minutes.
func (s *Store) LearnedSuggestions(readyOnly bool) ([]LearnedSuggestion, error) {
	query := `SELECT id,node,field,edge_kind,kind,phrase,evidence,status,severity,created_at,ready_at,decided_at,COALESCE((SELECT origin FROM learned_suggestion_details d WHERE d.suggestion_id=learned_suggestions.id),''),COALESCE((SELECT history_id FROM learned_suggestion_details d WHERE d.suggestion_id=learned_suggestions.id),0),COALESCE((SELECT raw_phrase FROM learned_phrase_state p WHERE p.suggestion_id=learned_suggestions.id),'') FROM learned_suggestions`
	args := []any{}
	query += ` WHERE 1=1`
	if readyOnly {
		query += ` AND status='pending' AND ready_at<=?`
		args = append(args, s.now().UnixNano())
	}
	if s.phraseCleaner != nil {
		query += ` AND NOT (status='pending' AND field IN ('accepts','needs','consumers') AND EXISTS(SELECT 1 FROM learned_suggestion_details d WHERE d.suggestion_id=learned_suggestions.id AND d.origin='owner') AND NOT EXISTS(SELECT 1 FROM learned_phrase_state p WHERE p.suggestion_id=learned_suggestions.id))`
	}
	query += ` ORDER BY id`
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []LearnedSuggestion{}
	for rows.Next() {
		var v LearnedSuggestion
		var evidence string
		if err = rows.Scan(&v.ID, &v.Node, &v.Field, &v.EdgeKind, &v.Kind, &v.Phrase, &evidence, &v.Status, &v.Severity, &v.CreatedAt, &v.ReadyAt, &v.DecidedAt, &v.Origin, &v.HistoryID, &v.RawPhrase); err != nil {
			return nil, err
		}
		if err = json.Unmarshal([]byte(evidence), &v.Evidence); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

var learnedOwnerJob = regexp.MustCompile(`(?i)\b(?:all\s+)?(?:this\s+)?([^.!?;]{1,100}?)\s+(?:is|are)\s+(?:his|its)(?:\s+job)?\b`)
var learnedBelongs = regexp.MustCompile(`(?i)(?:^|[.!?;:\n])\s*(?:all\s+)?(?:this\s+)?([^.!?;:]{1,100}?)\s+(?:belongs|goes|should go|is handled)\s+(?:in|to|by|with)\s+([^.!?;:\n]+)`)
var learnedRedirect = regexp.MustCompile(`(?i)^(.{1,100}?)\s+(?:belongs|goes|should go|is handled)\s+(?:in|to|by|with)\b`)

func learnedPhrase(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.LastIndexAny(s, ":\n"); i >= 0 {
		s = strings.TrimSpace(s[i+1:])
	}
	for _, p := range []string{"all this ", "this ", "the "} {
		if strings.HasPrefix(strings.ToLower(s), p) {
			s = strings.TrimSpace(s[len(p):])
			break
		}
	}
	if s == "" || len([]rune(s)) > 100 {
		return ""
	}
	return s
}
func learnedExcerpt(s string) string {
	r := []rune(strings.TrimSpace(s))
	if len(r) > 300 {
		r = r[:300]
	}
	return string(r)
}
func learnedTouch(tx *sql.Tx, at int64, nodes ...string) error {
	for _, node := range nodes {
		if node == "" {
			continue
		}
		if _, err := tx.Exec(`UPDATE learned_suggestions SET ready_at=? WHERE node=? AND status='pending'`, at+int64(2*time.Minute), node); err != nil {
			return err
		}
	}
	return nil
}
func learnedAdd(tx *sql.Tx, at int64, node, field, edgeKind, phrase, severity, origin string, evidence []LearnedEvidence) error {
	if node == "" || phrase == "" || len(evidence) == 0 {
		return nil
	}
	if _, err := graphKind(tx, node); err != nil {
		if _, ok := err.(Rejection); ok {
			return nil
		}
		return err
	}
	if field == "edge" {
		if _, err := graphKind(tx, phrase); err != nil {
			if _, ok := err.(Rejection); ok {
				return nil
			}
			return err
		}
	}
	var rejected int
	if err := tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM learned_suggestions l LEFT JOIN learned_phrase_state p ON p.suggestion_id=l.id WHERE l.node=? AND l.field=? AND (l.phrase=? COLLATE NOCASE OR p.raw_phrase=? COLLATE NOCASE) AND l.status='rejected')`, node, field, phrase, phrase).Scan(&rejected); err != nil {
		return err
	}
	if rejected != 0 {
		return nil
	}
	// A repeated signal should not spend another worker run when its earlier
	// learned item is still in the charter. If the owner removed it, a future
	// signal can propose it again.
	if field != "edge" {
		var previous, current string
		err := tx.QueryRow(`SELECT l.phrase FROM learned_suggestions l LEFT JOIN learned_phrase_state p ON p.suggestion_id=l.id WHERE l.node=? AND l.field=? AND l.status='applied' AND (l.phrase=? COLLATE NOCASE OR p.raw_phrase=? COLLATE NOCASE) ORDER BY l.id DESC LIMIT 1`, node, field, phrase, phrase).Scan(&previous)
		if err != nil && err != sql.ErrNoRows {
			return err
		}
		if err == nil {
			err = tx.QueryRow(`SELECT value FROM graph_charter WHERE node=? AND attr=?`, node, field).Scan(&current)
			if err != nil && err != sql.ErrNoRows {
				return err
			}
			for _, item := range strings.Split(current, ";") {
				if strings.EqualFold(strings.TrimSpace(item), previous) {
					return nil
				}
			}
		}
	}
	var existing string
	var id int64
	err := tx.QueryRow(`SELECT l.id,l.evidence FROM learned_suggestions l LEFT JOIN learned_phrase_state p ON p.suggestion_id=l.id WHERE l.node=? AND l.field=? AND l.edge_kind=? AND l.kind='add' AND (l.phrase=? COLLATE NOCASE OR p.raw_phrase=? COLLATE NOCASE) AND l.status='pending' ORDER BY l.id LIMIT 1`, node, field, edgeKind, phrase, phrase).Scan(&id, &existing)
	if err != nil && err != sql.ErrNoRows {
		return err
	}
	var merged []LearnedEvidence
	if err == nil {
		if err = json.Unmarshal([]byte(existing), &merged); err != nil {
			return err
		}
	}
	seen := map[string]bool{}
	for _, e := range merged {
		seen[e.ID] = true
	}
	for _, e := range evidence {
		if e.ID != "" && !seen[e.ID] {
			e.Excerpt = learnedExcerpt(e.Excerpt)
			merged = append(merged, e)
			seen[e.ID] = true
		}
	}
	data, err := json.Marshal(merged)
	if err != nil {
		return err
	}
	if id != 0 {
		_, err = tx.Exec(`UPDATE learned_suggestions SET evidence=? WHERE id=?`, string(data), id)
		return err
	}
	// Critical nodes are always classified major, even before the application phase.
	var maturity string
	err = tx.QueryRow(`SELECT value FROM graph_charter WHERE node=? AND attr='maturity'`, node).Scan(&maturity)
	if err != nil && err != sql.ErrNoRows {
		return err
	}
	if maturity == "critical" || strings.HasPrefix(maturity, "critical - ") {
		severity = "major"
	}
	res, err := tx.Exec(`INSERT INTO learned_suggestions(node,field,edge_kind,kind,phrase,evidence,status,severity,created_at,ready_at) VALUES(?,?,?,'add',?,?,'pending',?,?,?)`, node, field, edgeKind, phrase, string(data), severity, at, at+int64(2*time.Minute))
	if err != nil {
		return err
	}
	id, err = res.LastInsertId()
	if err != nil {
		return err
	}
	_, err = tx.Exec(`INSERT INTO learned_suggestion_details(suggestion_id,origin) VALUES(?,?)`, id, origin)
	return err
}

func (s *Store) learnOwner(tx *sql.Tx, from, to, id, prompt string, at int64) error {
	if err := learnedTouch(tx, at, from, to); err != nil {
		return err
	}
	var target, phrase string
	if m := learnedBelongs.FindStringSubmatch(prompt); len(m) > 0 {
		phrase = learnedPhrase(m[1])
		name := strings.TrimSpace(m[2])
		// The named target must be an exact known graph name, not a guess based on
		// the message recipient.
		rows, err := tx.Query(`SELECT name FROM graph_nodes UNION SELECT repo_project FROM sessions WHERE repo_project<>'' UNION SELECT name FROM projects`)
		if err != nil {
			return err
		}
		for rows.Next() {
			var candidate string
			if err = rows.Scan(&candidate); err != nil {
				rows.Close()
				return err
			}
			if strings.EqualFold(candidate, name) {
				target = candidate
			}
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
	} else if m := learnedOwnerJob.FindStringSubmatch(prompt); len(m) > 0 && mentionsName(prompt, to) {
		target = to
		phrase = learnedPhrase(m[1])
	}
	if target == "" || phrase == "" {
		return nil
	}
	if err := learnedTouch(tx, at, target); err != nil {
		return err
	}
	ev := []LearnedEvidence{{ID: id, Excerpt: prompt}}
	if err := learnedAdd(tx, at, target, "accepts", "", phrase, "minor", "owner", ev); err != nil {
		return err
	}
	if target == from {
		return nil
	}
	return learnedAdd(tx, at, from, "needs", "", target+" for "+phrase, "minor", "owner", ev)
}

func (s *Store) learnOutcome(tx *sql.Tx, replyID string, at int64) error {
	var from, to, outcome, reason, redirect string
	if err := tx.QueryRow(`SELECT source,target,outcome,reason,redirect_to FROM graph_outcomes WHERE reply_id=?`, replyID).Scan(&from, &to, &outcome, &reason, &redirect); err != nil {
		return err
	}
	if err := learnedTouch(tx, at, from, to, redirect); err != nil {
		return err
	}
	if outcome != "redirect" && outcome != "reject" {
		return nil
	}
	phrase := learnedPhrase(reason)
	if outcome == "redirect" {
		m := learnedRedirect.FindStringSubmatch(strings.TrimSpace(reason))
		if len(m) == 0 || redirect == "" {
			return nil
		}
		phrase = learnedPhrase(m[1])
	}
	if phrase == "" {
		return nil
	}
	rows, err := tx.Query(`SELECT message_id,reply_id,reason,redirect_to FROM graph_outcomes WHERE target=? AND outcome=? AND at>=? AND at<=? ORDER BY at,reply_id`, to, outcome, at-int64(7*24*time.Hour), at)
	if err != nil {
		return err
	}
	var ev []LearnedEvidence
	seen := map[string]bool{}
	for rows.Next() {
		var msg, id, text, dest string
		if err = rows.Scan(&msg, &id, &text, &dest); err != nil {
			rows.Close()
			return err
		}
		p := learnedPhrase(text)
		if outcome == "redirect" {
			m := learnedRedirect.FindStringSubmatch(strings.TrimSpace(text))
			if len(m) == 0 || !strings.EqualFold(dest, redirect) {
				continue
			}
			p = learnedPhrase(m[1])
		}
		if strings.EqualFold(p, phrase) && !seen[msg] {
			seen[msg] = true
			ev = append(ev, LearnedEvidence{ID: id, Excerpt: text})
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	if outcome == "reject" {
		if len(ev) < 3 {
			return nil
		}
		return learnedAdd(tx, at, to, "refuses", "", phrase, "major", "outcome", ev)
	}
	if len(ev) < 2 || to == redirect {
		return nil
	}
	if err = learnedAdd(tx, at, to, "needs", "", redirect+" for "+phrase, "minor", "outcome", ev); err != nil {
		return err
	}
	return learnedAdd(tx, at, to, "edge", "depends", redirect, "minor", "outcome", ev)
}

func (s *Store) learnDelivery(tx *sql.Tx, id string, at int64) error {
	var from, to, body string
	err := tx.QueryRow(`SELECT COALESCE(NULLIF(a.repo_project,''),ap.project,''),COALESCE(NULLIF(b.repo_project,''),bp.project,''),m.body FROM messages m JOIN sessions a ON a.id=m.sender JOIN sessions b ON b.id=m.recipient LEFT JOIN profiles ap ON ap.session_id=a.id LEFT JOIN profiles bp ON bp.session_id=b.id WHERE m.id=?`, id).Scan(&from, &to, &body)
	if err == sql.ErrNoRows {
		return nil
	}
	if err != nil {
		return err
	}
	if from == "" || to == "" || from == to {
		return nil
	}
	res, err := tx.Exec(`INSERT OR IGNORE INTO learned_message_flow(message_id,source,target,excerpt,at) VALUES(?,?,?,?,?)`, id, from, to, learnedExcerpt(body), at)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil || n == 0 {
		return err
	}
	if err = learnedTouch(tx, at, from, to); err != nil {
		return err
	}
	var connected int
	if err = tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM graph_edges WHERE (source=? AND target=?) OR (source=? AND target=?))`, from, to, to, from).Scan(&connected); err != nil {
		return err
	}
	if connected != 0 {
		return nil
	}
	rows, err := tx.Query(`SELECT message_id,excerpt FROM learned_message_flow WHERE ((source=? AND target=?) OR (source=? AND target=?)) AND at>=? AND at<=? ORDER BY at,message_id`, from, to, to, from, at-int64(7*24*time.Hour), at)
	if err != nil {
		return err
	}
	var ev []LearnedEvidence
	for rows.Next() {
		var e LearnedEvidence
		if err = rows.Scan(&e.ID, &e.Excerpt); err != nil {
			rows.Close()
			return err
		}
		ev = append(ev, e)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	if len(ev) < 5 {
		return nil
	}
	// Once a pair has a pending edge proposal, keep its direction when a
	// message arrives in the other direction and merge the new evidence.
	var existingNode, existingTarget string
	err = tx.QueryRow(`SELECT node,phrase FROM learned_suggestions WHERE field='edge' AND edge_kind='depends' AND kind='add' AND status='pending' AND ((node=? AND phrase=?) OR (node=? AND phrase=?)) ORDER BY id LIMIT 1`, from, to, to, from).Scan(&existingNode, &existingTarget)
	if err != nil && err != sql.ErrNoRows {
		return err
	}
	if err == nil {
		from, to = existingNode, existingTarget
	}
	return learnedAdd(tx, at, from, "edge", "depends", to, "minor", "flow", ev)
}
