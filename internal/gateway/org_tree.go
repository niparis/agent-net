package gateway

import (
	"database/sql"
	"errors"

	"github.com/niparis/agent-net/internal/protocol"
)

// The org tree (decision 0160) is stored in the records the graph already
// has: a reports_to edge in graph_edges, agents.lead_session and
// sessions.started_by. This file holds its checks and its one-time build.

// orgTreeBuiltKey is the owner_settings marker of the one-time build.
const orgTreeBuiltKey = "org_tree_built"

// repoTopAgentSQL is the Agent at the top of a Repo: its lead, else its owner.
const repoTopAgentSQL = `SELECT COALESCE(CASE WHEN EXISTS(SELECT 1 FROM repo_leads WHERE repo_id=repos.id) THEN (SELECT agent_id FROM repo_leads WHERE repo_id=repos.id) ELSE owner_agent_id END,'') FROM repos WHERE id=?`

// checkReportsTo holds the three rules of the tree: one parent per Agent, no
// cycle, and a merger is never a lead. The parent is a live Agent or the root.
func checkReportsTo(tx *sql.Tx, from, to string) error {
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
	var parent string
	err := tx.QueryRow(`SELECT target FROM graph_edges WHERE source=? AND kind='reports_to' AND target<>?`, from, to).Scan(&parent)
	if err == nil {
		return reject("%s already reports to %s; remove that edge first", from, parent)
	}
	if err != sql.ErrNoRows {
		return err
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

// addReportsToTx adds a parent where the tree has room for it. An Agent that
// already has a parent keeps it, and an edge the rules refuse is skipped: the
// build and a merger start fill gaps, they never move an Agent.
func addReportsToTx(tx *sql.Tx, from, to, actor string, now int64) error {
	if from == "" || to == "" || from == to {
		return nil
	}
	if err := checkReportsTo(tx, from, to); err != nil {
		var refused Rejection
		if errors.As(err, &refused) {
			return nil
		}
		return err
	}
	return storeEdgeTx(tx, protocol.Edge{From: from, To: to, Kind: protocol.EdgeReportsTo, Rule: "normal"}, actor, now)
}

// bindMergerAgentTx gives a project's merger session its own Agent, role
// merger, under the Repo's top Agent. The merger then never runs as the
// Repo's Agent, so it is never taken for the Repo lead.
func bindMergerAgentTx(tx *sql.Tx, project, session, actor string, now int64) error {
	agent := project + " merger"
	if _, err := tx.Exec(`INSERT INTO agents(id,display_name,role,created_at,updated_at,tombstone) VALUES(?,?,'merger',?,?,0)
		ON CONFLICT(id) DO UPDATE SET role='merger',tombstone=0,updated_at=excluded.updated_at`, agent, agent, now, now); err != nil {
		return err
	}
	var top string
	if err := tx.QueryRow(repoTopAgentSQL, project).Scan(&top); err != nil && err != sql.ErrNoRows {
		return err
	}
	if err := addReportsToTx(tx, agent, top, actor, now); err != nil {
		return err
	}
	_, err := tx.Exec(`UPDATE sessions SET agent_id=? WHERE id=?`, agent, session)
	return err
}

// setAgentLeadSessionTx stores an Agent's lead session. The Team and Repo
// columns that name one are read-only copies until their readers move to the
// Agent: a copy that names a session follows; one that allows any session of
// the Agent stays as it is.
func setAgentLeadSessionTx(tx *sql.Tx, agent, session string) error {
	for _, q := range []string{
		`UPDATE agents SET lead_session=? WHERE id=?`,
		`UPDATE repo_leads SET lead_session=? WHERE agent_id=? AND lead_session IS NOT NULL`,
		`UPDATE teams SET lead_session=? WHERE owner_agent_id=? AND lead_session IS NOT NULL`,
	} {
		if _, err := tx.Exec(q, session, agent); err != nil {
			return err
		}
	}
	return nil
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

// StoredLead reads the lead of a Repo or Team, or an Agent's own row, back as
// stored: the lead Agent and its lead session.
func (s *Store) StoredLead(ref protocol.GraphRef) (protocol.LeadSetResult, error) {
	out := protocol.LeadSetResult{OK: true, Ref: ref}
	var agent, session sql.NullString
	var err error
	switch ref.Kind {
	case "repo":
		err = s.db.QueryRow(`SELECT (`+repoTopAgentSQL+`),(SELECT lead_session FROM repo_leads WHERE repo_id=?)`, ref.ID, ref.ID).Scan(&agent, &session)
	case "team":
		err = s.db.QueryRow(`SELECT owner_agent_id,lead_session FROM teams WHERE id=?`, ref.ID).Scan(&agent, &session)
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
	// The order is the priority: the roots first, then Team members to their
	// lead, then leads with no Team above them to the CTO, then Agents that
	// work in a Repo outside any Team to its owner. An Agent keeps the first
	// parent it gets.
	pairs := []string{
		`SELECT 'cto', '` + protocol.OrgRoot + `'`,
		`SELECT agent_id, '` + protocol.OrgRoot + `' FROM sessions WHERE id=(SELECT value FROM owner_settings WHERE key='chief_session') AND agent_id IS NOT NULL`,
		`SELECT tm.member_id, t.owner_agent_id FROM team_members tm JOIN teams t ON t.id=tm.team_id
			WHERE t.active=1 AND tm.member_kind='agent' AND t.owner_agent_id IS NOT NULL ORDER BY t.id, tm.member_id`,
		`SELECT COALESCE(CASE WHEN EXISTS(SELECT 1 FROM repo_leads WHERE repo_id=r.id) THEN (SELECT agent_id FROM repo_leads WHERE repo_id=r.id) ELSE r.owner_agent_id END,''), t.owner_agent_id
			FROM team_members tm JOIN teams t ON t.id=tm.team_id JOIN repos r ON r.id=tm.member_id
			WHERE t.active=1 AND tm.member_kind='repo' AND t.owner_agent_id IS NOT NULL ORDER BY t.id, tm.member_id`,
		`SELECT DISTINCT owner_agent_id, 'cto' FROM teams WHERE active=1 AND owner_agent_id IS NOT NULL ORDER BY owner_agent_id`,
		`SELECT DISTINCT s.agent_id, r.owner_agent_id FROM sessions s JOIN repos r ON r.id=s.repo_id
			WHERE s.agent_id IS NOT NULL AND r.owner_agent_id IS NOT NULL AND s.agent_id<>r.owner_agent_id AND s.worker=''
			AND NOT EXISTS(SELECT 1 FROM project_mergers pm WHERE pm.session_id=s.id) ORDER BY s.agent_id, r.owner_agent_id`,
	}
	for _, query := range pairs {
		rows, err := tx.Query(query)
		if err != nil {
			return err
		}
		var edges [][2]string
		for rows.Next() {
			var from, to sql.NullString
			if err := rows.Scan(&from, &to); err != nil {
				rows.Close()
				return err
			}
			edges = append(edges, [2]string{from.String, to.String})
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		for _, e := range edges {
			if err := addReportsToTx(tx, e[0], e[1], protocol.StartedByAgentNet, now); err != nil {
				return err
			}
		}
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
		if err := bindMergerAgentTx(tx, m[0], m[1], protocol.StartedByAgentNet, now); err != nil {
			return err
		}
	}
	for _, q := range []string{
		`UPDATE agents SET lead_session=COALESCE(
			(SELECT lead_session FROM repo_leads WHERE agent_id=agents.id AND lead_session IS NOT NULL ORDER BY repo_id LIMIT 1),
			(SELECT lead_session FROM teams WHERE owner_agent_id=agents.id AND lead_session IS NOT NULL ORDER BY id LIMIT 1))
			WHERE lead_session IS NULL`,
		`UPDATE sessions SET started_by='` + protocol.StartedByAgentNet + `' WHERE worker<>'' OR harness='cto'
			OR id=(SELECT value FROM owner_settings WHERE key='cto_session')
			OR id IN (SELECT session_id FROM project_mergers)
			OR EXISTS(SELECT 1 FROM session_agent_starts a WHERE a.node_id=sessions.node_id AND a.harness=sessions.harness AND a.native_id=sessions.native_id)`,
	} {
		if _, err := tx.Exec(q); err != nil {
			return err
		}
	}
	_, err = tx.Exec(`INSERT INTO owner_settings(key,value,updated_at) VALUES(?,'1',?)`, orgTreeBuiltKey, now)
	return err
}
