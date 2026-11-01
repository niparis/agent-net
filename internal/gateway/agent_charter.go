package gateway

import "github.com/niparis/agent-net/internal/protocol"

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
	// Autonomy is a public rule even though it is stored as a node setting.
	rows, err = s.db.Query(`SELECT node,value FROM graph_node_settings WHERE setting=?`, protocol.SettingAutonomy)
	if err != nil {
		return err
	}
	autonomy := map[string]string{}
	for rows.Next() {
		var node, value string
		if err := rows.Scan(&node, &value); err != nil {
			rows.Close()
			return err
		}
		autonomy[node] = value
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
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
		node := a.Repo.Project
		if node == "" {
			node = a.Profile.Project
		}
		c := byNode[a.Repo.Project]
		if c.Purpose == "" && a.Repo.Description != "" {
			c.Purpose = a.Repo.Description
			c.PurposeFallback = true
		}
		c.Autonomy = protocol.NodeAutonomy(map[string]string{protocol.SettingAutonomy: autonomy[node]})
		c.Purpose = cut(c.Purpose)
		c.Maturity = cut(c.Maturity)
		c.Accepts = cut(c.Accepts)
		c.Refuses = cut(c.Refuses)
		c.FeatureRequests = cut(c.FeatureRequests)
		a.Charter = c
	}
	return nil
}
