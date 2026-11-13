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
// A lead named by its session also stores that session as the Agent's lead
// session (decision 0160).
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
	if q.Ref.Kind != "repo" && q.Ref.Kind != "team" && q.Ref.Kind != "agent" {
		return reject("lead target must be a Repo, Team or Agent")
	}
	if q.Ref.Kind == "agent" && q.Lead != nil && (q.Lead.Kind != "agent" || q.Lead.ID != q.Ref.ID) {
		return reject("lead level is granted to the Agent itself: name it in lead")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var old sql.NullString
	var lead, session any
	if q.Lead != nil {
		switch q.Lead.Kind {
		case "session":
			var agent, repo string
			err = tx.QueryRow(`SELECT COALESCE(agent_id,''),COALESCE(repo_id,'') FROM sessions WHERE id=?`, q.Lead.ID).Scan(&agent, &repo)
			if err == sql.ErrNoRows || agent == "" {
				return reject("session has no Agent")
			}
			if err != nil {
				return err
			}
			if q.Ref.Kind == "repo" && repo != q.Ref.ID {
				return reject("session is not in this Repo")
			}
			lead, session = agent, q.Lead.ID
		case "agent":
			lead = q.Lead.ID
		default:
			return reject("lead must be an Agent or session")
		}
		if err = liveTeamMember(tx, protocol.GraphRef{Kind: "agent", ID: lead.(string)}); err != nil {
			return err
		}
		if err = refuseBotLead(tx, lead.(string)); err != nil {
			return err
		}
	}
	now := s.now().UnixNano()
	historyAttr := "lead"
	switch q.Ref.Kind {
	case "repo":
		var explicit bool
		if err = tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM repo_leads WHERE repo_id=?)`, q.Ref.ID).Scan(&explicit); err != nil {
			return err
		}
		if !explicit {
			historyAttr = "lead/default"
		}
		err = tx.QueryRow(`SELECT CASE WHEN EXISTS(SELECT 1 FROM repo_leads WHERE repo_id=repos.id) THEN (SELECT agent_id FROM repo_leads WHERE repo_id=repos.id) ELSE owner_agent_id END FROM repos WHERE id=? AND NOT EXISTS(SELECT 1 FROM archived_repos WHERE repo_id=repos.id)`, q.Ref.ID).Scan(&old)
		if err == sql.ErrNoRows {
			return reject("unknown or archived Repo")
		}
		if err != nil {
			return err
		}
		if q.Lead != nil {
			var belongs bool
			err = tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM repos WHERE id=? AND owner_agent_id=? UNION ALL SELECT 1 FROM sessions WHERE repo_id=? AND agent_id=?)`, q.Ref.ID, lead, q.Ref.ID, lead).Scan(&belongs)
			if err != nil {
				return err
			}
			if !belongs {
				return reject("lead Agent has no session in this Repo")
			}
		}
		_, err = tx.Exec(`INSERT INTO repo_leads(repo_id,agent_id,lead_session) VALUES(?,?,?) ON CONFLICT(repo_id) DO UPDATE SET agent_id=excluded.agent_id,lead_session=excluded.lead_session`, q.Ref.ID, lead, session)
	case "team":
		err = tx.QueryRow(`SELECT owner_agent_id FROM teams WHERE id=? AND active=1`, q.Ref.ID).Scan(&old)
		if err == sql.ErrNoRows {
			return reject("unknown or archived Team")
		}
		if err != nil {
			return err
		}
		_, err = tx.Exec(`UPDATE teams SET owner_agent_id=?,lead_session=?,updated_at=? WHERE id=?`, lead, session, now, q.Ref.ID)
	case "agent":
		if err = liveTeamMember(tx, q.Ref); err != nil {
			return err
		}
		if err = refuseBotLead(tx, q.Ref.ID); err != nil {
			return err
		}
		var on int
		err = tx.QueryRow(`SELECT lead_level FROM agents WHERE id=? AND tombstone=0`, q.Ref.ID).Scan(&on)
		if err == sql.ErrNoRows {
			return reject("unknown or inactive Agent")
		}
		if err != nil {
			return err
		}
		historyAttr = "lead_level"
		if on != 0 {
			old.String = "1"
		}
		level := 0
		if q.Lead != nil {
			level = 1
		}
		_, err = tx.Exec(`UPDATE agents SET lead_level=?, updated_at=? WHERE id=?`, level, now, q.Ref.ID)
	}
	if err != nil {
		return err
	}
	if session != nil {
		if err = setAgentLeadSessionTx(tx, lead.(string), session.(string)); err != nil {
			return err
		}
	}
	// Every later write keeps the stored tree in step with the old tables,
	// in the same transaction (decision 0160).
	newLead := ""
	if q.Lead != nil {
		newLead = lead.(string)
	}
	switch q.Ref.Kind {
	case "repo":
		if old.String != "" {
			if err = placeAgentTx(tx, old.String); err != nil {
				return err
			}
		}
		if err = placeRepoAgentsTx(tx, q.Ref.ID); err != nil {
			return err
		}
	case "team":
		for _, agent := range []string{old.String, newLead} {
			if agent != "" {
				if err = placeAgentTx(tx, agent); err != nil {
					return err
				}
			}
		}
		if err = placeTeamMembersTx(tx, q.Ref.ID); err != nil {
			return err
		}
	}
	next := ""
	if q.Lead != nil {
		next = q.Lead.ID
		if q.Ref.Kind == "agent" {
			next = "1"
		}
	}
	if _, err = tx.Exec(`INSERT INTO graph_history(type,node,attr,old_value,new_value,at,actor,target_kind) VALUES('lead',?,?,?,?,?,?,?)`, q.Ref.ID, historyAttr, old.String, next, now, actor, q.Ref.Kind); err != nil {
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
