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
	err = s.db.QueryRow(`SELECT COUNT(*) FROM sessions s WHERE s.repo_id=? AND s.online=1 AND s.worker='' AND s.harness NOT IN ('owner','cto','bot') AND s.id<>COALESCE((SELECT value FROM gateway_state WHERE key='cto_session'),'') AND NOT EXISTS(SELECT 1 FROM repos m WHERE m.merger_session=s.id)`, repo).Scan(&out.AgentCount)
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
	if q.Ref.Kind == "agent" && q.Lead != nil {
		if q.Lead.Kind == "agent" && q.Lead.ID != q.Ref.ID {
			return reject("lead level is granted to the Agent itself: name it in lead")
		}
		if q.Lead.Kind != "agent" && q.Lead.Kind != "session" {
			return reject("lead must be an Agent or session")
		}
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
			if q.Ref.Kind == "agent" && agent != q.Ref.ID {
				return reject("session is not of this Agent")
			}
			// A member session named as the Repo's lead takes the lead Agent.
			if agent, err = s.memberTakesLeadAgentTx(tx, q.Ref, q.Lead.ID, agent); err != nil {
				return err
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
		err = tx.QueryRow(`SELECT `+repoTopAgentExpr+` FROM repos WHERE id=? AND archived=0`, q.Ref.ID).Scan(&old)
		if err == sql.ErrNoRows {
			return reject("unknown or archived Repo")
		}
		if err != nil {
			return err
		}
		if q.Lead != nil {
			var belongs bool
			err = tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM repos WHERE id=? AND lead_agent_id=? UNION ALL SELECT 1 FROM sessions WHERE repo_id=? AND agent_id=?)`, q.Ref.ID, lead, q.Ref.ID, lead).Scan(&belongs)
			if err != nil {
				return err
			}
			if !belongs {
				return reject("lead Agent has no session in this Repo")
			}
		}
		_, err = tx.Exec(`UPDATE repos SET lead_agent_id=? WHERE id=?`, storedLead(lead), q.Ref.ID)
	case "team":
		err = tx.QueryRow(`SELECT owner_agent_id FROM teams WHERE id=? AND active=1`, q.Ref.ID).Scan(&old)
		if err == sql.ErrNoRows {
			return reject("unknown or archived Team")
		}
		if err != nil {
			return err
		}
		_, err = tx.Exec(`UPDATE teams SET owner_agent_id=?,updated_at=? WHERE id=?`, lead, now, q.Ref.ID)
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
	// A named session is the CTO's override and leads as named. A lead named
	// as an Agent keeps I1 like any other write: its live session leads.
	if session != nil {
		if err = s.setAgentLeadSessionTx(tx, lead.(string), session.(string)); err != nil {
			return err
		}
	} else if lead != nil {
		if err = s.keepLeadTx(tx, lead.(string)); err != nil {
			return err
		}
	}
	if q.Ref.Kind == "agent" {
		if err = s.keepLeadTx(tx, q.Ref.ID); err != nil {
			return err
		}
	}
	// Every later write keeps the stored tree in step with the old tables,
	// in the same transaction (decision 0160).
	newLead := ""
	if q.Lead != nil {
		newLead = lead.(string)
	}
	// The Agent that stops leading this Repo or Team loses its lead session
	// once it leads no other one; the pin would name a session from the place
	// it just left.
	if q.Ref.Kind != "agent" && old.String != "" && old.String != newLead {
		var before, after string
		if err = tx.QueryRow(`SELECT COALESCE(lead_session,'') FROM agents WHERE id=?`, old.String).Scan(&before); err != nil && err != sql.ErrNoRows {
			return err
		}
		if err = s.keepLeadTx(tx, old.String); err != nil {
			return err
		}
		// A lead session that lost its place is stood down for the new
		// lead's session, so its late messages reach the new lead.
		if err = tx.QueryRow(`SELECT COALESCE(lead_session,'') FROM agents WHERE id=?`, old.String).Scan(&after); err != nil && err != sql.ErrNoRows {
			return err
		}
		if before != "" && after == "" {
			successor := ""
			if newLead != "" {
				if err = tx.QueryRow(`SELECT COALESCE(lead_session,'') FROM agents WHERE id=?`, newLead).Scan(&successor); err != nil && err != sql.ErrNoRows {
					return err
				}
			}
			if err = standDownTx(tx, before, stoodDownLead, successor); err != nil {
				return err
			}
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
	if validate {
		return nil
	}
	return tx.Commit()
}

// memberTakesLeadAgentTx moves a session of a Repo's member Agent, named as
// the Repo's lead, to the Repo's lead Agent, and returns the Agent the session
// holds after it. The project keeps one lead Agent (I1, I2): the member Agent
// never becomes a second lead. A session of any other Agent keeps it, and
// that Agent becomes the lead as before.
func (s *Store) memberTakesLeadAgentTx(tx *sql.Tx, ref protocol.GraphRef, session, agent string) (string, error) {
	if ref.Kind != "repo" || (agent != ref.ID+"-agent" && agent != ref.ID) {
		return agent, nil
	}
	var place sql.NullString
	err := tx.QueryRow(`SELECT `+repoTopAgentExpr+` FROM repos WHERE id=? AND archived=0`, ref.ID).Scan(&place)
	if err == sql.ErrNoRows || place.String == "" || place.String == agent {
		return agent, nil
	}
	if err != nil {
		return agent, err
	}
	leads, err := agentLeadsAnythingQ(tx, agent)
	if err != nil || leads {
		return agent, err
	}
	if _, err = tx.Exec(`UPDATE sessions SET agent_id=? WHERE id=?`, place.String, session); err != nil {
		return agent, err
	}
	return place.String, nil
}
