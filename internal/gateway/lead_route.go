package gateway

import (
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

type leadDB interface {
	QueryRow(string, ...any) *sql.Row
	Query(string, ...any) (*sql.Rows, error)
}

const repoLeadExpr = `CASE WHEN EXISTS(SELECT 1 FROM repo_leads WHERE repo_id=repos.id) THEN (SELECT agent_id FROM repo_leads WHERE repo_id=repos.id) ELSE owner_agent_id END`

func repoLeadQuery(where string) string {
	return `SELECT id,display_name,` + repoLeadExpr + `,(SELECT lead_session FROM repo_leads WHERE repo_id=repos.id) FROM repos WHERE ` + where
}
func repoLeadRows(db leadDB, where string, args ...any) (*sql.Rows, error) {
	return db.Query(repoLeadQuery(where), args...)
}
func teamLeadRows(db leadDB, name string) (*sql.Rows, error) {
	return db.Query(`SELECT id,owner_agent_id,lead_session FROM teams WHERE (id=? OR name=? COLLATE NOCASE) AND active=1`, name, name)
}

// notMergerSQL keeps a project merger out of the picks for a lead. A merger
// shares its project's lead agent, so this test is the only thing that keeps a
// request meant for the lead away from it.
func notMergerSQL(alias string) string {
	return "NOT EXISTS(SELECT 1 FROM project_mergers pm WHERE pm.session_id=" + alias + ".id)"
}

// isProjectMerger says whether a session is registered as the merger of a project.
func isProjectMerger(q leadDB, session string) (bool, error) {
	var merger bool
	err := q.QueryRow(`SELECT EXISTS(SELECT 1 FROM project_mergers WHERE session_id=?)`, session).Scan(&merger)
	return merger, err
}

// liveLeadSession picks the lead's session. A session under a provider usage
// limit comes last: another live session of the same agent answers sooner. If
// all of them are limited, the usual order decides and the message is delivered.
// A project merger is never picked.
func liveLeadSession(db leadDB, agent, repo, preferred string, withName bool, now time.Time) *sql.Row {
	selectPart, join := "s.id", ""
	if withName {
		selectPart, join = "s.id,a.display_name", " JOIN agents a ON a.id=s.agent_id"
	}
	return db.QueryRow(`SELECT `+selectPart+` FROM sessions s`+join+` WHERE s.agent_id=? AND s.online=1 AND s.worker='' AND s.harness NOT IN ('owner','cto') AND `+notMergerSQL("s")+` ORDER BY CASE WHEN s.limited_until>? THEN 1 ELSE 0 END,CASE WHEN s.id=? THEN 0 ELSE 1 END,CASE WHEN s.repo_id=? THEN 0 ELSE 1 END,s.last_seen DESC,s.id LIMIT 1`, agent, now.Unix(), preferred, repo)
}

// isLeadSession says whether a session is its lead's session: any session of the
// lead Agent, or only the pinned lead session when one is set. Repo and Team
// routing both use this test.
func isLeadSession(lead, pinned sql.NullString, agent, session string) bool {
	return lead.Valid && lead.String == agent && (!pinned.Valid || pinned.String == session)
}

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
	if merger, err := isProjectMerger(tx, m.To); err != nil || merger {
		return "", err
	}
	var repoLead, repoSession sql.NullString
	var repoName string
	if repo != "" {
		var repoID string
		err = tx.QueryRow(repoLeadQuery(`id=?`), repo).Scan(&repoID, &repoName, &repoLead, &repoSession)
		if err != nil && err != sql.ErrNoRows {
			return "", err
		}
	}
	try := func(lead, preferred sql.NullString, scope, inRepo string) (string, bool, error) {
		if !lead.Valid || lead.String == "" || isLeadSession(lead, preferred, agent, m.To) {
			return "", false, nil
		}
		var id, name string
		err := liveLeadSession(tx, lead.String, inRepo, preferred.String, true, now).Scan(&id, &name)
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
	if repo != "" && senderRepo != repo && repoLead.Valid {
		// The target is its Repo's lead, so the message stays with it. The Team
		// lead is not on this path.
		if isLeadSession(repoLead, repoSession, agent, m.To) {
			return "", nil
		}
		note, done, e := try(repoLead, repoSession, repoName, repo)
		if done || e != nil {
			return note, e
		}
	}
	// The target's repo team is the session context; otherwise use its Agent team.
	var teamID, teamName string
	var teamLead, teamSession sql.NullString
	err = tx.QueryRow(`SELECT t.id,t.name,t.owner_agent_id,t.lead_session FROM teams t JOIN team_members tm ON tm.team_id=t.id WHERE t.active=1 AND ((tm.member_kind='repo' AND tm.member_id=?) OR (tm.member_kind='agent' AND tm.member_id=?)) ORDER BY CASE tm.member_kind WHEN 'repo' THEN 0 ELSE 1 END LIMIT 1`, repo, agent).Scan(&teamID, &teamName, &teamLead, &teamSession)
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
			note, done, e := try(teamLead, teamSession, teamName, "")
			if done || e != nil {
				return note, e
			}
			if !teamLead.Valid {
				return "No lead is set; delivered as addressed.", nil
			}
		}
	}
	if repo != "" && senderRepo != repo && !repoLead.Valid {
		return "No lead is set; delivered as addressed.", nil
	}
	return "", nil
}

// resolveLeadSession picks the session of a lead that gets a message: a live
// session first, then the pinned session, then the newest non-archived,
// non-worker session in the Repo (for a repo address), then the newest such
// session anywhere. The chosen session waits in the queue and the wake on
// message resumes it. It returns sql.ErrNoRows when the lead has no session at
// all. A project merger is never chosen, live or waiting.
func resolveLeadSession(db leadDB, agent, repo, preferred string, now time.Time) (string, error) {
	var id string
	err := liveLeadSession(db, agent, repo, preferred, false, now).Scan(&id)
	if err == nil {
		return id, nil
	}
	if err != sql.ErrNoRows {
		return "", err
	}
	// The pinned lead session comes next, even when it is offline.
	if preferred != "" {
		err = db.QueryRow(`SELECT s.id FROM sessions s WHERE s.id=? AND s.archived_at=0 AND `+notMergerSQL("s"), preferred).Scan(&id)
		if err == nil {
			return id, nil
		}
		if err != sql.ErrNoRows {
			return "", err
		}
	}
	// A message to a Repo waits for the lead in that Repo before its sessions
	// anywhere else.
	if repo != "" {
		err = db.QueryRow(`SELECT s.id FROM sessions s WHERE s.agent_id=? AND s.repo_id=? AND s.worker='' AND s.archived_at=0
			AND s.harness NOT IN ('owner','cto') AND `+notMergerSQL("s")+` ORDER BY s.last_seen DESC,s.id LIMIT 1`, agent, repo).Scan(&id)
		if err == nil {
			return id, nil
		}
		if err != sql.ErrNoRows {
			return "", err
		}
	}
	err = db.QueryRow(`SELECT s.id FROM sessions s WHERE s.agent_id=? AND s.worker='' AND s.archived_at=0
		AND s.harness NOT IN ('owner','cto') AND `+notMergerSQL("s")+` ORDER BY s.last_seen DESC,s.id LIMIT 1`, agent).Scan(&id)
	if err == sql.ErrNoRows {
		return "", sql.ErrNoRows
	}
	return id, err
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
	var rows *sql.Rows
	var err error
	if kind == "team" {
		rows, err = teamLeadRows(s.db, name)
	} else {
		rows, err = repoLeadRows(s.db, `(id=? OR display_name=? COLLATE NOCASE) AND NOT EXISTS(SELECT 1 FROM archived_repos WHERE repo_id=repos.id)`, name, name)
	}
	if err != nil {
		return "", err
	}
	var id string
	var lead, preferred sql.NullString
	count := 0
	for rows.Next() {
		var repoName string
		if kind == "team" {
			err = rows.Scan(&id, &lead, &preferred)
		} else {
			err = rows.Scan(&id, &repoName, &lead, &preferred)
		}
		if err != nil {
			rows.Close()
			return "", err
		}
		count++
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return "", err
	}
	if count == 0 {
		return "", reject("unknown %s %q", kind, name)
	}
	if count > 1 {
		return "", reject("ambiguous %s %q; use its ID", kind, name)
	}
	if !lead.Valid {
		return "", reject("%s %s has no lead", kind, name)
	}
	repo := ""
	if kind == "repo" {
		repo = id
	}
	session, err := resolveLeadSession(s.db, lead.String, repo, preferred.String, s.now())
	if err == sql.ErrNoRows {
		return "", reject("%s %s lead has no session", kind, name)
	}
	return session, err
}

// validateLeadAddress checks the explicit address again in the send transaction.
// A sender cannot use a forged address to avoid ordinary lead routing.
func validateLeadAddress(tx *sql.Tx, address, recipient string, now time.Time) error {
	kind, name, ok := strings.Cut(address, ":")
	name = strings.TrimSpace(name)
	if !ok || name == "" || (kind != "repo" && kind != "team") {
		return reject("invalid lead address")
	}
	var rows *sql.Rows
	var err error
	if kind == "team" {
		rows, err = teamLeadRows(tx, name)
	} else {
		rows, err = repoLeadRows(tx, `(id=? OR display_name=? COLLATE NOCASE) AND NOT EXISTS(SELECT 1 FROM archived_repos WHERE repo_id=repos.id)`, name, name)
	}
	if err != nil {
		return err
	}
	var lead, preferred sql.NullString
	var leadID string
	count := 0
	for rows.Next() {
		var repoName string
		if kind == "team" {
			err = rows.Scan(&leadID, &lead, &preferred)
		} else {
			err = rows.Scan(&leadID, &repoName, &lead, &preferred)
		}
		if err != nil {
			rows.Close()
			return err
		}
		count++
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	var match bool
	if count == 1 && lead.Valid {
		repo := ""
		if kind == "repo" {
			repo = leadID
		}
		var chosen string
		chosen, err = resolveLeadSession(tx, lead.String, repo, preferred.String, now)
		if err != nil && err != sql.ErrNoRows {
			return err
		}
		match = err == nil && chosen == recipient
	}
	if !match {
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
