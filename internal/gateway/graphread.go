package gateway

import (
	"database/sql"
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
	p := a.RepoContext
	if p == "" {
		p = a.Repo.Project
	}
	if p == "" {
		p = a.Profile.Project
	}
	return strings.TrimSpace(p)
}

// nodeName is the graph node a session speaks for. The CTO session speaks for
// the fixed CTO node.
func nodeName(a protocol.AgentView) string {
	if a.Harness == "cto" {
		return protocol.CTONodeName
	}
	return projectOfSession(a)
}

// RelationFor returns the relation line for a request from one session to
// another. It is empty for a pair of sessions of the same node or when a node
// is unknown.
func (s *Store) RelationFor(fromSession, toSession string) (string, error) {
	from, _, err := s.graphIdentity(fromSession)
	if err != nil {
		if err == sql.ErrNoRows {
			return "", nil
		}
		return "", err
	}
	to, _, err := s.graphIdentity(toSession)
	if err != nil {
		if err == sql.ErrNoRows {
			return "", nil
		}
		return "", err
	}
	g, err := s.Graph()
	if err != nil {
		return "", err
	}
	// Dependencies speak about repos.
	for _, e := range g.Edges {
		f, t := from.repo, to.repo
		if e.From == f && e.To == t && f != "" && t != "" && f != t && e.Kind == "depends" {
			return protocol.RelationText(protocol.Graph{Edges: []protocol.Edge{e}}, f, t), nil
		}
	}
	return protocol.RelationText(protocol.Graph{}, from.repo, to.repo), nil
}

// handleGraphRead serves "graph.read".
func (g *Gateway) handleGraphRead(raw []byte) (any, error) {
	p, err := decode[protocol.GraphRead](raw)
	if err != nil {
		return nil, reject("bad graph.read")
	}
	var graph protocol.Graph
	if p.Typed || p.Ref != nil || p.Root != "" {
		graph, err = g.store.GraphForRoot(p.Ref, p.Root)
	} else {
		graph, err = g.store.GraphFor(p.Project)
		if err == nil && p.Also && p.Project != "" {
			var more []protocol.Node
			if more, err = g.store.sameNameNodes(p.Project, graph); err == nil {
				graph.Nodes = append(graph.Nodes, more...)
			}
		}
	}
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

// RequestRule resolves the most specific request rule for two sessions.
type graphIdentity struct{ repo, agent string }

type identityQuerier interface{ QueryRow(string, ...any) *sql.Row }

func graphIdentityQuery(q identityQuerier, session string) (graphIdentity, bool, error) {
	project, agent, harness, worker, err := sessionIdentityQuery(q, session)
	if err != nil {
		return graphIdentity{}, false, err
	}
	project, agent = strings.TrimSpace(project), strings.TrimSpace(agent)
	if harness == "cto" {
		return graphIdentity{protocol.CTONodeName, protocol.CTONodeName}, false, nil
	}
	// Workers are not their caller. Keep their existing repo endpoint for
	// routing until runs have their own graph endpoints.
	if worker || agent == "" {
		agent = project
	}
	// A bot's node carries its charter and place, but its messages route as
	// before: it has no project, so the node adds no routing identity.
	if harness == "bot" {
		agent = project
	}
	var trust bool
	err = q.QueryRow(`SELECT peer_trust FROM sessions WHERE id=?`, session).Scan(&trust)
	return graphIdentity{project, agent}, trust, err
}

func (s *Store) graphIdentity(session string) (graphIdentity, bool, error) {
	return graphIdentityQuery(s.db, session)
}

// requestRuleTx is shared by dispatch and the atomic send path. Resolve each
// endpoint once so fallback identities cannot differ between those paths.
func requestRuleTx(q identityQuerier, fromSession, toSession string) (string, error) {
	from, _, err := graphIdentityQuery(q, fromSession)
	if err == sql.ErrNoRows {
		return "normal", nil
	}
	if err != nil {
		return "normal", err
	}
	to, trust, err := graphIdentityQuery(q, toSession)
	if err == sql.ErrNoRows {
		return "normal", nil
	}
	if err != nil {
		return "normal", err
	}
	if to.repo == "" && to.agent == "" && trust {
		return "auto", nil
	}
	var rule string
	err = q.QueryRow(`SELECT rule FROM graph_edges WHERE
 (kind IN ('depends','deploys') AND target=? AND target<>'' AND (source=? OR source='*'))
 ORDER BY CASE WHEN source='*' THEN 1 ELSE 0 END, kind LIMIT 1`, to.repo, from.repo).Scan(&rule)
	if err == sql.ErrNoRows {
		return "normal", nil
	}
	return rule, err
}

func (s *Store) RequestRule(fromSession, toSession string) (string, error) {
	return requestRuleTx(s.db, fromSession, toSession)
}

func chartedProject(g protocol.Graph, project string) string {
	for _, node := range g.Nodes {
		if node.Name == project {
			return project
		}
	}
	return ""
}

// MergeAuthorityForSession resolves the receiving node's effective setting.
func (s *Store) MergeAuthorityForSession(session string) (string, error) {
	authority, _, err := s.MergeRuleForSession(session)
	return authority, err
}

// MergeRuleForSession resolves authority and its text from one snapshot.
func (s *Store) MergeRuleForSession(session string) (string, string, error) {
	a, ok, err := s.Agent(session)
	if err != nil || !ok {
		return protocol.MergeAsk, "", err
	}
	c, err := s.resolveSessionContext(a.SessionID)
	if err != nil {
		return "", "", err
	}
	project, snap := c.Repo, c.Settings
	authority, err := deliveryValue(snap, "delivery.merge_authority", project)
	if err != nil {
		return "", "", err
	}
	if authority == protocol.ShorthandCustom && project != "" {
		return authority, protocol.NodeDefaultsWithSettings(snap.Maturity[project], nil, snap, project).MergeText(), nil
	}
	return authority, protocol.MergeTextWithSettings(authority, snap), nil
}

// sameNameNodes returns the typed nodes, other than the one an untyped read
// already returns, that share the node's name: a repo and an agent or a team
// can. The untyped read finds only the repo node, so an agent node of the same
// name would be hidden. It is empty before the typed graph is on.
func (s *Store) sameNameNodes(name string, legacy protocol.Graph) ([]protocol.Node, error) {
	if !s.graphRefsOn.Load() {
		return nil, nil
	}
	typed, err := s.graphForRef(nil, false)
	if err != nil {
		return nil, err
	}
	shown := ""
	for _, n := range legacy.Nodes {
		if n.Name == name {
			shown = n.Kind
		}
	}
	var out []protocol.Node
	for _, n := range typed.Nodes {
		if n.Name != name || n.Ref == nil {
			continue
		}
		// The legacy node stands for the repo, or for an architect agent.
		if n.Kind == "repo" || (n.Kind == "agent" && shown == "architect") {
			continue
		}
		out = append(out, n)
	}
	return out, nil
}
