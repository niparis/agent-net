package gateway

import (
	"sort"
	"strings"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// The Graph carries each record's place (decision 0089) so the owner's card
// reads the same Team, lead and held back data as the turn text. A graph read
// builds the place index once, in a handful of set-based queries, then joins in
// Go. It never runs a query per node.

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

type placeTeam struct {
	name, mode, held, owner string
}

type placeIndex struct {
	teams         map[string]placeTeam
	teamByRepo    map[string]string
	teamByAgent   map[string]string
	teamByOwner   map[string]string
	repoName      map[string]string
	repoLeadAgent map[string]string
	repoHeld      map[string]string
	agentName     map[string]string
	agentLead     map[string]string
	sessionLabel  map[string]string
	liveByAgent   map[string][]string
	ownedRepos    map[string][]string
	sessionRepo   map[string]string
}

// buildPlaceIndex loads the tree once. The set-based queries are the same data
// placeText reads one session at a time.
func (s *Store) buildPlaceIndex(repos []RepoRecord, agents []AgentRecord, now time.Time) (*placeIndex, error) {
	idx := &placeIndex{
		teams:         map[string]placeTeam{},
		teamByRepo:    map[string]string{},
		teamByAgent:   map[string]string{},
		teamByOwner:   map[string]string{},
		repoName:      map[string]string{},
		repoLeadAgent: map[string]string{},
		repoHeld:      map[string]string{},
		agentName:     map[string]string{},
		agentLead:     map[string]string{},
		sessionLabel:  map[string]string{},
		liveByAgent:   map[string][]string{},
		ownedRepos:    map[string][]string{},
		sessionRepo:   map[string]string{},
	}
	for _, a := range agents {
		idx.agentName[a.ID] = a.DisplayName
	}
	for _, r := range repos {
		if r.Archived {
			continue
		}
		idx.repoName[r.ID] = r.DisplayName
		if r.OwnerAgentID != nil {
			idx.ownedRepos[*r.OwnerAgentID] = append(idx.ownedRepos[*r.OwnerAgentID], r.ID)
		}
	}
	var err error
	if idx.repoLeadAgent, err = orgPairs(s.db, `SELECT r.id,COALESCE(CASE WHEN rl.repo_id IS NULL THEN r.owner_agent_id ELSE rl.agent_id END,'') FROM repos r LEFT JOIN repo_leads rl ON rl.repo_id=r.id`); err != nil {
		return nil, err
	}
	if idx.agentLead, err = orgPairs(s.db, `SELECT id,COALESCE(lead_session,'') FROM agents`); err != nil {
		return nil, err
	}

	rows, err := s.db.Query(`SELECT id,name,authority_mode,held_back,COALESCE(owner_agent_id,'') FROM teams WHERE active=1 ORDER BY id`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var id string
		var t placeTeam
		if err := rows.Scan(&id, &t.name, &t.mode, &t.held, &t.owner); err != nil {
			rows.Close()
			return nil, err
		}
		idx.teams[id] = t
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()

	// One team per owner, lowest ID, matches the place query's order.
	for id, t := range idx.teams {
		if t.owner == "" {
			continue
		}
		if old, ok := idx.teamByOwner[t.owner]; !ok || id < old {
			idx.teamByOwner[t.owner] = id
		}
	}

	rows, err = s.db.Query(`SELECT team_id,member_kind,member_id FROM team_members ORDER BY team_id`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var team, kind, member string
		if err := rows.Scan(&team, &kind, &member); err != nil {
			rows.Close()
			return nil, err
		}
		switch kind {
		case "repo":
			if old, ok := idx.teamByRepo[member]; !ok || team < old {
				idx.teamByRepo[member] = team
			}
		case "agent":
			if old, ok := idx.teamByAgent[member]; !ok || team < old {
				idx.teamByAgent[member] = team
			}
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()

	rows, err = s.db.Query(`SELECT repo_id,held_back FROM repo_held_back`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var repo, held string
		if err := rows.Scan(&repo, &held); err != nil {
			rows.Close()
			return nil, err
		}
		idx.repoHeld[repo] = held
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()

	rows, err = s.db.Query(`SELECT s.id,COALESCE(s.agent_id,''),COALESCE(s.repo_id,''),s.online,s.worker,s.harness,COALESCE(s.limited_until,0),s.last_seen,
		COALESCE(NULLIF(p.name,''),NULLIF(s.name,''),NULLIF(s.title,''),NULLIF(s.repo_project,''),NULLIF(p.project,''),'')
		FROM sessions s LEFT JOIN profiles p ON p.session_id=s.id
		ORDER BY s.last_seen DESC,s.id`)
	if err != nil {
		return nil, err
	}
	type liveSession struct {
		id      string
		limited int64
		last    int64
	}
	live := map[string][]liveSession{}
	for rows.Next() {
		var id, agent, repo, worker, harness, label string
		var online, limited, last int64
		if err := rows.Scan(&id, &agent, &repo, &online, &worker, &harness, &limited, &last, &label); err != nil {
			rows.Close()
			return nil, err
		}
		idx.sessionLabel[id] = label
		if agent != "" && repo != "" && idx.sessionRepo[agent] == "" {
			idx.sessionRepo[agent] = repo
		}
		if online == 1 && worker == "" && harness != "owner" && harness != "cto" && agent != "" {
			live[agent] = append(live[agent], liveSession{id: id, limited: limited, last: last})
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()
	for agent, list := range live {
		sort.SliceStable(list, func(i, j int) bool {
			il, jl := list[i].limited > now.Unix(), list[j].limited > now.Unix()
			if il != jl {
				return !il
			}
			if list[i].last != list[j].last {
				return list[i].last > list[j].last
			}
			return list[i].id < list[j].id
		})
		ids := make([]string, 0, len(list))
		for _, s := range list {
			ids = append(ids, s.id)
		}
		idx.liveByAgent[agent] = ids
	}
	return idx, nil
}

// leadName mirrors leadNameQ: a live session of the agent, preferring the
// pinned one, else the agent's display name, then the agent's project. It
// never returns an internal ID.
func (idx *placeIndex) leadName(agent string) string {
	if agent == "" {
		return ""
	}
	if live := idx.liveByAgent[agent]; len(live) > 0 {
		id := live[0]
		if pinned := idx.agentLead[agent]; pinned != "" {
			for _, candidate := range live {
				if candidate == pinned {
					id = pinned
					break
				}
			}
		}
		if label := idx.sessionLabel[id]; label != "" {
			return label
		}
	}
	if name := idx.agentName[agent]; name != "" {
		return name
	}
	if repo := idx.repoForAgent(agent); repo != "" {
		if name := idx.repoName[repo]; name != "" {
			return name
		}
	}
	return ""
}

// repoForAgent is the repo that gives the agent its context: its only owned
// repo, else the repo of its newest session.
func (idx *placeIndex) repoForAgent(agent string) string {
	if owned := idx.ownedRepos[agent]; len(owned) == 1 {
		return owned[0]
	}
	return idx.sessionRepo[agent]
}

func (idx *placeIndex) place(ref protocol.GraphRef, repo, agent string) (protocol.NodePlace, bool) {
	p := protocol.NodePlace{}
	switch ref.Kind {
	case "team":
		t, ok := idx.teams[ref.ID]
		if !ok {
			return p, false
		}
		p.Team, p.Mode = t.name, protocol.NormalAuthorityMode(t.mode)
		p.TeamLead = idx.leadName(t.owner)
		for _, item := range heldBackParts(t.held) {
			p.HeldBack = append(p.HeldBack, protocol.PlaceHeldBack{Text: item, From: "team"})
		}
		return p, true
	case "repo":
		repo = ref.ID
	case "agent":
		agent = ref.ID
	default:
		return p, false
	}
	if repo == "" && agent != "" {
		repo = idx.repoForAgent(agent)
	}
	if repo != "" {
		p.Repo = idx.repoName[repo]
		p.RepoLead = idx.leadName(idx.repoLeadAgent[repo])
	}
	teamID := ""
	if repo != "" {
		teamID = idx.teamByRepo[repo]
	}
	if teamID == "" && agent != "" {
		teamID = idx.teamByAgent[agent]
	}
	if teamID == "" && agent != "" {
		teamID = idx.teamByOwner[agent]
	}
	if t, ok := idx.teams[teamID]; ok {
		p.Team, p.Mode = t.name, protocol.NormalAuthorityMode(t.mode)
		p.TeamLead = idx.leadName(t.owner)
		for _, item := range heldBackParts(t.held) {
			p.HeldBack = append(p.HeldBack, protocol.PlaceHeldBack{Text: item, From: "team"})
		}
	}
	if repo != "" {
		for _, item := range heldBackParts(idx.repoHeld[repo]) {
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
	return p, true
}
