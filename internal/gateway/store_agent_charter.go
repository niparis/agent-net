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
	return s.charterWrote(tx.Commit())
}

func setAgentCharterTx(tx *sql.Tx, agent, attr, value, actor string, now int64) error {
	return setAgentCharterIn(tx, charterValuesLayout, agent, attr, value, actor, now)
}

// charterLayout is where agent charter values are stored. A migration step
// that ran before the tables merged must read the layout the store had at that
// step, not the current one.
type charterLayout struct {
	get func(tx *sql.Tx, agent, attr string) (string, error)
	put func(tx *sql.Tx, agent, attr, value, actor string, now int64) error
}

// charterValuesLayout is the layout since the charter tables merged.
var charterValuesLayout = charterLayout{
	get: func(tx *sql.Tx, agent, attr string) (string, error) { return charterValueTx(tx, "agent", agent, attr) },
	put: func(tx *sql.Tx, agent, attr, value, actor string, now int64) error {
		return putCharterValueTx(tx, "agent", agent, attr, value, actor, now)
	},
}

func setAgentCharterIn(tx *sql.Tx, layout charterLayout, agent, attr, value, actor string, now int64) error {
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
	old, err := layout.get(tx, agent, attr)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if old == value {
		return nil
	}
	if err = layout.put(tx, agent, attr, value, actor, now); err != nil {
		return err
	}
	return graphRecord(tx, "agent_charter", agent, attr, old, value, actor, now)
}
