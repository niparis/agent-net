package gateway

import (
	"database/sql"
	"strings"
)

// seedLegacyCharter installs an old recipe fixture. New writes intentionally
// reject critical, but its historical recipe remains readable by old clients.
func (s *Store) seedLegacyCharter(node, attr, value, actor string) error {
	if !strings.HasPrefix(value, "critical") {
		return s.SetCharter(node, attr, value, actor)
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var old string
	err = tx.QueryRow(`SELECT value FROM graph_charter WHERE node=? AND attr=?`, node, attr).Scan(&old)
	if err != nil && err != sql.ErrNoRows {
		return err
	}
	if old == value {
		return nil
	}
	now := s.now().UnixNano()
	if _, err = tx.Exec(`INSERT OR IGNORE INTO graph_nodes VALUES(?,'component',?)`, node, now); err != nil {
		return err
	}
	if _, err = tx.Exec(`INSERT INTO graph_charter VALUES(?,?,?,?,?) ON CONFLICT(node,attr) DO UPDATE SET value=excluded.value`, node, attr, value, now, actor); err != nil {
		return err
	}
	if err = graphRecord(tx, "charter", node, attr, old, value, actor, now); err != nil {
		return err
	}
	if err = bumpSettingsRevision(tx); err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	s.refreshDeliveryTasks()
	s.settingsNotify()
	return nil
}
