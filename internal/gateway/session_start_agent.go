package gateway

import (
	"database/sql"
	"errors"

	"github.com/niparis/agent-net/internal/protocol"
)

// ReserveSessionAgent binds only a future native identity. A blank native ID
// validates the choice before the daemon creates folders or launches a harness.
// The daemon admits this call only through its owner or approved-proposal path.
func (s *Store) ReserveSessionAgent(node string, p protocol.SessionAgentReservation) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var allowed bool
	if err = tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM sessions WHERE id=? AND node_id=? AND harness='owner' AND worker='')`, p.Owner, node).Scan(&allowed); err != nil {
		return err
	}
	if !allowed {
		return reject("session Agent selection requires the owner of this machine")
	}
	if p.Harness != "claude" && p.Harness != "pi" && p.Harness != "opencode" {
		return reject("Agent selection requires a harness with a known native session ID")
	}
	if err = tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM agents WHERE id=? AND tombstone=0)`, p.AgentID).Scan(&allowed); err != nil {
		return err
	}
	if !allowed {
		return reject("unknown or tombstoned Agent %q", p.AgentID)
	}
	if p.NativeID == "" {
		return tx.Commit()
	}
	if err = tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM sessions WHERE node_id=? AND harness=? AND native_id=?)`, node, p.Harness, p.NativeID).Scan(&allowed); err != nil {
		return err
	}
	if allowed {
		return reject("session is already registered; its Agent cannot be selected at start")
	}
	_, err = tx.Exec(`INSERT INTO session_agent_starts(node_id,harness,native_id,agent_id) VALUES(?,?,?,?)`, node, p.Harness, p.NativeID, p.AgentID)
	if err != nil {
		return err
	}
	return tx.Commit()
}

func applyStartedAgent(q interface {
	execer
	QueryRow(string, ...any) *sql.Row
}, node string, in protocol.SessionInfo, id string, known bool) error {
	var agent string
	err := q.QueryRow(`SELECT agent_id FROM session_agent_starts WHERE node_id=? AND harness=? AND native_id=?`, node, in.Harness, in.NativeID).Scan(&agent)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if known || in.Worker != nil {
		return reject("Agent start reservation cannot replace a registered or worker identity")
	}
	var active bool
	if err = q.QueryRow(`SELECT EXISTS(SELECT 1 FROM agents WHERE id=? AND tombstone=0)`, agent).Scan(&active); err != nil {
		return err
	}
	if !active {
		return reject("selected Agent was tombstoned before registration")
	}
	if _, err = q.Exec(`UPDATE sessions SET agent_id=? WHERE id=?`, agent, id); err != nil {
		return err
	}
	_, err = q.Exec(`DELETE FROM session_agent_starts WHERE node_id=? AND harness=? AND native_id=?`, node, in.Harness, in.NativeID)
	return err
}
