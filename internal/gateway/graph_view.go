package gateway

import "github.com/niparis/agent-net/internal/protocol"

// handleGraphView answers "query.graph" from a machine's daemon.
func (g *Gateway) handleGraphView(raw []byte) (any, error) {
	p, err := decode[protocol.QueryGraph](raw)
	if err != nil {
		return nil, reject("bad query.graph")
	}
	return g.store.GraphView(p.Project, protocol.GraphHistoryPerNode)
}

// GraphView reads the component graph for the map. With an empty project it
// returns every node; with a project it returns that node, the nodes on an
// edge with it and those edges. Changes are the newest per node, so a node's
// history is always there, however busy the rest of the graph is.
func (s *Store) GraphView(project string, historyPerNode int) (protocol.GraphView, error) {
	out := protocol.GraphView{Nodes: []protocol.Node{}, Edges: []protocol.Edge{}, Changes: []protocol.CharterChange{}}
	g, err := s.Graph()
	if err != nil {
		return out, err
	}
	if project == "" {
		out.Nodes, out.Edges = g.Nodes, g.Edges
	} else {
		keep := map[string]bool{}
		for _, n := range g.Nodes {
			if n.Name == project {
				keep[n.Name] = true
			}
		}
		if !keep[project] {
			return out, nil // an unknown project is an empty neighbourhood
		}
		for _, e := range g.Edges {
			if e.From == project || e.To == project {
				keep[e.From], keep[e.To] = true, true
				out.Edges = append(out.Edges, e)
			}
		}
		for _, n := range g.Nodes {
			if keep[n.Name] {
				out.Nodes = append(out.Nodes, n)
			}
		}
	}
	if historyPerNode <= 0 {
		historyPerNode = protocol.GraphHistoryPerNode
	}
	if historyPerNode > 100 {
		historyPerNode = 100
	}
	want := map[string]bool{}
	for _, n := range out.Nodes {
		want[n.Name] = true
	}
	rows, err := s.db.Query(`SELECT id,node,attr,old_value,new_value,at,actor FROM (
		SELECT id,node,attr,old_value,new_value,at,actor,
		       ROW_NUMBER() OVER (PARTITION BY node ORDER BY id DESC) AS rn
		FROM graph_history WHERE type='charter'
	) WHERE rn<=? ORDER BY id DESC`, historyPerNode)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var h protocol.CharterChange
		if err := rows.Scan(&h.ID, &h.Node, &h.Attr, &h.OldValue, &h.NewValue, &h.At, &h.By); err != nil {
			return out, err
		}
		if want[h.Node] {
			out.Changes = append(out.Changes, h)
		}
	}
	return out, rows.Err()
}
