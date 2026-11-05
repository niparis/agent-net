package gateway

import (
	"database/sql"
	"errors"
	"strings"

	"github.com/niparis/agent-net/internal/protocol"
)

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
	rows, err := s.db.Query(`SELECT id,display_name,role FROM agents WHERE tombstone=0 AND role<>'cto' ORDER BY display_name,id`)
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

func (s *Store) setSessionAgent(node string, p protocol.SessionAgentSetRequest) error {
	p.Agent = strings.TrimSpace(p.Agent)
	if p.SessionID == "" || p.Agent == "" {
		return reject("session and agent are required")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := sessionAgentOwner(tx, node, p.Owner); err != nil {
		return err
	}
	var harness, worker, old string
	var online bool
	err = tx.QueryRow(`SELECT harness,worker,COALESCE(agent_id,''),online FROM sessions WHERE id=?`, p.SessionID).Scan(&harness, &worker, &old, &online)
	if errors.Is(err, sql.ErrNoRows) {
		return reject("unknown session %q", p.SessionID)
	}
	if err != nil {
		return err
	}
	if protocol.IsOwner(harness) || harness == "cto" || harness == "bot" || worker != "" {
		return reject("only a lasting agent session can change its agent")
	}
	if !online {
		return reject("session is offline")
	}
	var exists bool
	if err = tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM agents WHERE id=? AND tombstone=0 AND role<>'cto')`, p.Agent).Scan(&exists); err != nil {
		return err
	}
	if !exists {
		return reject("unknown or unavailable agent")
	}
	if old == p.Agent {
		return tx.Commit()
	}
	if _, err = tx.Exec(`UPDATE sessions SET agent_id=? WHERE id=?`, p.Agent, p.SessionID); err != nil {
		return err
	}
	if _, err = tx.Exec(`INSERT INTO session_agent_history(session_id,old_agent_id,new_agent_id,actor,at) VALUES(?,?,?,?,?)`, p.SessionID, old, p.Agent, p.Owner, s.now().UnixNano()); err != nil {
		return err
	}
	return tx.Commit()
}
