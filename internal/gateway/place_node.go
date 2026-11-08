package gateway

import (
	"database/sql"
	"strings"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// The Graph carries each record's place (decision 0089) so the owner's card
// reads the same Team, lead and held back data as the turn text. It is built
// on a graph read, never on a per-second path.

// heldBackParts splits free text into the items the owner named.
func heldBackParts(text string) []string {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	var out []string
	for _, part := range strings.FieldsFunc(text, func(r rune) bool { return r == ';' || r == '\n' }) {
		if part = strings.Join(strings.Fields(part), " "); part != "" {
			out = append(out, part)
		}
	}
	return out
}

func (s *Store) nodePlaceFor(ref protocol.GraphRef, repo, agent string, now time.Time) (protocol.NodePlace, bool, error) {
	p := protocol.NodePlace{}
	switch ref.Kind {
	case "team":
		var name, mode, held, owner string
		var pinned sql.NullString
		err := s.db.QueryRow(`SELECT name,authority_mode,held_back,COALESCE(owner_agent_id,''),lead_session FROM teams WHERE id=? AND active=1`, ref.ID).
			Scan(&name, &mode, &held, &owner, &pinned)
		if err == sql.ErrNoRows {
			return p, false, nil
		}
		if err != nil {
			return p, false, err
		}
		p.Team, p.Mode = name, protocol.NormalAuthorityMode(mode)
		if owner != "" {
			if p.TeamLead, err = leadNameQ(s.db, owner, pinned.String, now); err != nil {
				return p, false, err
			}
		}
		for _, item := range heldBackParts(held) {
			p.HeldBack = append(p.HeldBack, protocol.PlaceHeldBack{Text: item, From: "team"})
		}
		return p, true, nil
	case "repo":
		repo = ref.ID
	case "agent":
		agent = ref.ID
	default:
		return p, false, nil
	}
	if repo == "" && agent != "" {
		var err error
		if repo, err = s.repoForAgent(agent); err != nil {
			return p, false, err
		}
	}
	if repo != "" {
		var err error
		if p.Repo, err = s.repoDisplayName(repo); err != nil {
			return p, false, err
		}
		if p.RepoLead, err = s.repoLeadName(repo, now); err != nil {
			return p, false, err
		}
	}
	teamID, teamName, mode, held, err := s.teamForPlace(repo, agent)
	if err != nil {
		return p, false, err
	}
	if teamID != "" {
		p.Team, p.Mode = teamName, protocol.NormalAuthorityMode(mode)
		var owner string
		var pinned sql.NullString
		if err = s.db.QueryRow(`SELECT COALESCE(owner_agent_id,''),lead_session FROM teams WHERE id=?`, teamID).Scan(&owner, &pinned); err != nil {
			return p, false, err
		}
		if owner != "" {
			if p.TeamLead, err = leadNameQ(s.db, owner, pinned.String, now); err != nil {
				return p, false, err
			}
		}
		for _, item := range heldBackParts(held) {
			p.HeldBack = append(p.HeldBack, protocol.PlaceHeldBack{Text: item, From: "team"})
		}
	}
	if repo != "" {
		held, err := repoHeldBack(s.db, repo)
		if err != nil {
			return p, false, err
		}
		for _, item := range heldBackParts(held) {
			p.HeldBack = append(p.HeldBack, protocol.PlaceHeldBack{Text: item, From: "repo"})
		}
	}
	switch {
	case p.TeamLead != "":
		p.ReportsTo = "the " + p.Team + " lead"
	case p.RepoLead != "" && p.RepoLead != agent && p.RepoLead != ref.ID:
		p.ReportsTo = "the repo lead " + p.RepoLead
	default:
		p.ReportsTo = "the owner"
	}
	return p, true, nil
}

// repoForAgent is the repo that gives the agent its context: its only owned
// repo, else the repo of its newest session.
func (s *Store) repoForAgent(agent string) (string, error) {
	var repo string
	err := s.db.QueryRow(`SELECT CASE WHEN count(*)=1 THEN min(id) ELSE '' END FROM repos WHERE owner_agent_id=? AND NOT EXISTS(SELECT 1 FROM archived_repos WHERE repo_id=repos.id)`, agent).Scan(&repo)
	if err != nil || repo != "" {
		return repo, err
	}
	err = s.db.QueryRow(`SELECT COALESCE(repo_id,'') FROM sessions WHERE agent_id=? AND COALESCE(repo_id,'')<>'' AND archived_at=0 ORDER BY last_seen DESC LIMIT 1`, agent).Scan(&repo)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return repo, err
}

func (s *Store) repoDisplayName(repo string) (string, error) {
	var name string
	err := s.db.QueryRow(`SELECT display_name FROM repos WHERE id=? AND NOT EXISTS(SELECT 1 FROM archived_repos WHERE repo_id=repos.id)`, repo).Scan(&name)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return name, err
}

func (s *Store) repoLeadName(repo string, now time.Time) (string, error) {
	var lead, session sql.NullString
	err := s.db.QueryRow(`SELECT (SELECT lead_session FROM repo_leads WHERE repo_id=repos.id), CASE WHEN EXISTS(SELECT 1 FROM repo_leads WHERE repo_id=repos.id) THEN (SELECT agent_id FROM repo_leads WHERE repo_id=repos.id) ELSE owner_agent_id END FROM repos WHERE id=? AND NOT EXISTS(SELECT 1 FROM archived_repos WHERE repo_id=repos.id)`, repo).Scan(&session, &lead)
	if err == sql.ErrNoRows {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	if session.Valid && session.String != "" {
		return sessionLabel(s.db, session.String), nil
	}
	if lead.Valid && lead.String != "" {
		return leadNameQ(s.db, lead.String, "", now)
	}
	return "", nil
}

// teamForPlace is the active team that places a repo or agent. Repo membership
// wins over direct agent membership, as in the turn text, and a team's owner
// leads it even when the owner has no membership row.
func (s *Store) teamForPlace(repo, agent string) (id, name, mode, held string, err error) {
	err = s.db.QueryRow(`SELECT t.id,t.name,t.authority_mode,t.held_back FROM teams t
		LEFT JOIN team_members tm ON tm.team_id=t.id
		WHERE t.active=1 AND (
			(tm.member_kind='repo' AND tm.member_id=? AND tm.member_id<>'')
			OR (tm.member_kind='agent' AND tm.member_id=? AND tm.member_id<>'')
			OR (t.owner_agent_id=? AND ?<>''))
		ORDER BY CASE WHEN tm.member_kind='repo' THEN 0 WHEN tm.member_kind='agent' THEN 1 ELSE 2 END,t.id LIMIT 1`,
		repo, agent, agent, agent).Scan(&id, &name, &mode, &held)
	if err == sql.ErrNoRows {
		return "", "", "", "", nil
	}
	return
}
