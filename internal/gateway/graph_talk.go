package gateway

import (
	"sort"
	"strings"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// GraphTalkCounts counts inter-project messages in both directions. Owner
// sends add voice-only links without changing the talk counts.
func (s *Store) GraphTalkCounts() ([]protocol.GraphTalkCount, error) {
	result, err := s.GraphTalkSnapshot()
	return result.Counts, err
}

// GraphTalkSnapshot reads counts and the feed position in one transaction.
func (s *Store) GraphTalkSnapshot() (protocol.GraphTalkResult, error) {
	var result protocol.GraphTalkResult
	tx, err := s.db.Begin()
	if err != nil {
		return result, err
	}
	defer tx.Rollback()
	if err = tx.QueryRow(`SELECT COALESCE(MAX(id),0) FROM events`).Scan(&result.LastID); err != nil {
		return result, err
	}
	now := s.now()
	rows, err := tx.Query(`SELECT COALESCE(NULLIF(src.repo_project,''),sp.project,''), COALESCE(NULLIF(dst.repo_project,''),dp.project,''), src.harness, dst.harness, COUNT(*), MAX(CASE WHEN m.sender_cto <> 0 OR src.harness = 'owner' OR EXISTS (SELECT 1 FROM charter_signals cs WHERE cs.message_id=m.id AND cs.prompt <> '') THEN 1 ELSE 0 END)
 FROM messages m JOIN sessions src ON src.id=m.sender JOIN sessions dst ON dst.id=m.recipient
 LEFT JOIN profiles sp ON sp.session_id=src.id LEFT JOIN profiles dp ON dp.session_id=dst.id
 WHERE m.created_at>=? AND m.created_at<=?
 GROUP BY 1,2,3,4`, now.Add(-7*24*time.Hour).UnixNano(), now.UnixNano())
	if err != nil {
		return result, err
	}
	counts := map[[2]string]int{}
	voices := map[[2]string]bool{}
	result.Activity = map[string]int{}
	for rows.Next() {
		var a, b, ah, bh string
		var n, voice int
		if err := rows.Scan(&a, &b, &ah, &bh, &n, &voice); err != nil {
			rows.Close()
			return result, err
		}
		a, b = strings.TrimSpace(a), strings.TrimSpace(b)
		if ah != "owner" && a != "" {
			result.Activity[a] += n
		}
		if bh != "owner" && b != "" && (a != b || ah == "owner") {
			result.Activity[b] += n
		}
		if ah == "owner" {
			a = protocol.GraphVoiceOwner
		}
		if bh == "owner" && voice != 0 {
			b = protocol.GraphVoiceOwner
		}
		if a == "" || b == "" || a == b {
			continue
		}
		if a > b {
			a, b = b, a
		}
		pair := [2]string{a, b}
		if voice != 0 {
			voices[pair] = true
		}
		if ah != "owner" && bh != "owner" {
			counts[pair] += n
		} else if voice != 0 {
			counts[pair] += 0
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return result, err
	}
	if err = tx.Commit(); err != nil {
		return result, err
	}
	graph, err := s.Graph()
	if err != nil {
		return result, err
	}
	out := make([]protocol.GraphTalkCount, 0, len(counts))
	for pair, n := range counts {
		item := protocol.GraphTalkCount{From: pair[0], To: pair[1], Count: n, Voice: voices[pair]}
		for _, edge := range graph.Edges {
			if edge.From == pair[0] && edge.To == pair[1] || edge.From == pair[1] && edge.To == pair[0] {
				item.HasEdge = true
				break
			}
		}
		out = append(out, item)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].From != out[j].From {
			return out[i].From < out[j].From
		}
		return out[i].To < out[j].To
	})
	result.Counts = out
	return result, nil
}
