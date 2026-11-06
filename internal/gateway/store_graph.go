package gateway

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"unicode"

	"github.com/niparis/agent-net/internal/protocol"
	"github.com/niparis/agent-net/internal/repoinfo"
	"github.com/niparis/agent-net/internal/settings"
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
	if name == protocol.CTONodeName {
		return "cto", nil
	}
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
	if actor == "CTO (minor)" {
		class, err := ctoGraphClassTx(tx, typ, node, attr, old, value)
		if err != nil {
			return err
		}
		if class != "minor" {
			return reject("CTO graph change now requires the owner")
		}
	}
	if actor == "" {
		return reject("actor required")
	}
	targetKind := ""
	if typ == "agent_charter" {
		targetKind = "agent"
	} else if typ == "charter" {
		targetKind = "repo"
		var architect bool
		if err := tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM graph_nodes WHERE name=? AND kind='architect')`, node).Scan(&architect); err != nil {
			return err
		}
		if architect {
			targetKind = "agent"
		}
	}
	_, err := tx.Exec(`INSERT INTO graph_history(type,node,attr,old_value,new_value,at,actor,target_kind) VALUES(?,?,?,?,?,?,?,?)`, typ, node, attr, old, value, at, actor, targetKind)
	return err
}

// AddArchitect creates a named architect without a project of the same name.
func (s *Store) AddArchitect(name, actor string, validate ...bool) error {
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
	if len(validate) > 0 && validate[0] {
		return nil
	}
	return tx.Commit()
}

// RemoveArchitect removes an architect with no edges, recording cleared attributes.
func (s *Store) RemoveArchitect(name, actor string, validate ...bool) error {
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
	// Legacy node settings are kept as a downgrade backup.
	settingsChanged, err := s.removeRepoSettingsTx(tx, name, actor)
	if err != nil {
		return err
	}
	if _, err = tx.Exec(`DELETE FROM graph_nodes WHERE name=?`, name); err != nil {
		return err
	}
	if err = graphRecord(tx, "node", name, "", "architect", "", actor, s.now().UnixNano()); err != nil {
		return err
	}
	maturityChanged := false
	for _, a := range attributes {
		if a[0] == "maturity" {
			maturityChanged = true
			if !settingsChanged {
				if err = bumpSettingsRevision(tx); err != nil {
					return err
				}
			}
			break
		}
	}
	if len(validate) > 0 && validate[0] {
		return nil
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	if maturityChanged || settingsChanged {
		s.settingsNotify()
	}
	return nil
}

// SetCharter sets or clears one charter attribute, preserving its history.
func (s *Store) SetCharter(node, attr, value, actor string) error {
	if on, err := s.graphRefsEnabled(); err != nil {
		return err
	} else if on {
		var architect bool
		if err = s.db.QueryRow(`SELECT EXISTS(SELECT 1 FROM graph_nodes WHERE name=? AND kind='architect')`, node).Scan(&architect); err != nil {
			return err
		}
		if architect {
			return s.SetAgentCharter(node, attr, value, actor)
		}
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var before int64
	if attr == "maturity" {
		if err = tx.QueryRow(`SELECT revision FROM setting_revision WHERE id=1`).Scan(&before); err != nil {
			return err
		}
	}
	if err = setCharterTx(tx, node, attr, value, actor, s.now().UnixNano()); err != nil {
		return err
	}
	var after int64
	if attr == "maturity" {
		if err = tx.QueryRow(`SELECT revision FROM setting_revision WHERE id=1`).Scan(&after); err != nil {
			return err
		}
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	if attr == "maturity" && after != before {
		s.refreshDeliveryTasks()
		s.settingsNotify()
	}
	return nil
}

func setCharterTx(tx *sql.Tx, node, attr, value, actor string, now int64) error {
	value, err := protocol.CleanCharterValue(attr, value)
	if err != nil {
		return reject("%v", err)
	}
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
	if err := graphRecord(tx, "charter", node, attr, old, value, actor, now); err != nil {
		return err
	}
	if attr == "maturity" {
		return bumpSettingsRevision(tx)
	}
	return nil
}

// SetNodeSetting sets or clears one node override of the maturity defaults
// (decision 0022). The value is checked against its setting. An empty value
// clears the override.
func (s *Store) SetNodeSetting(node, setting, value, actor string, validate ...bool) error {
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
	if key, moved := deliveryNodeKeys[setting]; moved {
		storedKey := key
		if canonical := settings.DeliveryCanonicalKey(key); canonical != "" {
			storedKey = canonical
		}
		var prior sql.NullString
		err = tx.QueryRow(`SELECT value FROM setting_values WHERE key=? AND level='repo' AND scope=? AND item=''`, storedKey, node).Scan(&prior)
		if err != nil && err != sql.ErrNoRows {
			return err
		}
		change := SettingsChange{Key: key, Level: "repo", Scope: node}
		if prior.Valid {
			change.Expect = json.RawMessage(prior.String)
			if storedKey != key {
				if shorthand, err := settings.DeliveryShorthandValue(key, change.Expect); err == nil {
					change.Expect = shorthand
				}
			}
		}
		if value == "" {
			change.Delete = true
		} else {
			change.Value, _ = json.Marshal(value)
		}
		result, err := s.writeSettingsTx(tx, []SettingsChange{change}, actor, "node settings adapter", "write", nil)
		if err != nil {
			return err
		}
		if len(validate) > 0 && validate[0] {
			return nil
		}
		if err = tx.Commit(); err != nil {
			return err
		}
		if result.changed {
			s.deliverySettingsChanged([]SettingsChange{change})
			s.settingsNotify()
		}
		return nil
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
	if len(validate) > 0 && validate[0] {
		return nil
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
	explicit, err := s.nodeExplicitSettings(node)
	if err != nil {
		return out, err
	}
	out.Overrides = explicit
	out.Maturity = n.Charter.Maturity
	out.Defaults = protocol.DefaultsForMaturity(n.Charter.Maturity)
	out.Effective = protocol.NodeDefaults(n.Charter.Maturity, n.Settings)
	out.Autonomy = protocol.NodeAutonomy(n.Settings)
	return out, nil
}

// CharterHistory lists changes newest first, bounded to 1000 entries.
func (s *Store) CharterHistory(node string, limit int) ([]protocol.CharterChange, error) {
	if on, err := s.graphRefsEnabled(); err != nil {
		return nil, err
	} else if on {
		var architect bool
		if err = s.db.QueryRow(`SELECT EXISTS(SELECT 1 FROM graph_nodes WHERE name=? AND kind='architect')`, node).Scan(&architect); err != nil {
			return nil, err
		}
		if architect {
			return s.RefCharterHistory(protocol.GraphRef{Kind: "agent", ID: node}, limit)
		}
	}
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
	return s.edgeHistory(node, nil, limit)
}
func (s *Store) edgeHistory(node string, ref *protocol.GraphRef, limit int) ([]protocol.EdgeChange, error) {
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
		match := node == "" || e.From == node || e.To == node
		if ref != nil {
			match = false
			for _, edge := range []*protocol.Edge{h.Old, h.New} {
				if edge == nil {
					continue
				}
				typedHistoryEdge(edge)
				match = match || protocol.RefMatches(edge.FromRef, ref) || protocol.RefMatches(edge.ToRef, ref)
			}
		}
		if match {
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
func (s *Store) GraphUndo(id int64, actor string, validate ...bool) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var typ, node, attr, old, value, targetKind string
	query := `SELECT id,type,node,attr,old_value,new_value,target_kind FROM graph_history WHERE id=?`
	if id == 0 {
		query = `SELECT id,type,node,attr,old_value,new_value,target_kind FROM graph_history ORDER BY id DESC LIMIT 1`
		err = tx.QueryRow(query).Scan(&id, &typ, &node, &attr, &old, &value, &targetKind)
	} else {
		err = tx.QueryRow(query, id).Scan(&id, &typ, &node, &attr, &old, &value, &targetKind)
	}
	if err != nil {
		return err
	}

	if typ == "charter" && targetKind == "agent" {
		typ = "agent_charter"
	}
	now := s.now().UnixNano()
	switch typ {
	case "agent_charter":
		var current string
		err = tx.QueryRow(`SELECT value FROM agent_charter WHERE agent_id=? AND attr=?`, node, attr).Scan(&current)
		if err != nil && err != sql.ErrNoRows {
			return err
		}
		if current != value {
			return reject("value changed since that history entry")
		}
		if err = setAgentCharterTx(tx, node, attr, old, actor, now); err != nil {
			return err
		}
		if len(validate) > 0 && validate[0] {
			return nil
		}
		return tx.Commit()
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
		if _, moved := deliveryNodeKeys[attr]; moved {
			return reject("%s moved to settings; use settings history to undo", attr)
		}
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
		if deliveryEdgeStep(e.Kind) != "" && !actorAllowed(actor, "rule") {
			return reject("only the owner may change delivery step owners")
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
			if deliveryEdgeStep(e.Kind) != "" && e.Kind != "deploys" {
				var count int
				if err = tx.QueryRow(`SELECT COUNT(*) FROM graph_edges WHERE target=? AND kind=? AND source<>?`, e.To, e.Kind, e.From).Scan(&count); err != nil {
					return err
				}
				if count != 0 {
					return reject("%s already has an owner for %s", e.To, deliveryEdgeStep(e.Kind))
				}
			}
			owner := protocol.DeliveryOwnerFromSource(e.From)
			if owner.Kind != protocol.OwnerNode {
				source, ownerErr := protocol.DeliveryOwnerSource("@"+owner.Kind, deliveryEdgeStep(e.Kind))
				if ownerErr != nil || source != e.From {
					return reject("invalid special delivery owner")
				}
			} else if e.From != "*" {
				if _, err = graphKind(tx, e.From); err != nil {
					return err
				}
			}
			if _, err = graphKind(tx, e.To); err != nil {
				return err
			}
			_, err = tx.Exec(`INSERT INTO graph_edges(source,target,kind,mode,rule) VALUES(?,?,?,?,?) ON CONFLICT(source,target,kind) DO UPDATE SET mode=excluded.mode,rule=excluded.rule`, e.From, e.To, e.Kind, e.Mode, e.Rule)
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
			if err = tx.QueryRow(`SELECT (SELECT COUNT(*) FROM graph_edges WHERE source=? OR target=?) + (SELECT COUNT(*) FROM graph_charter WHERE node=?) + (SELECT COUNT(*) FROM setting_values WHERE level='repo' AND scope=?)`, node, node, node, node).Scan(&n); err != nil {
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
	if _, err = tx.Exec(`UPDATE learned_suggestions SET status='undone',decided_at=? WHERE status='applied' AND id IN (SELECT suggestion_id FROM learned_suggestion_details WHERE history_id=?)`, now, id); err != nil {
		return err
	}
	maturityChanged := typ == "charter" && attr == "maturity" && old != value
	ownerChanged := typ == "edge" && deliveryEdgeStep(attr) != ""
	if maturityChanged || ownerChanged {
		if err = bumpSettingsRevision(tx); err != nil {
			return err
		}
	}
	if len(validate) > 0 && validate[0] {
		return nil
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	if maturityChanged || ownerChanged {
		s.refreshDeliveryTasks()
		s.settingsNotify()
	}
	return nil
}

// SetEdge validates and stores a directed graph edge.
func (s *Store) SetEdge(e protocol.Edge, actor string, validate ...bool) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = setEdgeTx(tx, e, actor, s.now().UnixNano()); err != nil {
		return err
	}
	if len(validate) > 0 && validate[0] {
		return nil
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	if deliveryEdgeStep(e.Kind) != "" {
		s.refreshDeliveryTasks()
		s.settingsNotify()
	}
	return nil
}

// SetDeliveryOwner replaces a step's assignment in one transaction. The
// expected owner prevents an old Settings screen from overwriting a change.
func (s *Store) SetDeliveryOwner(q protocol.DeliveryOwnerSetRequest, actor string) error {
	if !actorAllowed(actor, "rule") {
		return reject("only the owner may change delivery step owners")
	}
	kind := protocol.DeliveryRefEdge(q.Step, q.For, q.AfterMerge)
	if kind == "" || q.Project == "" {
		return reject("invalid project or delivery step")
	}
	node, err := protocol.DeliveryOwnerSource(q.Node, q.Step)
	if err != nil {
		return reject("%v", err)
	}
	expect, err := protocol.DeliveryOwnerSource(q.Expect, q.Step)
	if err != nil {
		return reject("%v", err)
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	projectKind, err := graphKind(tx, q.Project)
	if err != nil {
		return err
	}
	if projectKind != "component" {
		return reject("delivery owners are assigned to a project")
	}
	rows, err := tx.Query(`SELECT source,mode,rule FROM graph_edges WHERE target=? AND kind=? ORDER BY source`, q.Project, kind)
	if err != nil {
		return err
	}
	var existing []protocol.Edge
	for rows.Next() {
		e := protocol.Edge{To: q.Project, Kind: kind}
		if err := rows.Scan(&e.From, &e.Mode, &e.Rule); err != nil {
			rows.Close()
			return err
		}
		existing = append(existing, e)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	if len(existing) > 1 {
		return reject("%s has more than one %s edge; resolve the edges first", q.Project, kind)
	}
	current := ""
	if len(existing) == 1 {
		current = existing[0].From
	}
	if current != expect {
		return reject("%s owner changed; reload Settings", q.Step)
	}
	if current == node {
		return nil
	}
	if current != "" {
		if _, err := tx.Exec(`DELETE FROM graph_edges WHERE source=? AND target=? AND kind=?`, current, q.Project, kind); err != nil {
			return err
		}
		old, _ := json.Marshal(existing[0])
		if err := graphRecord(tx, "edge", current, kind, string(old), "", actor, s.now().UnixNano()); err != nil {
			return err
		}
	}
	if node != "" {
		if err := setEdgeTx(tx, protocol.Edge{From: node, To: q.Project, Kind: kind}, actor, s.now().UnixNano()); err != nil {
			return err
		}
	} else if err := bumpSettingsRevision(tx); err != nil {
		return err
	}
	_, err = tx.Exec(`UPDATE messages SET state='expired' WHERE state IN ('stored','dispatched') AND id IN
        (SELECT m.message_id FROM delivery_step_messages m JOIN delivery_step_tasks t ON t.change_id=m.change_id AND t.revision=m.revision AND t.step=m.step AND t.for_step=m.for_step AND t.after_merge=m.after_merge
        JOIN delivery_changes c ON c.id=t.change_id WHERE c.project=? AND t.step=? AND t.for_step=? AND t.after_merge=? AND t.claimed_by='' AND t.completed=0)`, q.Project, q.Step, q.For, boolInt(q.AfterMerge))
	if err != nil {
		return err
	}
	owner := protocol.DeliveryOwnerFromSource(node)
	if owner.Kind == protocol.OwnerNode && node != "" {
		_, err = tx.Exec(`UPDATE delivery_step_tasks SET owner_node=?, urgent=1 WHERE claimed_by='' AND completed=0 AND step=? AND for_step=? AND after_merge=? AND change_id IN (SELECT id FROM delivery_changes WHERE project=?)`, node, q.Step, q.For, boolInt(q.AfterMerge), q.Project)
	} else {
		_, err = tx.Exec(`UPDATE delivery_step_tasks SET completed=1 WHERE claimed_by='' AND completed=0 AND step=? AND for_step=? AND after_merge=? AND change_id IN (SELECT id FROM delivery_changes WHERE project=?)`, q.Step, q.For, boolInt(q.AfterMerge), q.Project)
	}
	if err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	s.refreshDeliveryTasks()
	s.settingsNotify()
	return nil
}

func setEdgeTx(tx *sql.Tx, e protocol.Edge, actor string, now int64) error {
	if err := validateEdgeRefs(e); err != nil {
		return err
	}
	typed := e.FromRef != nil
	if typed {
		var enabled bool
		if err := tx.QueryRow(`SELECT enabled FROM graph_ref_gate WHERE id=1`).Scan(&enabled); err != nil {
			return err
		}
		if !enabled {
			return reject("typed Graph cutover has not been enabled")
		}
		for _, ref := range []*protocol.GraphRef{e.FromRef, e.ToRef} {
			if ref.Kind == "special" {
				continue
			}
			table := "repos"
			if ref.Kind == "agent" {
				table = "agents"
			}
			var exists bool
			if err := tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM `+table+` WHERE id=?)`, ref.ID).Scan(&exists); err != nil {
				return err
			}
			if !exists {
				return reject("unknown %s endpoint", ref.Kind)
			}
		}
	}
	same := e.From == e.To && (!typed || *e.FromRef == *e.ToRef)
	// History retains the old keys; its typed columns supply endpoint kinds.
	e = legacyRefEdge(e)
	if same {
		return reject("self edge")
	}
	if e.Rule == "" {
		e.Rule = "normal"
	}
	if e.Rule != "normal" && e.Rule != "auto" && e.Rule != "ask" && e.Rule != "never" {
		return reject("invalid edge rule")
	}
	deliveryEdge := deliveryEdgeStep(e.Kind) != "" && e.Kind != "deploys"
	if e.Kind != "depends" && e.Kind != "governs" && e.Kind != "deploys" && !deliveryEdge {
		return reject("invalid edge kind")
	}
	if deliveryEdgeStep(e.Kind) != "" && !actorAllowed(actor, "rule") {
		return reject("only the owner may change delivery step owners")
	}
	if (e.Kind == "depends" || e.Kind == "deploys" || deliveryEdge) && e.Mode != "" {
		return reject("this edge kind has no mode")
	}
	if e.Kind == "governs" && e.Mode != "advisor" && e.Mode != "arbiter" && e.Mode != "owner" {
		return reject("invalid mode")
	}
	var err error
	from := "component"
	special := protocol.DeliveryOwnerFromSource(e.From)
	if special.Kind != protocol.OwnerNode {
		if !deliveryEdge {
			return reject("special owner requires a delivery assignment")
		}
		resolved, err := protocol.DeliveryOwnerSource("@"+special.Kind, deliveryEdgeStep(e.Kind))
		if err != nil {
			return reject("%v", err)
		}
		if resolved != e.From {
			return reject("unknown special delivery owner")
		}
	} else if e.From != "*" {
		if typed {
			from = "component"
			if e.Kind == "governs" {
				from = "architect"
			}
		} else {
			from, err = graphKind(tx, e.From)
		}
		if err != nil {
			return err
		}
	} else if e.Kind != "depends" {
		return reject("wildcard requires dependency")
	}
	to := "component"
	if !typed {
		to, err = graphKind(tx, e.To)
	}
	if err != nil {
		return err
	}
	if e.Kind == "governs" && (from != "architect" || to != "component") {
		return reject("governs requires architect to component")
	}
	if deliveryEdge {
		if to != "component" || (from != "component" && from != "architect") {
			return reject("delivery owner requires a component or architect to a component")
		}
		var count int
		if err := tx.QueryRow(`SELECT COUNT(*) FROM graph_edges WHERE target=? AND kind=? AND source<>?`, e.To, e.Kind, e.From).Scan(&count); err != nil {
			return err
		}
		if count != 0 {
			return reject("%s already has an owner for %s", e.To, deliveryEdgeStep(e.Kind))
		}
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
	if _, err = tx.Exec(`INSERT INTO graph_edges(source,target,kind,mode,rule) VALUES(?,?,?,?,?) ON CONFLICT(source,target,kind) DO UPDATE SET mode=excluded.mode,rule=excluded.rule`, e.From, e.To, e.Kind, e.Mode, e.Rule); err != nil {
		return err
	}
	if err = graphRecord(tx, "edge", e.From, e.Kind, previous, string(b), actor, now); err != nil {
		return err
	}
	if deliveryEdgeStep(e.Kind) != "" {
		if err := bumpSettingsRevision(tx); err != nil {
			return err
		}
	}
	return nil
}

// RemoveEdge deletes an edge and records its previous value.
func (s *Store) RemoveEdge(e protocol.Edge, actor string, validate ...bool) error {
	if err := validateEdgeRefs(e); err != nil {
		return err
	}
	if e.FromRef != nil {
		if err := s.requireGraphRefs(); err != nil {
			return err
		}
	}
	e = legacyRefEdge(e)
	if deliveryEdgeStep(e.Kind) != "" && !actorAllowed(actor, "rule") {
		return reject("only the owner may change delivery step owners")
	}
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
	if deliveryEdgeStep(e.Kind) != "" {
		if err := bumpSettingsRevision(tx); err != nil {
			return err
		}
	}
	if len(validate) > 0 && validate[0] {
		return nil
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	if deliveryEdgeStep(e.Kind) != "" {
		s.refreshDeliveryTasks()
		s.settingsNotify()
	}
	return nil
}

// Graph returns explicit and implicit nodes and all edges.
func (s *Store) Graph() (protocol.Graph, error) {
	g := protocol.Graph{Nodes: []protocol.Node{}, Edges: []protocol.Edge{}}
	rows, err := s.db.Query(`SELECT name,kind,created_at,1,'',2 FROM graph_nodes
 UNION ALL SELECT name,'component',0,0,path,graph_excluded FROM projects WHERE graph_excluded<>1 AND name NOT IN (SELECT name FROM graph_nodes)
 UNION ALL SELECT repo_project,'component',0,0,repo_root,graph_excluded FROM sessions WHERE graph_excluded<>1 AND repo_project<>'' AND repo_project NOT IN (SELECT name FROM graph_nodes)
 UNION ALL SELECT source,'component',0,1,'',2 FROM graph_edges WHERE source<>'*' AND source NOT LIKE '/delivery/%' AND source NOT IN (SELECT name FROM graph_nodes)
 UNION ALL SELECT target,'component',0,1,'',2 FROM graph_edges WHERE target NOT IN (SELECT name FROM graph_nodes) ORDER BY 1`)
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
	charterQuery := `SELECT node,attr,value FROM graph_charter`
	if on, err := s.graphRefsEnabled(); err != nil {
		return g, err
	} else if on {
		charterQuery = `SELECT node,attr,value FROM graph_charter WHERE node NOT IN (SELECT name FROM graph_nodes WHERE kind='architect') UNION ALL SELECT a.agent_id,a.attr,a.value FROM agent_charter a JOIN graph_nodes n ON n.name=a.agent_id AND n.kind='architect'`
	}
	rows, err = s.db.Query(charterQuery)
	if err != nil {
		return g, err
	}
	byName := map[string]*protocol.Node{}
	byLower := map[string]*protocol.Node{}
	for i := range g.Nodes {
		byName[g.Nodes[i].Name] = &g.Nodes[i]
		byLower[strings.ToLower(g.Nodes[i].Name)] = &g.Nodes[i]
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
	snap, err := s.SettingsSnapshot()
	if err != nil {
		return g, err
	}
	for _, v := range snap.Values {
		if v.Level != "repo" || v.Item != "" {
			continue
		}
		n := byName[v.Scope]
		if n == nil {
			continue
		}
		for old, key := range deliveryNodeKeys {
			if key != v.Key {
				continue
			}
			var value string
			if json.Unmarshal(v.Value, &value) != nil || protocol.ValidateNodeSetting(old, value) != nil {
				continue
			}
			if n.Settings == nil {
				n.Settings = map[string]string{}
			}
			n.Settings[old] = value
		}
	}
	for i := range g.Nodes {
		n := &g.Nodes[i]
		n.Settings = nodeEffectiveSettings(snap, n.Name, n.Charter.Maturity, n.Settings)
		if len(n.Settings) == 0 {
			n.Settings = nil
		}
	}
	// The owner's delegation policies are rules agents follow, so the public
	// read shows them read-only. They are stored per machine and project.
	rows, err = s.db.Query(`SELECT node_id,project,policy FROM project_policies ORDER BY project,node_id`)
	if err != nil {
		return g, err
	}
	for rows.Next() {
		var machine, project, raw string
		if err = rows.Scan(&machine, &project, &raw); err != nil {
			rows.Close()
			return g, err
		}
		var policy protocol.DelegationPolicy
		if json.Unmarshal([]byte(raw), &policy) != nil {
			continue
		}
		policy.Workers = "" // legacy worker value is a downgrade backup
		if n := byLower[strings.ToLower(project)]; n != nil {
			for _, v := range snap.Values {
				if v.Key == workerSandboxKey && v.Level == "repo" && v.Scope == n.Name && v.Item == "" {
					_ = json.Unmarshal(v.Value, &policy.Workers)
				}
			}
			if !policy.Empty() {
				n.Delegation = append(n.Delegation, protocol.NodeDelegation{Machine: machine, DelegationPolicy: policy})
			}
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
	typed, err := s.graphRefsEnabled()
	if err != nil {
		return nil, err
	}
	rows, err := s.db.Query(`SELECT id,type,node,attr,old_value,new_value,at,target_kind FROM graph_history WHERE (actor='cto' OR actor IN ('CTO (minor)','CTO (owner window)') OR actor GLOB 'owner (CTO proposal p_[0-9]*)') AND id>? ORDER BY id`, after)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []protocol.CTOGraphChange{}
	for rows.Next() {
		var v protocol.CTOGraphChange
		var targetKind string
		if err = rows.Scan(&v.ID, &v.Kind, &v.Node, &v.Attr, &v.OldValue, &v.NewValue, &v.At, &targetKind); err != nil {
			return nil, err
		}
		if typed {
			if v.Kind == "edge" {
				for _, raw := range []*string{&v.OldValue, &v.NewValue} {
					if *raw == "" {
						continue
					}
					var edge protocol.Edge
					if err = json.Unmarshal([]byte(*raw), &edge); err != nil {
						return nil, err
					}
					typedHistoryEdge(&edge)
					v.Ref = edge.FromRef
					b, _ := json.Marshal(edge)
					*raw = string(b)
				}
			} else {
				if v.Kind == "node" {
					targetKind = "agent"
				}
				if targetKind != "" {
					id := v.Node
					if targetKind == "agent" && id == protocol.CTONodeName {
						id = "cto"
					}
					v.Ref = &protocol.GraphRef{Kind: targetKind, ID: id}
				}
			}
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

func (s *Store) CTOLastGraphID() (int64, error) {
	var id int64
	err := s.db.QueryRow(`SELECT COALESCE(MAX(id),0) FROM graph_history WHERE actor='cto' OR actor IN ('CTO (minor)','CTO (owner window)') OR actor GLOB 'owner (CTO proposal p_[0-9]*)'`).Scan(&id)
	return id, err
}

func graphProjectRoot(root string) bool {
	if !repoinfo.GraphProjectRoot(root) {
		return false
	}
	parts := strings.Split(strings.Trim(root, "/"), "/")
	return len(parts) != 2 || (parts[0] != "home" && parts[0] != "Users")
}
