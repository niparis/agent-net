package gateway

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"unicode"

	"github.com/niparis/agent-net/internal/protocol"
	"github.com/niparis/agent-net/internal/repoinfo"
)

func validGraphName(name string) bool {
	if name == "" || len(name) > protocol.ProjectMaxFieldLen || strings.TrimSpace(name) != name {
		return false
	}
	for _, r := range name {
		if !unicode.IsPrint(r) || r == '/' || r == '\\' {
			return false
		}
	}
	return name != "." && name != ".."
}

func graphKind(tx *sql.Tx, name string) (string, error) {
	var kind string
	err := tx.QueryRow(`SELECT kind FROM graph_nodes WHERE name=?`, name).Scan(&kind)
	if err == nil {
		return kind, nil
	}
	if err != sql.ErrNoRows {
		return "", err
	}
	var exists int
	err = tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM projects WHERE name=? UNION ALL SELECT 1 FROM sessions WHERE repo_project=?)`, name, name).Scan(&exists)
	if err != nil {
		return "", err
	}
	if exists == 0 {
		return "", reject("unknown node %q", name)
	}
	return "component", nil
}
func graphRecord(tx *sql.Tx, typ, node, attr, old, value, actor string, at int64) error {
	if actor == "" {
		return reject("actor required")
	}
	_, err := tx.Exec(`INSERT INTO graph_history(type,node,attr,old_value,new_value,at,actor) VALUES(?,?,?,?,?,?,?)`, typ, node, attr, old, value, at, actor)
	return err
}

// AddArchitect creates a named architect without a project of the same name.
func (s *Store) AddArchitect(name, actor string) error {
	if !validGraphName(name) {
		return reject("invalid node name")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = graphKind(tx, name); err == nil {
		return reject("node already exists")
	} else if _, ok := err.(Rejection); !ok {
		return err
	}
	now := s.now().UnixNano()
	if _, err = tx.Exec(`INSERT INTO graph_nodes VALUES(?,?,?)`, name, "architect", now); err != nil {
		return err
	}
	if err = graphRecord(tx, "node", name, "", "", "architect", actor, now); err != nil {
		return err
	}
	return tx.Commit()
}

// RemoveArchitect removes an architect with no edges, recording cleared attributes.
func (s *Store) RemoveArchitect(name, actor string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	kind, err := graphKind(tx, name)
	if err != nil {
		return err
	}
	if kind != "architect" {
		return reject("not an architect")
	}
	var n int
	if err = tx.QueryRow(`SELECT COUNT(*) FROM graph_edges WHERE source=? OR target=?`, name, name).Scan(&n); err != nil {
		return err
	}
	if n != 0 {
		return reject("node has edges")
	}
	// Record each removed attribute before deleting it.
	rows, err := tx.Query(`SELECT attr,value FROM graph_charter WHERE node=?`, name)
	if err != nil {
		return err
	}
	var attributes [][2]string
	for rows.Next() {
		var attr, value string
		if err = rows.Scan(&attr, &value); err != nil {
			rows.Close()
			return err
		}
		attributes = append(attributes, [2]string{attr, value})
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, a := range attributes {
		if err = graphRecord(tx, "charter", name, a[0], a[1], "", actor, s.now().UnixNano()); err != nil {
			return err
		}
	}
	if _, err = tx.Exec(`DELETE FROM graph_charter WHERE node=?`, name); err != nil {
		return err
	}
	// Node settings go with the node too.
	rows, err = tx.Query(`SELECT setting,value FROM graph_node_settings WHERE node=?`, name)
	if err != nil {
		return err
	}
	var settings [][2]string
	for rows.Next() {
		var setting, value string
		if err = rows.Scan(&setting, &value); err != nil {
			rows.Close()
			return err
		}
		settings = append(settings, [2]string{setting, value})
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, a := range settings {
		if err = graphRecord(tx, "setting", name, a[0], a[1], "", actor, s.now().UnixNano()); err != nil {
			return err
		}
	}
	if _, err = tx.Exec(`DELETE FROM graph_node_settings WHERE node=?`, name); err != nil {
		return err
	}
	if _, err = tx.Exec(`DELETE FROM graph_nodes WHERE name=?`, name); err != nil {
		return err
	}
	if err = graphRecord(tx, "node", name, "", "architect", "", actor, s.now().UnixNano()); err != nil {
		return err
	}
	return tx.Commit()
}

// SetCharter sets or clears one charter attribute, preserving its history.
func (s *Store) SetCharter(node, attr, value, actor string) error {
	value, err := protocol.CleanCharterValue(attr, value)
	if err != nil {
		return reject("%v", err)
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	kind, err := graphKind(tx, node)
	if err != nil {
		return err
	}
	var old string
	err = tx.QueryRow(`SELECT value FROM graph_charter WHERE node=? AND attr=?`, node, attr).Scan(&old)
	if err != nil && err != sql.ErrNoRows {
		return err
	}
	if old == value {
		return nil
	}
	now := s.now().UnixNano()
	if _, err = tx.Exec(`INSERT OR IGNORE INTO graph_nodes VALUES(?,?,?)`, node, kind, now); err != nil {
		return err
	}
	if value == "" {
		_, err = tx.Exec(`DELETE FROM graph_charter WHERE node=? AND attr=?`, node, attr)
	} else {
		_, err = tx.Exec(`INSERT INTO graph_charter VALUES(?,?,?,?,?) ON CONFLICT(node,attr) DO UPDATE SET value=excluded.value,updated_at=excluded.updated_at,updated_by=excluded.updated_by`, node, attr, value, now, actor)
	}
	if err != nil {
		return err
	}
	if err = graphRecord(tx, "charter", node, attr, old, value, actor, now); err != nil {
		return err
	}
	return tx.Commit()
}

// SetNodeSetting sets or clears one node override of the maturity defaults
// (decision 0022). The value is checked against its setting. An empty value
// clears the override.
func (s *Store) SetNodeSetting(node, setting, value, actor string) error {
	value = strings.ToLower(strings.TrimSpace(value))
	if err := protocol.ValidateNodeSetting(setting, value); err != nil {
		return reject("%v", err)
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = graphKind(tx, node); err != nil {
		return err
	}
	var old string
	err = tx.QueryRow(`SELECT value FROM graph_node_settings WHERE node=? AND setting=?`, node, setting).Scan(&old)
	if err != nil && err != sql.ErrNoRows {
		return err
	}
	if old == value {
		return nil
	}
	now := s.now().UnixNano()
	if value == "" {
		_, err = tx.Exec(`DELETE FROM graph_node_settings WHERE node=? AND setting=?`, node, setting)
	} else {
		_, err = tx.Exec(`INSERT INTO graph_node_settings VALUES(?,?,?,?,?) ON CONFLICT(node,setting) DO UPDATE SET value=excluded.value,updated_at=excluded.updated_at,updated_by=excluded.updated_by`, node, setting, value, now, actor)
	}
	if err != nil {
		return err
	}
	if err = graphRecord(tx, "setting", node, setting, old, value, actor, now); err != nil {
		return err
	}
	return tx.Commit()
}

// NodeSettings returns a node's overrides, its maturity defaults and the
// effective defaults after the overrides. The node must exist in the graph.
func (s *Store) NodeSettings(node string) (protocol.NodeSettingsResult, error) {
	out := protocol.NodeSettingsResult{Node: node, Overrides: map[string]string{}}
	g, err := s.Graph()
	if err != nil {
		return out, err
	}
	n := protocol.Node{}
	found := false
	for _, candidate := range g.Nodes {
		if candidate.Name == node {
			n, found = candidate, true
			break
		}
	}
	if !found {
		return out, reject("unknown node %q", node)
	}
	if n.Settings != nil {
		out.Overrides = n.Settings
	}
	out.Maturity = n.Charter.Maturity
	out.Defaults = protocol.DefaultsForMaturity(n.Charter.Maturity)
	out.Effective = protocol.NodeDefaults(n.Charter.Maturity, n.Settings)
	return out, nil
}

// CharterHistory lists changes newest first, bounded to 1000 entries.
func (s *Store) CharterHistory(node string, limit int) ([]protocol.CharterChange, error) {
	if limit <= 0 || limit > 1000 {
		limit = 1000
	}
	rows, err := s.db.Query(`SELECT id,node,attr,old_value,new_value,at,actor FROM graph_history WHERE type='charter' AND node=? ORDER BY id DESC LIMIT ?`, node, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []protocol.CharterChange{}
	for rows.Next() {
		var h protocol.CharterChange
		if err := rows.Scan(&h.ID, &h.Node, &h.Attr, &h.OldValue, &h.NewValue, &h.At, &h.By); err != nil {
			return nil, err
		}
		out = append(out, h)
	}
	return out, rows.Err()
}

// EdgeHistory lists edge changes touching node (or all edges when node is empty).
func (s *Store) EdgeHistory(node string, limit int) ([]protocol.EdgeChange, error) {
	if limit <= 0 || limit > 1000 {
		limit = 1000
	}
	// Filter after decoding so either endpoint can be selected.
	rows, err := s.db.Query(`SELECT id,old_value,new_value,at,actor FROM graph_history WHERE type='edge' ORDER BY id DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []protocol.EdgeChange{}
	for rows.Next() {
		var h protocol.EdgeChange
		var old, value string
		if err := rows.Scan(&h.ID, &old, &value, &h.At, &h.By); err != nil {
			return nil, err
		}
		if old != "" {
			h.Old = new(protocol.Edge)
			if err := json.Unmarshal([]byte(old), h.Old); err != nil {
				return nil, err
			}
		}
		if value != "" {
			h.New = new(protocol.Edge)
			if err := json.Unmarshal([]byte(value), h.New); err != nil {
				return nil, err
			}
		}
		e := h.New
		if e == nil {
			e = h.Old
		}
		if e == nil {
			return nil, fmt.Errorf("invalid edge history %d", h.ID)
		}
		if node == "" || e.From == node || e.To == node {
			out = append(out, h)
			if len(out) == limit {
				break
			}
		}
	}
	return out, rows.Err()
}

// GraphUndo checks and reverses a history row in one transaction. The reversal
// is itself recorded, so it can be undone. A stale row cannot overwrite changes.
func (s *Store) GraphUndo(id int64, actor string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var typ, node, attr, old, value string
	query := `SELECT type,node,attr,old_value,new_value FROM graph_history WHERE id=?`
	if id == 0 {
		query = `SELECT type,node,attr,old_value,new_value FROM graph_history ORDER BY id DESC LIMIT 1`
		err = tx.QueryRow(query).Scan(&typ, &node, &attr, &old, &value)
	} else {
		err = tx.QueryRow(query, id).Scan(&typ, &node, &attr, &old, &value)
	}
	if err != nil {
		return err
	}
	now := s.now().UnixNano()
	switch typ {
	case "charter":
		var current string
		err = tx.QueryRow(`SELECT value FROM graph_charter WHERE node=? AND attr=?`, node, attr).Scan(&current)
		if err != nil && err != sql.ErrNoRows {
			return err
		}
		if current != value {
			return reject("value changed since that history entry")
		}
		if _, err = graphKind(tx, node); err != nil {
			return err
		}
		if old == "" {
			_, err = tx.Exec(`DELETE FROM graph_charter WHERE node=? AND attr=?`, node, attr)
		} else {
			_, err = tx.Exec(`INSERT INTO graph_charter VALUES(?,?,?,?,?) ON CONFLICT(node,attr) DO UPDATE SET value=excluded.value,updated_at=excluded.updated_at,updated_by=excluded.updated_by`, node, attr, old, now, actor)
		}
	case "setting":
		var current string
		err = tx.QueryRow(`SELECT value FROM graph_node_settings WHERE node=? AND setting=?`, node, attr).Scan(&current)
		if err != nil && err != sql.ErrNoRows {
			return err
		}
		if current != value {
			return reject("value changed since that history entry")
		}
		if _, err = graphKind(tx, node); err != nil {
			return err
		}
		if old == "" {
			_, err = tx.Exec(`DELETE FROM graph_node_settings WHERE node=? AND setting=?`, node, attr)
		} else {
			_, err = tx.Exec(`INSERT INTO graph_node_settings VALUES(?,?,?,?,?) ON CONFLICT(node,setting) DO UPDATE SET value=excluded.value,updated_at=excluded.updated_at,updated_by=excluded.updated_by`, node, attr, old, now, actor)
		}
	case "edge":
		var e protocol.Edge
		data := value
		if data == "" {
			data = old
		}
		if err = json.Unmarshal([]byte(data), &e); err != nil {
			return err
		}
		var current protocol.Edge
		current.From, current.To, current.Kind = e.From, e.To, e.Kind
		err = tx.QueryRow(`SELECT mode,rule FROM graph_edges WHERE source=? AND target=? AND kind=?`, e.From, e.To, e.Kind).Scan(&current.Mode, &current.Rule)
		actual := ""
		if err == nil {
			b, _ := json.Marshal(current)
			actual = string(b)
		} else if err != sql.ErrNoRows {
			return err
		}
		if actual != value {
			return reject("edge changed since that history entry")
		}
		if old == "" {
			_, err = tx.Exec(`DELETE FROM graph_edges WHERE source=? AND target=? AND kind=?`, e.From, e.To, e.Kind)
		} else {
			if err = json.Unmarshal([]byte(old), &e); err != nil {
				return err
			}
			if e.From != "*" {
				if _, err = graphKind(tx, e.From); err != nil {
					return err
				}
			}
			if _, err = graphKind(tx, e.To); err != nil {
				return err
			}
			_, err = tx.Exec(`INSERT INTO graph_edges VALUES(?,?,?,?,?) ON CONFLICT(source,target,kind) DO UPDATE SET mode=excluded.mode,rule=excluded.rule`, e.From, e.To, e.Kind, e.Mode, e.Rule)
		}
	case "node":
		var kind string
		err = tx.QueryRow(`SELECT kind FROM graph_nodes WHERE name=?`, node).Scan(&kind)
		if err != nil && err != sql.ErrNoRows {
			return err
		}
		if kind != value {
			return reject("node changed since that history entry")
		}
		if old == "" {
			var n int
			if err = tx.QueryRow(`SELECT (SELECT COUNT(*) FROM graph_edges WHERE source=? OR target=?) + (SELECT COUNT(*) FROM graph_charter WHERE node=?) + (SELECT COUNT(*) FROM graph_node_settings WHERE node=?)`, node, node, node, node).Scan(&n); err != nil {
				return err
			}
			if n != 0 {
				return reject("node has edges or charter")
			}
			_, err = tx.Exec(`DELETE FROM graph_nodes WHERE name=?`, node)
		} else {
			if _, e := graphKind(tx, node); e == nil {
				return reject("node already exists")
			} else if _, ok := e.(Rejection); !ok {
				return e
			}
			_, err = tx.Exec(`INSERT INTO graph_nodes VALUES(?,?,?)`, node, old, now)
		}
	default:
		return reject("unknown history type %s", typ)
	}
	if err != nil {
		return err
	}
	if err = graphRecord(tx, typ, node, attr, value, old, actor, now); err != nil {
		return err
	}
	return tx.Commit()
}

// SetEdge validates and stores a directed graph edge.
func (s *Store) SetEdge(e protocol.Edge, actor string) error {
	if e.From == e.To {
		return reject("self edge")
	}
	if e.Rule == "" {
		e.Rule = "normal"
	}
	if e.Rule != "normal" && e.Rule != "auto" && e.Rule != "ask" && e.Rule != "never" {
		return reject("invalid edge rule")
	}
	if e.Kind != "depends" && e.Kind != "governs" && e.Kind != "deploys" {
		return reject("invalid edge kind")
	}
	if (e.Kind == "depends" || e.Kind == "deploys") && e.Mode != "" {
		return reject("this edge kind has no mode")
	}
	if e.Kind == "governs" && e.Mode != "advisor" && e.Mode != "arbiter" && e.Mode != "owner" {
		return reject("invalid mode")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	from := "component"
	if e.From != "*" {
		from, err = graphKind(tx, e.From)
		if err != nil {
			return err
		}
	} else if e.Kind != "depends" {
		return reject("wildcard requires dependency")
	}
	to, err := graphKind(tx, e.To)
	if err != nil {
		return err
	}
	if e.Kind == "governs" && (from != "architect" || to != "component") {
		return reject("governs requires architect to component")
	}
	var old protocol.Edge
	old.From = e.From
	old.To = e.To
	old.Kind = e.Kind
	err = tx.QueryRow(`SELECT mode,rule FROM graph_edges WHERE source=? AND target=? AND kind=?`, e.From, e.To, e.Kind).Scan(&old.Mode, &old.Rule)
	previous := ""
	if err == nil {
		b, _ := json.Marshal(old)
		previous = string(b)
	} else if err != sql.ErrNoRows {
		return err
	}
	b, _ := json.Marshal(e)
	if previous == string(b) {
		return nil
	}
	if _, err = tx.Exec(`INSERT INTO graph_edges VALUES(?,?,?,?,?) ON CONFLICT(source,target,kind) DO UPDATE SET mode=excluded.mode,rule=excluded.rule`, e.From, e.To, e.Kind, e.Mode, e.Rule); err != nil {
		return err
	}
	if err = graphRecord(tx, "edge", e.From, e.Kind, previous, string(b), actor, s.now().UnixNano()); err != nil {
		return err
	}
	return tx.Commit()
}

// RemoveEdge deletes an edge and records its previous value.
func (s *Store) RemoveEdge(e protocol.Edge, actor string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	err = tx.QueryRow(`SELECT mode,rule FROM graph_edges WHERE source=? AND target=? AND kind=?`, e.From, e.To, e.Kind).Scan(&e.Mode, &e.Rule)
	if err != nil {
		return err
	}
	b, _ := json.Marshal(e)
	if _, err = tx.Exec(`DELETE FROM graph_edges WHERE source=? AND target=? AND kind=?`, e.From, e.To, e.Kind); err != nil {
		return err
	}
	if err = graphRecord(tx, "edge", e.From, e.Kind, string(b), "", actor, s.now().UnixNano()); err != nil {
		return err
	}
	return tx.Commit()
}

// Graph returns explicit and implicit nodes and all edges.
func (s *Store) Graph() (protocol.Graph, error) {
	g := protocol.Graph{Nodes: []protocol.Node{}, Edges: []protocol.Edge{}}
	rows, err := s.db.Query(`SELECT name,kind,created_at,1,'',2 FROM graph_nodes
 UNION ALL SELECT name,'component',0,0,path,graph_excluded FROM projects WHERE graph_excluded<>1 AND name NOT IN (SELECT name FROM graph_nodes)
 UNION ALL SELECT repo_project,'component',0,0,repo_root,graph_excluded FROM sessions WHERE graph_excluded<>1 AND repo_project<>'' AND repo_project NOT IN (SELECT name FROM graph_nodes) ORDER BY 1`)
	if err != nil {
		return g, err
	}
	seen := map[string]bool{}
	for rows.Next() {
		var n protocol.Node
		var root string
		var explicit, checked int
		if err = rows.Scan(&n.Name, &n.Kind, &n.CreatedAt, &explicit, &root, &checked); err != nil {
			rows.Close()
			return g, err
		}
		if explicit == 0 && (root == "" || (checked == 0 && !graphProjectRoot(root))) {
			continue
		}
		if seen[n.Name] {
			continue
		}
		seen[n.Name] = true
		g.Nodes = append(g.Nodes, n)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return g, err
	}
	rows, err = s.db.Query(`SELECT node,attr,value FROM graph_charter`)
	if err != nil {
		return g, err
	}
	byName := map[string]*protocol.Node{}
	for i := range g.Nodes {
		byName[g.Nodes[i].Name] = &g.Nodes[i]
	}
	for rows.Next() {
		var name, attr, value string
		if err = rows.Scan(&name, &attr, &value); err != nil {
			rows.Close()
			return g, err
		}
		n := byName[name]
		if n == nil {
			rows.Close()
			return g, fmt.Errorf("orphan charter: %s", name)
		}
		switch attr {
		case "purpose":
			n.Charter.Purpose = value
		case "maturity":
			n.Charter.Maturity = value
		case "accepts":
			n.Charter.Accepts = value
		case "refuses":
			n.Charter.Refuses = value
		case "feature_requests":
			n.Charter.FeatureRequests = value
		case "consumers":
			n.Charter.Consumers = value
		case "escalate":
			n.Charter.Escalate = value
		case "needs":
			n.Charter.Needs = value
		case "notes":
			n.Charter.Notes = value
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return g, err
	}
	rows, err = s.db.Query(`SELECT node,setting,value FROM graph_node_settings`)
	if err != nil {
		return g, err
	}
	for rows.Next() {
		var name, setting, value string
		if err = rows.Scan(&name, &setting, &value); err != nil {
			rows.Close()
			return g, err
		}
		if n := byName[name]; n != nil {
			if n.Settings == nil {
				n.Settings = map[string]string{}
			}
			n.Settings[setting] = value
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return g, err
	}
	rows, err = s.db.Query(`SELECT source,target,kind,mode,rule FROM graph_edges ORDER BY source,target,kind`)
	if err != nil {
		return g, err
	}
	defer rows.Close()
	for rows.Next() {
		var e protocol.Edge
		if err = rows.Scan(&e.From, &e.To, &e.Kind, &e.Mode, &e.Rule); err != nil {
			return g, err
		}
		g.Edges = append(g.Edges, e)
	}
	return g, rows.Err()
}

// CTOGraphHistory returns all attributed CTO changes after a history ID.
func (s *Store) CTOGraphHistory(after int64) ([]protocol.CTOGraphChange, error) {
	rows, err := s.db.Query(`SELECT id,type,node,attr,old_value,new_value,at FROM graph_history WHERE actor='cto' AND id>? ORDER BY id`, after)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []protocol.CTOGraphChange{}
	for rows.Next() {
		var v protocol.CTOGraphChange
		if err = rows.Scan(&v.ID, &v.Kind, &v.Node, &v.Attr, &v.OldValue, &v.NewValue, &v.At); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

func (s *Store) CTOLastGraphID() (int64, error) {
	var id int64
	err := s.db.QueryRow(`SELECT COALESCE(MAX(id),0) FROM graph_history WHERE actor='cto'`).Scan(&id)
	return id, err
}

func graphProjectRoot(root string) bool {
	if !repoinfo.GraphProjectRoot(root) {
		return false
	}
	parts := strings.Split(strings.Trim(root, "/"), "/")
	return len(parts) != 2 || (parts[0] != "home" && parts[0] != "Users")
}
