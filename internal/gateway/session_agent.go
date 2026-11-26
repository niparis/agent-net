package gateway

import (
	"database/sql"
	"errors"
	"strings"

	"github.com/niparis/agent-net/internal/protocol"
)

// checkActiveSessionIdentity runs in the same transaction as identity writes.
func checkActiveSessionIdentity(q interface{ QueryRow(string, ...any) *sql.Row }, session string) error {
	var inactive bool
	err := q.QueryRow(`SELECT EXISTS(SELECT 1 FROM sessions s WHERE s.id=? AND s.online=1
 AND `+graphRefGateSQL+`='1' AND
 (EXISTS(SELECT 1 FROM repos WHERE id=s.repo_id AND archived<>0) OR EXISTS(SELECT 1 FROM agents WHERE id=s.agent_id AND tombstone<>0)))`, session).Scan(&inactive)
	if err != nil {
		return err
	}
	if inactive {
		return reject("session identity is inactive")
	}
	return nil
}

// sessionAgentOwner checks an authenticated machine's owner pseudo-session.
func sessionAgentOwner(q interface{ QueryRow(string, ...any) *sql.Row }, node, owner string) error {
	var valid bool
	if err := q.QueryRow(`SELECT EXISTS(SELECT 1 FROM sessions WHERE id=? AND node_id=? AND harness='owner' AND worker='')`, owner, node).Scan(&valid); err != nil {
		return err
	}
	if !valid {
		return reject("only this machine's owner may change session agents")
	}
	return nil
}

func (s *Store) sessionAgentOptions(node, owner string) (protocol.SessionAgentOptions, error) {
	out := protocol.SessionAgentOptions{Agents: []protocol.SessionAgentOption{}}
	if err := sessionAgentOwner(s.db, node, owner); err != nil {
		return out, err
	}
	rows, err := s.db.Query(`SELECT id,display_name,role FROM agents WHERE tombstone=0 AND role NOT IN ('cto','bot') ORDER BY display_name,id`)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var a protocol.SessionAgentOption
		if err := rows.Scan(&a.ID, &a.Name, &a.Role); err != nil {
			return out, err
		}
		out.Agents = append(out.Agents, a)
	}
	return out, rows.Err()
}

func (s *Store) setSessionAgent(node string, p protocol.SessionAgentSetRequest) (protocol.SessionAgentSetResult, error) {
	res := protocol.SessionAgentSetResult{OK: true}
	p.Agent = strings.TrimSpace(p.Agent)
	if p.SessionID == "" || p.Agent == "" {
		return res, reject("session and agent are required")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return res, err
	}
	defer tx.Rollback()
	if err := sessionAgentOwner(tx, node, p.Owner); err != nil {
		return res, err
	}
	var harness, worker, old string
	var online bool
	err = tx.QueryRow(`SELECT harness,worker,COALESCE(agent_id,''),online FROM sessions WHERE id=?`, p.SessionID).Scan(&harness, &worker, &old, &online)
	if errors.Is(err, sql.ErrNoRows) {
		return res, reject("unknown session %q", p.SessionID)
	}
	if err != nil {
		return res, err
	}
	if protocol.IsOwner(harness) || harness == "cto" || harness == "bot" || worker != "" {
		return res, reject("only a lasting agent session can change its agent")
	}
	if !online {
		return res, reject("session is offline")
	}
	var exists bool
	if err = tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM agents WHERE id=? AND tombstone=0 AND role NOT IN ('cto','bot'))`, p.Agent).Scan(&exists); err != nil {
		return res, err
	}
	if !exists {
		return res, reject("unknown or unavailable agent")
	}
	if old == p.Agent {
		return res, tx.Commit()
	}
	if _, err = tx.Exec(`UPDATE sessions SET agent_id=? WHERE id=?`, p.Agent, p.SessionID); err != nil {
		return res, err
	}
	// The new Agent and the one the session left both keep I1: a lead that
	// changes Agent hands its lead to a live session of the old one.
	if err = s.keepLeadTx(tx, p.Agent); err != nil {
		return res, err
	}
	if err = s.keepLeadTx(tx, old); err != nil {
		return res, err
	}
	if err = checkActiveSessionIdentity(tx, p.SessionID); err != nil {
		return res, err
	}
	if p.CopyNotes {
		if res.CopiedNotes, res.KeptNotes, err = copyAgentNotesQ(tx, old, p.Agent, s.now()); err != nil {
			return res, err
		}
	}
	return res, tx.Commit()
}
