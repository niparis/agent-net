package gateway

import (
	"database/sql"
	"errors"

	"github.com/niparis/agent-net/internal/protocol"
)

// ReserveSessionAgent validates a choice, prepares a durable launch hold when
// Ref is present, or binds its exact native ID after the harness creates it.
// The daemon admits this call only through its owner or approved-proposal path.
func (s *Store) ReserveSessionAgent(node string, p protocol.SessionAgentReservation) error {
	if _, err := s.ExpireSessionAgentStarts(); err != nil {
		return err
	}
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
	if p.Harness != "claude" && p.Harness != "pi" && p.Harness != "opencode" && p.Harness != "codex" {
		return reject("Agent selection requires a harness with a known native session ID")
	}
	if p.Failed {
		return s.cancelSessionAgentStart(tx, node, p)
	}
	if p.Ref != "" {
		var failed bool
		if err = tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM session_agent_start_failures WHERE node_id=? AND ref=?)`, node, p.Ref).Scan(&failed); err != nil {
			return err
		}
		if failed {
			if err = s.noteLateAgentNative(tx, node, p); err != nil {
				return err
			}
			if err = tx.Commit(); err != nil {
				return err
			}
			return reject("Agent choice for %s failed; session uses the Repo-owner default", p.Ref)
		}
	}
	if err = tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM agents WHERE id=? AND tombstone=0)`, p.AgentID).Scan(&allowed); err != nil {
		return err
	}
	if !allowed {
		return reject("unknown or tombstoned Agent %q", p.AgentID)
	}
	if p.Ref != "" {
		if p.NativeID == "" {
			if p.Cwd == "" {
				return reject("pending start needs cwd")
			}
			_, err = tx.Exec(`INSERT INTO session_agent_starts(node_id,harness,native_id,agent_id,ref,cwd,existing,expires_at,owner_session,project) SELECT ?,?,'',?,?,?,COALESCE(json_group_array(id),'[]'),?,?,? FROM sessions WHERE node_id=?`, node, p.Harness, p.AgentID, p.Ref, p.Cwd, s.now().Add(sessionAgentStartTimeout).UnixNano(), p.Owner, p.Project, node)
			if err != nil {
				return err
			}
			return tx.Commit()
		}
		return s.bindStartedAgent(tx, node, p)
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
	_, err = tx.Exec(`INSERT INTO session_agent_starts(node_id,harness,native_id,agent_id,ref,expires_at,owner_session,project) VALUES(?,?,?,?,?,?,?,?)`, node, p.Harness, p.NativeID, p.AgentID, p.NativeID, s.now().Add(sessionAgentStartTimeout).UnixNano(), p.Owner, p.Project)
	if err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) applyStartedAgent(q interface {
	execer
	QueryRow(string, ...any) *sql.Row
}, node string, in protocol.SessionInfo, id string, known bool) error {
	var agent string
	err := q.QueryRow(`SELECT agent_id FROM session_agent_starts WHERE node_id=? AND harness=? AND native_id=?`, node, in.Harness, in.NativeID).Scan(&agent)
	if errors.Is(err, sql.ErrNoRows) {
		// A pending launch must not temporarily impersonate the Repo's default Agent.
		_, err = q.Exec(`UPDATE sessions SET agent_id=NULL WHERE id=? AND `+sessionAgentHoldSQL("sessions"), id)
		return err
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

// Hold new lasting sessions of this harness until its exact native ID is known.
// Baseline IDs exclude existing sessions even when clocks are equal or rows are
// removed. Cwd is not used to guess which concurrent session the launch made.
func sessionAgentHoldSQL(alias string) string {
	return `EXISTS(SELECT 1 FROM session_agent_starts a WHERE a.node_id=` + alias + `.node_id AND a.harness=` + alias + `.harness AND a.native_id='' AND ` + alias + `.worker='' AND NOT EXISTS(SELECT 1 FROM session_agent_start_failures f WHERE f.node_id=` + alias + `.node_id AND f.harness=` + alias + `.harness AND f.native_id=` + alias + `.native_id) AND NOT EXISTS(SELECT 1 FROM json_each(a.existing) e WHERE e.value=` + alias + `.id))`
}

func (s *Store) SessionAgentPending(id string) (bool, error) {
	if _, err := s.ExpireSessionAgentStarts(); err != nil {
		return false, err
	}
	var pending bool
	err := s.db.QueryRow(`SELECT EXISTS(SELECT 1 FROM sessions s WHERE s.id=? AND `+sessionAgentHoldSQL("s")+`)`, id).Scan(&pending)
	return pending, err
}

func (s *Store) bindStartedAgent(tx *sql.Tx, node string, p protocol.SessionAgentReservation) error {
	var cwd, baseline string
	var expires int64
	if err := tx.QueryRow(`SELECT cwd,existing,expires_at FROM session_agent_starts WHERE node_id=? AND harness=? AND native_id='' AND ref=? AND agent_id=?`, node, p.Harness, p.Ref, p.AgentID).Scan(&cwd, &baseline, &expires); err != nil {
		return reject("unknown pending Agent start")
	}
	var id, actualCwd, worker string
	err := tx.QueryRow(`SELECT id,cwd,worker FROM sessions WHERE node_id=? AND harness=? AND native_id=?`, node, p.Harness, p.NativeID).Scan(&id, &actualCwd, &worker)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if id != "" {
		var old bool
		if err = tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM json_each(?) WHERE value=?)`, baseline, id).Scan(&old); err != nil {
			return err
		}
		if old || worker != "" || actualCwd != cwd {
			return reject("start must bind its newly created session")
		}
		if _, err = tx.Exec(`UPDATE sessions SET agent_id=? WHERE id=?`, p.AgentID, id); err != nil {
			return err
		}
	} else {
		if _, err = tx.Exec(`INSERT INTO session_agent_starts(node_id,harness,native_id,agent_id,ref,cwd,existing,expires_at,owner_session,project) VALUES(?,?,?,?,?,?,?,?,?,?)`, node, p.Harness, p.NativeID, p.AgentID, p.Ref, cwd, baseline, expires, p.Owner, p.Project); err != nil {
			return err
		}
	}
	if _, err = tx.Exec(`DELETE FROM session_agent_starts WHERE node_id=? AND harness=? AND native_id='' AND ref=?`, node, p.Harness, p.Ref); err != nil {
		return err
	}
	// Release unrelated new sessions to their ordinary Repo-owner default. The
	// selected session already has its explicit Agent, or a native reservation.
	if _, err = tx.Exec(`UPDATE sessions SET agent_id=(SELECT owner_agent_id FROM repos WHERE id=sessions.repo_id) WHERE node_id=? AND harness=? AND worker='' AND agent_id IS NULL`, node, p.Harness); err != nil {
		return err
	}
	return tx.Commit()
}
