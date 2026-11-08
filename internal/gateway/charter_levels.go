package gateway

import (
	"database/sql"

	"github.com/niparis/agent-net/internal/protocol"
)

// Global and Team charters share a table. Repo and Agent values retain their
// existing tables, keys and history. Empty values mean inherit.
func (s *Store) SetLevelCharter(q protocol.CharterSetRequest, actor string, validate bool) error {
	level, scope := q.Level, q.Node
	if q.Level != "" && !protocol.CharterAttributeAtLevel(q.Level, q.Attr) {
		return reject("attribute %s is not allowed at %s level", q.Attr, q.Level)
	}
	if q.Ref != nil {
		if err := q.Ref.Validate(); err != nil {
			return err
		}
		if level != "" && level != q.Ref.Kind {
			return reject("level does not match ref")
		}
		level, scope = q.Ref.Kind, q.Ref.ID
	}
	if level == "" {
		level = "repo"
	}
	if level == "repo" {
		if q.Ref != nil {
			return s.SetRefCharter(*q.Ref, q.Attr, q.Value, actor, validate)
		}
		if !validate {
			return s.SetCharter(scope, q.Attr, q.Value, actor)
		}
		tx, err := s.db.Begin()
		if err != nil {
			return err
		}
		defer tx.Rollback()
		return setCharterTx(tx, scope, q.Attr, q.Value, actor, s.now().UnixNano())
	}
	if level == "agent" {
		return s.SetRefCharter(protocol.GraphRef{Kind: "agent", ID: scope}, q.Attr, q.Value, actor, validate)
	}
	if level != "global" && level != "team" {
		return reject("invalid charter level")
	}
	// Work mode is advice: the CTO sets it without a proposal.
	kind := "rule"
	if q.Attr == protocol.AttrWorkMode {
		kind = "advice"
	}
	if !actorAllowed(actor, kind) {
		return reject("only the owner or CTO may change inherited charters")
	}
	value, err := protocol.CleanCharterValue(q.Attr, q.Value)
	if err != nil {
		return reject("%v", err)
	}
	if q.Attr != "purpose" && q.Attr != "refuses" && q.Attr != "rules" && q.Attr != "escalate" && q.Attr != protocol.AttrWorkMode {
		return reject("attribute is not allowed at %s level", level)
	}
	if level == "global" {
		scope = ""
	}
	// Purpose stays in the existing Team column, so old tools and undo keep working.
	if level == "team" && q.Attr == "purpose" {
		tw := protocol.TeamWrite{Action: "purpose", ID: scope, Purpose: value}
		if validate {
			return s.ValidateTeam(tw, actor)
		}
		return s.WriteTeam(tw, actor)
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if level == "team" {
		var live bool
		if err = tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM teams WHERE id=? AND active=1)`, scope).Scan(&live); err != nil {
			return err
		}
		if !live {
			return reject("unknown active team")
		}
	}
	var old string
	err = tx.QueryRow(`SELECT value FROM charter_values WHERE level=? AND scope=? AND attr=?`, level, scope, q.Attr).Scan(&old)
	if err != nil && err != sql.ErrNoRows {
		return err
	}
	if old == value {
		return nil
	}
	now := s.now().UnixNano()
	if value == "" {
		_, err = tx.Exec(`DELETE FROM charter_values WHERE level=? AND scope=? AND attr=?`, level, scope, q.Attr)
	} else {
		_, err = tx.Exec(`INSERT INTO charter_values VALUES(?,?,?,?,?,?) ON CONFLICT(level,scope,attr) DO UPDATE SET value=excluded.value,updated_at=excluded.updated_at,updated_by=excluded.updated_by`, level, scope, q.Attr, value, now, actor)
	}
	if err != nil {
		return err
	}
	if err = graphRecord(tx, "level_charter", scope, level+":"+q.Attr, old, value, actor, now); err != nil {
		return err
	}
	if validate {
		return nil
	}
	return tx.Commit()
}

// EffectiveCharter resolves global -> Team -> Repo -> Agent. An Agent may
// work in multiple Repos; the caller supplies its current Repo explicitly.
// With no Repo context, only the Agent's direct Team applies.
func (s *Store) EffectiveCharter(repo, agent, team string) (protocol.Charter, error) {
	return s.newCharterResolver().resolve(repo, agent, team)
}

func (s *Store) applyEffectiveCharters(g *protocol.Graph) error {
	for i := range g.Nodes {
		n := &g.Nodes[i]
		if n.Active != nil && !*n.Active {
			continue
		}
		repo, agent, team := "", "", ""
		if n.Ref != nil {
			switch n.Ref.Kind {
			case "repo":
				repo = n.Ref.ID
			case "agent":
				agent = n.Ref.ID
				// A standalone Agent uses its sole owned Repo when that context is unique.
				if err := s.db.QueryRow(`SELECT CASE WHEN count(*)=1 THEN min(id) ELSE '' END FROM repos WHERE owner_agent_id=? AND NOT EXISTS(SELECT 1 FROM archived_repos WHERE repo_id=repos.id)`, agent).Scan(&repo); err != nil {
					return err
				}
			case "team":
				team = n.Ref.ID
			default:
				continue
			}
		} else if n.Kind == "architect" && s.graphRefsOn.Load() {
			agent = n.Name
		} else {
			repo = n.Name
		}
		c, err := s.EffectiveCharter(repo, agent, team)
		if err != nil {
			return err
		}
		n.Charter = c
	}
	return nil
}
