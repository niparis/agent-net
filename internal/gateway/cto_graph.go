package gateway

import (
	"database/sql"
	"encoding/json"
	"strings"

	"github.com/niparis/agent-net/internal/protocol"
)

// CTOGraphClass is a preflight. graphRecord repeats the check in the mutation
// transaction, so a newly critical node cannot race a minor change.
func (s *Store) CTOGraphClass(tool string, raw json.RawMessage) (string, error) {
	if (tool == "node_add" || tool == "node_remove") && s.graphRefsOn.Load() {
		return "rule", nil
	}
	if tool == "charter_set" {
		var q protocol.CharterSetRequest
		if err := json.Unmarshal(raw, &q); err != nil {
			return "", err
		}
		if q.Level == "global" || q.Level == "team" || (q.Ref != nil && q.Ref.Kind == "team") {
			return "rule", nil
		}
		if q.Level != "" && q.Ref == nil {
			q.Ref = &protocol.GraphRef{Kind: q.Level, ID: q.Node}
			raw, _ = json.Marshal(q)
		}
	}
	var target struct {
		Ref *protocol.GraphRef `json:"ref"`
	}
	if err := json.Unmarshal(raw, &target); err != nil {
		return "", err
	}
	if tool == "scope_set" || tool == "scope_remove" {
		return "rule", nil
	}
	if tool == "lead_set" || tool == "repo_create" || tool == "repo_archive" || tool == "agent_create" || tool == "agent_tombstone" ||
		tool == "team_create" || tool == "team_rename" || tool == "team_purpose" || tool == "team_charter" || tool == "team_archive" || tool == "team_member_add" || tool == "team_member_remove" {
		if err := s.requireGraphRefs(); err != nil {
			return "", err
		}
		return "rule", nil
	}
	if target.Ref != nil {
		if err := s.requireGraphRefs(); err != nil {
			return "", err
		}
		if err := target.Ref.Validate(); err != nil {
			return "", err
		}
		if target.Ref.Kind == "agent" {
			var q protocol.CharterSetRequest
			if err := json.Unmarshal(raw, &q); err != nil {
				return "", err
			}
			tx, err := s.db.Begin()
			if err != nil {
				return "", err
			}
			defer tx.Rollback()
			var old string
			if err = tx.QueryRow(`SELECT value FROM agent_charter WHERE agent_id=? AND attr=?`, target.Ref.ID, q.Attr).Scan(&old); err != nil && err != sql.ErrNoRows {
				return "", err
			}
			return ctoGraphClassTx(tx, "agent_charter", target.Ref.ID, q.Attr, old, q.Value)
		}
		if target.Ref.Kind != "repo" {
			return "", reject("invalid charter target")
		}
		var args map[string]json.RawMessage
		if err := json.Unmarshal(raw, &args); err != nil {
			return "", err
		}
		args["node"], _ = json.Marshal(target.Ref.ID)
		raw, _ = json.Marshal(args)
	}
	if tool == "edge_set" || tool == "edge_remove" {
		var edge protocol.Edge
		if err := json.Unmarshal(raw, &edge); err != nil {
			return "", err
		}
		if err := validateEdgeRefs(edge); err != nil {
			return "", err
		}
		if edge.FromRef != nil {
			if err := s.requireGraphRefs(); err != nil {
				return "", err
			}
		}
		edge = legacyRefEdge(edge)
		raw, _ = json.Marshal(edge)
	}
	var q struct {
		Node, Name, Attr, Value, From, To, Kind, Mode, Rule, Setting string
		ID                                                           int64
	}
	if err := json.Unmarshal(raw, &q); err != nil {
		return "", err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	typ, node, attr, old, value := "", q.Node, q.Attr, "", q.Value
	switch tool {
	case "charter_set":
		typ = "charter"
		err = tx.QueryRow(`SELECT value FROM graph_charter WHERE node=? AND attr=?`, node, attr).Scan(&old)
		if err != nil && err != sql.ErrNoRows {
			return "", err
		}
	case "node_add":
		typ, node, value = "node", q.Name, "architect"
	case "node_remove":
		var count int
		if err = tx.QueryRow(`SELECT COUNT(*) FROM setting_values WHERE level='repo' AND scope=?`, q.Name).Scan(&count); err != nil {
			return "", err
		}
		if count > 0 {
			return "rule", nil
		}
		typ, node, old, value = "node", q.Name, "architect", ""
	case "edge_set", "edge_remove":
		if deliveryEdgeStep(q.Kind) != "" {
			return "rule", nil
		}
		typ, node, attr = "edge", q.From, q.Kind
		b, _ := json.Marshal(protocol.Edge{From: q.From, To: q.To, Kind: q.Kind, Mode: q.Mode, Rule: q.Rule})
		value = string(b)
		var previous protocol.Edge
		previous.From, previous.To, previous.Kind = q.From, q.To, q.Kind
		e := tx.QueryRow(`SELECT mode,rule FROM graph_edges WHERE source=? AND target=? AND kind=?`, q.From, q.To, q.Kind).Scan(&previous.Mode, &previous.Rule)
		if e != nil && e != sql.ErrNoRows {
			return "", e
		}
		if e == nil {
			b, _ = json.Marshal(previous)
			old = string(b)
		}
		if tool == "edge_remove" {
			if old == "" {
				old = value
			}
			value = ""
		}
	case "node_settings_set":
		// The standing loop is advice: the CTO sets it without a proposal.
		if strings.HasPrefix(q.Setting, "loop_") {
			return "major", nil
		}
		if q.Setting == protocol.SettingWorkMode || q.Setting == protocol.SettingWakeOnMessage {
			return "minor", nil
		}
		if _, moved := deliveryNodeKeys[q.Setting]; moved {
			return "rule", nil
		}
		return "major", nil
	case "graph_undo":
		query := `SELECT type,node,attr,new_value,old_value FROM graph_history WHERE id=?`
		if q.ID == 0 {
			query = `SELECT type,node,attr,new_value,old_value FROM graph_history ORDER BY id DESC LIMIT 1`
			err = tx.QueryRow(query).Scan(&typ, &node, &attr, &old, &value)
		} else {
			err = tx.QueryRow(query, q.ID).Scan(&typ, &node, &attr, &old, &value)
		}
		if err != nil {
			return "", err
		}
	default:
		return "major", nil
	}
	return ctoGraphClassTx(tx, typ, node, attr, old, value)
}

func ctoGraphClassTx(tx *sql.Tx, typ, node, attr, old, value string) (string, error) {
	// Work mode is advice at every level: the CTO sets it without a proposal.
	if attr == protocol.AttrWorkMode || strings.HasSuffix(attr, ":"+protocol.AttrWorkMode) ||
		attr == protocol.SettingWakeOnMessage || strings.HasSuffix(attr, ":"+protocol.SettingWakeOnMessage) ||
		strings.HasPrefix(attr, protocol.SettingWakeOnMessage+"@") {
		return "minor", nil
	}
	if typ == "lifecycle" || typ == "level_charter" || typ == "scope" {
		return "rule", nil
	}
	if typ == "edge" && deliveryEdgeStep(attr) != "" {
		return "rule", nil
	}
	critical := func(name string) (bool, error) {
		var maturity string
		err := tx.QueryRow(`SELECT value FROM graph_charter WHERE node=? AND attr='maturity'`, name).Scan(&maturity)
		if err != nil && err != sql.ErrNoRows {
			return false, err
		}
		return strings.Split(maturity, " - ")[0] == "critical", nil
	}
	if yes, err := critical(node); err != nil {
		return "", err
	} else if yes {
		return "major", nil
	}
	v := LearnedSuggestion{Kind: "add", Origin: "owner", Field: attr, Phrase: value}
	switch typ {
	case "node":
		if value != "" {
			return "minor", nil
		}
		return "major", nil
	case "charter", "agent_charter":
		if strings.TrimSpace(value) == "" {
			v.Kind = "remove"
		}
		switch attr {
		case "accepts", "rules":
			// Learned items are separated with semicolons. Removing an existing item
			// is a removal, even if the replacement itself has no restrictive words.
			for _, item := range strings.FieldsFunc(old, func(r rune) bool { return r == ';' || r == '\n' }) {
				found := false
				for _, next := range strings.FieldsFunc(value, func(r rune) bool { return r == ';' || r == '\n' }) {
					if strings.EqualFold(strings.TrimSpace(item), strings.TrimSpace(next)) {
						found = true
						break
					}
				}
				if strings.TrimSpace(item) != "" && !found {
					v.Kind = "remove"
					break
				}
			}
		}
		// Wording uses the same restriction rule as learned additions. These fields
		// do not set authority; maturity, refuses and escalate still fail closed.
		switch attr {
		case "purpose", "rules", "maturity_note", "feature_requests":
			v.Field = "rules"
		}
	case "edge":
		data := value
		if data == "" {
			data = old
		}
		var e protocol.Edge
		if err := json.Unmarshal([]byte(data), &e); err != nil {
			return "", err
		}
		if yes, err := critical(e.To); err != nil {
			return "", err
		} else if yes {
			return "major", nil
		}
		v.Field, v.EdgeKind = "edge", e.Kind
		// A rule is a request permission, even on an otherwise advisory edge.
		if e.Mode != "" || (e.Rule != "" && e.Rule != "normal") {
			return "major", nil
		}
		if old != "" && value != "" {
			var previous protocol.Edge
			if err := json.Unmarshal([]byte(old), &previous); err != nil {
				return "", err
			}
			if previous.Rule != "" && previous.Rule != "normal" {
				return "major", nil
			}
		}
	default:
		return "major", nil
	}
	return learnedClass(v), nil
}
