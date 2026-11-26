package gateway

import (
	"database/sql"
	"errors"
)

// The session tree is right because the writes keep it right (decision 0193,
// I1 to I3). Every write that can change who leads an Agent calls keepLeadTx
// for that Agent in its own transaction: a new session, an Agent change, a
// lead_set, a stop, an archive and a replace. Nothing repairs the tree later,
// and nothing asks the CTO which session leads.

// leadCandidateSQL is the condition, on sessions alias s, that a session may
// be the lead session of the Agent given as its one argument: a lasting
// session of that Agent, not archived or replaced, not the owner, the CTO, a
// bot or a merger.
var leadCandidateSQL = leadCandidateOfSQL("?")

// leadCandidateOfSQL is leadCandidateSQL for the Agent the SQL expression
// agent names, so the tree self-check reads the same condition in a join.
// The test that merger_session is not empty lets SQLite use the partial
// index repos_merger_session instead of scanning repos for each session.
func leadCandidateOfSQL(agent string) string {
	return `s.agent_id=` + agent + ` AND s.worker='' AND s.archived_at=0 AND s.replaced_by=''
	AND s.harness NOT IN ('owner','cto','bot')
	AND NOT EXISTS(SELECT 1 FROM repos pm WHERE pm.merger_session=s.id AND pm.merger_session<>'')`
}

// keepLeadTx holds I1 for one Agent. An Agent that leads nothing has no lead
// session. An Agent that leads something keeps its lead session while that
// session is online and not stopped. Otherwise the lead goes to a live
// session of the Agent, in this order: the session that replaced the old
// lead, the one the owner used last, the oldest. Offline sessions never take
// the lead (I4); an offline lead with no live session to follow it stays
// the lead until it is archived or replaced, so a lead that is away does not
// leave its members without a parent.
func (s *Store) keepLeadTx(q askDB, agent string) error {
	if agent == "" || agent == "cto" {
		return nil
	}
	var pin string
	err := q.QueryRow(`SELECT COALESCE(lead_session,'') FROM agents WHERE id=?`, agent).Scan(&pin)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	leads, err := agentLeadsAnythingQ(q, agent)
	if err != nil {
		return err
	}
	if !leads {
		if pin == "" {
			return nil
		}
		_, err = q.Exec(`UPDATE agents SET lead_session=NULL WHERE id=?`, agent)
		return err
	}
	var next string
	err = q.QueryRow(`SELECT s.id FROM sessions s WHERE `+leadCandidateSQL+` AND s.online=1 AND s.stood_down=''
		ORDER BY s.id=? DESC, s.id=COALESCE((SELECT replaced_by FROM sessions WHERE id=?),'') DESC,
		s.owner_used_at DESC, s.created_at, s.id LIMIT 1`, agent, pin, pin).Scan(&next)
	if errors.Is(err, sql.ErrNoRows) {
		if pin == "" {
			return nil
		}
		var kept bool
		if err = q.QueryRow(`SELECT EXISTS(SELECT 1 FROM sessions s WHERE s.id=? AND `+leadCandidateSQL+`)`, pin, agent).Scan(&kept); err != nil || kept {
			return err
		}
		_, err = q.Exec(`UPDATE agents SET lead_session=NULL WHERE id=?`, agent)
		return err
	}
	if err != nil || next == pin {
		return err
	}
	return s.setAgentLeadSessionTx(q, agent, next)
}

// keepLeadOfSessionTx runs keepLeadTx for the Agent a session holds now.
func (s *Store) keepLeadOfSessionTx(q askDB, session string) error {
	var agent string
	err := q.QueryRow(`SELECT COALESCE(agent_id,'') FROM sessions WHERE id=?`, session).Scan(&agent)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	return s.keepLeadTx(q, agent)
}

// leadingAgentsSQL lists the Agents that lead something: a Repo, an active
// Team, or a lead-level grant (a merger's grant does not count). Only such an
// Agent gets a lead session pinned, and the session tree (sessionParent)
// reads the same list.
var leadingAgentsSQL = `SELECT agent FROM (
	SELECT ` + repoTopAgentOfSQL("r.id") + ` AS agent FROM repos r WHERE r.archived=0
	UNION SELECT owner_agent_id FROM teams WHERE active=1
	UNION SELECT id FROM agents WHERE lead_level<>0 AND role<>'merger')
	WHERE agent IS NOT NULL AND agent<>''`

// agentLeadsAnythingQ says whether an Agent is in leadingAgentsSQL.
func agentLeadsAnythingQ(q leadDB, agent string) (bool, error) {
	var leads bool
	err := q.QueryRow(`SELECT EXISTS(SELECT 1 FROM (`+leadingAgentsSQL+`) WHERE agent=?)`, agent).Scan(&leads)
	return leads, err
}

// leadHeldQ says whether a lead Agent has a lead session that a new session
// must not take the Agent from: pinned, of that Agent, not archived, replaced
// or stopped. An offline lead still counts, so a session started while the
// lead is away joins as a member and does not take the lead (I2).
func leadHeldQ(q leadDB, agent, excludeSession string) (bool, error) {
	var held bool
	err := q.QueryRow(`SELECT EXISTS(SELECT 1 FROM agents a JOIN sessions s ON s.id=a.lead_session
		WHERE a.id=? AND s.id<>? AND s.agent_id=a.id AND s.archived_at=0 AND s.replaced_by='' AND s.stood_down='')`,
		agent, excludeSession).Scan(&held)
	return held, err
}

// memberAgentTx returns the member Agent of a Repo (I2): the Agent named
// like the Repo, or "<repo>-agent", or another Agent already working in the
// Repo, as long as it leads nothing. A Repo with none gets "<repo>-agent" at
// once. Its sessions sit in the Repo, so it reports to the Repo's lead Agent.
func (s *Store) memberAgentTx(q askDB, repoID, leadAgentID string) (string, error) {
	isMember := `a.tombstone=0 AND a.role NOT IN ('cto','bot','merger') AND a.id<>? AND a.id NOT IN (` + leadingAgentsSQL + `)`
	for _, cand := range []string{repoID, repoID + "-agent"} {
		var ok bool
		if err := q.QueryRow(`SELECT EXISTS(SELECT 1 FROM agents a WHERE a.id=? AND `+isMember+`)`, cand, leadAgentID).Scan(&ok); err != nil {
			return "", err
		}
		if ok {
			return cand, nil
		}
	}
	var existing string
	err := q.QueryRow(`SELECT a.id FROM agents a JOIN sessions s ON s.agent_id=a.id
		WHERE s.repo_id=? AND s.worker='' AND `+isMember+` ORDER BY a.id LIMIT 1`, repoID, leadAgentID).Scan(&existing)
	if err == nil {
		return existing, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return "", err
	}
	member := repoID + "-agent"
	now := s.now().UnixNano()
	_, err = q.Exec(`INSERT INTO agents(id,display_name,role,created_at,updated_at,tombstone) VALUES(?,?,'',?,?,0)
		ON CONFLICT(id) DO UPDATE SET tombstone=0,updated_at=excluded.updated_at`, member, member, now, now)
	return member, err
}

// defaultSessionAgentQ picks the Agent of a new lasting session in repoID that
// was not started for a named Agent (I2): the Repo's lead Agent while it has
// no lead session, else the Repo's member Agent.
func (s *Store) defaultSessionAgentQ(q askDB, repoID, excludeSession string) (string, error) {
	if repoID == "" {
		return "", nil
	}
	var leadAgent string
	err := q.QueryRow(`SELECT COALESCE(lead_agent_id,'') FROM repos WHERE id=?`, repoID).Scan(&leadAgent)
	if errors.Is(err, sql.ErrNoRows) || leadAgent == "" {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	held, err := leadHeldQ(q, leadAgent, excludeSession)
	if err != nil || !held {
		return leadAgent, err
	}
	return s.memberAgentTx(q, repoID, leadAgent)
}

// assignDefaultSessionAgentsQ gives the matching sessions with no Agent their
// default Agent, and keeps I1 for each Agent it hands out.
func (s *Store) assignDefaultSessionAgentsQ(q askDB, where string, args ...any) error {
	rows, err := q.Query(`SELECT id, repo_id FROM sessions WHERE worker='' AND agent_id IS NULL AND repo_id IS NOT NULL AND repo_id<>'' AND `+where, args...)
	if err != nil {
		return err
	}
	type pair struct{ id, repo string }
	var targets []pair
	for rows.Next() {
		var p pair
		if err := rows.Scan(&p.id, &p.repo); err != nil {
			rows.Close()
			return err
		}
		targets = append(targets, p)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	for _, t := range targets {
		agent, err := s.defaultSessionAgentQ(q, t.repo, t.id)
		if err != nil {
			return err
		}
		if agent == "" {
			continue
		}
		if _, err := q.Exec(`UPDATE sessions SET agent_id=? WHERE id=? AND agent_id IS NULL`, agent, t.id); err != nil {
			return err
		}
		if err := s.keepLeadTx(q, agent); err != nil {
			return err
		}
	}
	return nil
}
