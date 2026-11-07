package gateway

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// ReserveRepoStart serializes admission with the live count and other launches.
// Registration itself remains unrestricted, including terminal sessions.
func (s *Store) ReserveRepoStart(node string, p protocol.RepoStartReservation) (protocol.RepoStartResult, error) {
	var out protocol.RepoStartResult
	tx, err := s.db.Begin()
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	var allowed bool
	if err = tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM sessions WHERE id=? AND node_id=? AND harness='owner' AND worker='')`, p.Owner, node).Scan(&allowed); err != nil {
		return out, err
	}
	if !allowed {
		return out, reject("session start requires the owner of this machine")
	}
	if p.Ref == "" {
		return out, reject("session start needs a reference")
	}
	if p.Release {
		_, err = tx.Exec(`DELETE FROM repo_start_reservations WHERE node_id=? AND ref=?`, node, p.Ref)
		if err != nil {
			return out, err
		}
		return out, tx.Commit()
	}
	if p.NativeID != "" {
		_, err = tx.Exec(`UPDATE repo_start_reservations SET native_id=? WHERE node_id=? AND ref=?`, p.NativeID, node, p.Ref)
		if err != nil {
			return out, err
		}
		_, err = tx.Exec(`DELETE FROM repo_start_reservations WHERE node_id=? AND ref=? AND EXISTS(SELECT 1 FROM sessions WHERE node_id=? AND harness=? AND native_id=? AND online=1)`, node, p.Ref, node, p.Harness, p.NativeID)
		if err != nil {
			return out, err
		}
		return out, tx.Commit()
	}
	now := s.now().UnixNano()
	if _, err = tx.Exec(`DELETE FROM repo_start_reservations WHERE expires_at<=?`, now); err != nil {
		return out, err
	}
	var max int
	err = tx.QueryRow(`SELECT CAST(value AS INTEGER) FROM setting_values WHERE key='max_agents' AND level='repo' AND scope=? AND item=''`, p.Project).Scan(&max)
	if err != nil && err != sql.ErrNoRows {
		return out, err
	}
	if max == 0 {
		return out, tx.Commit()
	}
	var existing bool
	if err = tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM repo_start_reservations WHERE node_id=? AND ref=?)`, node, p.Ref).Scan(&existing); err != nil {
		return out, err
	}
	if existing {
		out.Reserved = true
		return out, tx.Commit()
	}
	var count int
	err = tx.QueryRow(`SELECT (SELECT COUNT(*) FROM sessions s WHERE s.repo_id=? AND s.online=1 AND s.worker='' AND s.harness NOT IN ('owner','cto','bot') AND s.id<>COALESCE((SELECT value FROM owner_settings WHERE key='cto_session'),'') AND NOT EXISTS(SELECT 1 FROM project_mergers m WHERE m.session_id=s.id)) + (SELECT COUNT(*) FROM repo_start_reservations r WHERE r.repo_id=? AND NOT EXISTS(SELECT 1 FROM sessions s WHERE s.node_id=r.node_id AND s.harness=r.harness AND s.native_id=r.native_id AND r.native_id<>'' AND s.online=1))`, p.Project, p.Project).Scan(&count)
	if err != nil {
		return out, err
	}
	if count >= max {
		var name string
		if e := tx.QueryRow(`SELECT display_name FROM repos WHERE id=?`, p.Project).Scan(&name); e != nil {
			name = p.Project
		}
		value, _ := json.Marshal(max + 1)
		expect, _ := json.Marshal(max)
		out.Status = "failed"
		out.Error = fmt.Sprintf("%s already has %d agents (max %d). Stop one, or raise the max to %d.", name, count, max, max+1)
		out.SuggestedChange = &protocol.SettingsChange{Key: "max_agents", Level: "repo", Scope: p.Project, Value: value, Expect: expect}
		return out, tx.Commit()
	}
	if p.Check {
		return out, tx.Commit()
	}
	_, err = tx.Exec(`INSERT INTO repo_start_reservations(node_id,ref,repo_id,harness,expires_at) VALUES(?,?,?,?,?)`, node, p.Ref, p.Project, p.Harness, s.now().Add(2*time.Minute).UnixNano())
	if err != nil {
		return out, err
	}
	out.Reserved = true
	return out, tx.Commit()
}
