package gateway

import (
	"encoding/json"
	"strings"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// recentChangeWindow is how far back the hand-back hint looks for merged changes.
const recentChangeWindow = 7 * 24 * time.Hour

// maxHintChanges is the most recent changes the hand-back hint lists.
const maxHintChanges = 5

// RecentWorkerChanges returns merged changes in project in the last seven
// days, newest first, made by a worker other than excludeSession and other
// than excludeChange. It is the data behind the hand-back hint. A change with
// no merged commit is left out: the hand-back hint cannot check its files.
func (s *Store) RecentWorkerChanges(project, excludeSession, excludeChange string, limit int) ([]protocol.RecentWorkerChange, error) {
	project = strings.TrimSpace(project)
	if project == "" {
		return nil, nil
	}
	if limit <= 0 || limit > maxHintChanges {
		limit = maxHintChanges
	}
	cut := s.now().Add(-recentChangeWindow).Unix()
	rows, err := s.db.Query(`SELECT id, title, origin, updated_at, merged_commit FROM delivery_changes
		WHERE project=? AND state=? AND updated_at>=? ORDER BY updated_at DESC`, project, protocol.ChangeMerged, cut)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []protocol.RecentWorkerChange{}
	for rows.Next() {
		var id, title, rawOrigin, commit string
		var mergedAt int64
		if err := rows.Scan(&id, &title, &rawOrigin, &mergedAt, &commit); err != nil {
			return nil, err
		}
		if id == excludeChange || commit == "" {
			continue
		}
		var origin protocol.ChangeOrigin
		if json.Unmarshal([]byte(rawOrigin), &origin) != nil || origin.Kind != protocol.OriginWorker {
			continue
		}
		if origin.SessionID == "" || origin.SessionID == excludeSession {
			continue
		}
		out = append(out, protocol.RecentWorkerChange{ChangeID: id, Title: strings.TrimSpace(title),
			WorkerID: origin.WorkerID, WorkerSession: origin.SessionID, MergedAt: mergedAt, MergedCommit: commit})
		if len(out) >= limit {
			break
		}
	}
	return out, rows.Err()
}
