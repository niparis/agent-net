package gateway

import (
	"database/sql"
	"encoding/json"
	"strings"

	"github.com/niparis/agent-net/internal/protocol"
)

// charterValue is one stored level contribution, in query order.
type charterValue struct {
	attr  string
	value string
}

// charterResolver applies the global -> Team -> Repo -> Agent precedence from
// values held in memory. A single read loads only what one row needs; a list
// preloads the same maps once for every row. Both paths call resolve, so the
// precedence and the refuses union live in one place.
type charterResolver struct {
	store *Store

	global       []charterValue
	globalLoaded bool

	memberships       map[string]string // member kind and id -> active Team
	membershipsLoaded bool

	purposes       map[string]string
	purposesLoaded bool

	teamValues      map[string][]charterValue
	teamValuesReady bool // whole level read: a missing key is empty

	repoValues      map[string][]charterValue
	repoValuesReady bool

	agentValues      map[string][]charterValue
	agentValuesReady bool
}

func (s *Store) newCharterResolver() *charterResolver {
	return &charterResolver{
		store:       s,
		memberships: map[string]string{},
		purposes:    map[string]string{},
		teamValues:  map[string][]charterValue{},
		repoValues:  map[string][]charterValue{},
		agentValues: map[string][]charterValue{},
	}
}

func memberKey(kind, id string) string { return kind + "\x00" + id }

func (r *charterResolver) globalCharter() ([]charterValue, error) {
	if r.globalLoaded {
		return r.global, nil
	}
	rows, err := r.store.db.Query(`SELECT attr,value FROM charter_values WHERE level='global' AND scope='' ORDER BY attr`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var v charterValue
		if err := rows.Scan(&v.attr, &v.value); err != nil {
			return nil, err
		}
		r.global = append(r.global, v)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	r.globalLoaded = true
	return r.global, nil
}

func (r *charterResolver) loadMemberships() error {
	if r.membershipsLoaded {
		return nil
	}
	rows, err := r.store.db.Query(`SELECT m.member_kind,m.member_id,m.team_id FROM team_members m JOIN teams t ON t.id=m.team_id AND t.active=1`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var kind, id, team string
		if err := rows.Scan(&kind, &id, &team); err != nil {
			return err
		}
		r.memberships[memberKey(kind, id)] = team
	}
	if err := rows.Err(); err != nil {
		return err
	}
	r.membershipsLoaded = true
	return nil
}

func (r *charterResolver) memberTeam(kind, id string) (string, error) {
	if err := r.loadMemberships(); err != nil {
		return "", err
	}
	return r.memberships[memberKey(kind, id)], nil
}

func (r *charterResolver) teamPurpose(team string) (string, error) {
	if r.purposesLoaded {
		purpose, ok := r.purposes[team]
		if !ok {
			return "", sql.ErrNoRows
		}
		return purpose, nil
	}
	var purpose string
	if err := r.store.db.QueryRow(`SELECT purpose FROM teams WHERE id=? AND active=1`, team).Scan(&purpose); err != nil {
		return "", err
	}
	r.purposes[team] = purpose
	return purpose, nil
}

func (r *charterResolver) teamCharter(team string) ([]charterValue, error) {
	if values, ok := r.teamValues[team]; ok {
		return values, nil
	}
	if r.teamValuesReady {
		return nil, nil
	}
	values, err := r.scanValues(`SELECT attr,value FROM charter_values WHERE level='team' AND scope=? ORDER BY attr`, team)
	if err != nil {
		return nil, err
	}
	r.teamValues[team] = values
	return values, nil
}

func (r *charterResolver) repoCharter(repo string) ([]charterValue, error) {
	if values, ok := r.repoValues[repo]; ok {
		return values, nil
	}
	if r.repoValuesReady {
		return nil, nil
	}
	values, err := r.scanValues(`SELECT attr,value FROM graph_charter WHERE node=? ORDER BY attr`, repo)
	if err != nil {
		return nil, err
	}
	r.repoValues[repo] = values
	return values, nil
}

func (r *charterResolver) agentCharter(agent string) ([]charterValue, error) {
	if values, ok := r.agentValues[agent]; ok {
		return values, nil
	}
	if r.agentValuesReady {
		return nil, nil
	}
	values, err := r.scanValues(`SELECT attr,value FROM agent_charter WHERE agent_id=? ORDER BY attr`, agent)
	if err != nil {
		return nil, err
	}
	r.agentValues[agent] = values
	return values, nil
}

func (r *charterResolver) scanValues(query string, args ...any) ([]charterValue, error) {
	rows, err := r.store.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var values []charterValue
	for rows.Next() {
		var v charterValue
		if err := rows.Scan(&v.attr, &v.value); err != nil {
			return nil, err
		}
		values = append(values, v)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return values, nil
}

// preloadTeams reads every active Team, its purpose, its memberships and its
// level values once, for a batch of rows.
func (r *charterResolver) preloadTeams() error {
	rows, err := r.store.db.Query(`SELECT id,purpose FROM teams WHERE active=1`)
	if err != nil {
		return err
	}
	for rows.Next() {
		var id, purpose string
		if err := rows.Scan(&id, &purpose); err != nil {
			rows.Close()
			return err
		}
		r.purposes[id] = purpose
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	r.purposesLoaded = true
	if err := r.loadMemberships(); err != nil {
		return err
	}
	rows, err = r.store.db.Query(`SELECT scope,attr,value FROM charter_values WHERE level='team' ORDER BY scope,attr`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var scope string
		var v charterValue
		if err := rows.Scan(&scope, &v.attr, &v.value); err != nil {
			return err
		}
		r.teamValues[scope] = append(r.teamValues[scope], v)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	r.teamValuesReady = true
	return nil
}

// preloadRepos reads the given Repos' charters in one query.
func (r *charterResolver) preloadRepos(repos []string) error {
	repos = uniqueNonEmpty(repos)
	rows, err := queryIn(r.store.db, `SELECT node,attr,value FROM graph_charter WHERE node IN (%s) ORDER BY node,attr`, repos)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var node string
		var v charterValue
		if err := rows.Scan(&node, &v.attr, &v.value); err != nil {
			return err
		}
		r.repoValues[node] = append(r.repoValues[node], v)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	r.repoValuesReady = true
	return nil
}

// preloadAgents reads the given Agents' charters in one query.
func (r *charterResolver) preloadAgents(agents []string) error {
	agents = uniqueNonEmpty(agents)
	rows, err := queryIn(r.store.db, `SELECT agent_id,attr,value FROM agent_charter WHERE agent_id IN (%s) ORDER BY agent_id,attr`, agents)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		var v charterValue
		if err := rows.Scan(&id, &v.attr, &v.value); err != nil {
			return err
		}
		r.agentValues[id] = append(r.agentValues[id], v)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	r.agentValuesReady = true
	return nil
}

func uniqueNonEmpty(values []string) []string {
	seen := make(map[string]bool, len(values))
	out := values[:0]
	for _, v := range values {
		if v == "" || seen[v] {
			continue
		}
		seen[v] = true
		out = append(out, v)
	}
	return out
}

func queryIn(db *sql.DB, format string, values []string) (*sql.Rows, error) {
	if len(values) == 0 {
		// No row names the level: read nothing without a round trip.
		return db.Query(`SELECT NULL,NULL,NULL WHERE 0`)
	}
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(values)), ",")
	args := make([]any, len(values))
	for i, v := range values {
		args[i] = v
	}
	return db.Query(strings.Replace(format, "%s", placeholders, 1), args...)
}

// resolve applies the stored levels to one Repo, Agent and Team context.
func (r *charterResolver) resolve(repo, agent, team string) (protocol.Charter, error) {
	var c protocol.Charter
	sources := protocol.CharterSources{}
	// refuses and rules are united across levels by item, so a rule set at the
	// team still reaches a member whose repo or agent has rules of its own.
	// Items are split the same way every reader splits them, each shows once,
	// and the lower level comes after the higher one (team before repo).
	united := map[string]map[string]bool{"refuses": {}, "rules": {}}
	apply := func(level, scope, attr, value string) error {
		if value == "" {
			return nil
		}
		if _, ok := protocol.CharterGuideFor(attr); !ok {
			return nil
		}
		src := protocol.CharterSource{Level: level, Scope: scope, Value: value}
		if seen, ok := united[attr]; ok {
			sources[attr] = append(sources[attr], src)
			field := &c.Refuses
			if attr == "rules" {
				field = &c.Rules
			}
			for _, item := range protocol.SplitCharterList(value) {
				if seen[item] {
					continue
				}
				if *field != "" {
					*field += "; "
				}
				*field += item
				seen[item] = true
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
	applyAll := func(level, scope string, values []charterValue) error {
		for _, v := range values {
			if err := apply(level, scope, v.attr, v.value); err != nil {
				return err
			}
		}
		return nil
	}
	global, err := r.globalCharter()
	if err != nil {
		return c, err
	}
	if err = applyAll("global", "", global); err != nil {
		return c, err
	}
	if team == "" {
		// Repo membership takes precedence over direct Agent membership in a Repo context.
		kind, id := "agent", agent
		if repo != "" {
			kind, id = "repo", repo
		}
		team, err = r.memberTeam(kind, id)
		if err != nil {
			return c, err
		}
		if team == "" && agent != "" && repo != "" {
			team, err = r.memberTeam("agent", agent)
			if err != nil {
				return c, err
			}
		}
	}
	if team != "" {
		purpose, err := r.teamPurpose(team)
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
		values, err := r.teamCharter(team)
		if err != nil {
			return c, err
		}
		if err = applyAll("team", team, values); err != nil {
			return c, err
		}
	}
	// A directly assigned Agent Team retains its safety refusals and its rules
	// even when the current Repo belongs to a different Team.
	if agent != "" {
		ownTeam, err := r.memberTeam("agent", agent)
		if err != nil {
			return c, err
		}
		if ownTeam != "" && ownTeam != team {
			values, err := r.teamCharter(ownTeam)
			if err != nil {
				return c, err
			}
			for _, v := range values {
				if v.attr != "refuses" && v.attr != "rules" {
					continue
				}
				if err = apply("team", ownTeam, v.attr, v.value); err != nil {
					return c, err
				}
			}
		}
	}
	if repo != "" {
		values, err := r.repoCharter(repo)
		if err != nil {
			return c, err
		}
		if err = applyAll("repo", repo, values); err != nil {
			return c, err
		}
	}
	if agent != "" {
		values, err := r.agentCharter(agent)
		if err != nil {
			return c, err
		}
		if err = applyAll("agent", agent, values); err != nil {
			return c, err
		}
	}
	if len(sources) > 0 {
		c.Sources = &sources
	}
	return c, nil
}
