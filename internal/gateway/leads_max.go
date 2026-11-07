package gateway

import (
	"database/sql"
	"encoding/json"

	"github.com/niparis/agent-net/internal/protocol"
)

// RepoAgentLimit counts sessions, including sessions discovered outside agent-net.
func (s *Store) RepoAgentLimit(repo string) (protocol.RepoAgentLimit, error) {
	var out protocol.RepoAgentLimit
	var raw string
	err := s.db.QueryRow(`SELECT value FROM setting_values WHERE key='max_agents' AND level='repo' AND scope=? AND item=''`, repo).Scan(&raw)
	if err != nil && err != sql.ErrNoRows {
		return out, err
	}
	if err == nil {
		if err = json.Unmarshal([]byte(raw), &out.MaxAgents); err != nil {
			return out, err
		}
	}
	err = s.db.QueryRow(`SELECT COUNT(*) FROM sessions s WHERE s.repo_id=? AND s.online=1 AND s.worker='' AND s.harness NOT IN ('owner','cto','bot') AND s.id<>COALESCE((SELECT value FROM owner_settings WHERE key='cto_session'),'') AND NOT EXISTS(SELECT 1 FROM project_mergers m WHERE m.session_id=s.id)`, repo).Scan(&out.AgentCount)
	out.OverMax = out.MaxAgents > 0 && out.AgentCount > out.MaxAgents
	return out, err
}

// SetLead changes a typed Repo or Team lead under owner authority. A nil lead
// explicitly clears it; an unconfigured Repo defaults to its existing owner.
func (s *Store) SetLead(q protocol.LeadSetRequest, actor string, validate bool) error {
	if err := s.requireGraphRefs(); err != nil {
		return err
	}
	if !actorAllowed(actor, "rule") {
		return reject("only the owner may set a lead")
	}
	if err := q.Ref.Validate(); err != nil {
		return err
	}
	if q.Ref.Kind != "repo" && q.Ref.Kind != "team" {
		return reject("lead target must be a Repo or Team")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if q.Lead != nil {
		if q.Lead.Kind != "agent" {
			return reject("lead must be an Agent")
		}
		if err = liveTeamMember(tx, *q.Lead); err != nil {
			return err
		}
	}
	var old sql.NullString
	var lead any
	if q.Lead != nil {
		lead = q.Lead.ID
	}
	now := s.now().UnixNano()
	switch q.Ref.Kind {
	case "repo":
		err = tx.QueryRow(`SELECT CASE WHEN EXISTS(SELECT 1 FROM repo_leads WHERE repo_id=repos.id) THEN (SELECT agent_id FROM repo_leads WHERE repo_id=repos.id) ELSE owner_agent_id END FROM repos WHERE id=? AND NOT EXISTS(SELECT 1 FROM archived_repos WHERE repo_id=repos.id)`, q.Ref.ID).Scan(&old)
		if err == sql.ErrNoRows {
			return reject("unknown or archived Repo")
		}
		if err != nil {
			return err
		}
		if q.Lead != nil {
			var belongs bool
			err = tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM repos WHERE id=? AND owner_agent_id=? UNION ALL SELECT 1 FROM sessions WHERE repo_id=? AND agent_id=?)`, q.Ref.ID, q.Lead.ID, q.Ref.ID, q.Lead.ID).Scan(&belongs)
			if err != nil {
				return err
			}
			if !belongs {
				return reject("lead Agent has no session in this Repo")
			}
		}
		_, err = tx.Exec(`INSERT INTO repo_leads(repo_id,agent_id) VALUES(?,?) ON CONFLICT(repo_id) DO UPDATE SET agent_id=excluded.agent_id`, q.Ref.ID, lead)
	case "team":
		err = tx.QueryRow(`SELECT owner_agent_id FROM teams WHERE id=? AND active=1`, q.Ref.ID).Scan(&old)
		if err == sql.ErrNoRows {
			return reject("unknown or archived Team")
		}
		if err != nil {
			return err
		}
		_, err = tx.Exec(`UPDATE teams SET owner_agent_id=?,updated_at=? WHERE id=?`, lead, now, q.Ref.ID)
	}
	if err != nil {
		return err
	}
	next := ""
	if q.Lead != nil {
		next = q.Lead.ID
	}
	if _, err = tx.Exec(`INSERT INTO graph_history(type,node,attr,old_value,new_value,at,actor,target_kind) VALUES('lead',?,'lead',?,?,?,?,?)`, q.Ref.ID, old.String, next, now, actor, q.Ref.Kind); err != nil {
		return err
	}
	if q.Ref.Kind == "team" {
		if _, err = tx.Exec(`INSERT INTO team_history(action,team_id,old_team_id,member_kind,member_id,old_value,new_value,at,actor) VALUES('lead',?,'','','',?,?,?,?)`, q.Ref.ID, old.String, next, now, actor); err != nil {
			return err
		}
	}
	if validate {
		return nil
	}
	return tx.Commit()
}
