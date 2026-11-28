package gateway

import (
	"database/sql"

	"github.com/niparis/agent-net/internal/protocol"
)

// memberTeam is the active team of a session that has a lead to send to: its
// team, lead and settings that concern a member.
type memberTeam struct {
	name, leadAgent, mode, held string
	asksToLead, leadMerges      bool
}

// memberTeamOf finds the active team of a session, with a lead that is not the
// session itself. The session's repo team comes first, then the team of its
// agent, as lead routing does. ok is false when the session is in no such
// team, or is the lead itself.
func (s *Store) memberTeamOf(session string) (t memberTeam, ok bool, err error) {
	return memberTeamIn(s.db, session)
}

// memberTeamIn is memberTeamOf on any reader, so a caller inside a transaction
// reads through it.
func memberTeamIn(q leadDB, session string) (t memberTeam, ok bool, err error) {
	var agent, repo, harness, worker string
	err = q.QueryRow(`SELECT COALESCE(agent_id,''),COALESCE(repo_id,''),harness,worker FROM sessions WHERE id=?`, session).Scan(&agent, &repo, &harness, &worker)
	if err == sql.ErrNoRows {
		return t, false, nil
	}
	if err != nil {
		return t, false, err
	}
	// Only agents in the tree have a lead. The owner, the CTO and workers
	// (which their caller answers) do not.
	if agent == "" || worker != "" || protocol.IsOwner(harness) || harness == "cto" {
		return t, false, nil
	}
	var teamID, asksTo, leadMerges string
	err = q.QueryRow(`SELECT t.id,t.name,t.owner_agent_id,t.authority_mode,t.held_back,t.asks_to,t.lead_merges FROM teams t
		LEFT JOIN repos r ON r.id=? AND r.team_id=t.id
		LEFT JOIN agents a ON a.id=? AND a.team_id=t.id
		WHERE t.active=1 AND (r.id IS NOT NULL OR a.id IS NOT NULL)
		ORDER BY CASE WHEN r.id IS NOT NULL THEN 0 ELSE 1 END,t.id LIMIT 1`, repo, agent).Scan(&teamID, &t.name, &t.leadAgent, &t.mode, &t.held, &asksTo, &leadMerges)
	if err == sql.ErrNoRows {
		return t, false, nil
	}
	if err != nil {
		return t, false, err
	}
	if t.leadAgent == "" {
		return t, false, nil
	}
	if same, err := isLeadSessionQ(q, t.leadAgent, agent, session); err != nil {
		return t, false, err
	} else if same {
		return t, false, nil
	}
	t.asksToLead = asksTo == protocol.AsksToLead
	t.leadMerges = protocol.NormalLeadMerges(leadMerges) == protocol.LeadMergesOn
	return t, true, nil
}

// askerTeam finds the active team that sends the questions of a session to its
// lead: a team of the session with asks_to=lead. ok is false otherwise.
func (s *Store) askerTeam(session string) (teamName, leadAgent string, ok bool, err error) {
	t, ok, err := s.memberTeamOf(session)
	if err != nil || !ok || !t.asksToLead {
		return "", "", false, err
	}
	return t.name, t.leadAgent, true, nil
}

// leadAskSession picks the session of the lead that gets the question: a live
// one, otherwise the latest one the lead agent had. The message waits for it.
// A merger is never the lead's session.
func (s *Store) leadAskSession(leadAgent string) (id, name string, err error) {
	id, name, err = liveLeadSessionQ(s.db, leadAgent, "", s.now())
	if err != sql.ErrNoRows {
		return id, name, err
	}
	err = s.db.QueryRow(`SELECT s.id,a.display_name FROM sessions s JOIN agents a ON a.id=s.agent_id
		WHERE s.agent_id=? AND s.worker='' AND s.archived_at=0 AND s.replaced_by='' AND s.harness NOT IN ('owner','cto') AND a.role<>'merger' ORDER BY s.last_seen DESC,s.id LIMIT 1`, leadAgent).Scan(&id, &name)
	if err == sql.ErrNoRows {
		return "", "", nil
	}
	return id, name, err
}

// TeamLeadFor returns the team lead of a session whose team sends blocked
// steps to its lead. It is the read behind query.team_lead: the Claude Code
// hint hook uses it to name the lead when a tool call was denied. An empty
// result means the session is outside such a team, or is the lead itself.
// It also answers for a team whose lead_merges is on, whatever its asks_to: the
// hook then states the grant.
func (s *Store) TeamLeadFor(session string) (protocol.TeamLeadResult, error) {
	var out protocol.TeamLeadResult
	t, ok, err := s.memberTeamOf(session)
	if err != nil || !ok || (!t.asksToLead && !t.leadMerges) {
		return out, err
	}
	leadSession, leadName, err := s.leadAskSession(t.leadAgent)
	if err != nil || leadSession == "" || leadSession == session {
		return out, err
	}
	if leadName == "" {
		leadName = t.leadAgent
	}
	return protocol.TeamLeadResult{Team: t.name, Lead: leadName, LeadSession: leadSession, LeadMerges: t.leadMerges, AsksToLead: t.asksToLead}, nil
}
