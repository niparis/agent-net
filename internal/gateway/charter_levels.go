package gateway

import (
	"database/sql"
	"encoding/json"

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
	if !actorAllowed(actor, "rule") {
		return reject("only the owner or CTO may change inherited charters")
	}
	value, err := protocol.CleanCharterValue(q.Attr, q.Value)
	if err != nil {
		return reject("%v", err)
	}
	if q.Attr != "purpose" && q.Attr != "refuses" && q.Attr != "rules" && q.Attr != "escalate" {
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
	var c protocol.Charter
	sources := protocol.CharterSources{}
	refused := map[string]bool{}
	apply := func(level, scope, attr, value string) error {
		if value == "" {
			return nil
		}
		if _, ok := protocol.CharterGuideFor(attr); !ok {
			return nil
		}
		src := protocol.CharterSource{Level: level, Scope: scope, Value: value}
		if attr == "refuses" {
			sources[attr] = append(sources[attr], src)
			// Union by complete refusal. Do not split prose or interpret rules.
			if !refused[value] {
				if c.Refuses != "" {
					c.Refuses += "\n"
				}
				c.Refuses += value
				refused[value] = true
			}
			return nil
		}
		b, _ := json.Marshal(map[string]string{attr: value})
		if err := json.Unmarshal(b, &c); err != nil {
			return err
		}
		sources[attr] = []protocol.CharterSource{src}
		return nil
	}
	read := func(query, level, scope string, args ...any) error {
		rows, err := s.db.Query(query, args...)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var attr, value string
			if err = rows.Scan(&attr, &value); err != nil {
				return err
			}
			if err = apply(level, scope, attr, value); err != nil {
				return err
			}
		}
		return rows.Err()
	}
	if err := read(`SELECT attr,value FROM charter_values WHERE level='global' AND scope='' ORDER BY attr`, "global", ""); err != nil {
		return c, err
	}
	if team == "" {
		// Repo membership takes precedence over direct Agent membership in a Repo context.
		kind, id := "agent", agent
		if repo != "" {
			kind, id = "repo", repo
		}
		err := s.db.QueryRow(`SELECT m.team_id FROM team_members m JOIN teams t ON t.id=m.team_id AND t.active=1 WHERE m.member_kind=? AND m.member_id=?`, kind, id).Scan(&team)
		if err != nil && err != sql.ErrNoRows {
			return c, err
		}
		if team == "" && agent != "" && repo != "" {
			err = s.db.QueryRow(`SELECT m.team_id FROM team_members m JOIN teams t ON t.id=m.team_id AND t.active=1 WHERE m.member_kind='agent' AND m.member_id=?`, agent).Scan(&team)
			if err != nil && err != sql.ErrNoRows {
				return c, err
			}
		}
	}
	if team != "" {
		var purpose string
		err := s.db.QueryRow(`SELECT purpose FROM teams WHERE id=? AND active=1`, team).Scan(&purpose)
		if err != nil {
			return c, err
		}
		if err = apply("team", team, "purpose", purpose); err != nil {
			return c, err
		}
		if c.Escalate == "" {
			if err = apply("team", team, "escalate", "ask the team lead"); err != nil {
				return c, err
			}
		}
		if err = read(`SELECT attr,value FROM charter_values WHERE level='team' AND scope=? ORDER BY attr`, "team", team, team); err != nil {
			return c, err
		}
	}
	// A directly assigned Agent Team retains its safety refusals even when
	// the current Repo belongs to a different Team.
	if agent != "" {
		var ownTeam string
		err := s.db.QueryRow(`SELECT m.team_id FROM team_members m JOIN teams t ON t.id=m.team_id AND t.active=1 WHERE m.member_kind='agent' AND m.member_id=?`, agent).Scan(&ownTeam)
		if err != nil && err != sql.ErrNoRows {
			return c, err
		}
		if ownTeam != "" && ownTeam != team {
			if err = read(`SELECT attr,value FROM charter_values WHERE level='team' AND scope=? AND attr='refuses'`, "team", ownTeam, ownTeam); err != nil {
				return c, err
			}
		}
	}
	if repo != "" {
		if err := read(`SELECT attr,value FROM graph_charter WHERE node=? ORDER BY attr`, "repo", repo, repo); err != nil {
			return c, err
		}
	}
	if agent != "" {
		if err := read(`SELECT attr,value FROM agent_charter WHERE agent_id=? ORDER BY attr`, "agent", agent, agent); err != nil {
			return c, err
		}
	}
	if len(sources) > 0 {
		c.Sources = &sources
	}
	return c, nil
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
