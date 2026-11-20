package gateway

import (
	"database/sql"
	"encoding/json"
	"errors"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/niparis/agent-net/internal/inventory"
	"github.com/niparis/agent-net/internal/protocol"
)

// Skill lists on graph nodes (decision 0169, task 3). A node's own rows are
// merged along its path: global, team, repo, agent. A remove at one level takes
// away a higher add; a lower add wins over a higher remove.

// skillListRow is one stored name on one node.
type skillListRow struct {
	Level  string
	Scope  string
	Name   string
	Action string
	Pin    int
}

// skillMeta is one live archive version without its bundle.
type skillMeta struct {
	Version     int
	Hash        string
	Description string
	SizeTokens  int
	// ContextTokens is the name and description every session loads.
	ContextTokens int
}

// skillArchiveIndex holds the live archive versions once for a read.
type skillArchiveIndex struct {
	latest map[string]int
	meta   map[string]map[int]skillMeta
}

func (s *Store) skillArchiveIndex() (*skillArchiveIndex, error) {
	idx := &skillArchiveIndex{latest: map[string]int{}, meta: map[string]map[int]skillMeta{}}
	rows, err := s.db.Query(`SELECT name, version, hash, description, tokens, context_tokens FROM skill_archive WHERE removed_at=0 ORDER BY name, version`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var name string
		var m skillMeta
		if err = rows.Scan(&name, &m.Version, &m.Hash, &m.Description, &m.SizeTokens, &m.ContextTokens); err != nil {
			return nil, err
		}
		if idx.meta[name] == nil {
			idx.meta[name] = map[int]skillMeta{}
		}
		idx.meta[name][m.Version] = m
		if m.Version > idx.latest[name] {
			idx.latest[name] = m.Version
		}
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	// The latest row wins while its version is live (decision 0169).
	pointers, err := archivePointers(s.db)
	if err != nil {
		return nil, err
	}
	for name, v := range pointers {
		if _, live := idx.meta[name][v]; live {
			idx.latest[name] = v
		}
	}
	return idx, nil
}

// version resolves a pin, or the latest live version. ok is false when the
// wanted version is not in the archive any more.
func (idx *skillArchiveIndex) version(name string, pin int) (skillMeta, bool) {
	if pin > 0 {
		m, ok := idx.meta[name][pin]
		return m, ok
	}
	v, ok := idx.latest[name]
	if !ok {
		return skillMeta{}, false
	}
	return idx.meta[name][v], true
}

func (s *Store) skillListRows() (map[string][]skillListRow, error) {
	rows, err := s.db.Query(`SELECT level, scope, name, action, pin FROM skill_node_lists ORDER BY level, scope, name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string][]skillListRow{}
	for rows.Next() {
		var r skillListRow
		if err = rows.Scan(&r.Level, &r.Scope, &r.Name, &r.Action, &r.Pin); err != nil {
			return nil, err
		}
		key := r.Level + "\x00" + r.Scope
		out[key] = append(out[key], r)
	}
	return out, rows.Err()
}

func (s *Store) skillMemberships() (map[string]string, error) {
	rows, err := s.db.Query(`SELECT m.member_kind, m.member_id, m.team_id FROM team_members m JOIN teams t ON t.id=m.team_id AND t.active=1`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var kind, id, team string
		if err = rows.Scan(&kind, &id, &team); err != nil {
			return nil, err
		}
		out[kind+"\x00"+id] = team
	}
	return out, rows.Err()
}

// skillPath is the node path of one target, from global down.
func skillNodePath(level, scope, repoCtx string, members map[string]string) []skillListRow {
	path := []skillListRow{{Level: "global"}}
	var team string
	if repoCtx != "" {
		team = members["repo\x00"+repoCtx]
	}
	if team == "" && level == "agent" {
		team = members["agent\x00"+scope]
	}
	if team != "" {
		path = append(path, skillListRow{Level: "team", Scope: team})
	}
	if repoCtx != "" {
		path = append(path, skillListRow{Level: "repo", Scope: repoCtx})
	}
	switch level {
	case "team":
		path = append(path, skillListRow{Level: "team", Scope: scope})
	case "agent":
		path = append(path, skillListRow{Level: "agent", Scope: scope})
	}
	return path
}

// resolveSkillPath merges the rows along a path into the effective list. The
// order is stable by name so the CTO sees the same answer every time.
func resolveSkillPath(path []skillListRow, rows map[string][]skillListRow, idx *skillArchiveIndex) []protocol.EffectiveSkill {
	byName := map[string]protocol.EffectiveSkill{}
	for _, node := range path {
		list := rows[node.Level+"\x00"+node.Scope]
		// Removes first, then adds, so a lower level can put back what a
		// higher level removed.
		for _, action := range []string{"remove", "add"} {
			for _, r := range list {
				if r.Action != action {
					continue
				}
				if action == "remove" {
					delete(byName, r.Name)
					continue
				}
				entry := protocol.EffectiveSkill{Name: r.Name, Pin: r.Pin, Level: node.Level, Scope: node.Scope}
				if meta, ok := idx.version(r.Name, r.Pin); ok {
					entry.Version, entry.Hash = meta.Version, meta.Hash
					entry.Description, entry.SizeTokens, entry.ContextTokens = meta.Description, meta.SizeTokens, meta.ContextTokens
				} else {
					entry.PinMissing = r.Pin > 0
				}
				byName[r.Name] = entry
			}
		}
	}
	out := make([]protocol.EffectiveSkill, 0, len(byName))
	for _, entry := range byName {
		out = append(out, entry)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// skillContextTokens totals what a session loads for a list at start. The size
// of the bundles is not counted: a bundle loads only when its skill is used.
func skillListContextTokens(list []protocol.EffectiveSkill) int {
	total := 0
	for _, entry := range list {
		total += entry.ContextTokens
	}
	return total
}

// skillTarget resolves the node a request names: its stored scope and the repo
// context of an Agent or Repo.
func (s *Store) skillTarget(level, scope string) (resolved, repoCtx string, err error) {
	level, scope = strings.TrimSpace(level), strings.TrimSpace(scope)
	switch level {
	case "global":
		return "", "", nil
	case "team":
		var id string
		if err = s.db.QueryRow(`SELECT id FROM teams WHERE active=1 AND (id=? OR name=? COLLATE NOCASE) LIMIT 1`, scope, scope).Scan(&id); err != nil {
			return "", "", reject("unknown team %q", scope)
		}
		return id, "", nil
	case "repo":
		id, e := s.skillRepoID(scope)
		if e != nil {
			return "", "", e
		}
		return id, id, nil
	case "agent":
		var id string
		if err = s.db.QueryRow(`SELECT id FROM agents WHERE tombstone=0 AND (id=? OR display_name=? COLLATE NOCASE) LIMIT 1`, scope, scope).Scan(&id); err != nil {
			return "", "", reject("unknown agent %q", scope)
		}
		repo, e := s.skillAgentRepo(id)
		return id, repo, e
	}
	return "", "", reject("level must be global, team, repo or agent")
}

// skillRepoID resolves a repo by typed id, display name or project name.
func (s *Store) skillRepoID(scope string) (string, error) {
	if scope == "" {
		return "", reject("repo needs a name")
	}
	var id string
	err := s.db.QueryRow(`SELECT id FROM repos WHERE (id=? OR display_name=? COLLATE NOCASE)
		AND NOT EXISTS(SELECT 1 FROM archived_repos WHERE repo_id=repos.id) LIMIT 1`, scope, scope).Scan(&id)
	if err == nil {
		return id, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return "", err
	}
	if err = s.db.QueryRow(`SELECT name FROM projects WHERE name=? COLLATE NOCASE LIMIT 1`, scope).Scan(&id); err == nil {
		return id, nil
	}
	return "", reject("unknown repo %q", scope)
}

// skillAgentRepo is the Agent's repo context: its sole owned Repo, else the
// repo of its latest session. An Agent may work in several Repos; the skills
// path uses the one context it has.
func (s *Store) skillAgentRepo(agent string) (string, error) {
	var owned string
	if err := s.db.QueryRow(`SELECT CASE WHEN count(*)=1 THEN min(id) ELSE '' END FROM repos WHERE owner_agent_id=?
		AND NOT EXISTS(SELECT 1 FROM archived_repos WHERE repo_id=repos.id)`, agent).Scan(&owned); err != nil {
		return "", err
	}
	if owned != "" {
		return owned, nil
	}
	var project string
	if err := s.db.QueryRow(`SELECT repo_project FROM sessions WHERE agent_id=? AND repo_project<>'' ORDER BY last_seen DESC LIMIT 1`, agent).Scan(&project); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", nil
		}
		return "", err
	}
	if id, err := s.skillRepoID(project); err == nil {
		return id, nil
	}
	return project, nil
}

// SkillListsEffective answers one node, or every node the graph holds.
func (s *Store) SkillListsEffective(level, scope string) ([]protocol.EffectiveSkills, error) {
	idx, err := s.skillArchiveIndex()
	if err != nil {
		return nil, err
	}
	rows, err := s.skillListRows()
	if err != nil {
		return nil, err
	}
	members, err := s.skillMemberships()
	if err != nil {
		return nil, err
	}
	type target struct{ level, scope, repo string }
	var targets []target
	if level != "" {
		resolved, repo, err := s.skillTarget(level, scope)
		if err != nil {
			return nil, err
		}
		targets = append(targets, target{level, resolved, repo})
	} else {
		targets = append(targets, target{level: "global"})
		teamRows, err := s.db.Query(`SELECT id FROM teams WHERE active=1 ORDER BY id`)
		if err != nil {
			return nil, err
		}
		for teamRows.Next() {
			var id string
			if err = teamRows.Scan(&id); err != nil {
				teamRows.Close()
				return nil, err
			}
			targets = append(targets, target{"team", id, ""})
		}
		teamRows.Close()
		repoRows, err := s.db.Query(`SELECT id FROM repos
			WHERE NOT EXISTS(SELECT 1 FROM archived_repos WHERE repo_id=repos.id) ORDER BY id`)
		if err != nil {
			return nil, err
		}
		for repoRows.Next() {
			var id string
			if err = repoRows.Scan(&id); err != nil {
				repoRows.Close()
				return nil, err
			}
			targets = append(targets, target{"repo", id, id})
		}
		repoRows.Close()
		agentRows, err := s.db.Query(`SELECT id FROM agents WHERE tombstone=0 ORDER BY id`)
		if err != nil {
			return nil, err
		}
		for agentRows.Next() {
			var id string
			if err = agentRows.Scan(&id); err != nil {
				agentRows.Close()
				return nil, err
			}
			repo, _ := s.skillAgentRepo(id)
			targets = append(targets, target{"agent", id, repo})
		}
		agentRows.Close()
	}
	out := make([]protocol.EffectiveSkills, 0, len(targets))
	for _, t := range targets {
		list := resolveSkillPath(skillNodePath(t.level, t.scope, t.repo, members), rows, idx)
		out = append(out, protocol.EffectiveSkills{Level: t.level, Scope: t.scope, Skills: list, ContextTokens: skillListContextTokens(list)})
	}
	return out, nil
}

// SkillFolder resolves a launch target (decision 0169, task 4) and returns its
// effective skills at their versions. The bundles are read one at a time with
// SkillVersion: a whole set can be larger than one frame may carry.
func (s *Store) SkillFolder(p protocol.SkillFolderRequest) (protocol.SkillFolder, error) {
	level, scope := s.skillFolderTarget(p)
	nodes, err := s.SkillListsEffective(level, scope)
	if err != nil {
		return protocol.SkillFolder{}, err
	}
	out := protocol.SkillFolder{Level: level, Scope: scope, Skills: []protocol.EffectiveSkill{}}
	if len(nodes) == 1 {
		out.Skills = nodes[0].Skills
	}
	return out, nil
}

// SkillVersion reads one stored version's bundle, or part part of it when the
// bundle is over one part (decision 0169). The caller checks the hash before
// it uses the files.
func (s *Store) SkillVersion(name string, version, part int) (protocol.SkillVersion, error) {
	out := protocol.SkillVersion{Name: name, Version: version}
	if !validSkillName(name) {
		return out, reject("bad skill name")
	}
	if part < 0 {
		return out, reject("skill version part %d is not in the archive", part)
	}
	var size int64
	start := int64(part) * protocol.TransferPartSize
	// SQLite counts substr from 1. A bundle of one part comes whole from the
	// first part.
	err := s.db.QueryRow(`SELECT version, hash, length(content), substr(content, ?, ?) FROM skill_archive WHERE name=? AND version=? AND removed_at=0`,
		start+1, protocol.TransferPartSize, name, version).Scan(&out.Version, &out.Hash, &size, &out.Data)
	if errors.Is(err, sql.ErrNoRows) {
		return out, reject("skill version is not in the archive")
	}
	if err != nil {
		return out, err
	}
	if size <= protocol.TransferPartSize {
		if part != 0 {
			return out, reject("skill version is one part")
		}
		return out, nil
	}
	parts := protocol.PartCount(size)
	if part >= parts {
		return out, reject("skill version part %d is not in the archive", part)
	}
	out.Parts, out.Size = parts, size
	return out, nil
}

// skillFolderTarget is the node a launch reads. A session uses its Agent, else
// its Repo, else the global node; a named Agent or Repo that no longer exists
// falls back the same way, so a start never fails on a stale identity.
func (s *Store) skillFolderTarget(p protocol.SkillFolderRequest) (level, scope string) {
	if p.Session != "" {
		var agent, repo, project string
		err := s.db.QueryRow(`SELECT COALESCE(agent_id,''), COALESCE(repo_id,''), COALESCE(repo_project,'') FROM sessions WHERE id=?`, p.Session).
			Scan(&agent, &repo, &project)
		if err == nil {
			if agent != "" && s.skillAgentLive(agent) {
				return "agent", agent
			}
			if repo == "" && project != "" {
				repo, _ = s.skillRepoID(project)
			}
			if repo != "" {
				return "repo", repo
			}
		}
	}
	if p.Agent != "" && s.skillAgentLive(p.Agent) {
		return "agent", p.Agent
	}
	if p.Repo != "" {
		if id, err := s.skillRepoID(p.Repo); err == nil {
			return "repo", id
		}
	}
	return "global", ""
}

// skillAgentLive says the Agent still exists. A tombstoned one has no path.
func (s *Store) skillAgentLive(id string) bool {
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM agents WHERE id=? AND tombstone=0`, id).Scan(&n); err != nil {
		return false
	}
	return n > 0
}

// skillEntryState is one row of a node's own list, kept for a change record.
type skillEntryState struct {
	Name   string `json:"name"`
	Action string `json:"action"`
	Pin    int    `json:"pin,omitempty"`
}

func (s *Store) skillNodeState(level, scope string) ([]skillEntryState, error) {
	rows, err := s.db.Query(`SELECT name, action, pin FROM skill_node_lists WHERE level=? AND scope=? ORDER BY name`, level, scope)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []skillEntryState{}
	for rows.Next() {
		var e skillEntryState
		if err = rows.Scan(&e.Name, &e.Action, &e.Pin); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

func skillStatesEqual(a, b []skillEntryState) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// SkillListSet replaces one node's own add and remove lists, records the
// change with undo, and answers the diff and the sessions to restart.
func (s *Store) SkillListSet(req protocol.SkillSetRequest) (protocol.SkillSetResult, error) {
	level := strings.TrimSpace(req.Level)
	scope := strings.TrimSpace(req.Scope)
	add, remove, pins := req.Add, req.Remove, req.Pins
	if req.Suggestion != "" {
		// A suggestion names the node, so level and scope may be left out.
		var sLevel, sScope, data string
		if err := s.db.QueryRow(`SELECT level, scope, data FROM skill_suggestions WHERE id=?`, req.Suggestion).Scan(&sLevel, &sScope, &data); err != nil {
			return protocol.SkillSetResult{}, reject("unknown suggestion %q", req.Suggestion)
		}
		var stored protocol.SkillSuggestion
		if json.Unmarshal([]byte(data), &stored) != nil {
			return protocol.SkillSetResult{}, reject("suggestion is unreadable")
		}
		if level == "" {
			level = sLevel
		}
		if scope == "" {
			scope = sScope
		}
		add = nil
		for _, skill := range stored.Skills {
			add = append(add, skill.Name)
		}
	}
	resolved, repoCtx, err := s.skillTarget(level, scope)
	if err != nil {
		return protocol.SkillSetResult{}, err
	}
	scope = resolved
	out := protocol.SkillSetResult{Level: level, Scope: scope}
	if req.Suggestion != "" {
		var sLevel, sScope string
		if err = s.db.QueryRow(`SELECT level, scope FROM skill_suggestions WHERE id=?`, req.Suggestion).Scan(&sLevel, &sScope); err != nil {
			return out, reject("unknown suggestion %q", req.Suggestion)
		}
		if sLevel != level || sScope != scope {
			return out, reject("suggestion is for %s %s", sLevel, sScope)
		}
	}
	add, remove = uniqueNames(add), uniqueNames(remove)
	for _, name := range add {
		if !validSkillName(name) {
			return out, reject("bad skill name %q", name)
		}
	}
	for _, name := range remove {
		if !validSkillName(name) {
			return out, reject("bad skill name %q", name)
		}
	}
	inBoth := map[string]bool{}
	for _, name := range add {
		inBoth[name] = true
	}
	for _, name := range remove {
		if inBoth[name] {
			return out, reject("%q cannot be added and removed on the same node", name)
		}
	}
	for name := range pins {
		if !inBoth[name] {
			return out, reject("pin names %q, which this set does not add", name)
		}
	}
	idx, err := s.skillArchiveIndex()
	if err != nil {
		return out, err
	}
	mature := false
	if repoCtx != "" {
		if c, e := s.EffectiveCharter(repoCtx, "", ""); e == nil {
			mature = c.Maturity == protocol.MaturityMature || c.Maturity == protocol.MaturityCritical
		}
	}
	after := []skillEntryState{}
	for _, name := range add {
		if _, ok := idx.latest[name]; !ok {
			return out, reject("skill %q is not in the archive; add it with skill_archive first", name)
		}
		pin := pins[name]
		if pin == 0 && mature {
			pin = idx.latest[name]
			out.PinnedByMaturity = true
		}
		if pin > 0 {
			if _, ok := idx.meta[name][pin]; !ok {
				return out, reject("skill %q has no live version %d", name, pin)
			}
		}
		after = append(after, skillEntryState{Name: name, Action: "add", Pin: pin})
	}
	for _, name := range remove {
		after = append(after, skillEntryState{Name: name, Action: "remove"})
	}
	sort.Slice(after, func(i, j int) bool { return after[i].Name < after[j].Name })
	before, err := s.skillNodeState(level, scope)
	if err != nil {
		return out, err
	}
	rows, err := s.skillListRows()
	if err != nil {
		return out, err
	}
	members, err := s.skillMemberships()
	if err != nil {
		return out, err
	}
	path := skillNodePath(level, scope, repoCtx, members)
	rows[level+"\x00"+scope] = listRows(after)
	beforeList := resolveSkillPath(path, withoutNode(rows, level, scope, before), idx)
	afterList := resolveSkillPath(path, rows, idx)
	out.Effective = afterList
	out.BeforeTokens, out.AfterTokens = skillListContextTokens(beforeList), skillListContextTokens(afterList)
	out.Added, out.Removed = listDelta(beforeList, afterList)
	for _, entry := range afterList {
		if entry.Pin == 0 {
			continue
		}
		old, had := findSkill(beforeList, entry.Name)
		if !had || old.Pin != entry.Pin {
			out.Pinned = append(out.Pinned, entry.Name)
		}
	}
	for _, entry := range beforeList {
		if entry.Pin == 0 {
			continue
		}
		now, ok := findSkill(afterList, entry.Name)
		if !ok || now.Pin == 0 {
			out.Unpinned = append(out.Unpinned, entry.Name)
		}
	}
	if skillStatesEqual(before, after) {
		out.Restart = s.skillRestartOffer(level, scope)
		return out, nil
	}
	tx, err := s.db.Begin()
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	if _, err = tx.Exec(`DELETE FROM skill_node_lists WHERE level=? AND scope=?`, level, scope); err != nil {
		return out, err
	}
	now := s.now().UnixNano()
	for _, e := range after {
		if _, err = tx.Exec(`INSERT INTO skill_node_lists(level,scope,name,action,pin,added_by,added_at) VALUES(?,?,?,?,?,?,?)`,
			level, scope, e.Name, e.Action, e.Pin, req.By, now); err != nil {
			return out, err
		}
	}
	beforeJSON, _ := json.Marshal(before)
	afterJSON, _ := json.Marshal(after)
	out.Change = protocol.NewID("sl")
	if _, err = tx.Exec(`INSERT INTO skill_list_changes(id,level,scope,before,after,actor,at) VALUES(?,?,?,?,?,?,?)`,
		out.Change, level, scope, string(beforeJSON), string(afterJSON), req.By, now); err != nil {
		return out, err
	}
	if err = tx.Commit(); err != nil {
		return out, err
	}
	out.Restart = s.skillRestartOffer(level, scope)
	return out, nil
}

func uniqueNames(names []string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, name := range names {
		name = strings.TrimSpace(name)
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

func listRows(states []skillEntryState) []skillListRow {
	out := make([]skillListRow, 0, len(states))
	for _, e := range states {
		out = append(out, skillListRow{Name: e.Name, Action: e.Action, Pin: e.Pin})
	}
	return out
}

// withoutNode copies the row map with one node set to a given state.
func withoutNode(rows map[string][]skillListRow, level, scope string, states []skillEntryState) map[string][]skillListRow {
	out := make(map[string][]skillListRow, len(rows))
	for k, v := range rows {
		if k == level+"\x00"+scope {
			continue
		}
		out[k] = v
	}
	out[level+"\x00"+scope] = listRows(states)
	return out
}

func findSkill(list []protocol.EffectiveSkill, name string) (protocol.EffectiveSkill, bool) {
	for _, entry := range list {
		if entry.Name == name {
			return entry, true
		}
	}
	return protocol.EffectiveSkill{}, false
}

// listDelta is the names that joined or left the effective list.
func listDelta(before, after []protocol.EffectiveSkill) (added, removed []string) {
	for _, entry := range after {
		if _, ok := findSkill(before, entry.Name); !ok {
			added = append(added, entry.Name)
		}
	}
	for _, entry := range before {
		if _, ok := findSkill(after, entry.Name); !ok {
			removed = append(removed, entry.Name)
		}
	}
	return added, removed
}

// SkillListUndo restores the state before one recorded set.
func (s *Store) SkillListUndo(change string) (protocol.SkillSetResult, error) {
	out := protocol.SkillSetResult{}
	if change == "" {
		return out, reject("undo needs a change id")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	var level, scope, beforeJSON string
	var undone int64
	if err = tx.QueryRow(`SELECT level, scope, before, undone_at FROM skill_list_changes WHERE id=?`, change).Scan(&level, &scope, &beforeJSON, &undone); err != nil {
		return out, reject("unknown skill list change")
	}
	if undone != 0 {
		return out, reject("skill list change was already undone")
	}
	var before []skillEntryState
	if json.Unmarshal([]byte(beforeJSON), &before) != nil {
		return out, reject("skill list change is unreadable")
	}
	if _, err = tx.Exec(`DELETE FROM skill_node_lists WHERE level=? AND scope=?`, level, scope); err != nil {
		return out, err
	}
	now := s.now().UnixNano()
	for _, e := range before {
		if _, err = tx.Exec(`INSERT INTO skill_node_lists(level,scope,name,action,pin,added_by,added_at) VALUES(?,?,?,?,?,?,?)`,
			level, scope, e.Name, e.Action, e.Pin, "owner undo", now); err != nil {
			return out, err
		}
	}
	if _, err = tx.Exec(`UPDATE skill_list_changes SET undone_at=? WHERE id=?`, now, change); err != nil {
		return out, err
	}
	if err = tx.Commit(); err != nil {
		return out, err
	}
	resolved, repoCtx, _ := s.skillTarget(level, scope)
	idx, err := s.skillArchiveIndex()
	if err != nil {
		return out, err
	}
	rows, err := s.skillListRows()
	if err != nil {
		return out, err
	}
	members, err := s.skillMemberships()
	if err != nil {
		return out, err
	}
	list := resolveSkillPath(skillNodePath(level, resolved, repoCtx, members), rows, idx)
	out.Level, out.Scope, out.Effective = level, scope, list
	out.BeforeTokens, out.AfterTokens = skillListContextTokens(list), skillListContextTokens(list)
	out.Restart = s.skillRestartOffer(level, scope)
	return out, nil
}

// skillRestartOffer lists the live sessions on the node and below that still
// run the old set. Nothing restarts here; the CTO acts on this list.
func (s *Store) skillRestartOffer(level, scope string) protocol.SkillRestartOffer {
	out := protocol.SkillRestartOffer{Call: "session_change restart"}
	query := `SELECT s.id, COALESCE(NULLIF(p.name,''), NULLIF(s.name,''), '') FROM sessions s
		LEFT JOIN profiles p ON p.session_id=s.id
		WHERE s.online=1 AND s.archived_at=0 AND s.harness NOT IN ('owner','cto','bot')`
	args := []any{}
	// The sessions of a node and below. A repo includes its sessions; a team
	// includes its member repos and agents; global includes everything.
	switch level {
	case "repo":
		query += ` AND (s.repo_project=? OR s.repo_id=?)`
		args = append(args, scope, scope)
	case "agent":
		query += ` AND s.agent_id=?`
		args = append(args, scope)
	case "team":
		query += ` AND (s.repo_project IN (SELECT member_id FROM team_members WHERE team_id=? AND member_kind='repo')
			OR s.agent_id IN (SELECT member_id FROM team_members WHERE team_id=? AND member_kind='agent'))`
		args = append(args, scope, scope)
	}
	query += ` ORDER BY s.id`
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return out
	}
	defer rows.Close()
	out.Sessions = []protocol.SkillRestartSession{}
	for rows.Next() {
		var session protocol.SkillRestartSession
		if err = rows.Scan(&session.SessionID, &session.Name); err != nil {
			return protocol.SkillRestartOffer{Call: out.Call, Sessions: []protocol.SkillRestartSession{}}
		}
		session.Level, session.Scope = level, scope
		out.Sessions = append(out.Sessions, session)
	}
	return out
}

// --- suggest ---

// skillNodeContext is the text a suggestion matches skills against. It holds
// only what the node says it is: the purpose, a repo's description and name,
// and an Agent's function and expertise. Process attributes (rules, refuses,
// escalate, maturity and the like) are the same agent-net vocabulary on every
// node, and inherited levels and the project's bar repeat it, so none of them
// picks a skill.
func (s *Store) skillNodeContext(level, scope, repoCtx string) (string, error) {
	var parts []string
	add := func(query string, args ...any) error {
		rows, err := s.db.Query(query, args...)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var text string
			if err := rows.Scan(&text); err != nil {
				return err
			}
			parts = append(parts, text)
		}
		return rows.Err()
	}
	switch level {
	case "global":
		if err := add(`SELECT value FROM charter_values WHERE level='global' AND scope='' AND attr='purpose'`); err != nil {
			return "", err
		}
	case "team":
		if err := add(`SELECT value FROM charter_values WHERE level='team' AND scope=? AND attr='purpose'`, scope); err != nil {
			return "", err
		}
		if err := add(`SELECT purpose FROM teams WHERE id=?`, scope); err != nil {
			return "", err
		}
	case "agent":
		if err := add(`SELECT value FROM agent_charter WHERE agent_id=? AND attr='purpose'`, scope); err != nil {
			return "", err
		}
		if err := add(`SELECT COALESCE(p.function,'')||' '||COALESCE(p.expertise,'')
			FROM sessions s LEFT JOIN profiles p ON p.session_id=s.id
			WHERE s.agent_id=? ORDER BY s.last_seen DESC LIMIT 1`, scope); err != nil {
			return "", err
		}
	}
	if repoCtx != "" {
		if err := add(`SELECT value FROM graph_charter WHERE node=? AND attr='purpose'`, repoCtx); err != nil {
			return "", err
		}
		parts = append(parts, repoCtx)
		if err := add(`SELECT repo_description FROM sessions WHERE repo_project=? AND repo_description<>'' ORDER BY last_seen DESC LIMIT 1`, repoCtx); err != nil {
			return "", err
		}
	}
	return strings.Join(parts, "\n"), nil
}

var skillWordSplit = regexp.MustCompile(`[^a-z0-9]+`)

var skillStopwords = func() map[string]bool {
	out := map[string]bool{}
	// The filler words are the ones a repo's own text uses without saying what
	// it is. The last group is agent-net's own vocabulary, which every node's
	// purpose and sessions carry.
	for _, word := range strings.Fields("this that with from your when then will have been does what into over under about they them their there here which where while should could would must only also just make made more most some such than the and for not are was were you our its his her has had how why who all any can may use used using none still whether needs need needed hand live data json request requests state like keep each other same very much well first last next before after between through during within without across every file files uses work works main agent agents node nodes session sessions claude codex harness charter maturity skill skills repo repos project projects") {
		out[word] = true
	}
	return out
}()

func skillWords(text string) map[string]bool {
	out := map[string]bool{}
	for _, word := range skillWordSplit.Split(strings.ToLower(text), -1) {
		if len(word) >= 4 && !skillStopwords[word] {
			out[word] = true
		}
	}
	return out
}

func skillReason(matched []string, description string) string {
	reason := ""
	if len(matched) > 0 {
		if len(matched) > 3 {
			matched = matched[:3]
		}
		reason = "matches " + strings.Join(matched, ", ")
	}
	if description != "" {
		if i := strings.IndexAny(description, ".!?"); i >= 0 {
			description = description[:i]
		}
		description = strings.TrimSpace(description)
		if len(description) > 160 {
			description = strings.TrimSpace(description[:160])
		}
		if reason != "" {
			reason += ". " + description
		} else {
			reason = description
		}
	}
	return reason
}

// skillSuggest ranks archive skills against the node's own text. It makes no
// model call: distinctive words shared with the skill's name and description
// decide, and the description explains the pick.
func (g *Gateway) skillSuggest(req protocol.SkillSuggestRequest) (protocol.SkillSuggestion, error) {
	level := strings.TrimSpace(req.Level)
	scope := strings.TrimSpace(req.Scope)
	resolved, repoCtx, err := g.store.skillTarget(level, scope)
	if err != nil {
		return protocol.SkillSuggestion{}, err
	}
	scope = resolved
	limit := req.Limit
	if limit <= 0 {
		limit = 8
	}
	idx, err := g.store.skillArchiveIndex()
	if err != nil {
		return protocol.SkillSuggestion{}, err
	}
	effective, err := g.store.SkillListsEffective(level, scope)
	if err != nil {
		return protocol.SkillSuggestion{}, err
	}
	already := map[string]bool{}
	if len(effective) > 0 {
		for _, entry := range effective[0].Skills {
			already[entry.Name] = true
		}
	}
	nodeText, err := g.store.skillNodeContext(level, scope, repoCtx)
	if err != nil {
		return protocol.SkillSuggestion{}, err
	}
	contextWords := skillWords(nodeText)
	// A word that many archive skills share says nothing about which one fits
	// this node, so it never selects a skill. Each word that is left weighs more
	// the fewer skills use it.
	words := map[string]map[string]bool{}
	uses := map[string]int{}
	for name, latest := range idx.latest {
		words[name] = skillWords(name + " " + idx.meta[name][latest].Description)
		for word := range words[name] {
			uses[word]++
		}
	}
	commonLimit := max(1, len(idx.latest)/10)
	type scored struct {
		name    string
		version int
		reason  string
		size    int
		context int
		score   float64
		matched []string
		already bool
	}
	var ranked []scored
	for name, latest := range idx.latest {
		meta := idx.meta[name][latest]
		var matched []string
		score := 0.0
		for word := range words[name] {
			if !contextWords[word] || uses[word] > commonLimit {
				continue
			}
			matched = append(matched, word)
			score += math.Log(1 + float64(len(idx.latest))/float64(uses[word]))
		}
		sort.Slice(matched, func(i, j int) bool {
			if uses[matched[i]] != uses[matched[j]] {
				return uses[matched[i]] < uses[matched[j]]
			}
			return matched[i] < matched[j]
		})
		ranked = append(ranked, scored{
			name: name, version: latest, size: meta.SizeTokens, context: meta.ContextTokens, score: score, matched: matched,
			reason: skillReason(matched, meta.Description), already: already[name],
		})
	}
	sort.Slice(ranked, func(i, j int) bool {
		if ranked[i].score != ranked[j].score {
			return ranked[i].score > ranked[j].score
		}
		return ranked[i].name < ranked[j].name
	})
	suggestion := protocol.SkillSuggestion{Level: level, Scope: scope}
	for _, row := range ranked {
		if row.already {
			suggestion.Already = append(suggestion.Already, row.name)
			continue
		}
		if row.score == 0 || len(suggestion.Skills) >= limit {
			continue
		}
		suggestion.ContextTokens += row.context
		suggestion.Skills = append(suggestion.Skills, protocol.SuggestedSkill{
			Name: row.name, Version: row.version, Reason: row.reason, ContextTokens: row.context, SizeTokens: row.size,
		})
	}
	available, err := g.store.skillMCPNames(repoCtx)
	if err != nil {
		return suggestion, err
	}
	for i := range suggestion.Skills {
		missing, err := g.skillMissingMCP(suggestion.Skills[i].Name, suggestion.Skills[i].Version, available)
		if err != nil {
			return suggestion, err
		}
		suggestion.Skills[i].MissingMcp = missing
	}
	if len(suggestion.Skills) == 0 {
		suggestion.Note = "No archive skill shares a distinctive word with this node's text. Add skills with skill_archive, or give the node a charter and description."
	}
	suggestion.ID = protocol.NewID("sg")
	data, _ := json.Marshal(suggestion)
	if _, err = g.store.db.Exec(`INSERT INTO skill_suggestions(id,level,scope,data,actor,at) VALUES(?,?,?,?,?,?)`,
		suggestion.ID, level, scope, string(data), req.By, g.store.now().UnixNano()); err != nil {
		return suggestion, err
	}
	return suggestion, nil
}

var skillMCPName = regexp.MustCompile(`mcp__([A-Za-z0-9_-]+)__`)

// skillMissingMCP reads one stored version's text files and names the MCP
// servers it calls that the node does not have.
func (g *Gateway) skillMissingMCP(name string, version int, available map[string]bool) ([]string, error) {
	var bundle []byte
	if err := g.store.db.QueryRow(`SELECT content FROM skill_archive WHERE name=? AND version=? AND removed_at=0`, name, version).Scan(&bundle); err != nil {
		return nil, nil
	}
	dir, err := os.MkdirTemp("", "agent-net-skill-mcp-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)
	if inventory.UnpackSkillLimit(bundle, dir, inventory.ArchiveLimit) != nil {
		return nil, nil
	}
	missing := map[string]bool{}
	err = filepath.WalkDir(dir, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil || entry.IsDir() {
			return walkErr
		}
		rel, e := filepath.Rel(dir, path)
		if e != nil {
			return e
		}
		text, e := inventory.ReadSkillText(dir, filepath.ToSlash(rel))
		if e != nil {
			return nil
		}
		for _, match := range skillMCPName.FindAllStringSubmatch(string(text), -1) {
			if !available[match[1]] {
				missing[match[1]] = true
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(missing))
	for server := range missing {
		out = append(out, server)
	}
	sort.Strings(out)
	return out, nil
}

// skillMCPNames is the shared MCP servers a node can call: enabled and global,
// or enabled and assigned to the node's repo.
func (s *Store) skillMCPNames(repoCtx string) (map[string]bool, error) {
	servers, err := s.MCPServers()
	if err != nil {
		return nil, err
	}
	assigned, err := s.MCPAssignments()
	if err != nil {
		return nil, err
	}
	out := map[string]bool{}
	for _, server := range servers {
		if !server.Enabled {
			continue
		}
		scope, projects := "global", []string(nil)
		if a, ok := assigned[server.Name]; ok {
			scope, projects = a.Scope, a.Projects
		}
		if scope == "global" {
			out[server.Name] = true
			continue
		}
		if repoCtx == "" || scope != "projects" {
			continue
		}
		for _, project := range projects {
			if project == repoCtx {
				out[server.Name] = true
			}
		}
	}
	return out, nil
}

// newProjectSkillNotice tells the CTO, once per open notice, that a new repo
// was registered. The owner chose: suggest, set, then tell him, with undo.
func (s *Store) newProjectSkillNotice(repo string) error {
	var cto string
	if err := s.db.QueryRow(`SELECT value FROM owner_settings WHERE key='cto_session'`).Scan(&cto); err != nil || cto == "" {
		return nil
	}
	ref := "skills:new-project:" + repo
	var open int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM inbox_items WHERE source_ref=? AND status='open'`, ref).Scan(&open); err != nil {
		return err
	}
	if open > 0 {
		return nil
	}
	_, err := s.db.Exec(`INSERT INTO inbox_items (id,kind,session_id,node,text,recommendation,message_id,conversation_id,created_at,status,options,asker_name,change_title,
		from_kind,source_ref,about_kind,about_id,next_who,why_you,detail,route_fixed,category)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		protocol.NewID("i"), protocol.AskKindCheck, cto, repo,
		"new project "+repo+": run skills suggest and set, then tell the owner",
		"The owner chose: apply the suggestion and tell him. The set is undoable in the Desk.",
		"", "", s.now().UnixNano(), "open", "[]", "agent-net", "",
		protocol.AskFromGateway, ref, "repo", repo, cto, "", "", 1, protocol.CategoryNormal)
	return err
}
