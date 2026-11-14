package gateway

import (
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// routeLead runs in the message transaction after the original reply has been
// authorized. The normal request gate then checks the actual recipient.
func routeLead(tx *sql.Tx, m *protocol.Message, now time.Time) (string, error) {
	var senderRepo, senderAgent, harness, worker, cto string
	err := tx.QueryRow(`SELECT COALESCE(repo_id,''),COALESCE(agent_id,''),harness,worker,COALESCE((SELECT value FROM owner_settings WHERE key='cto_session'),'') FROM sessions WHERE id=?`, m.From).Scan(&senderRepo, &senderAgent, &harness, &worker, &cto)
	if err != nil {
		return "", err
	}
	if protocol.IsOwner(harness) || m.From == cto || m.FromCTO {
		return "", nil
	}
	// A reply always reaches the session that sent the message it answers.
	// Lead routing only applies to new requests and notifications.
	if m.ParentID != "" {
		var parentSender string
		if err = tx.QueryRow(`SELECT sender FROM messages WHERE id=?`, m.ParentID).Scan(&parentSender); err != nil && err != sql.ErrNoRows {
			return "", err
		}
		if parentSender == m.To {
			return "", nil
		}
	}
	if m.ConversationID != "" {
		var inThread bool
		if err = tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM messages WHERE conversation_id=? AND sender=?)`, m.ConversationID, m.To).Scan(&inThread); err != nil {
			return "", err
		}
		if inThread {
			return "", nil
		}
	}
	if worker != "" {
		var owner string
		if err = tx.QueryRow(`SELECT COALESCE(CASE WHEN json_valid(worker) THEN json_extract(worker,'$.owner') END,'') FROM sessions WHERE id=?`, m.From).Scan(&owner); err != nil {
			return "", err
		}
		if owner == m.To {
			return "", nil
		}
	}
	var repo, agent, targetName string
	err = tx.QueryRow(`SELECT COALESCE(repo_id,''),COALESCE(agent_id,''),COALESCE(NULLIF(name,''),id) FROM sessions WHERE id=?`, m.To).Scan(&repo, &agent, &targetName)
	if err != nil {
		return "", err
	}
	if agent == "" {
		return "", nil
	}
	// A merger runs as its own Agent (decision 0160). A message addressed to
	// it stays with it, as when it ran as the Repo's Agent.
	if merger, err := sessionIsMergerQ(tx, m.To); err != nil || merger {
		return "", err
	}
	var repoLead, repoName string
	if repo != "" {
		if repoLead, err = repoTopAgentQ(tx, repo); err != nil {
			return "", err
		}
		if repoLead != "" {
			if repoName, err = repoNameQ(tx, repo); err != nil {
				return "", err
			}
		}
	}
	try := func(lead, scope, inRepo string) (string, bool, error) {
		if lead == "" {
			return "", false, nil
		}
		if same, err := isLeadSessionQ(tx, lead, agent, m.To); err != nil {
			return "", false, err
		} else if same {
			return "", false, nil
		}
		id, name, err := liveLeadSessionQ(tx, lead, inRepo, now)
		if err == sql.ErrNoRows {
			return "Lead has no live session; delivered as addressed.", true, nil
		}
		if err != nil {
			return "", false, err
		}
		if id == m.From || id == m.To {
			return "", true, nil
		}
		note := fmt.Sprintf("Redirected to %s (lead of %s); originally sent to %s.", name, scope, targetName)
		// The sender's receipt keeps the short note. The lead's copy also says
		// what to do with the request.
		m.To = id
		m.Body = note + "\n" + fmt.Sprintf("You lead %s: answer this or hand it off to %s with agent_message_handoff.", scope, targetName) + "\n" + m.Body
		return note, true, nil
	}
	if repo != "" && senderRepo != repo && repoLead != "" {
		// The target is its Repo's lead, so the message stays with it. The Team
		// lead is not on this path.
		if same, err := isLeadSessionQ(tx, repoLead, agent, m.To); err != nil {
			return "", err
		} else if same {
			return "", nil
		}
		note, done, e := try(repoLead, repoName, repo)
		if done || e != nil {
			return note, e
		}
	}
	// The target's repo team is the session context; otherwise use its Agent team.
	var teamID, teamName, teamLead string
	err = tx.QueryRow(`SELECT t.id,t.name,COALESCE(t.owner_agent_id,'') FROM teams t JOIN team_members tm ON tm.team_id=t.id WHERE t.active=1 AND ((tm.member_kind='repo' AND tm.member_id=?) OR (tm.member_kind='agent' AND tm.member_id=?)) ORDER BY CASE tm.member_kind WHEN 'repo' THEN 0 ELSE 1 END LIMIT 1`, repo, agent).Scan(&teamID, &teamName, &teamLead)
	if err != nil && err != sql.ErrNoRows {
		return "", err
	}
	if err == nil {
		var inCrew bool
		err = tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM team_members WHERE team_id=? AND ((member_kind='repo' AND member_id=? AND member_id<>'') OR (member_kind='agent' AND member_id=? AND member_id<>'')))`, teamID, senderRepo, senderAgent).Scan(&inCrew)
		if err != nil {
			return "", err
		}
		if !inCrew {
			note, done, e := try(teamLead, teamName, "")
			if done || e != nil {
				return note, e
			}
			if teamLead == "" {
				return "No lead is set; delivered as addressed.", nil
			}
		}
	}
	if repo != "" && senderRepo != repo && repoLead == "" {
		return "No lead is set; delivered as addressed.", nil
	}
	return "", nil
}

// resolveSessionName resolves an exact session name to the session that gets a
// message: a live session with that name first, as before, otherwise the newest
// non-archived, non-worker session of the same Agent. That session waits in the
// queue and the wake on message resumes it, like a send to its ID.
func (s *Store) resolveSessionName(name string) (string, error) {
	rows, err := s.db.Query(`SELECT s.id,COALESCE(s.agent_id,''),s.online,s.worker,s.harness,COALESCE(n.hostname,'-'),s.cwd
		FROM sessions s LEFT JOIN profiles p ON p.session_id=s.id LEFT JOIN nodes n ON n.id=s.node_id
		WHERE COALESCE(NULLIF(p.name,''),s.name)=? AND s.archived_at=0`, name)
	if err != nil {
		return "", err
	}
	type liveSession struct{ id, harness, host, cwd string }
	var live []liveSession
	agents := map[string]bool{}
	for rows.Next() {
		var id, agent, worker, harness, host, cwd string
		var online int
		if err := rows.Scan(&id, &agent, &online, &worker, &harness, &host, &cwd); err != nil {
			rows.Close()
			return "", err
		}
		if harness == "owner" {
			continue
		}
		if online != 0 {
			live = append(live, liveSession{id, harness, host, cwd})
			continue
		}
		if worker == "" && agent != "" && harness != "cto" {
			agents[agent] = true
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return "", err
	}
	switch {
	case len(live) == 1:
		return live[0].id, nil
	case len(live) > 1:
		lines := make([]string, 0, len(live))
		for _, s := range live {
			lines = append(lines, fmt.Sprintf("%s (%s on %s, %s)", s.id, s.harness, s.host, s.cwd))
		}
		return "", reject("%d sessions are named %q; use a session ID: %s", len(live), name, strings.Join(lines, "; "))
	}
	if len(agents) == 0 {
		return "", reject("no session is named %q; use agent_sessions_find to find a session_id", name)
	}
	if len(agents) > 1 {
		return "", reject("more than one agent is named %q; use a session ID", name)
	}
	var agent string
	for a := range agents {
		agent = a
	}
	var id string
	err = s.db.QueryRow(`SELECT s.id FROM sessions s WHERE s.agent_id=? AND s.worker='' AND s.archived_at=0
		AND s.harness NOT IN ('owner','cto') ORDER BY s.last_seen DESC,s.id LIMIT 1`, agent).Scan(&id)
	if err == sql.ErrNoRows {
		return "", reject("no session is named %q; use agent_sessions_find to find a session_id", name)
	}
	return id, err
}

// ResolveLeadAddress resolves a Repo or Team name or ID, or an exact session
// name, to the session that gets the message. A live session comes first. When
// the lead or agent has no live session, the newest one is returned so the
// caller stores and queues the message exactly as for a session-ID send.
func (s *Store) ResolveLeadAddress(address string) (string, error) {
	kind, name, ok := strings.Cut(strings.TrimSpace(address), ":")
	name = strings.TrimSpace(name)
	if !ok {
		address = strings.TrimSpace(address)
		if address == "" {
			return "", reject("use a session ID, an exact name, repo:<name> or team:<name>")
		}
		return s.resolveSessionName(address)
	}
	if name == "" || (kind != "repo" && kind != "team") {
		return "", reject("use repo:<name> or team:<name>")
	}
	return leadAddressSessionQ(s.db, kind, name, s.now())
}

// validateLeadAddress checks the explicit address again in the send transaction.
// A sender cannot use a forged address to avoid ordinary lead routing.
func validateLeadAddress(tx *sql.Tx, address, recipient string, now time.Time) error {
	kind, name, ok := strings.Cut(address, ":")
	name = strings.TrimSpace(name)
	if !ok || name == "" || (kind != "repo" && kind != "team") {
		return reject("invalid lead address")
	}
	chosen, err := leadAddressSessionQ(tx, kind, name, now)
	if err != nil || chosen != recipient {
		return reject("lead for %s changed or went offline; retry the address", address)
	}
	return nil
}

// limitedNote is the extra line in the sender's receipt when the recipient
// hit its provider usage limit. The message is still delivered.
func limitedNote(tx *sql.Tx, recipient string, now time.Time) (string, error) {
	var until int64
	var reason, name string
	err := tx.QueryRow(`SELECT s.limited_until,s.limited_reason,COALESCE(NULLIF(p.name,''),NULLIF(s.name,''),s.id) FROM sessions s LEFT JOIN profiles p ON p.session_id=s.id WHERE s.id=?`, recipient).Scan(&until, &reason, &name)
	if err == sql.ErrNoRows {
		return "", nil
	}
	if err != nil || until <= now.Unix() {
		return "", err
	}
	return fmt.Sprintf("%s hit its %s; it can answer after %s UTC", name, protocol.LimitKind(reason), time.Unix(until, 0).UTC().Format("15:04")), nil
}
