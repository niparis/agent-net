package gateway

import "github.com/niparis/agent-net/internal/protocol"

// Outcomes lists recorded decisions for an edge, newest first. Empty node
// filters match all nodes; limit is bounded.
func (s *Store) Outcomes(from, to string, limit int) ([]protocol.EdgeOutcome, error) {
	if limit <= 0 || limit > 1000 {
		limit = 1000
	}
	rows, err := s.db.Query(`SELECT source,target,message_id,reply_id,outcome,reason,redirect_to,recommendation,at FROM graph_outcomes WHERE (?='' OR source=?) AND (?='' OR target=?) ORDER BY at DESC, rowid DESC LIMIT ?`, from, from, to, to, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []protocol.EdgeOutcome{}
	for rows.Next() {
		var o protocol.EdgeOutcome
		if err := rows.Scan(&o.From, &o.To, &o.MessageID, &o.ReplyID, &o.Outcome, &o.Reason, &o.RedirectTo, &o.Recommendation, &o.At); err != nil {
			return nil, err
		}
		out = append(out, o)
	}
	return out, rows.Err()
}
