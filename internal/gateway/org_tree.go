package gateway

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/niparis/agent-net/internal/protocol"
)

// The org tree (decision 0160, 0184) is computed on read from the leads of
// repos and teams, team membership and roles. The only stored reports_to rows
// are explicit moves by the CTO or the owner (edge_set); they win over the
// computed parent. This file holds the computation and the checks of a move.

// orgTreeBuiltKey is the gateway_state marker of the one-time build.
const orgTreeBuiltKey = "org_tree_built"

// agentParentsQ computes the reports_to parent of every live Agent from the
// authoritative leadership and membership columns, never from stored edges.
// The priority is: merger under its Repo's lead, cto and chief under owner,
// Team Agent members under their Team lead, Repo leads of a Team's Repos under
// the Team lead, Team leads and Repo leads of a Repo in no Team under cto, and
// Agents working in a Repo under that Repo's lead. Leading a Repo outranks
// working in another one. Targets that are tombstoned, mergers, or would make
// a cycle are skipped. A stored reports_to edge (an explicit move) replaces
// the computed parent when its target is valid.
func agentParentsQ(q leadDB) (map[string]string, error) {
	type agentRow struct {
		id, role, teamID string
	}
	rows, err := q.Query(`SELECT id, role, team_id FROM agents WHERE tombstone=0 ORDER BY id`)
	if err != nil {
		return nil, err
	}
	var agents []agentRow
	liveNonMerger := map[string]bool{}
	for rows.Next() {
		var a agentRow
		if err = rows.Scan(&a.id, &a.role, &a.teamID); err != nil {
			rows.Close()
			return nil, err
		}
		agents = append(agents, a)
		if a.role != "merger" {
			liveNonMerger[a.id] = true
		}
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()

	teamOwner := map[string]string{}
	leadsTeam := map[string]bool{}
	rows, err = q.Query(`SELECT id, COALESCE(owner_agent_id,'') FROM teams WHERE active=1 ORDER BY id`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var id, owner string
		if err = rows.Scan(&id, &owner); err != nil {
			rows.Close()
			return nil, err
		}
		teamOwner[id] = owner
		if owner != "" {
			leadsTeam[owner] = true
		}
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()

	repoLead := map[string]string{}
	// First active Team whose member Repo is led by agent (ordered by team_id, repo_id).
	repoTeamParent := map[string]string{}
	// An Agent that leads at least one Repo in no Team. Such a Repo has no team
	// lead above its lead, so the lead sits under the CTO.
	leadsSoloRepo := map[string]bool{}
	rows, err = q.Query(`SELECT id, COALESCE(lead_agent_id,''), team_id, archived FROM repos ORDER BY team_id, id`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var id, lead, teamID string
		var archived int
		if err = rows.Scan(&id, &lead, &teamID, &archived); err != nil {
			rows.Close()
			return nil, err
		}
		repoLead[id] = lead
		if lead == "" {
			continue
		}
		if teamID == "" {
			// An archived Repo places nobody: its former lead keeps whatever
			// other place it has.
			if archived == 0 {
				leadsSoloRepo[lead] = true
			}
			continue
		}
		if owner := teamOwner[teamID]; owner != "" && owner != lead && repoTeamParent[lead] == "" {
			repoTeamParent[lead] = owner
		}
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()

	var chiefAgent string
	err = q.QueryRow(`SELECT COALESCE(agent_id,'') FROM sessions WHERE id=(SELECT value FROM gateway_state WHERE key='chief_session')`).Scan(&chiefAgent)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}

	mergerRepo := map[string]string{}
	rows, err = q.Query(`SELECT s.agent_id, r.id FROM repos r JOIN sessions s ON s.id=r.merger_session WHERE s.agent_id IS NOT NULL ORDER BY r.id`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var agent, repo string
		if err = rows.Scan(&agent, &repo); err != nil {
			rows.Close()
			return nil, err
		}
		if mergerRepo[agent] == "" {
			mergerRepo[agent] = repo
		}
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()

	rows, err = q.Query(`SELECT node, SUBSTR(over_ref,6) FROM node_scopes WHERE kind='merge' AND over_ref LIKE 'repo:%' ORDER BY over_ref`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var agent, repo string
		if err = rows.Scan(&agent, &repo); err != nil {
			rows.Close()
			return nil, err
		}
		if mergerRepo[agent] == "" {
			mergerRepo[agent] = repo
		}
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()

	sessionRepoLead := map[string]string{}
	rows, err = q.Query(`SELECT DISTINCT s.agent_id, s.repo_id FROM sessions s
		WHERE s.worker='' AND s.agent_id IS NOT NULL AND s.agent_id<>'' AND s.repo_id IS NOT NULL AND s.repo_id<>''
		AND NOT EXISTS(SELECT 1 FROM repos pm WHERE pm.merger_session=s.id)`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var agent, repo string
		if err = rows.Scan(&agent, &repo); err != nil {
			rows.Close()
			return nil, err
		}
		lead := repoLead[repo]
		if lead == "" || lead == agent {
			continue
		}
		if prev := sessionRepoLead[agent]; prev == "" || lead < prev {
			sessionRepoLead[agent] = lead
		}
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()

	explicit := map[string]string{}
	rows, err = q.Query(`SELECT source,target FROM graph_edges WHERE kind='reports_to'`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var from, to string
		if err = rows.Scan(&from, &to); err != nil {
			rows.Close()
			return nil, err
		}
		explicit[from] = to
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()

	rawParent := func(a agentRow) string {
		if a.role == "merger" {
			repo := mergerRepo[a.id]
			if repo == "" && strings.HasSuffix(a.id, "-merger") {
				cand := strings.TrimSuffix(a.id, "-merger")
				if _, ok := repoLead[cand]; ok {
					repo = cand
				}
			}
			return repoLead[repo]
		}
		if a.id == "cto" || a.id == chiefAgent {
			return protocol.OrgRoot
		}
		if a.teamID != "" {
			if owner := teamOwner[a.teamID]; owner != "" && owner != a.id {
				return owner
			}
		}
		if owner := repoTeamParent[a.id]; owner != "" {
			return owner
		}
		// Leading a Repo outranks working in one. A team lead, and the lead of
		// a Repo in no Team, reports to the CTO, not to the lead of another
		// Repo the Agent happens to work in.
		if leadsTeam[a.id] || leadsSoloRepo[a.id] {
			return "cto"
		}
		return sessionRepoLead[a.id]
	}

	parents := make(map[string]string, len(agents))
	valid := func(a agentRow, want string) bool {
		if want == "" || want == a.id || (want != protocol.OrgRoot && !liveNonMerger[want]) {
			return false
		}
		for cur, steps := want, 0; cur != "" && cur != protocol.OrgRoot && steps <= len(agents); cur, steps = parents[cur], steps+1 {
			if cur == a.id {
				return false
			}
		}
		return true
	}
	// Explicit moves go first, so a computed parent that would close a cycle
	// with one of them gives way.
	for _, a := range agents {
		if want := explicit[a.id]; valid(a, want) {
			parents[a.id] = want
		}
	}
	for _, a := range agents {
		if _, moved := parents[a.id]; !moved {
			if want := rawParent(a); valid(a, want) {
				parents[a.id] = want
			}
		}
	}
	return parents, nil
}

// checkReportsToTarget holds the endpoint rules of a move: the source is a
// live Agent, the target is a live Agent that is not a merger, or the root,
// and the move makes no cycle with other explicit moves.
func checkReportsToTarget(tx *sql.Tx, from, to string) error {
	var live bool
	if err := tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM agents WHERE id=? AND tombstone=0)`, from).Scan(&live); err != nil {
		return err
	}
	if !live {
		return reject("unknown or inactive Agent %q", from)
	}
	if to != protocol.OrgRoot {
		var role sql.NullString
		if err := tx.QueryRow(`SELECT role FROM agents WHERE id=? AND tombstone=0`, to).Scan(&role); err != nil && err != sql.ErrNoRows {
			return err
		}
		if !role.Valid {
			return reject("unknown or inactive Agent %q", to)
		}
		if role.String == "merger" {
			return reject("a merger is never a lead: nothing reports to %s", to)
		}
	}
	// Only explicit moves can close a cycle: a computed parent gives way.
	var cycle bool
	if err := tx.QueryRow(`WITH RECURSIVE up(id) AS (SELECT ? UNION SELECT e.target FROM graph_edges e JOIN up ON e.source=up.id WHERE e.kind='reports_to')
		SELECT EXISTS(SELECT 1 FROM up WHERE id=?)`, to, from).Scan(&cycle); err != nil {
		return err
	}
	if cycle {
		return reject("%s reports up to %s already; this edge would make a cycle", to, from)
	}
	return nil
}

// checkReportsTo adds the one-parent rule: an Agent has one explicit parent.
func checkReportsTo(tx *sql.Tx, from, to string) error {
	if err := checkReportsToTarget(tx, from, to); err != nil {
		return err
	}
	var parent string
	err := tx.QueryRow(`SELECT target FROM graph_edges WHERE source=? AND kind='reports_to' AND target<>?`, from, to).Scan(&parent)
	if err == nil {
		return reject("%s already reports to %s; remove that edge first", from, parent)
	}
	if err != sql.ErrNoRows {
		return err
	}
	return nil
}

// moveReportsToTx stores an explicit reports_to edge and replaces the Agent's
// current explicit parent in the same transaction. It writes one history row
// whose old value is the old parent edge, so one undo restores that parent.
func moveReportsToTx(tx *sql.Tx, e protocol.Edge, actor string, now int64) error {
	var parent string
	err := tx.QueryRow(`SELECT target FROM graph_edges WHERE source=? AND kind='reports_to' AND target<>?`, e.From, e.To).Scan(&parent)
	if err == sql.ErrNoRows {
		return storeEdgeTx(tx, e, actor, now)
	}
	if err != nil {
		return err
	}
	var previous protocol.Edge
	previous.From, previous.To, previous.Kind = e.From, parent, e.Kind
	if err := tx.QueryRow(`SELECT mode,rule FROM graph_edges WHERE source=? AND target=? AND kind=?`, e.From, parent, e.Kind).Scan(&previous.Mode, &previous.Rule); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM graph_edges WHERE source=? AND target=? AND kind=?`, e.From, parent, e.Kind); err != nil {
		return err
	}
	if _, err := tx.Exec(`INSERT INTO graph_edges(source,target,kind,mode,rule) VALUES(?,?,?,?,?) ON CONFLICT(source,target,kind) DO UPDATE SET mode=excluded.mode,rule=excluded.rule`, e.From, e.To, e.Kind, e.Mode, e.Rule); err != nil {
		return err
	}
	before, _ := json.Marshal(previous)
	after, _ := json.Marshal(e)
	return graphRecord(tx, "edge", e.From, e.Kind, string(before), string(after), actor, now)
}

// agentAboveTx says whether one Agent is above another in the org tree: its
// parent, or a parent of its parent, and so on. An Agent is not above itself.
func agentAboveTx(tx *sql.Tx, above, agent string) (bool, error) {
	if above == "" || agent == "" {
		return false, nil
	}
	parents, err := agentParentsQ(tx)
	if err != nil {
		return false, err
	}
	for cur, steps := parents[agent], 0; cur != "" && steps <= len(parents); cur, steps = parents[cur], steps+1 {
		if cur == above {
			return true, nil
		}
	}
	return false, nil
}

// repairOrgTreeTx is kept for the historical migration runner; reports_to is
// now computed on read.
func repairOrgTreeTx(tx *sql.Tx) error { return nil }

// bindMergerAgentTx gives a project's merger session its own Agent, role
// merger, under the Repo's top Agent. Its ID has no space, as Agent IDs are
// used in scope refs and command arguments; the name keeps the session's. The merger then never runs as the
// Repo's Agent, so it is never taken for the Repo lead.
func bindMergerAgentTx(tx *sql.Tx, project, session string, now int64) error {
	agent := project + "-merger"
	if _, err := tx.Exec(`INSERT INTO agents(id,display_name,role,created_at,updated_at,tombstone) VALUES(?,?,'merger',?,?,0)
		ON CONFLICT(id) DO UPDATE SET role='merger',tombstone=0,updated_at=excluded.updated_at`, agent, project+" merger", now, now); err != nil {
		return err
	}
	_, err := tx.Exec(`UPDATE sessions SET agent_id=? WHERE id=?`, agent, session)
	return err
}

// setAgentLeadSessionTx stores an Agent's lead session and closes the open CTO
// question about it. The Agent is the only lead session (decision 0160); the
// old Team and Repo copy columns are gone.
func (s *Store) setAgentLeadSessionTx(tx *sql.Tx, agent, session string) error {
	if _, err := tx.Exec(`UPDATE agents SET lead_session=? WHERE id=?`, session, agent); err != nil {
		return err
	}
	return closeLeadSessionQuestionQ(tx, agent, session, s.now().UnixNano())
}

// clearLostLeadSessionTx clears an Agent's lead session once the Agent leads no
// Team or Repo. The pin would otherwise name a session from a place the Agent
// no longer leads, the stale pin the copies caused.
func clearLostLeadSessionTx(tx *sql.Tx, agent string) error {
	if agent == "" {
		return nil
	}
	_, err := tx.Exec(`UPDATE agents SET lead_session=NULL WHERE id=? AND lead_session IS NOT NULL
		AND NOT EXISTS(SELECT 1 FROM repos WHERE lead_agent_id=?)
		AND NOT EXISTS(SELECT 1 FROM teams WHERE active=1 AND owner_agent_id=?)`, agent, agent, agent)
	return err
}

// StoredEdge reads an edge back as stored, with typed endpoints.
func (s *Store) StoredEdge(e protocol.Edge) (protocol.Edge, error) {
	e = legacyRefEdge(e)
	if err := s.db.QueryRow(`SELECT mode,rule FROM graph_edges WHERE source=? AND target=? AND kind=?`, e.From, e.To, e.Kind).Scan(&e.Mode, &e.Rule); err != nil {
		return e, err
	}
	typedHistoryEdge(&e)
	return e, nil
}

// leadCopyColumn reports whether a table still has its lead_session copy
// column. The drop migration removes them, and a rewound store may replay the
// one-time build after that.
func leadCopyColumn(tx *sql.Tx, table string) (bool, error) {
	var has int
	err := tx.QueryRow(`SELECT COUNT(*) FROM pragma_table_info(?) WHERE name='lead_session'`, table).Scan(&has)
	return has > 0, err
}

// seedAgentLeadSessionsTx takes each Agent's lead session from the two copies
// once, as decision 0160's build does: a Repo copy wins over a Team copy, and
// an Agent with no copy keeps what it has. It is the last read of the copies;
// the drop migration removes them.
func seedAgentLeadSessionsTx(tx *sql.Tx) error {
	repo, err := leadCopyColumn(tx, "repo_leads")
	if err != nil {
		return err
	}
	team, err := leadCopyColumn(tx, "teams")
	if err != nil {
		return err
	}
	var source, named []string
	if repo {
		source = append(source, `(SELECT lead_session FROM repo_leads WHERE agent_id=agents.id AND lead_session IS NOT NULL ORDER BY repo_id LIMIT 1)`)
		named = append(named, `EXISTS(SELECT 1 FROM repo_leads WHERE agent_id=agents.id AND lead_session IS NOT NULL)`)
	}
	if team {
		source = append(source, `(SELECT lead_session FROM teams WHERE owner_agent_id=agents.id AND lead_session IS NOT NULL ORDER BY id LIMIT 1)`)
		named = append(named, `EXISTS(SELECT 1 FROM teams WHERE owner_agent_id=agents.id AND lead_session IS NOT NULL)`)
	}
	if len(source) == 0 {
		return nil
	}
	_, err = tx.Exec(`UPDATE agents SET lead_session=COALESCE(` + strings.Join(source, ",") + `)
		WHERE lead_session IS NULL AND (` + strings.Join(named, " OR ") + `)`)
	return err
}

// dropLeadSessionCopiesTx makes agents.lead_session the only lead session. A
// non-empty Team or Repo copy must already equal its Agent's lead session;
// otherwise the migration refuses to drop them and the gateway stops, so a
// stale pin is seen and fixed rather than thrown away.
func dropLeadSessionCopiesTx(tx *sql.Tx) error {
	copies := []struct{ table, ref, owner string }{
		{"repo_leads", "repo_id", "agent_id"},
		{"teams", "id", "owner_agent_id"},
	}
	for _, c := range copies {
		has, err := leadCopyColumn(tx, c.table)
		if err != nil {
			return err
		}
		if !has {
			continue
		}
		var ref, owner, copyValue, agentValue string
		err = tx.QueryRow(`SELECT `+c.ref+`,COALESCE(`+c.owner+`,''),lead_session,
			COALESCE((SELECT lead_session FROM agents WHERE id=`+c.table+`.`+c.owner+`),'')
			FROM `+c.table+` WHERE COALESCE(lead_session,'')<>''
			AND lead_session<>COALESCE((SELECT lead_session FROM agents WHERE id=`+c.table+`.`+c.owner+`),'') LIMIT 1`).Scan(&ref, &owner, &copyValue, &agentValue)
		if err == nil {
			return fmt.Errorf("%s %q names lead session %q for Agent %q, which has %q: fix the copy before this gateway drops the column", c.table, ref, copyValue, owner, agentValue)
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if _, err := tx.Exec(`ALTER TABLE ` + c.table + ` DROP COLUMN lead_session`); err != nil {
			return err
		}
	}
	return nil
}

// StoredLead reads the lead of a Repo or Team, or an Agent's own row, back as
// stored: the lead Agent and its lead session.
func (s *Store) StoredLead(ref protocol.GraphRef) (protocol.LeadSetResult, error) {
	out := protocol.LeadSetResult{OK: true, Ref: ref}
	var agent, session sql.NullString
	var err error
	switch ref.Kind {
	case "repo":
		err = s.db.QueryRow(`SELECT `+repoTopAgentExpr+`,
			(SELECT a.lead_session FROM agents a WHERE a.id=`+repoTopAgentExpr+`) FROM repos WHERE id=?`, ref.ID).Scan(&agent, &session)
	case "team":
		err = s.db.QueryRow(`SELECT t.owner_agent_id,a.lead_session FROM teams t LEFT JOIN agents a ON a.id=t.owner_agent_id WHERE t.id=?`, ref.ID).Scan(&agent, &session)
	default:
		err = s.db.QueryRow(`SELECT id,lead_session FROM agents WHERE id=?`, ref.ID).Scan(&agent, &session)
	}
	out.Agent, out.LeadSession = agent.String, session.String
	return out, err
}

// buildOrgTreeOnceTx adds the org tree columns, then builds the tree from
// the records that implied it before (decision 0160). The columns are added
// whenever they are missing; the tree is built once, then the gateway_state
// marker stops it, so a replayed migration never undoes a later edit.
func buildOrgTreeOnceTx(tx *sql.Tx, now int64) error {
	for _, col := range []struct{ table, name, spec string }{
		{"agents", "lead_session", "TEXT"},
		{"sessions", "started_by", "TEXT NOT NULL DEFAULT '" + protocol.StartedByOwner + "'"},
	} {
		var has int
		if err := tx.QueryRow(`SELECT COUNT(*) FROM pragma_table_info(?) WHERE name=?`, col.table, col.name).Scan(&has); err != nil {
			return err
		}
		if has == 0 {
			if _, err := tx.Exec(`ALTER TABLE ` + col.table + ` ADD COLUMN ` + col.name + ` ` + col.spec); err != nil {
				return err
			}
		}
	}
	if _, built, err := markerGetTx(tx, orgTreeBuiltKey); err != nil || built {
		return err
	}
	// Each merger row gets its Agent first, so the Agent loop finds it. The
	// Agent loop then uses the same rule as every later write: the old tables
	// decide one Agent's parent. The lead session comes from the two copies
	// once, before the drop migration removes them.
	if err := seedAgentLeadSessionsTx(tx); err != nil {
		return err
	}
	rows, err := tx.Query(`SELECT project, session_id FROM project_mergers ORDER BY project, node_id`)
	if err != nil {
		return err
	}
	var mergers [][2]string
	for rows.Next() {
		var project, session string
		if err := rows.Scan(&project, &session); err != nil {
			rows.Close()
			return err
		}
		mergers = append(mergers, [2]string{project, session})
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, m := range mergers {
		if err := bindMergerAgentTx(tx, m[0], m[1], now); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(`UPDATE sessions SET started_by='` + protocol.StartedByAgentNet + `' WHERE worker<>'' OR harness='cto'
		OR id=(SELECT value FROM owner_settings WHERE key='cto_session')
		OR id IN (SELECT session_id FROM project_mergers)`); err != nil {
		return err
	}
	return markerSetTx(tx, orgTreeBuiltKey, "1", now)
}

// orgSessionNameSQL names session x the way the graph lists a session: its
// name, else its harness title.
const orgSessionNameSQL = `COALESCE(NULLIF(x.name,''),x.title,'')`

// addOrgTree puts the org tree on the Agent nodes of a typed read: the
// parent, the lead session, who started each listed session, and the
// scopes. It reads each table once, whatever the number of Agents.
func (s *Store) addOrgTree(byRef map[protocol.GraphRef]*protocol.Node) error {
	agent := func(id string) *protocol.Node { return byRef[protocol.GraphRef{Kind: "agent", ID: id}] }
	parents, err := agentParentsQ(s.db)
	if err != nil {
		return err
	}
	for id, parent := range parents {
		if n := agent(id); n != nil {
			n.ReportsTo = parent
		}
	}
	rows, err := s.db.Query(`SELECT a.id, a.lead_session, ` + orgSessionNameSQL + `, COALESCE(x.online,0), COALESCE(x.started_by,'')
		FROM agents a
		LEFT JOIN sessions x ON x.id=a.lead_session
		WHERE a.tombstone=0 AND a.lead_session IS NOT NULL`)
	if err != nil {
		return err
	}
	for rows.Next() {
		var id string
		var lead protocol.GraphSession
		if err = rows.Scan(&id, &lead.ID, &lead.Name, &lead.Online, &lead.StartedBy); err != nil {
			rows.Close()
			return err
		}
		if n := agent(id); n != nil && lead.ID != "" {
			n.LeadSession = &lead
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	rows, err = s.db.Query(`SELECT id, started_by FROM sessions WHERE online=1 AND agent_id IS NOT NULL`)
	if err != nil {
		return err
	}
	startedBy := map[string]string{}
	for rows.Next() {
		var id, by string
		if err = rows.Scan(&id, &by); err != nil {
			rows.Close()
			return err
		}
		startedBy[id] = by
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, n := range byRef {
		for i := range n.Sessions {
			n.Sessions[i].StartedBy = startedBy[n.Sessions[i].ID]
		}
	}
	scopes, err := s.AllScopes()
	if err != nil {
		return err
	}
	for _, sc := range scopes {
		if n := agent(sc.Node); n != nil {
			n.Scopes = append(n.Scopes, sc)
		}
	}
	return nil
}

// GraphForRoot is GraphForRef limited, when root is set, to one subtree of
// the org tree: the Agent root and every Agent under it, with the edges
// between them. root OrgRoot keeps every Agent the tree places under the
// owner.
func (s *Store) GraphForRoot(ref *protocol.GraphRef, root string) (protocol.Graph, error) {
	g, err := s.GraphForRef(ref)
	if err != nil || root == "" {
		return g, err
	}
	children := map[string][]string{}
	found := root == protocol.OrgRoot
	for _, n := range g.Nodes {
		if n.Ref == nil || n.Ref.Kind != "agent" {
			continue
		}
		found = found || n.Ref.ID == root
		if n.ReportsTo != "" {
			children[n.ReportsTo] = append(children[n.ReportsTo], n.Ref.ID)
		}
	}
	if !found {
		return protocol.Graph{}, reject("unknown agent %q in root", root)
	}
	keep := map[string]bool{}
	for queue := []string{root}; len(queue) > 0; queue = queue[1:] {
		if !keep[queue[0]] {
			keep[queue[0]] = true
			queue = append(queue, children[queue[0]]...)
		}
	}
	kept := func(r *protocol.GraphRef) bool { return r != nil && r.Kind == "agent" && keep[r.ID] }
	out := protocol.Graph{Warnings: g.Warnings, Nodes: []protocol.Node{}, Edges: []protocol.Edge{}}
	for _, n := range g.Nodes {
		if kept(n.Ref) {
			out.Nodes = append(out.Nodes, n)
		}
	}
	for _, e := range g.Edges {
		if kept(e.FromRef) && kept(e.ToRef) {
			out.Edges = append(out.Edges, e)
		}
	}
	return out, nil
}

// sessionOrgLines are the two org lines of agent_session_get. A session that
// is not its Agent's lead session reports to that lead session; the lead
// session, or a session of an Agent with none, reports to the Agent's parent.
// Every scope another Agent holds over the session's Agent, Repo, one of
// their Teams, or all, is someone it also answers to.
func (s *Store) sessionOrgLines(session string) (reportsTo, also string, err error) {
	var repo, agent, lead, leadName string
	name := func(col string) string {
		return `COALESCE((SELECT ` + orgSessionNameSQL + ` FROM sessions x WHERE x.id=` + col + `),'')`
	}
	err = s.db.QueryRow(`SELECT COALESCE(s.repo_id,''), COALESCE(a.id,''), COALESCE(a.lead_session,''), `+name("a.lead_session")+`
		FROM sessions s
		LEFT JOIN agents a ON a.id=s.agent_id AND a.tombstone=0
		WHERE s.id=?`, session).Scan(&repo, &agent, &lead, &leadName)
	if err != nil {
		return "", "", err
	}
	line := func(who, lead, leadName string) string {
		if lead == "" {
			return who
		}
		if leadName == "" {
			leadName = lead
		}
		return who + " (lead session " + leadName + ")"
	}
	switch {
	case lead != "" && lead != session:
		reportsTo = line(agent, lead, leadName)
	case agent != "":
		parent, err := agentParentQ(s.db, agent)
		if err != nil {
			return "", "", err
		}
		if parent != "" {
			var parentLead, parentLeadName string
			if err := s.db.QueryRow(`SELECT COALESCE(lead_session,''), `+name("lead_session")+` FROM agents WHERE id=?`, parent).Scan(&parentLead, &parentLeadName); err != nil && !errors.Is(err, sql.ErrNoRows) {
				return "", "", err
			}
			reportsTo = line(parent, parentLead, parentLeadName)
		}
	}
	rows, err := s.db.Query(`SELECT ns.node, ns.kind FROM node_scopes ns WHERE ns.node<>?1 AND (ns.over_ref='all'
		OR ns.over_ref='agent:'||?1 OR ns.over_ref='repo:'||?2
		OR EXISTS(SELECT 1 FROM teams t WHERE t.active=1 AND ns.over_ref='team:'||t.id
			AND ((?1<>'' AND EXISTS(SELECT 1 FROM agents ag WHERE ag.id=?1 AND ag.team_id=t.id)) OR (?2<>'' AND EXISTS(SELECT 1 FROM repos rp WHERE rp.id=?2 AND rp.team_id=t.id)))))
		ORDER BY ns.node, ns.kind`, agent, repo)
	if err != nil {
		return "", "", err
	}
	defer rows.Close()
	var holders []string
	kinds := map[string][]string{}
	for rows.Next() {
		var node, kind string
		if err := rows.Scan(&node, &kind); err != nil {
			return "", "", err
		}
		if kinds[node] == nil {
			holders = append(holders, node)
		}
		kinds[node] = append(kinds[node], kind)
	}
	parts := make([]string, 0, len(holders))
	for _, node := range holders {
		parts = append(parts, node+" ("+strings.Join(kinds[node], ", ")+")")
	}
	return reportsTo, strings.Join(parts, ", "), rows.Err()
}
