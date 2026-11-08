package gateway

import (
	"database/sql"
	"fmt"
	"strings"

	"github.com/niparis/agent-net/internal/protocol"
)

// GraphLifecycle creates a typed identity or deactivates it without deleting data.
// Validation uses the same transaction as a write, but rolls it back.
func (s *Store) GraphLifecycle(action string, p protocol.GraphLifecycleRequest, actor string, validate bool) error {
	if err := s.requireGraphRefs(); err != nil {
		return err
	}
	if !actorAllowed(actor, "rule") {
		return reject("only the owner may change repo or agent lifecycle")
	}
	kind, verb, ok := strings.Cut(action, ".")
	if !ok || (kind != "repo" && kind != "agent") {
		return reject("invalid lifecycle action")
	}
	if actor == "cto" && (action == "repo.archive" || action == "agent.tombstone") {
		return reject("owner approval required")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	now := s.now().UnixNano()
	var id string
	if verb == "create" {
		if !validGraphName(p.Name) {
			return reject("invalid %s name", kind)
		}
		if kind == "repo" && p.Role != "" {
			return reject("role belongs to an agent")
		}
		if p.Role == "bot" {
			return reject("the role bot belongs to connected apps")
		}
		if len(p.Role) > protocol.CharterValueMaxBytes || strings.IndexFunc(p.Role, func(r rune) bool { return r < 32 || r == 127 }) >= 0 {
			return reject("invalid agent role")
		}
		id = p.Name
		// Old architect charters still use this legacy key. Keep a new Repo's
		// identity separate from that copy when their display names coincide.
		if kind == "repo" {
			var architect bool
			if err = tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM graph_nodes WHERE name=? AND kind='architect')`, id).Scan(&architect); err != nil {
				return err
			}
			if architect {
				base := id + "~repo"
				id = base
				for n := 2; ; n++ {
					var used bool
					if err = tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM repos WHERE id=? UNION ALL SELECT 1 FROM graph_nodes WHERE name=?)`, id, id).Scan(&used); err != nil {
						return err
					}
					if !used {
						break
					}
					id = fmt.Sprintf("%s%d", base, n)
				}
			}
		}
		table := "repos"
		if kind == "agent" {
			table = "agents"
		}
		var exists bool
		if err = tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM `+table+` WHERE id=? OR display_name=? COLLATE NOCASE)`, id, p.Name).Scan(&exists); err != nil {
			return err
		}
		if exists {
			return reject("%s name or identity already exists: %q", kind, p.Name)
		}
		if kind == "repo" {
			_, err = tx.Exec(`INSERT INTO repos(id,display_name,created_at,updated_at) VALUES(?,?,?,?)`, id, p.Name, now, now)
		} else {
			_, err = tx.Exec(`INSERT INTO agents(id,display_name,role,created_at,updated_at) VALUES(?,?,?,?,?)`, id, p.Name, p.Role, now, now)
		}
		if err != nil {
			return err
		}
		err = recordLifecycle(tx, kind, id, "inactive", "active", actor, now)
	} else {
		if (kind == "repo" && verb != "archive") || (kind == "agent" && verb != "tombstone") || p.Ref == nil || p.Ref.Kind != kind {
			return reject("%s requires a typed %s ref", verb, kind)
		}
		if err = p.Ref.Validate(); err != nil {
			return err
		}
		id = p.Ref.ID
		err = setLifecycleTx(tx, kind, id, "active", "inactive", actor, now)
	}
	if err != nil {
		return err
	}
	if validate {
		return nil
	}
	return tx.Commit()
}

func recordLifecycle(tx *sql.Tx, kind, id, old, value, actor string, now int64) error {
	_, err := tx.Exec(`INSERT INTO graph_history(type,node,attr,old_value,new_value,at,actor,target_kind) VALUES('lifecycle',?,'state',?,?,?,?,?)`, id, old, value, now, actor, kind)
	return err
}

// setLifecycleTx also powers undo, including undo of a create. The old state
// must still match, and deactivation always checks today's references.
func setLifecycleTx(tx *sql.Tx, kind, id, expected, next, actor string, now int64) error {
	if !actorAllowed(actor, "rule") {
		return reject("only the owner may change repo or agent lifecycle")
	}
	var inactive bool
	var err error
	switch kind {
	case "repo":
		err = tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM archived_repos WHERE repo_id=repos.id) FROM repos WHERE id=?`, id).Scan(&inactive)
	case "agent":
		err = tx.QueryRow(`SELECT tombstone FROM agents WHERE id=?`, id).Scan(&inactive)
	default:
		return reject("invalid lifecycle kind")
	}
	if err == sql.ErrNoRows {
		return reject("unknown %s %q", kind, id)
	}
	if err != nil {
		return err
	}
	current := "active"
	if inactive {
		current = "inactive"
	}
	if current != expected || (next != "active" && next != "inactive") || next == current {
		return reject("%s state changed since that history entry", kind)
	}
	if kind == "agent" && next == "inactive" {
		var bot bool
		if err = tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM agents WHERE id=? AND role='bot')`, id).Scan(&bot); err != nil {
			return err
		}
		if bot {
			return reject("a bot node is archived when its app is revoked")
		}
	}
	if next == "inactive" {
		refs, err := lifecycleReferences(tx, kind, id)
		if err != nil {
			return err
		}
		if len(refs) > 0 {
			return reject("cannot deactivate %s %q; still referenced by: %s", kind, id, strings.Join(refs, "; "))
		}
	}
	if kind == "repo" {
		if next == "inactive" {
			_, err = tx.Exec(`INSERT INTO archived_repos(repo_id) VALUES(?)`, id)
		} else {
			_, err = tx.Exec(`DELETE FROM archived_repos WHERE repo_id=?`, id)
		}
	} else {
		_, err = tx.Exec(`UPDATE agents SET tombstone=?,updated_at=? WHERE id=?`, next == "inactive", now, id)
	}
	if err != nil {
		return err
	}
	return recordLifecycle(tx, kind, id, current, next, actor, now)
}

// Only active references block deactivation. Charters, settings and historical
// sessions/runs/changes stay attached, as does an archived repo's owner.
func lifecycleReferences(tx *sql.Tx, kind, id string) ([]string, error) {
	type query struct {
		label, sql string
		args       []any
	}
	queries := []query{
		{"edge", `SELECT source||' -> '||target||' ('||kind||')' FROM graph_edges WHERE (source_kind=? AND source=?) OR (target_kind=? AND target=?) ORDER BY source,target,kind`, []any{kind, id, kind, id}},
	}
	if kind == "repo" {
		queries = append(queries,
			query{"session", `SELECT id FROM sessions WHERE repo_id=? AND online=1 ORDER BY id`, []any{id}},
			query{"checkout", `SELECT node_id FROM projects WHERE name=? ORDER BY node_id,path`, []any{id}},
			query{"delivery change", `SELECT id FROM delivery_changes WHERE project=? AND state IN ('open','merged') ORDER BY id`, []any{id}},
			query{"merger", `SELECT node_id||':'||session_id FROM project_mergers WHERE project=? ORDER BY node_id`, []any{id}},
			query{"worker start", `SELECT ref FROM worker_starts WHERE project=? AND state='open' ORDER BY ref`, []any{id}},
		)
	} else {
		if id == "cto" {
			return []string{"the CTO identity"}, nil
		}
		queries = append(queries,
			query{"repo owner", `SELECT id FROM repos WHERE owner_agent_id=? ORDER BY id`, []any{id}},
			query{"repo lead", `SELECT repo_id FROM repo_leads WHERE agent_id=? ORDER BY repo_id`, []any{id}},
			query{"session", `SELECT id FROM sessions WHERE agent_id=? AND online=1 ORDER BY id`, []any{id}},
			query{"delivery task", `SELECT change_id||':'||step FROM delivery_step_tasks WHERE owner_node=? AND completed=0 ORDER BY change_id,step`, []any{id}},
			query{"worker start", `SELECT ref FROM worker_starts WHERE caller_agent_id=? AND state='open' ORDER BY ref`, []any{id}},
		)
	}
	var refs []string
	for _, q := range queries {
		rows, err := tx.Query(q.sql, q.args...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var v string
			if err = rows.Scan(&v); err != nil {
				rows.Close()
				return nil, err
			}
			refs = append(refs, fmt.Sprintf("%s %s", q.label, v))
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, err
		}
	}
	return refs, nil
}

// Legacy edge writes and edge undo must not reattach an inactive identity.
func liveEdgeEndpoints(tx *sql.Tx, e protocol.Edge) error {
	from, to := e.EndpointKinds()
	for _, r := range []struct{ kind, id string }{{from, e.From}, {to, e.To}} {
		if r.kind == "special" || r.id == "*" {
			continue
		}
		if r.kind == "agent" && r.id == protocol.CTONodeName {
			r.id = "cto"
		}
		table, live := "repos", " AND NOT EXISTS(SELECT 1 FROM archived_repos WHERE repo_id=repos.id)"
		if r.kind == "agent" {
			table, live = "agents", " AND tombstone=0"
		}
		var found bool
		if err := tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM `+table+` WHERE id=?`+live+`)`, r.id).Scan(&found); err != nil {
			return err
		}
		if !found {
			return reject("unknown or inactive %s endpoint %q", r.kind, r.id)
		}
	}
	return nil
}
