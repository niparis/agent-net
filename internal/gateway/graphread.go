package gateway

import (
	"strings"

	"github.com/niparis/agent-net/internal/protocol"
)

// The graph read path (decision 0022). The owner's operator methods that
// change the graph are built elsewhere; this file only reads it: the daemon
// asks for a node's charter and edges every turn, and the gateway computes the
// relation line of a delivered request.

// GraphFor returns the graph of one node: the node, the nodes its edges name,
// and the edges that touch it. An empty name returns the whole graph.
func (s *Store) GraphFor(project string) (protocol.Graph, error) {
	all, err := s.Graph()
	if err != nil || project == "" {
		return all, err
	}
	out := protocol.Graph{Nodes: []protocol.Node{}, Edges: []protocol.Edge{}}
	keep := map[string]bool{}
	for _, e := range all.Edges {
		if e.From == project || e.To == project {
			out.Edges = append(out.Edges, e)
			keep[e.From] = true
			keep[e.To] = true
		}
	}
	for _, n := range all.Nodes {
		if n.Name == project || keep[n.Name] {
			out.Nodes = append(out.Nodes, n)
		}
	}
	return out, nil
}

// projectOfSession is the node a session speaks for: its repository's project,
// as the PROJECT column resolves it.
func projectOfSession(a protocol.AgentView) string {
	p := a.Repo.Project
	if p == "" {
		p = a.Profile.Project
	}
	return strings.TrimSpace(p)
}

// RelationFor returns the relation line for a request from one session to
// another. It is empty for a pair of sessions of the same node or when a node
// is unknown.
func (s *Store) RelationFor(fromSession, toSession string) (string, error) {
	from, ok, err := s.Agent(fromSession)
	if err != nil || !ok {
		return "", err
	}
	to, ok, err := s.Agent(toSession)
	if err != nil || !ok {
		return "", err
	}
	g, err := s.Graph()
	if err != nil {
		return "", err
	}
	return protocol.RelationText(g, projectOfSession(from), projectOfSession(to)), nil
}

// handleGraphRead serves "graph.read".
func (g *Gateway) handleGraphRead(raw []byte) (any, error) {
	p, err := decode[protocol.GraphRead](raw)
	if err != nil {
		return nil, reject("bad graph.read")
	}
	graph, err := g.store.GraphFor(p.Project)
	if err != nil {
		return nil, err
	}
	return protocol.GraphGot{Graph: graph}, nil
}

// relationForDelivery is the relation line the gateway puts on a request. Only
// a request gets one: a reply is information, and a notification is not a
// request.
func (g *Gateway) relationForDelivery(m protocol.Message) string {
	if m.Type != protocol.TypeRequest {
		return ""
	}
	rel, err := g.store.RelationFor(m.From, m.To)
	if err != nil {
		return ""
	}
	return rel
}
