package gateway

import (
	"database/sql"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// The org tree is how the gateway learns who reports to whom (decision 0160).

// leadDB is the read side of a transaction or the database.
type leadDB interface {
	QueryRow(string, ...any) *sql.Row
	Query(string, ...any) (*sql.Rows, error)
}

// repoTopAgentOfSQL is the top Agent of the Repo named by a SQL expression.
func repoTopAgentOfSQL(repo string) string {
	return `COALESCE((SELECT CASE WHEN rl.repo_id IS NULL THEN r.owner_agent_id ELSE rl.agent_id END
		FROM repos r LEFT JOIN repo_leads rl ON rl.repo_id=r.id WHERE r.id=` + repo + `),'')`
}

var (
	repoTopAgentExpr = repoTopAgentOfSQL("repos.id")
	repoTopAgentSQL  = `SELECT ` + repoTopAgentExpr + ` FROM repos WHERE id=?`
)

// mergerAgentSQL is true when the session alias runs as a merger Agent.
func mergerAgentSQL(alias string) string {
	return `EXISTS(SELECT 1 FROM agents ma WHERE ma.id=` + alias + `.agent_id AND ma.role='merger')`
}

// registeredMergerSQL is a merger Agent with a live registration.
func registeredMergerSQL(alias string) string {
	return `(` + mergerAgentSQL(alias) + ` AND EXISTS(SELECT 1 FROM project_mergers pm WHERE pm.session_id=` + alias + `.id))`
}

func orgPairs(q leadDB, query string, args ...any) (map[string]string, error) {
	rows, err := q.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			return nil, err
		}
		out[k] = v
	}
	return out, rows.Err()
}

func orgValue(q leadDB, query string, args ...any) (string, error) {
	var v string
	err := q.QueryRow(query, args...).Scan(&v)
	if err == sql.ErrNoRows {
		err = nil
	}
	return v, err
}

func repoTopAgentQ(q leadDB, repo string) (string, error) {
	return orgValue(q, repoTopAgentSQL, repo)
}

func agentLeadSessionQ(q leadDB, agent string) (string, error) {
	return orgValue(q, `SELECT COALESCE(lead_session,'') FROM agents WHERE id=?`, agent)
}

func agentNameQ(q leadDB, agent string) (string, error) {
	name, err := orgValue(q, `SELECT display_name FROM agents WHERE id=?`, agent)
	if name == "" {
		name = agent
	}
	return name, err
}

// agentParentOfSQL is the tree parent of the Agent named by a SQL expression.
func agentParentOfSQL(agent string) string {
	return `COALESCE((SELECT target FROM graph_edges WHERE source=` + agent + ` AND kind='reports_to'),'')`
}

func agentParentQ(q leadDB, agent string) (string, error) {
	return orgValue(q, `SELECT `+agentParentOfSQL("?"), agent)
}

// agentParentLabelQ names the parent the tree stores for one Agent.
func agentParentLabelQ(q leadDB, agent string) (string, error) {
	parent, err := agentParentQ(q, agent)
	if err != nil {
		return "", err
	}
	switch parent {
	case "", protocol.OrgRoot:
		return "the owner", nil
	case "cto":
		return "the CTO", nil
	}
	return agentNameQ(q, parent)
}

func repoNameQ(q leadDB, repo string) (string, error) {
	name, err := orgValue(q, `SELECT display_name FROM repos WHERE id=?`, repo)
	if name == "" && err == nil {
		name = repo
	}
	return name, err
}

func sessionIsMergerQ(q leadDB, session string) (bool, error) {
	var merger bool
	err := q.QueryRow(`SELECT EXISTS(SELECT 1 FROM sessions s JOIN agents a ON a.id=s.agent_id WHERE s.id=? AND a.role='merger')`, session).Scan(&merger)
	return merger, err
}

// isLeadSessionQ says whether a session is the lead's: only the Agent's pinned
// lead session is. A lead Agent with no pin has no lead session, so none of its
// sessions leads. The pin wins even when the session now belongs to another
// Agent node, so a split lead session is never redirected away from itself.
func isLeadSessionQ(q leadDB, lead, agent, session string) (bool, error) {
	if lead == "" || session == "" {
		return false, nil
	}
	pin, err := agentLeadSessionQ(q, lead)
	if err != nil {
		return false, err
	}
	return pin != "" && pin == session, nil
}

// liveLeadSessionQ picks a live session of a lead: not over its limit first,
// and never one that waits for its user's answer.
func liveLeadSessionQ(q leadDB, agent, repo string, now time.Time) (id, name string, err error) {
	err = q.QueryRow(`SELECT s.id,a.display_name FROM sessions s JOIN agents a ON a.id=s.agent_id
		WHERE s.agent_id=? AND s.online=1 AND s.worker='' AND s.harness NOT IN ('owner','cto') AND a.role<>'merger'
		AND COALESCE(s.waiting,'')=''
		ORDER BY CASE WHEN s.limited_until>? THEN 1 ELSE 0 END,CASE WHEN s.id=COALESCE(a.lead_session,'') THEN 0 ELSE 1 END,
		CASE WHEN s.repo_id=? THEN 0 ELSE 1 END,s.last_seen DESC,s.id LIMIT 1`, agent, now.Unix(), repo).Scan(&id, &name)
	return
}

// leadPinStateQ reports whether a pinned session exists, is online, waits for
// its user, or is over its usage limit.
func leadPinStateQ(q leadDB, pin string, now time.Time) (online, waiting, limited, exists bool, err error) {
	var on, wait, lim int
	err = q.QueryRow(`SELECT online,CASE WHEN COALESCE(waiting,'')<>'' THEN 1 ELSE 0 END,CASE WHEN limited_until>? THEN 1 ELSE 0 END
		FROM sessions WHERE id=? AND archived_at=0`, now.Unix(), pin).Scan(&on, &wait, &lim)
	if err == sql.ErrNoRows {
		return false, false, false, false, nil
	}
	if err != nil {
		return false, false, false, false, err
	}
	return on != 0, wait != 0, lim != 0, true, nil
}

// leadPick is the session a redirect goes to.
type leadPick struct {
	id, name string
	// fallback is true when the pinned session waits for its user, so a live
	// session of the lead takes the request instead. With ready false it says
	// the pin waits and no other live session can take the request.
	fallback bool
	// noLead is true when the lead Agent has no pinned session. Nothing leads,
	// so the request is delivered as addressed.
	noLead bool
}

// redirectLeadSessionQ picks the session a redirected request goes to. A
// pinned lead session takes it even when offline, queued for its wake. A
// pinned session that waits for its user is skipped for another live session
// of the lead. ready is false when the pin waits and no other live session can
// take the request, or when the lead has no pinned session at all; the returned
// pick marks which.
func redirectLeadSessionQ(q leadDB, agent, repo string, now time.Time) (pick leadPick, ready bool, err error) {
	pin, err := agentLeadSessionQ(q, agent)
	if err != nil {
		return pick, false, err
	}
	if pin == "" {
		return leadPick{noLead: true}, false, nil
	}
	online, waiting, limited, exists, err := leadPinStateQ(q, pin, now)
	if err != nil {
		return pick, false, err
	}
	if !exists {
		return leadPick{noLead: true}, false, nil
	}
	if !online || (!waiting && !limited) {
		name, err := agentNameQ(q, agent)
		return leadPick{id: pin, name: name}, true, err
	}
	// A pinned session that waits for its user or is over its limit gives way
	// to another live session of the lead, which is not told that it leads.
	id, name, err := liveLeadSessionQ(q, agent, repo, now)
	if err == sql.ErrNoRows {
		if waiting {
			return leadPick{fallback: true}, false, nil
		}
		name, err := agentNameQ(q, agent)
		return leadPick{id: pin, name: name}, true, err
	}
	return leadPick{id: id, name: name, fallback: true}, true, err
}

// resolveLeadSession picks the session of a lead that gets a message: the
// pinned session, then a live one, then the newest in the Repo, then the
// newest. A pinned session that waits for its user gives way to another live
// session of the lead, but still receives an addressed message when none is
// live.
func resolveLeadSession(q leadDB, agent, repo string, now time.Time) (string, error) {
	pin, err := agentLeadSessionQ(q, agent)
	if err != nil {
		return "", err
	}
	if pin != "" {
		online, waiting, limited, exists, err := leadPinStateQ(q, pin, now)
		if err != nil {
			return "", err
		}
		if exists {
			if !online || (!waiting && !limited) {
				return pin, nil
			}
			if id, _, err := liveLeadSessionQ(q, agent, repo, now); err != sql.ErrNoRows {
				return id, err
			}
			return pin, nil
		}
	}
	if id, _, err := liveLeadSessionQ(q, agent, repo, now); err != sql.ErrNoRows {
		return id, err
	}
	pick := func(where string, args ...any) (string, error) {
		var id string
		err := q.QueryRow(`SELECT s.id FROM sessions s JOIN agents a ON a.id=s.agent_id WHERE `+where+` ORDER BY s.last_seen DESC,s.id LIMIT 1`, args...).Scan(&id)
		return id, err
	}
	base := `s.agent_id=? AND s.worker='' AND s.archived_at=0 AND s.harness NOT IN ('owner','cto') AND a.role<>'merger'`
	if repo != "" {
		if id, err := pick(base+` AND s.repo_id=?`, agent, repo); err == nil {
			return id, nil
		} else if err != sql.ErrNoRows {
			return "", err
		}
	}
	return pick(base, agent)
}

type repoRef struct{ id, name string }

func ledReposQ(q leadDB, agent string) ([]repoRef, error) {
	rows, err := q.Query(`SELECT id,display_name FROM repos WHERE NOT EXISTS(SELECT 1 FROM archived_repos WHERE repo_id=repos.id) AND `+repoTopAgentExpr+`=? ORDER BY id`, agent)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []repoRef
	for rows.Next() {
		var r repoRef
		if err := rows.Scan(&r.id, &r.name); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func sessionLeadsNamedRepoQ(q leadDB, name, agent, session string) (bool, error) {
	repos, err := orgPairs(q, `SELECT id,id FROM repos WHERE (id=? OR display_name=? COLLATE NOCASE) AND NOT EXISTS(SELECT 1 FROM archived_repos WHERE repo_id=repos.id)`, name, name)
	if err != nil {
		return false, err
	}
	for repo := range repos {
		top, err := repoTopAgentQ(q, repo)
		if err != nil {
			return false, err
		}
		if top != agent {
			continue
		}
		if same, err := isLeadSessionQ(q, top, agent, session); err != nil || same {
			return same, err
		}
	}
	return false, nil
}

// leadAddressSessionQ resolves a Repo or Team address to its session.
func leadAddressSessionQ(q leadDB, kind, name string, now time.Time) (string, error) {
	query := `SELECT id,` + repoTopAgentExpr + ` FROM repos WHERE (id=? OR display_name=? COLLATE NOCASE) AND NOT EXISTS(SELECT 1 FROM archived_repos WHERE repo_id=repos.id)`
	if kind == "team" {
		query = `SELECT id,COALESCE(owner_agent_id,'') FROM teams WHERE (id=? OR name=? COLLATE NOCASE) AND active=1`
	}
	rows, err := q.Query(query, name, name)
	if err != nil {
		return "", err
	}
	type address struct{ id, lead string }
	var found []address
	for rows.Next() {
		var a address
		if err := rows.Scan(&a.id, &a.lead); err != nil {
			rows.Close()
			return "", err
		}
		found = append(found, a)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return "", err
	}
	switch {
	case len(found) == 0:
		return "", reject("unknown %s %q", kind, name)
	case len(found) > 1:
		return "", reject("ambiguous %s %q; use its ID", kind, name)
	case found[0].lead == "":
		return "", reject("%s %s has no lead", kind, name)
	}
	a, repo := found[0], ""
	if kind == "repo" {
		repo = a.id
	}
	session, err := resolveLeadSession(q, a.lead, repo, now)
	if err == sql.ErrNoRows {
		return "", reject("%s %s lead has no session", kind, name)
	}
	return session, err
}
