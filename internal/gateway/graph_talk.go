package gateway

import (
	"sort"
	"strings"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// GraphTalkCounts counts both directions together and excludes owner sessions.
func (s *Store) GraphTalkCounts() ([]protocol.GraphTalkCount, error) {
	rows, err := s.db.Query(`SELECT COALESCE(NULLIF(src.repo_project,''),sp.project,''), COALESCE(NULLIF(dst.repo_project,''),dp.project,''), COUNT(*)
 FROM messages m JOIN sessions src ON src.id=m.sender JOIN sessions dst ON dst.id=m.recipient
 LEFT JOIN profiles sp ON sp.session_id=src.id LEFT JOIN profiles dp ON dp.session_id=dst.id
 WHERE m.created_at>=? AND m.created_at<=? AND src.harness<>'owner' AND dst.harness<>'owner'
 GROUP BY 1,2`, s.now().Add(-7*24*time.Hour).UnixNano(), s.now().UnixNano())
	if err != nil {
		return nil, err
	}
	counts := map[[2]string]int{}
	for rows.Next() {
		var a, b string
		var n int
		if err := rows.Scan(&a, &b, &n); err != nil {
			rows.Close()
			return nil, err
		}
		a, b = strings.TrimSpace(a), strings.TrimSpace(b)
		if a == "" || b == "" || a == b {
			continue
		}
		if a > b {
			a, b = b, a
		}
		counts[[2]string{a, b}] += n
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	graph, err := s.Graph()
	if err != nil {
		return nil, err
	}
	out := make([]protocol.GraphTalkCount, 0, len(counts))
	for pair, n := range counts {
		item := protocol.GraphTalkCount{From: pair[0], To: pair[1], Count: n}
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
	return out, nil
}
