package gateway

import (
	"strings"

	"github.com/niparis/agent-net/internal/protocol"
)

// RecordSessionStartFailure records a start that the daemon answered as
// starting and then could not start. Only the owner of this machine may report
// one. The event is what the TUI feed and `agent-net` history show.
func (s *Store) RecordSessionStartFailure(node string, p protocol.SessionStartFailure) error {
	var allowed bool
	if err := s.db.QueryRow(`SELECT EXISTS(SELECT 1 FROM sessions WHERE id=? AND node_id=? AND harness='owner' AND worker='')`, p.Owner, node).Scan(&allowed); err != nil {
		return err
	}
	if !allowed {
		return reject("session start failure requires the owner of this machine")
	}
	if len(p.Error) > 300 {
		p.Error = p.Error[:300]
	}
	detail := strings.TrimPrefix(pairs("start", p.StartID, "project", p.Project, "harness", p.Harness, "error", p.Error), " ")
	s.Event("session.start_failed", node, "", "", detail)
	return nil
}
