package gateway

import (
	"encoding/json"
	"github.com/niparis/agent-net/internal/protocol"
)

// applyAgentCharters adds only the public charter fields to agent views.
func (s *Store) applyAgentCharters(agents []protocol.AgentView, short bool) error {
	rows, err := s.db.Query(`SELECT node,attr,value FROM graph_charter WHERE attr IN ('purpose','maturity','accepts','refuses','feature_requests')`)
	if err != nil {
		return err
	}
	defer rows.Close()
	byNode := map[string]protocol.AgentCharter{}
	for rows.Next() {
		var node, attr, value string
		if err := rows.Scan(&node, &attr, &value); err != nil {
			return err
		}
		c := byNode[node]
		switch attr {
		case "purpose":
			c.Purpose = value
		case "maturity":
			c.Maturity = value
		case "accepts":
			c.Accepts = value
		case "refuses":
			c.Refuses = value
		case "feature_requests":
			c.FeatureRequests = value
		}
		byNode[node] = c
	}
	if err := rows.Err(); err != nil {
		return err
	}
	rows.Close()
	settingsByNode := map[string]map[string]string{}
	if !short {
		rows, err := s.db.Query(`SELECT node,setting,value FROM graph_node_settings WHERE setting='worker_sandbox'`)
		if err != nil {
			return err
		}
		for rows.Next() {
			var node, setting, value string
			if err := rows.Scan(&node, &setting, &value); err != nil {
				rows.Close()
				return err
			}
			if settingsByNode[node] == nil {
				settingsByNode[node] = map[string]string{}
			}
			settingsByNode[node][setting] = value
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return err
		}
		rows.Close()
	}
	if !short {
		snap, err := s.SettingsSnapshot()
		if err != nil {
			return err
		}
		for _, v := range snap.Values {
			if v.Level != "repo" || v.Item != "" {
				continue
			}
			for old, key := range deliveryNodeKeys {
				if v.Key != key {
					continue
				}
				var value string
				if json.Unmarshal(v.Value, &value) != nil || protocol.ValidateNodeSetting(old, value) != nil {
					continue
				}
				if settingsByNode[v.Scope] == nil {
					settingsByNode[v.Scope] = map[string]string{}
				}
				settingsByNode[v.Scope][old] = value
			}
		}
		for _, a := range agents {
			project := a.Repo.Project
			settingsByNode[project] = nodeEffectiveSettings(snap, project, byNode[project].Maturity, settingsByNode[project])
		}
	}
	cut := func(s string) string {
		r := []rune(s)
		if short && len(r) > 300 {
			return string(r[:300])
		}
		return s
	}
	for i := range agents {
		a := &agents[i]
		c := byNode[a.Repo.Project]
		if c.Purpose == "" && a.Repo.Description != "" {
			c.Purpose = a.Repo.Description
			c.PurposeFallback = true
		}
		c.Purpose = cut(c.Purpose)
		c.Maturity = cut(c.Maturity)
		c.Accepts = cut(c.Accepts)
		c.Refuses = cut(c.Refuses)
		c.FeatureRequests = cut(c.FeatureRequests)
		if !short {
			// A single agent's view carries its node's rules, so a session can
			// check its own rules. A list stays compact.
			c.Rules = protocol.RulesFor(c.Maturity, settingsByNode[a.Repo.Project])
			if c.Rules == nil {
				c.Rules = []protocol.PublicRule{}
			}
		}
		a.Charter = c
	}
	return nil
}
