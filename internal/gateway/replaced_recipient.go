package gateway

import (
	"database/sql"
	"errors"

	"github.com/niparis/agent-net/internal/protocol"
)

// maxReplaceChain bounds how many replacements a message follows, so a loop of
// replacements (a -> b -> a) cannot keep the lookup going.
const maxReplaceChain = 8

// liveSession follows replaced_by from id to the session that holds the
// conversation now. ok is false when id is unknown. A chain that loops, is too
// long or ends at an unknown record keeps id: the message then waits under it,
// and nothing wakes a replaced session (wakeSessionAs).
func liveSession(q queryer, id string) (live string, ok bool, err error) {
	seen := map[string]bool{}
	cur := id
	for range maxReplaceChain + 1 {
		var next string
		err := q.QueryRow(`SELECT replaced_by FROM sessions WHERE id=?`, cur).Scan(&next)
		if errors.Is(err, sql.ErrNoRows) {
			return id, cur != id, nil
		}
		if err != nil {
			return "", false, err
		}
		if next == "" {
			return cur, true, nil
		}
		seen[cur] = true
		if seen[next] {
			return id, true, nil
		}
		cur = next
	}
	return id, true, nil
}

// redirectReplaced moves a message that is still waiting for a replaced
// session to the session that replaced it. Most agent-net notices are written
// straight into the store under the ID they were made for, so dispatch moves
// them here. moved is false when there is no live session to move to, or the
// message is no longer waiting.
func (s *Store) redirectReplaced(m protocol.Message) (protocol.Message, bool, error) {
	live, ok, err := liveSession(s.db, m.To)
	if err != nil || !ok || live == m.To {
		return m, false, err
	}
	res, err := s.db.Exec(`UPDATE messages SET recipient=? WHERE id=? AND recipient=? AND state IN ('held','stored','dispatched','failed')`,
		live, m.ID, m.To)
	if err != nil {
		return m, false, err
	}
	if n, err := res.RowsAffected(); err != nil || n != 1 {
		return m, false, err
	}
	m.To = live
	return m, true, nil
}
