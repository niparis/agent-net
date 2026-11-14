package gateway

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/niparis/agent-net/internal/protocol"
)

// A lead Agent has exactly one lead session (the owner's decision on lead
// routing). The pin is agents.lead_session. Nothing else counts: another
// session of the same Agent never leads. A lead_set that names an Agent pins
// its one session, and asks the CTO which session leads when there are several.
// A new session pins a lead Agent that had none.

// leadSessionAskRef is the source key of the CTO question about an Agent's lead
// session, so the same open question is updated rather than repeated.
const leadSessionAskRef = "lead_session:"

// leadSessionRef is one session of a lead Agent, as the CTO question lists it.
type leadSessionRef struct {
	id, label string
}

// leadSessionCandidatesQ lists the sessions that may be a lead session: not a
// worker, not a merger, not archived, and not the owner, CTO or a bot.
func leadSessionCandidatesQ(q leadDB, agent string) ([]leadSessionRef, error) {
	rows, err := q.Query(`SELECT s.id,
		COALESCE(NULLIF(p.name,''),NULLIF(s.name,''),NULLIF(s.title,''),NULLIF(s.repo_project,''),s.id)
		FROM sessions s LEFT JOIN profiles p ON p.session_id=s.id
		WHERE s.agent_id=? AND s.worker='' AND s.archived_at=0
		AND s.harness NOT IN ('owner','cto','bot')
		AND NOT EXISTS(SELECT 1 FROM project_mergers pm WHERE pm.session_id=s.id)
		AND NOT EXISTS(SELECT 1 FROM agents ma WHERE ma.id=s.agent_id AND ma.role='merger')
		ORDER BY s.last_seen DESC,s.id`, agent)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []leadSessionRef
	for rows.Next() {
		var s leadSessionRef
		if err := rows.Scan(&s.id, &s.label); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// agentLeadsAnythingQ says whether an Agent leads a Repo or an active Team, or
// holds a lead-level grant. Only such an Agent gets a lead session pinned.
func agentLeadsAnythingQ(q leadDB, agent string) (bool, error) {
	var leads bool
	err := q.QueryRow(`SELECT EXISTS(
		SELECT 1 FROM repos r WHERE NOT EXISTS(SELECT 1 FROM archived_repos ar WHERE ar.repo_id=r.id)
			AND COALESCE((SELECT rl.agent_id FROM repo_leads rl WHERE rl.repo_id=r.id),r.owner_agent_id)=?
		UNION ALL SELECT 1 FROM teams t WHERE t.active=1 AND t.owner_agent_id=?
		UNION ALL SELECT 1 FROM agents a WHERE a.id=? AND a.lead_level<>0 AND a.role<>'merger')`, agent, agent, agent).Scan(&leads)
	return leads, err
}

// pinFirstLeadSessionQ pins a new session when it is the only session of a lead
// Agent that has no pin. A later session never moves the pin.
func (s *Store) pinFirstLeadSessionQ(q askDB, session string) error {
	var agent, worker, harness string
	err := q.QueryRow(`SELECT COALESCE(agent_id,''),worker,harness FROM sessions WHERE id=?`, session).Scan(&agent, &worker, &harness)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if agent == "" || worker != "" || harness == "bot" || harness == "cto" || protocol.IsOwner(harness) {
		return nil
	}
	var pin string
	if err := q.QueryRow(`SELECT COALESCE(lead_session,'') FROM agents WHERE id=?`, agent).Scan(&pin); err != nil {
		return err
	}
	if pin != "" {
		return nil
	}
	sessions, err := leadSessionCandidatesQ(q, agent)
	if err != nil {
		return err
	}
	leads, err := agentLeadsAnythingQ(q, agent)
	if err != nil || !leads {
		return err
	}
	if len(sessions) == 1 && sessions[0].id == session {
		_, err = q.Exec(`UPDATE agents SET lead_session=? WHERE id=? AND COALESCE(lead_session,'')=''`, session, agent)
		return err
	}
	// A second session with no pin does not pick one: the CTO is asked once.
	if len(sessions) > 1 {
		return s.askWhichLeadSessionQ(q, agent, sessions)
	}
	return nil
}

// pinOrAskLeadSessionTx runs after a lead_set that names an Agent. One session
// is pinned. Several are not guessed at: the CTO is asked once which one leads.
func (s *Store) pinOrAskLeadSessionTx(tx *sql.Tx, agent string) error {
	sessions, err := leadSessionCandidatesQ(tx, agent)
	if err != nil {
		return err
	}
	switch len(sessions) {
	case 0:
		return nil
	case 1:
		return setAgentLeadSessionTx(tx, agent, sessions[0].id)
	default:
		return s.askWhichLeadSessionQ(tx, agent, sessions)
	}
}

// askWhichLeadSessionQ writes the CTO's one question about an Agent's lead
// session. It uses the Desk shape that routes an item to the CTO: next_who is
// the CTO session, so the item waits with the CTO and not on the owner's Desk.
func (s *Store) askWhichLeadSessionQ(q askDB, agent string, sessions []leadSessionRef) error {
	var cto string
	err := q.QueryRow(`SELECT value FROM owner_settings WHERE key='cto_session'`).Scan(&cto)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if cto == "" {
		return nil
	}
	name, err := agentNameQ(q, agent)
	if err != nil {
		return err
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Which session leads %s?", name)
	for _, session := range sessions {
		b.WriteString("\n- " + session.label)
	}
	_, err = s.createInboxItem(q, protocol.InboxItem{
		Kind: protocol.AskKindQuestion, SessionID: cto, Text: b.String(),
		FromKind: protocol.AskFromGateway, SourceRef: leadSessionAskRef + agent,
		NextWho: cto, AskerName: "agent-net",
	})
	return err
}

// askWhichLeadSession sends the question for an Agent whose pin was cleared.
// One remaining session still gets the question: the pin is the CTO's to name,
// not a guess from the archive path.
func (s *Store) askWhichLeadSession(agent string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	sessions, err := leadSessionCandidatesQ(tx, agent)
	if err != nil || len(sessions) == 0 {
		return err
	}
	if err = s.askWhichLeadSessionQ(tx, agent, sessions); err != nil {
		return err
	}
	return tx.Commit()
}

// repairArchivedLeadSessions clears a pin that names a session just archived,
// then asks the CTO about the sessions that are left.
func (s *Store) repairArchivedLeadSessions(archived []string) error {
	if len(archived) == 0 {
		return nil
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var agents []string
	for _, id := range archived {
		var agent string
		err := tx.QueryRow(`SELECT COALESCE(agent_id,'') FROM sessions WHERE id=? AND archived_at<>0`, id).Scan(&agent)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return err
		}
		if agent == "" {
			continue
		}
		res, err := tx.Exec(`UPDATE agents SET lead_session=NULL WHERE id=? AND lead_session=?`, agent, id)
		if err != nil {
			return err
		}
		if n, err := res.RowsAffected(); err != nil {
			return err
		} else if n > 0 {
			agents = append(agents, agent)
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	for _, agent := range agents {
		if err := s.askWhichLeadSession(agent); err != nil {
			return err
		}
	}
	return nil
}
