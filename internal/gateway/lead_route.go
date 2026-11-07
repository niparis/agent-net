package gateway

import (
	"database/sql"
	"fmt"
	"strings"

	"github.com/niparis/agent-net/internal/protocol"
)

type leadDB interface {
	QueryRow(string, ...any) *sql.Row
	Query(string, ...any) (*sql.Rows, error)
}

const repoLeadExpr = `CASE WHEN EXISTS(SELECT 1 FROM repo_leads WHERE repo_id=repos.id) THEN (SELECT agent_id FROM repo_leads WHERE repo_id=repos.id) ELSE owner_agent_id END`

func repoLeadQuery(where string) string {
	return `SELECT id,display_name,` + repoLeadExpr + ` FROM repos WHERE ` + where
}
func repoLeadRows(db leadDB, where string, args ...any) (*sql.Rows, error) {
	return db.Query(repoLeadQuery(where), args...)
}
func teamLeadRows(db leadDB, name string) (*sql.Rows, error) {
	return db.Query(`SELECT id,owner_agent_id FROM teams WHERE (id=? OR name=? COLLATE NOCASE) AND active=1`, name, name)
}
func liveLeadSession(db leadDB, agent, repo string, withName bool) *sql.Row {
	selectPart, join := "s.id", ""
	if withName {
		selectPart, join = "s.id,a.display_name", " JOIN agents a ON a.id=s.agent_id"
	}
	return db.QueryRow(`SELECT `+selectPart+` FROM sessions s`+join+` WHERE s.agent_id=? AND s.online=1 AND s.worker='' AND s.harness NOT IN ('owner','cto') ORDER BY CASE WHEN s.repo_id=? THEN 0 ELSE 1 END,s.last_seen DESC,s.id LIMIT 1`, agent, repo)
}

// routeLead runs in the message transaction after the original reply has been
// authorized. The normal request gate then checks the actual recipient.
func routeLead(tx *sql.Tx, m *protocol.Message) (string, error) {
	var senderRepo, senderAgent, harness, worker, cto string
	err := tx.QueryRow(`SELECT COALESCE(repo_id,''),COALESCE(agent_id,''),harness,worker,COALESCE((SELECT value FROM owner_settings WHERE key='cto_session'),'') FROM sessions WHERE id=?`, m.From).Scan(&senderRepo, &senderAgent, &harness, &worker, &cto)
	if err != nil {
		return "", err
	}
	if protocol.IsOwner(harness) || m.From == cto || m.FromCTO {
		return "", nil
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
	var repoLead sql.NullString
	var repoName string
	if repo != "" {
		var repoID string
		err = tx.QueryRow(repoLeadQuery(`id=?`), repo).Scan(&repoID, &repoName, &repoLead)
		if err != nil && err != sql.ErrNoRows {
			return "", err
		}
	}
	try := func(lead sql.NullString, scope, inRepo string) (string, bool, error) {
		if !lead.Valid || lead.String == "" || lead.String == agent {
			return "", false, nil
		}
		var id, name string
		err := liveLeadSession(tx, lead.String, inRepo, true).Scan(&id, &name)
		if err == sql.ErrNoRows {
			return "Lead has no live session; delivered as addressed.", true, nil
		}
		if err != nil {
			return "", false, err
		}
		if id == m.From {
			return "", true, nil
		}
		note := fmt.Sprintf("Redirected to %s (lead of %s); originally sent to %s.", name, scope, targetName)
		m.To = id
		m.Body = note + "\n" + m.Body
		return note, true, nil
	}
	if repo != "" && senderRepo != repo && repoLead.Valid && repoLead.String != agent {
		note, done, e := try(repoLead, repoName, repo)
		if done || e != nil {
			return note, e
		}
	}
	// The target's repo team is the session context; otherwise use its Agent team.
	var teamID, teamName string
	var teamLead sql.NullString
	err = tx.QueryRow(`SELECT t.id,t.name,t.owner_agent_id FROM teams t JOIN team_members tm ON tm.team_id=t.id WHERE t.active=1 AND ((tm.member_kind='repo' AND tm.member_id=?) OR (tm.member_kind='agent' AND tm.member_id=?)) ORDER BY CASE tm.member_kind WHEN 'repo' THEN 0 ELSE 1 END LIMIT 1`, repo, agent).Scan(&teamID, &teamName, &teamLead)
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

// ResolveLeadAddress resolves a Repo or Team name or ID to an online lead.
func (s *Store) ResolveLeadAddress(address string) (string, error) {
	kind, name, ok := strings.Cut(address, ":")
	name = strings.TrimSpace(name)
	if !ok || name == "" || (kind != "repo" && kind != "team") {
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
	var lead sql.NullString
	count := 0
	for rows.Next() {
		var repoName string
		if kind == "team" {
			err = rows.Scan(&id, &lead)
		} else {
			err = rows.Scan(&id, &repoName, &lead)
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
	var session string
	err = liveLeadSession(s.db, lead.String, id, false).Scan(&session)
	if err == sql.ErrNoRows {
		return "", reject("%s %s lead has no live session", kind, name)
	}
	return session, err
}

// validateLeadAddress checks the explicit address again in the send transaction.
// A sender cannot use a forged address to avoid ordinary lead routing.
func validateLeadAddress(tx *sql.Tx, address, recipient string) error {
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
	var lead sql.NullString
	count := 0
	for rows.Next() {
		var id, repoName string
		if kind == "team" {
			err = rows.Scan(&id, &lead)
		} else {
			err = rows.Scan(&id, &repoName, &lead)
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
		err = tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM sessions WHERE id=? AND agent_id=? AND online=1 AND worker='' AND harness NOT IN ('owner','cto'))`, recipient, lead.String).Scan(&match)
		if err != nil {
			return err
		}
	}
	if !match {
		return reject("lead for %s changed or went offline; retry the address", address)
	}
	return nil
}
