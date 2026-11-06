package gateway

import (
	"database/sql"
	"errors"

	"github.com/niparis/agent-net/internal/protocol"
)

// SetAgentCharter records an agent's own charter without changing its repo.
func (s *Store) SetAgentCharter(agent, attr, value, actor string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := setAgentCharterTx(tx, agent, attr, value, actor, s.now().UnixNano()); err != nil {
		return err
	}
	return tx.Commit()
}

func setAgentCharterTx(tx *sql.Tx, agent, attr, value, actor string, now int64) error {
	value, err := protocol.CleanCharterValue(attr, value)
	if err != nil {
		return err
	}
	var exists bool
	if err = tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM agents WHERE id=? AND tombstone=0)`, agent).Scan(&exists); err != nil {
		return err
	}
	if !exists {
		return reject("unknown agent %q", agent)
	}
	var old string
	err = tx.QueryRow(`SELECT value FROM agent_charter WHERE agent_id=? AND attr=?`, agent, attr).Scan(&old)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if old == value {
		return nil
	}
	if value == "" {
		_, err = tx.Exec(`DELETE FROM agent_charter WHERE agent_id=? AND attr=?`, agent, attr)
	} else {
		_, err = tx.Exec(`INSERT INTO agent_charter VALUES(?,?,?,?,?) ON CONFLICT(agent_id,attr) DO UPDATE SET value=excluded.value,updated_at=excluded.updated_at,updated_by=excluded.updated_by`, agent, attr, value, now, actor)
	}
	if err != nil {
		return err
	}
	if _, err = tx.Exec(`INSERT INTO agent_charter_history(agent_id,attr,old_value,new_value,at,actor) VALUES(?,?,?,?,?,?)`, agent, attr, old, value, now, actor); err != nil {
		return err
	}
	return graphRecord(tx, "agent_charter", agent, attr, old, value, actor, now)
}
