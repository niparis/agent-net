package gateway

import (
	"database/sql"
	"encoding/json"
	"errors"
	"strings"

	"github.com/niparis/agent-net/internal/protocol"
	"github.com/niparis/agent-net/internal/settings"
)

// Scopes are authority the owner delegates to a role node (an Agent). They sit
// next to agent_charter rather than in it: a charter attribute holds one value,
// while a node holds many scopes of one kind over different targets. Stored,
// not used yet: nothing reads or enforces them.
//
// nodeScopesMigration creates the tables and is replayable. The migration loop
// runs seedNodeScopesTx after it; the owner_settings marker keeps that seed to
// one run, so a scope the owner removed is never put back.
const nodeScopesMigration = `
-- Node scopes: delegated authority stored on role nodes (stored, not used yet).
CREATE TABLE IF NOT EXISTS node_scopes (
 node TEXT NOT NULL REFERENCES agents(id), kind TEXT NOT NULL, over_ref TEXT NOT NULL,
 note TEXT NOT NULL DEFAULT '', set_by TEXT NOT NULL, set_at INTEGER NOT NULL,
 PRIMARY KEY(node, kind, over_ref));
CREATE TABLE IF NOT EXISTS node_scopes_history (
 id INTEGER PRIMARY KEY AUTOINCREMENT, node TEXT NOT NULL, kind TEXT NOT NULL, over_ref TEXT NOT NULL,
 old_value TEXT NOT NULL, new_value TEXT NOT NULL, at INTEGER NOT NULL, actor TEXT NOT NULL);
CREATE INDEX IF NOT EXISTS node_scopes_history_node ON node_scopes_history(node, id);
SELECT 1 /* node scopes seed */;
`

const nodeScopesSeededKey = "node_scopes_seeded"

// SetScope adds a scope or changes its note, and returns the stored row.
func (s *Store) SetScope(q protocol.ScopeSetRequest, actor string, validate bool) (protocol.Scope, error) {
	note := strings.TrimSpace(q.Note)
	return s.writeScope(q, &note, actor, validate)
}

// RemoveScope removes a scope and returns the row it removed.
func (s *Store) RemoveScope(q protocol.ScopeSetRequest, actor string, validate bool) (protocol.Scope, error) {
	return s.writeScope(q, nil, actor, validate)
}

func (s *Store) writeScope(q protocol.ScopeSetRequest, note *string, actor string, validate bool) (protocol.Scope, error) {
	if err := s.requireGraphRefs(); err != nil {
		return protocol.Scope{}, err
	}
	if !actorAllowed(actor, "rule") {
		return protocol.Scope{}, reject("only the owner may change scopes")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return protocol.Scope{}, err
	}
	defer tx.Rollback()
	if err := validateScopeTx(tx, q.Node, q.Kind, q.Over); err != nil {
		return protocol.Scope{}, err
	}
	old, err := scopeRowTx(tx, q.Node, q.Kind, q.Over)
	if err != nil {
		return protocol.Scope{}, err
	}
	var want *protocol.Scope
	if note != nil {
		want = &protocol.Scope{Node: q.Node, Kind: q.Kind, Over: q.Over, Note: *note}
	} else if old == nil {
		return protocol.Scope{}, reject("%s has no %s scope over %s", q.Node, q.Kind, q.Over)
	}
	row, err := writeScopeTx(tx, old, want, actor, s.now().UnixNano())
	if err != nil || validate {
		return row, err
	}
	return row, tx.Commit()
}

// validateScopeTx checks the node, the kind and the over target. It is the
// only refusal scopes have.
func validateScopeTx(tx *sql.Tx, node, kind, over string) error {
	if !protocol.ValidScopeKind(kind) {
		return reject("unknown scope kind %q; kinds are %s", kind, strings.Join(protocol.ScopeKinds, ", "))
	}
	var ok bool
	if err := tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM agents WHERE id=? AND tombstone=0)`, node).Scan(&ok); err != nil {
		return err
	}
	if !ok {
		return reject("unknown agent %q", node)
	}
	target, id, err := protocol.ParseScopeOver(over)
	if err != nil {
		return reject("%v", err)
	}
	switch target {
	case "team":
		err = tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM teams WHERE id=? AND active=1)`, id).Scan(&ok)
	case "repo":
		err = tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM repos WHERE id=? AND NOT EXISTS(SELECT 1 FROM archived_repos WHERE repo_id=repos.id))`, id).Scan(&ok)
	case "agent":
		err = tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM agents WHERE id=? AND tombstone=0)`, id).Scan(&ok)
	}
	if err != nil {
		return err
	}
	if !ok {
		return reject("unknown %s %q in over", target, id)
	}
	return nil
}

func scopeRowTx(tx *sql.Tx, node, kind, over string) (*protocol.Scope, error) {
	row := protocol.Scope{Node: node, Kind: kind, Over: over}
	err := tx.QueryRow(`SELECT note,set_by,set_at FROM node_scopes WHERE node=? AND kind=? AND over_ref=?`, node, kind, over).Scan(&row.Note, &row.SetBy, &row.SetAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &row, nil
}

// scopeValue is the history form of a scope: its JSON without the audit
// fields, or "" when there is none.
func scopeValue(row *protocol.Scope) string {
	if row == nil {
		return ""
	}
	b, _ := json.Marshal(protocol.Scope{Node: row.Node, Kind: row.Kind, Over: row.Over, Note: row.Note})
	return string(b)
}

// writeScopeTx moves a scope from old to want (nil removes it) and records the
// change in node_scopes_history and graph_history, so graph_undo reverses it.
// It returns the stored row, or the removed one.
func writeScopeTx(tx *sql.Tx, old, want *protocol.Scope, actor string, now int64) (protocol.Scope, error) {
	ref := want
	if ref == nil {
		ref = old
	}
	oldValue := scopeValue(old)
	if want == nil {
		if _, err := tx.Exec(`DELETE FROM node_scopes WHERE node=? AND kind=? AND over_ref=?`, ref.Node, ref.Kind, ref.Over); err != nil {
			return protocol.Scope{}, err
		}
	} else {
		if old != nil && old.Note == want.Note {
			return *old, nil
		}
		want.SetBy, want.SetAt = actor, now
		if _, err := tx.Exec(`INSERT INTO node_scopes(node,kind,over_ref,note,set_by,set_at) VALUES(?,?,?,?,?,?)
 ON CONFLICT(node,kind,over_ref) DO UPDATE SET note=excluded.note,set_by=excluded.set_by,set_at=excluded.set_at`,
			want.Node, want.Kind, want.Over, want.Note, actor, now); err != nil {
			return protocol.Scope{}, err
		}
	}
	newValue := scopeValue(want)
	if _, err := tx.Exec(`INSERT INTO node_scopes_history(node,kind,over_ref,old_value,new_value,at,actor) VALUES(?,?,?,?,?,?,?)`,
		ref.Node, ref.Kind, ref.Over, oldValue, newValue, now, actor); err != nil {
		return protocol.Scope{}, err
	}
	if err := graphRecord(tx, "scope", ref.Node, ref.Kind+" "+ref.Over, oldValue, newValue, actor, now); err != nil {
		return protocol.Scope{}, err
	}
	return *ref, nil
}

// undoScopeTx restores the scope a graph_history row changed. It refuses when
// the scope changed since that row.
func undoScopeTx(tx *sql.Tx, node, attr, old, value, actor string, now int64) error {
	if !actorAllowed(actor, "rule") {
		return reject("only the owner may change scopes")
	}
	kind, over, ok := strings.Cut(attr, " ")
	if !ok {
		return reject("invalid scope history")
	}
	current, err := scopeRowTx(tx, node, kind, over)
	if err != nil {
		return err
	}
	if scopeValue(current) != value {
		return reject("scope changed since that history entry")
	}
	var want *protocol.Scope
	if old != "" {
		want = new(protocol.Scope)
		if err := json.Unmarshal([]byte(old), want); err != nil {
			return err
		}
		if err := validateScopeTx(tx, node, kind, over); err != nil {
			return err
		}
	}
	_, err = writeScopeTx(tx, current, want, actor, now)
	return err
}

// NodeScopes returns the scopes of one node.
func (s *Store) NodeScopes(node string) ([]protocol.Scope, error) {
	return s.scopes(`WHERE node=?`, node)
}

// AllScopes returns the scopes of every node.
func (s *Store) AllScopes() ([]protocol.Scope, error) {
	return s.scopes(``)
}

func (s *Store) scopes(where string, args ...any) ([]protocol.Scope, error) {
	rows, err := s.db.Query(`SELECT node,kind,over_ref,note,set_by,set_at FROM node_scopes `+where+` ORDER BY node,kind,over_ref`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []protocol.Scope{}
	for rows.Next() {
		var row protocol.Scope
		if err := rows.Scan(&row.Node, &row.Kind, &row.Over, &row.Note, &row.SetBy, &row.SetAt); err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

// wbgpocMergeRule is the owner rule that gives the wbgpoc team lead merge
// authority on its team's repos.
const wbgpocMergeRule = "so_f960d5fd38dcd366"

// seedNodeScopesTx turns today's grants into scopes, once. A grant whose node
// or target does not exist is skipped; a scope already stored is kept.
func seedNodeScopesTx(tx *sql.Tx, now int64) error {
	var done bool
	if err := tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM owner_settings WHERE key=?)`, nodeScopesSeededKey).Scan(&done); err != nil || done {
		return err
	}
	var seeds []protocol.Scope
	add := func(node, kind, over, note string) {
		if node != "" {
			seeds = append(seeds, protocol.Scope{Node: node, Kind: kind, Over: over, Note: note})
		}
	}
	column := func(query string, args ...any) ([]string, error) {
		rows, err := tx.Query(query, args...)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		var out []string
		for rows.Next() {
			var v string
			if err := rows.Scan(&v); err != nil {
				return nil, err
			}
			out = append(out, v)
		}
		return out, rows.Err()
	}

	chiefs, err := column(`SELECT s.agent_id FROM owner_settings o JOIN sessions s ON s.id=o.value WHERE o.key=? AND COALESCE(s.agent_id,'')<>''`, chiefSessionKey)
	if err != nil {
		return err
	}
	for _, a := range chiefs {
		add(a, protocol.ScopeAsks, protocol.ScopeOverAll, "sort, route, chase, close; never approve or execute")
	}
	ctos, err := column(`SELECT id FROM agents WHERE role='cto' AND tombstone=0 ORDER BY id`)
	if err != nil {
		return err
	}
	for _, a := range ctos {
		add(a, protocol.ScopeAdmin, protocol.ScopeOverAll, "")
		add(a, protocol.ScopePermissions, protocol.ScopeOverAll, "on owner words")
	}
	leads, err := column(`SELECT COALESCE(t.owner_agent_id,'') FROM teams t WHERE t.id='wbgpoc' AND t.active=1 AND EXISTS(SELECT 1 FROM standing_orders WHERE id=?)`, wbgpocMergeRule)
	if err != nil {
		return err
	}
	for _, a := range leads {
		add(a, protocol.ScopeMerge, "team:wbgpoc", "from owner rule "+wbgpocMergeRule)
	}

	type pair struct{ project, agent string }
	rows, err := tx.Query(`SELECT DISTINCT pm.project, s.agent_id FROM project_mergers pm JOIN sessions s ON s.id=pm.session_id WHERE COALESCE(s.agent_id,'')<>'' ORDER BY pm.project, s.agent_id`)
	if err != nil {
		return err
	}
	var mergers []pair
	for rows.Next() {
		var p pair
		if err := rows.Scan(&p.project, &p.agent); err != nil {
			rows.Close()
			return err
		}
		mergers = append(mergers, p)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	mergerAgents := map[string][]string{}
	for _, p := range mergers {
		add(p.agent, protocol.ScopeMerge, "repo:"+p.project, "holds the merger of "+p.project)
		mergerAgents[p.project] = append(mergerAgents[p.project], p.agent)
	}

	// Delivery edges name a step owner: an Agent, @merger (the agent that
	// holds the project's merger) or @caller (the repo's lead, who starts the
	// work). Other owners such as @github or @person are not role nodes.
	holders := func(source, project string) ([]string, error) {
		switch {
		case source == "/delivery/merger":
			return mergerAgents[project], nil
		case source == "/delivery/caller":
			return column(`SELECT COALESCE((SELECT agent_id FROM repo_leads WHERE repo_id=repos.id), owner_agent_id, '') FROM repos WHERE id=?`, project)
		case strings.HasPrefix(source, "/"):
			return nil, nil
		}
		return []string{source}, nil
	}
	rows, err = tx.Query(`SELECT source,target,kind FROM graph_edges WHERE kind IN ('releases','accepts','reviews','deploys','delivery_deploys') ORDER BY kind,source,target`)
	if err != nil {
		return err
	}
	var edges []protocol.Edge
	for rows.Next() {
		var e protocol.Edge
		if err := rows.Scan(&e.From, &e.To, &e.Kind); err != nil {
			rows.Close()
			return err
		}
		edges = append(edges, e)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	snap, err := settingsSnapshotTx(tx)
	if err != nil {
		return err
	}
	for _, e := range edges {
		agents, err := holders(e.From, e.To)
		if err != nil {
			return err
		}
		kind, note := "", ""
		switch e.Kind {
		case "releases":
			kind, note = protocol.ScopeRelease, "from delivery owner "+protocol.DeliveryOwnerSetting(e.From)
		case "accepts", "reviews":
			kind, note = protocol.ScopeReview, "from "+e.Kind+" edge, owner "+protocol.DeliveryOwnerSetting(e.From)
		default:
			rule := snap.String("delivery.deploy_rule", settings.Where{Repo: e.To})
			if rule == protocol.DeployOwner || rule == protocol.ShorthandCustom {
				continue // the owner keeps this deploy
			}
			kind, note = protocol.ScopeDeploy, "from deploys edge; deploy_rule "+rule
		}
		for _, a := range agents {
			add(a, kind, "repo:"+e.To, note)
		}
	}

	for i := range seeds {
		seed := seeds[i]
		if validateScopeTx(tx, seed.Node, seed.Kind, seed.Over) != nil {
			continue
		}
		old, err := scopeRowTx(tx, seed.Node, seed.Kind, seed.Over)
		if err != nil {
			return err
		}
		if old != nil {
			continue
		}
		if _, err := writeScopeTx(tx, nil, &seed, "migration", now); err != nil {
			return err
		}
	}
	_, err = tx.Exec(`INSERT INTO owner_settings(key,value,updated_at) VALUES(?,'1',?)`, nodeScopesSeededKey, now)
	return err
}
