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
		COALESCE(NULLIF(s.name,''),NULLIF(s.title,''),NULLIF(s.repo_project,''),s.id)
		FROM sessions s
		WHERE s.agent_id=? AND s.worker='' AND s.archived_at=0 AND s.replaced_by=''
		AND s.harness NOT IN ('owner','cto','bot')
		AND NOT EXISTS(SELECT 1 FROM repos pm WHERE pm.merger_session=s.id)
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
		SELECT 1 FROM repos r WHERE r.archived=0
			AND `+repoTopAgentOfSQL("r.id")+`=?
		UNION ALL SELECT 1 FROM teams t WHERE t.active=1 AND t.owner_agent_id=?
		UNION ALL SELECT 1 FROM agents a WHERE a.id=? AND a.lead_level<>0 AND a.role<>'merger')`, agent, agent, agent).Scan(&leads)
	return leads, err
}

// leadAgentHolderQ returns the live pinned lead session holding agent (other
// than excludeSession), or "" when agent is not a lead agent or has no other
// live pinned lead session.
func leadAgentHolderQ(q leadDB, agent, excludeSession string) (string, error) {
	if agent == "" {
		return "", nil
	}
	leads, err := agentLeadsAnythingQ(q, agent)
	if err != nil || !leads {
		return "", err
	}
	var holder string
	err = q.QueryRow(`SELECT COALESCE(a.lead_session,'')
		FROM agents a JOIN sessions s ON s.id=a.lead_session
		WHERE a.id=? AND a.lead_session<>'' AND a.lead_session<>?
		AND s.online=1 AND s.archived_at=0 AND s.replaced_by=''`, agent, excludeSession).Scan(&holder)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return holder, err
}

// repoMemberAgentQ finds a live non-lead member agent for repoID when the
// repo's lead agent is already held by its pinned lead session.
func repoMemberAgentQ(q leadDB, repoID, leadAgentID string) (string, error) {
	if repoID == "" {
		return "", nil
	}
	for _, cand := range []string{repoID, repoID + "-agent"} {
		if cand == "" || cand == leadAgentID {
			continue
		}
		var ok bool
		if err := q.QueryRow(`SELECT EXISTS(SELECT 1 FROM agents WHERE id=? AND tombstone=0 AND role NOT IN ('cto','bot','merger'))`, cand).Scan(&ok); err != nil {
			return "", err
		}
		if ok {
			return cand, nil
		}
	}
	if leadAgentID == repoID {
		return "", nil
	}
	var existing string
	err := q.QueryRow(`SELECT a.id FROM agents a
		JOIN sessions s ON s.agent_id=a.id
		WHERE s.repo_id=? AND a.id<>? AND a.tombstone=0 AND a.role NOT IN ('cto','bot','merger')
		AND NOT EXISTS(SELECT 1 FROM repos r WHERE r.lead_agent_id=a.id)
		ORDER BY a.id LIMIT 1`, repoID, leadAgentID).Scan(&existing)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return existing, err
}

// defaultSessionAgentQ picks the default agent for a new non-worker session in
// repoID. When the repo's lead agent is already held by another live pinned
// lead session and the repo has a member agent, the new session gets the
// member agent so only the pinned lead holds the lead agent_id.
func defaultSessionAgentQ(q leadDB, repoID, excludeSession string) (string, error) {
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
	holder, err := leadAgentHolderQ(q, leadAgent, excludeSession)
	if err != nil || holder == "" {
		return leadAgent, err
	}
	member, err := repoMemberAgentQ(q, repoID, leadAgent)
	if err != nil || member == "" {
		return leadAgent, err
	}
	return member, nil
}

// assignDefaultSessionAgentsQ fills agent_id for matching unassigned sessions
// using defaultSessionAgentQ.
func assignDefaultSessionAgentsQ(q askDB, where string, args ...any) error {
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
		agent, err := defaultSessionAgentQ(q, t.repo, t.id)
		if err != nil {
			return err
		}
		if agent == "" {
			continue
		}
		if _, err := q.Exec(`UPDATE sessions SET agent_id=? WHERE id=? AND agent_id IS NULL`, agent, t.id); err != nil {
			return err
		}
	}
	return nil
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
		if _, err = q.Exec(`UPDATE agents SET lead_session=? WHERE id=? AND COALESCE(lead_session,'')=''`, session, agent); err != nil {
			return err
		}
		return closeLeadSessionQuestionQ(q, agent, session, s.now().UnixNano())
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
		return s.setAgentLeadSessionTx(tx, agent, sessions[0].id)
	default:
		return s.askWhichLeadSessionQ(tx, agent, sessions)
	}
}

// askWhichLeadSessionQ writes the CTO's one question about an Agent's lead
// session. It uses the Desk shape that routes an item to the CTO: next_who is
// the CTO session, so the item waits with the CTO and not on the owner's Desk.
// An open question for the same Agent is refreshed, never repeated.
func (s *Store) askWhichLeadSessionQ(q askDB, agent string, sessions []leadSessionRef) error {
	var cto string
	err := q.QueryRow(`SELECT value FROM gateway_state WHERE key='cto_session'`).Scan(&cto)
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
		b.WriteString("\n- " + session.label + " (" + session.id + ")")
	}
	source := leadSessionAskRef + agent
	var id string
	err = q.QueryRow(`SELECT id FROM inbox_items WHERE source_ref=? AND status='open'`, source).Scan(&id)
	if err == nil {
		_, err = q.Exec(`UPDATE inbox_items SET text=? WHERE id=?`, b.String(), id)
		return err
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	_, err = s.createInboxItem(q, protocol.InboxItem{
		Kind: protocol.AskKindQuestion, SessionID: cto, Text: b.String(),
		FromKind: protocol.AskFromGateway, SourceRef: source,
		NextWho: cto, AskerName: "agent-net",
	})
	return err
}

// closeLeadSessionQuestionQ marks the open CTO question about an Agent answered
// when its lead session is pinned.
func closeLeadSessionQuestionQ(q askDB, agent, session string, now int64) error {
	_, err := q.Exec(`UPDATE inbox_items SET status='answered',answer=?,answered_at=?
		WHERE status='open' AND source_ref=?`, "Lead session: "+session, now, leadSessionAskRef+agent)
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
