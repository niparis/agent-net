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
		a.Charter = c
	}
	return nil
}
