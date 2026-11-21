package gateway

import (
	"database/sql"
	"errors"

	"github.com/niparis/agent-net/internal/protocol"
)

// CTOSessionID returns the registered CTO network session, or "" when the
// owner has not started the CTO yet.
func (s *Store) CTOSessionID() (string, error) {
	var id string
	err := s.db.QueryRow(`SELECT value FROM gateway_state WHERE key='cto_session'`).Scan(&id)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return "", err
	}
	return id, nil
}

// RegisterCTOSession moves the one network identity to the owner's current
// machine. Native harness conversations do not determine its identity.
func (s *Store) RegisterCTOSession(node string) (string, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	var id string
	err = tx.QueryRow(`SELECT value FROM gateway_state WHERE key='cto_session'`).Scan(&id)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return "", err
	}
	if id == "" {
		id = protocol.NewID("s")
	}
	if _, err = tx.Exec(`UPDATE sessions SET node_id=? WHERE id=? AND harness='cto'`, node, id); err != nil {
		return "", err
	}
	id, _, err = s.upsertSession(tx, node, protocol.SessionInfo{SessionID: id, Harness: "cto", NativeID: "cto", Name: "CTO", Status: "idle", Delivery: "cto"}, true)
	if err != nil {
		return "", err
	}
	if _, err = tx.Exec(`INSERT INTO gateway_state(key,value,updated_at) VALUES('cto_session',?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value, updated_at=excluded.updated_at`, id, s.now().UnixNano()); err != nil {
		return "", err
	}
	if _, err = tx.Exec(`DELETE FROM profiles WHERE session_id=?`, id); err != nil {
		return "", err
	}
	return id, tx.Commit()
}

// RoleNetwork reads which role sessions exist, for setup to tell a new
// network from one that already has a CTO, a chief or a merger.
func (s *Store) RoleNetwork() (protocol.RoleNetwork, error) {
	var out protocol.RoleNetwork
	var err error
	if out.CTOSession, err = s.CTOSessionID(); err != nil {
		return out, err
	}
	if out.ChiefSession, err = chiefSession(s.db); err != nil {
		return out, err
	}
	err = s.db.QueryRow(`SELECT COUNT(*) FROM project_mergers`).Scan(&out.Mergers)
	return out, err
}
