package gateway

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/niparis/agent-net/internal/protocol"
)

// The org tree (decision 0160) is stored in the records the graph already
// has: a reports_to edge in graph_edges, agents.lead_session and
// sessions.started_by. This file holds its checks, its one-time build and the
// update each later write runs so the tree follows the old tables.

// orgTreeBuiltKey is the owner_settings marker of the one-time build.
const orgTreeBuiltKey = "org_tree_built"

// checkReportsToTarget holds the endpoint rules of the tree: the source is a
// live Agent, and the target is a live Agent that is not a merger, or the
// root. It does not look at the source's current parent, so a move can use it.
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

// checkReportsTo holds the three rules of the tree: one parent per Agent, no
// cycle, and a merger is never a lead. The parent is a live Agent or the root.
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

// agentParentTx is the parent the old tables imply for one Agent. The order is
// the priority: a merger under its Repo's top Agent, the roots, Team members,
// the top Agent of a Team's member Repo, Team leads, then an Agent that works
// in a Repo whose top Agent it is not. An Agent with no rule has no parent.
func agentParentTx(tx *sql.Tx, agent string) (string, error) {
	var role string
	if err := tx.QueryRow(`SELECT role FROM agents WHERE id=? AND tombstone=0`, agent).Scan(&role); err != nil {
		if err == sql.ErrNoRows {
			return "", nil
		}
		return "", err
	}
	if role == "merger" {
		var repo string
		err := tx.QueryRow(`SELECT pm.project FROM project_mergers pm JOIN sessions s ON s.id=pm.session_id WHERE s.agent_id=? ORDER BY pm.project LIMIT 1`, agent).Scan(&repo)
		if err == sql.ErrNoRows {
			return "", nil
		}
		if err != nil {
			return "", err
		}
		var top string
		err = tx.QueryRow(repoTopAgentSQL, repo).Scan(&top)
		if err == sql.ErrNoRows {
			return "", nil
		}
		return top, err
	}
	if agent == "cto" {
		return protocol.OrgRoot, nil
	}
	var chief bool
	if err := tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM sessions WHERE agent_id=? AND id=(SELECT value FROM owner_settings WHERE key='chief_session'))`, agent).Scan(&chief); err != nil {
		return "", err
	}
	if chief {
		return protocol.OrgRoot, nil
	}
	var parent string
	err := tx.QueryRow(`SELECT t.owner_agent_id FROM team_members tm JOIN teams t ON t.id=tm.team_id
		WHERE tm.member_kind='agent' AND tm.member_id=? AND t.active=1 AND COALESCE(t.owner_agent_id,'')<>'' AND t.owner_agent_id<>?
		ORDER BY t.id LIMIT 1`, agent, agent).Scan(&parent)
	if err == nil {
		return parent, nil
	}
	if err != sql.ErrNoRows {
		return "", err
	}
	err = tx.QueryRow(`SELECT t.owner_agent_id FROM repos JOIN team_members tm ON tm.member_kind='repo' AND tm.member_id=repos.id
		JOIN teams t ON t.id=tm.team_id
		WHERE t.active=1 AND COALESCE(t.owner_agent_id,'')<>'' AND t.owner_agent_id<>? AND `+repoTopAgentExpr+`=?
		ORDER BY t.id, repos.id LIMIT 1`, agent, agent).Scan(&parent)
	if err == nil {
		return parent, nil
	}
	if err != sql.ErrNoRows {
		return "", err
	}
	var leads bool
	if err := tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM teams WHERE active=1 AND owner_agent_id=?)`, agent).Scan(&leads); err != nil {
		return "", err
	}
	if leads {
		return "cto", nil
	}
	err = tx.QueryRow(`SELECT `+repoTopAgentExpr+` FROM sessions s JOIN repos ON repos.id=s.repo_id
		WHERE s.agent_id=? AND s.worker='' AND NOT EXISTS(SELECT 1 FROM project_mergers pm WHERE pm.session_id=s.id)
		AND `+repoTopAgentExpr+`<>'' AND `+repoTopAgentExpr+`<>s.agent_id
		ORDER BY `+repoTopAgentExpr+` LIMIT 1`, agent).Scan(&parent)
	if err == nil {
		return parent, nil
	}
	if err == sql.ErrNoRows {
		return "", nil
	}
	return "", err
}

// removeReportsToTx deletes an Agent's parent. The tree is derived, so the
// move leaves no graph history row of its own; the old-table write that caused
// it carries the history.
func removeReportsToTx(tx *sql.Tx, from, to string) error {
	if to == "" {
		return nil
	}
	_, err := tx.Exec(`DELETE FROM graph_edges WHERE source=? AND target=? AND kind=?`, from, to, protocol.EdgeReportsTo)
	return err
}

// placeAgentTx makes one Agent's parent match the old tables. A parent the
// rules refuse is left as it is; an Agent with no parent loses its edge. The
// build and every later write use this one function. The Agent's lead session
// is not derived here: lead_set and session_replace own it.
func placeAgentTx(tx *sql.Tx, agent string) error {
	if agent == "" {
		return nil
	}
	var current string
	if err := tx.QueryRow(`SELECT target FROM graph_edges WHERE source=? AND kind='reports_to'`, agent).Scan(&current); err != nil && err != sql.ErrNoRows {
		return err
	}
	want, err := agentParentTx(tx, agent)
	if err != nil {
		return err
	}
	if want == agent {
		want = ""
	}
	if want == current {
		return nil
	}
	if want == "" {
		return removeReportsToTx(tx, agent, current)
	}
	if err := checkReportsToTarget(tx, agent, want); err != nil {
		var refused Rejection
		if errors.As(err, &refused) {
			return nil
		}
		return err
	}
	if current != "" {
		if err := removeReportsToTx(tx, agent, current); err != nil {
			return err
		}
	}
	_, err = tx.Exec(`INSERT INTO graph_edges(source,target,kind,mode,rule) VALUES(?,?,?,'','normal')
		ON CONFLICT(source,target,kind) DO UPDATE SET mode='',rule='normal'`, agent, want, protocol.EdgeReportsTo)
	return err
}

// placeRepoAgentsTx moves the tree for one Repo after its lead or owner
// changed: its top Agent and every Agent that works in it.
func placeRepoAgentsTx(tx *sql.Tx, repo string) error {
	var top string
	if err := tx.QueryRow(repoTopAgentSQL, repo).Scan(&top); err != nil {
		if err == sql.ErrNoRows {
			return nil
		}
		return err
	}
	agents := []string{}
	seen := map[string]bool{}
	if top != "" {
		agents = append(agents, top)
		seen[top] = true
	}
	rows, err := tx.Query(`SELECT DISTINCT s.agent_id FROM sessions s WHERE s.repo_id=? AND s.agent_id IS NOT NULL AND s.agent_id<>'' AND s.worker='' AND NOT EXISTS(SELECT 1 FROM project_mergers pm WHERE pm.session_id=s.id) ORDER BY s.agent_id`, repo)
	if err != nil {
		return err
	}
	for rows.Next() {
		var agent string
		if err = rows.Scan(&agent); err != nil {
			rows.Close()
			return err
		}
		if !seen[agent] {
			agents = append(agents, agent)
			seen[agent] = true
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, agent := range agents {
		if err := placeAgentTx(tx, agent); err != nil {
			return err
		}
	}
	return nil
}

// placeTeamMembersTx moves the tree for one Team after its lead changed: each
// Agent member and the top Agent of each member Repo.
func placeTeamMembersTx(tx *sql.Tx, team string) error {
	rows, err := tx.Query(`SELECT member_kind,member_id FROM team_members WHERE team_id=? ORDER BY member_kind,member_id`, team)
	if err != nil {
		return err
	}
	var agents []string
	for rows.Next() {
		var kind, id string
		if err = rows.Scan(&kind, &id); err != nil {
			rows.Close()
			return err
		}
		if kind == "agent" {
			agents = append(agents, id)
			continue
		}
		var top string
		err = tx.QueryRow(repoTopAgentSQL, id).Scan(&top)
		if err != nil && err != sql.ErrNoRows {
			rows.Close()
			return err
		}
		if top != "" {
			agents = append(agents, top)
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, agent := range agents {
		if err := placeAgentTx(tx, agent); err != nil {
			return err
		}
	}
	return nil
}

// placeEveryAgentTx makes every live Agent's parent and lead session match the
// old tables. The build and the one-time repair use it.
func placeEveryAgentTx(tx *sql.Tx) error {
	rows, err := tx.Query(`SELECT id FROM agents WHERE tombstone=0 ORDER BY id`)
	if err != nil {
		return err
	}
	var agents []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		agents = append(agents, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, agent := range agents {
		if err := placeAgentTx(tx, agent); err != nil {
			return err
		}
	}
	return nil
}

// repairOrgTreeTx re-places every Agent once, so an edge left stale by a lead
// write from before every writer re-placed its Repo or Team follows the old
// tables again. Two passes: an Agent the cycle check leaves in place can move
// on the second pass once its old parent has moved.
func repairOrgTreeTx(tx *sql.Tx) error {
	for pass := 0; pass < 2; pass++ {
		if err := placeEveryAgentTx(tx); err != nil {
			return err
		}
	}
	return nil
}

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
	if _, err := tx.Exec(`UPDATE sessions SET agent_id=? WHERE id=?`, agent, session); err != nil {
		return err
	}
	return placeAgentTx(tx, agent)
}

// setAgentLeadSessionTx stores an Agent's lead session. The Agent is the only
// lead session (decision 0160); the old Team and Repo copy columns are gone.
func setAgentLeadSessionTx(tx *sql.Tx, agent, session string) error {
	_, err := tx.Exec(`UPDATE agents SET lead_session=? WHERE id=?`, session, agent)
	return err
}

// clearLostLeadSessionTx clears an Agent's lead session once the Agent leads no
// Team or Repo. The pin would otherwise name a session from a place the Agent
// no longer leads, the stale pin the copies caused.
func clearLostLeadSessionTx(tx *sql.Tx, agent string) error {
	if agent == "" {
		return nil
	}
	_, err := tx.Exec(`UPDATE agents SET lead_session=NULL WHERE id=? AND lead_session IS NOT NULL
		AND NOT EXISTS(SELECT 1 FROM repo_leads WHERE agent_id=?)
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
// whenever they are missing; the tree is built once, then the owner_settings
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
	var built bool
	if err := tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM owner_settings WHERE key=?)`, orgTreeBuiltKey).Scan(&built); err != nil || built {
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
	if err := placeEveryAgentTx(tx); err != nil {
		return err
	}
	if _, err := tx.Exec(`UPDATE sessions SET started_by='` + protocol.StartedByAgentNet + `' WHERE worker<>'' OR harness='cto'
		OR id=(SELECT value FROM owner_settings WHERE key='cto_session')
		OR id IN (SELECT session_id FROM project_mergers)
		OR EXISTS(SELECT 1 FROM session_agent_starts a WHERE a.node_id=sessions.node_id AND a.harness=sessions.harness AND a.native_id=sessions.native_id)`); err != nil {
		return err
	}
	_, err = tx.Exec(`INSERT INTO owner_settings(key,value,updated_at) VALUES(?,'1',?)`, orgTreeBuiltKey, now)
	return err
}

// orgSessionNameSQL names session x the way the graph lists a session: its
// profile name, else its stored name, else its harness title.
const orgSessionNameSQL = `COALESCE(NULLIF(p.name,''),NULLIF(x.name,''),x.title,'')`

// addOrgTree puts the org tree on the Agent nodes of a typed read: the
// parent, the lead session, who started each listed session, and the
// scopes. It reads each table once, whatever the number of Agents.
func (s *Store) addOrgTree(byRef map[protocol.GraphRef]*protocol.Node) error {
	agent := func(id string) *protocol.Node { return byRef[protocol.GraphRef{Kind: "agent", ID: id}] }
	rows, err := s.db.Query(`SELECT a.id, COALESCE(e.target,''), COALESCE(a.lead_session,''), ` + orgSessionNameSQL + `, COALESCE(x.online,0), COALESCE(x.started_by,'')
		FROM agents a
		LEFT JOIN graph_edges e ON e.source=a.id AND e.kind='reports_to'
		LEFT JOIN sessions x ON x.id=a.lead_session
		LEFT JOIN profiles p ON p.session_id=a.lead_session
		WHERE a.tombstone=0 AND (e.target IS NOT NULL OR a.lead_session IS NOT NULL)`)
	if err != nil {
		return err
	}
	for rows.Next() {
		var id, parent string
		var lead protocol.GraphSession
		if err = rows.Scan(&id, &parent, &lead.ID, &lead.Name, &lead.Online, &lead.StartedBy); err != nil {
			rows.Close()
			return err
		}
		if n := agent(id); n != nil {
			n.ReportsTo = parent
			if lead.ID != "" {
				n.LeadSession = &lead
			}
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
	var repo, agent, lead, leadName, parent, parentLead, parentLeadName string
	name := func(col string) string {
		return `COALESCE((SELECT ` + orgSessionNameSQL + ` FROM sessions x LEFT JOIN profiles p ON p.session_id=x.id WHERE x.id=` + col + `),'')`
	}
	err = s.db.QueryRow(`SELECT COALESCE(s.repo_id,''), COALESCE(a.id,''), COALESCE(a.lead_session,''), `+name("a.lead_session")+`,
		COALESCE(e.target,''), COALESCE(pa.lead_session,''), `+name("pa.lead_session")+`
		FROM sessions s
		LEFT JOIN agents a ON a.id=s.agent_id AND a.tombstone=0
		LEFT JOIN graph_edges e ON e.source=a.id AND e.kind='reports_to'
		LEFT JOIN agents pa ON pa.id=e.target
		WHERE s.id=?`, session).Scan(&repo, &agent, &lead, &leadName, &parent, &parentLead, &parentLeadName)
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
	case parent != "":
		reportsTo = line(parent, parentLead, parentLeadName)
	}
	rows, err := s.db.Query(`SELECT ns.node, ns.kind FROM node_scopes ns WHERE ns.node<>?1 AND (ns.over_ref='all'
		OR ns.over_ref='agent:'||?1 OR ns.over_ref='repo:'||?2
		OR EXISTS(SELECT 1 FROM team_members tm JOIN teams t ON t.id=tm.team_id WHERE t.active=1 AND ns.over_ref='team:'||t.id
			AND ((tm.member_kind='agent' AND tm.member_id=?1) OR (tm.member_kind='repo' AND tm.member_id=?2))))
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
