package gateway

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/niparis/agent-net/internal/protocol"
	"github.com/niparis/agent-net/internal/upgrade"
)

// The persisted gate is loaded at startup and published only after cutover
// commits. Frame checks never need a database connection.
func (s *Store) graphRefsEnabled() (bool, error) { return s.graphRefsOn.Load(), nil }

func (s *Store) loadGraphRefs() error {
	var on bool
	if err := s.db.QueryRow(`SELECT enabled FROM graph_ref_gate WHERE id=1`).Scan(&on); err != nil {
		return err
	}
	s.graphRefsOn.Store(on)
	return nil
}

// SetNodeGraphRefs records every hello, including a downgraded daemon's report.
func (s *Store) SetNodeGraphRefs(node, version string, supported bool) error {
	_, release := upgrade.ParseRelease(version)
	supported = supported && release
	_, err := s.db.Exec(`INSERT INTO node_graph_refs VALUES(?,?,?) ON CONFLICT(node_id) DO UPDATE SET version=excluded.version,supported=excluded.supported`, node, version, supported)
	return err
}

// graphRefsBlockers uses the same report rule for status, cutover and its
// transaction recheck. Live links are included for the pre-backup checks.
func (g *Gateway) graphRefsBlockers(ctx context.Context, q interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}, live bool) ([]string, error) {
	unready := map[string]bool{}
	if live {
		g.mu.Lock()
		for id, conn := range g.conns {
			if !conn.graphRefs.Load() {
				unready[id] = true
			}
		}
		g.mu.Unlock()
	}
	rows, err := q.QueryContext(ctx, `SELECT n.id, n.hostname, (COALESCE(c.supported,0)=0 OR trim(COALESCE(c.version,'')) IN ('','unknown') OR c.version<>n.version) FROM nodes n LEFT JOIN node_graph_refs c ON c.node_id=n.id WHERE n.removed=0`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var links, reports []string
	for rows.Next() {
		var id, host string
		var blocked bool
		if err := rows.Scan(&id, &host, &blocked); err != nil {
			return nil, err
		}
		name := strings.TrimSpace(host)
		if name == "" {
			name = "(" + id + ")"
		}
		if unready[id] {
			links = append(links, fmt.Sprintf("upgrade required: machine %s has not completed typed Graph hello", name))
			delete(unready, id)
		}
		if blocked {
			reports = append(reports, fmt.Sprintf("upgrade required: machine %s has not reported typed Graph support", name))
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for id := range unready {
		links = append(links, fmt.Sprintf("upgrade required: machine (%s) has not completed typed Graph hello", id))
	}
	sort.Strings(links)
	sort.Strings(reports)
	return append(links, reports...), nil
}

func (s *Store) requireGraphRefs() error {
	on, err := s.graphRefsEnabled()
	if err != nil {
		return err
	}
	if !on {
		return reject("typed Graph cutover is waiting for all machines to upgrade")
	}
	return nil
}

func (s *Store) graphFrameAllowed(nc *nodeConn, typ string) error {
	on, err := s.graphRefsEnabled()
	if err != nil || !on || typ == "hello" {
		return err
	}
	if !nc.graphRefs.Load() {
		// Legacy and development daemons still need normal traffic, including
		// upgrades. Do not let them use Graph operations whose identities may
		// be ambiguous after the typed cutover.
		for _, prefix := range []string{"graph.", "team.", "charter.", "edge.", "cto.graph_"} {
			if strings.HasPrefix(typ, prefix) {
				return reject("upgrade required: daemon must report typed Graph support in hello")
			}
		}
		switch typ {
		case "lead.set", "query.graph", "query.graph_talk", "node.add", "node.remove", "repo.create", "repo.archive", "agent.create", "agent.tombstone":
			return reject("upgrade required: daemon must report typed Graph support in hello")
		}
	}
	return nil
}

// GraphForRef returns a typed neighbourhood, including same-ID Repo and Agent
// records only when an edge actually connects them. Nil selects every record.
func (s *Store) GraphForRef(ref *protocol.GraphRef) (protocol.Graph, error) {
	if err := s.requireGraphRefs(); err != nil {
		return protocol.Graph{}, err
	}
	return s.graphForRef(ref)
}

func (s *Store) graphForRef(ref *protocol.GraphRef) (protocol.Graph, error) {
	if ref != nil {
		if err := ref.Validate(); err != nil {
			return protocol.Graph{}, err
		}
		if ref.Kind == "special" {
			return protocol.Graph{}, reject("special endpoint is not a graph record")
		}
	}
	legacy, err := s.Graph()
	if err != nil {
		return legacy, err
	}
	repos, agents, err := s.RepoAgents()
	if err != nil {
		return protocol.Graph{}, err
	}
	g := protocol.Graph{Nodes: []protocol.Node{}, Edges: []protocol.Edge{}}
	for _, table := range []string{"repos", "agents"} {
		rows, err := s.db.Query(`SELECT display_name,group_concat(id, ', ') FROM ` + table + ` GROUP BY display_name COLLATE NOCASE HAVING COUNT(*)>1 ORDER BY display_name`)
		if err != nil {
			return g, err
		}
		for rows.Next() {
			var name, ids string
			if err = rows.Scan(&name, &ids); err != nil {
				rows.Close()
				return g, err
			}
			g.Warnings = append(g.Warnings, fmt.Sprintf("duplicate %s display name %q: %s; rename these records", table, name, ids))
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return g, err
		}
	}
	byLegacy := map[string]protocol.Node{}
	for _, n := range legacy.Nodes {
		byLegacy[n.Name] = n
	}
	for _, r := range repos {
		if r.Archived {
			continue
		}
		n := byLegacy[r.ID]
		n.Name, n.Kind, n.CreatedAt = r.DisplayName, "repo", r.CreatedAt
		n.Charter = protocol.Charter{}
		n.Ref = &protocol.GraphRef{Kind: "repo", ID: r.ID}
		if r.OwnerAgentID != nil {
			n.Owner = &protocol.GraphRef{Kind: "agent", ID: *r.OwnerAgentID}
		}
		var lead, session sql.NullString
		if err := s.db.QueryRow(`SELECT (SELECT lead_session FROM repo_leads WHERE repo_id=repos.id), CASE WHEN EXISTS(SELECT 1 FROM repo_leads WHERE repo_id=repos.id) THEN (SELECT agent_id FROM repo_leads WHERE repo_id=repos.id) ELSE owner_agent_id END FROM repos WHERE id=?`, r.ID).Scan(&session, &lead); err != nil {
			return g, err
		}
		if session.Valid {
			n.Lead = &protocol.GraphRef{Kind: "session", ID: session.String}
		} else if lead.Valid {
			n.Lead = &protocol.GraphRef{Kind: "agent", ID: lead.String}
		}
		limit, err := s.RepoAgentLimit(r.ID)
		if err != nil {
			return g, err
		}
		n.MaxAgents, n.AgentCount, n.OverMax = limit.MaxAgents, limit.AgentCount, limit.OverMax
		g.Nodes = append(g.Nodes, n)
	}
	for _, a := range agents {
		if a.Tombstone {
			continue
		}
		n := protocol.Node{Name: a.DisplayName, Kind: "agent", Role: a.Role, CreatedAt: a.CreatedAt, Ref: &protocol.GraphRef{Kind: "agent", ID: a.ID}}
		g.Nodes = append(g.Nodes, n)
	}
	teams, err := s.teamGraph()
	if err != nil {
		return g, err
	}
	g.Nodes = append(g.Nodes, teams.Nodes...)
	g.Edges = append(g.Edges, teams.Edges...)
	byRef := map[protocol.GraphRef]*protocol.Node{}
	for i := range g.Nodes {
		byRef[*g.Nodes[i].Ref] = &g.Nodes[i]
	}
	rows, err := s.db.Query(`SELECT node,attr,value FROM graph_charter`)
	if err != nil {
		return g, err
	}
	for rows.Next() {
		var id, attr, value string
		if err = rows.Scan(&id, &attr, &value); err != nil {
			rows.Close()
			return g, err
		}
		if n := byRef[protocol.GraphRef{Kind: "repo", ID: id}]; n != nil {
			b, _ := json.Marshal(map[string]string{attr: value})
			if err = json.Unmarshal(b, &n.Charter); err != nil {
				rows.Close()
				return g, err
			}
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return g, err
	}
	rows, err = s.db.Query(`SELECT agent_id,attr,value FROM agent_charter`)
	if err != nil {
		return g, err
	}
	for rows.Next() {
		var id, attr, value string
		if err = rows.Scan(&id, &attr, &value); err != nil {
			rows.Close()
			return g, err
		}
		if n := byRef[protocol.GraphRef{Kind: "agent", ID: id}]; n != nil {
			b, _ := json.Marshal(map[string]string{attr: value})
			if err = json.Unmarshal(b, &n.Charter); err != nil {
				rows.Close()
				return g, err
			}
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return g, err
	}
	rows, err = s.db.Query(`SELECT source,target,kind,mode,rule,source_kind,target_kind FROM graph_edges ORDER BY source_kind,source,target_kind,target,kind`)
	if err != nil {
		return g, err
	}
	for rows.Next() {
		var e protocol.Edge
		var f, t protocol.GraphRef
		if err = rows.Scan(&e.From, &e.To, &e.Kind, &e.Mode, &e.Rule, &f.Kind, &t.Kind); err != nil {
			rows.Close()
			return g, err
		}
		f.ID, t.ID = e.From, e.To
		if e.From == "*" {
			f.Kind = "special"
		}
		if f.ID == protocol.CTONodeName && f.Kind == "agent" {
			f.ID = "cto"
		}
		if t.ID == protocol.CTONodeName && t.Kind == "agent" {
			t.ID = "cto"
		}
		e.FromRef, e.ToRef = &f, &t
		if (f.Kind != "special" && byRef[f] == nil) || byRef[t] == nil {
			rows.Close()
			return g, reject("unresolved typed edge %s -> %s (%s)", e.From, e.To, e.Kind)
		}
		g.Edges = append(g.Edges, e)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return g, err
	}
	projects, err := s.Projects("")
	if err != nil {
		return g, err
	}
	for _, p := range projects {
		if n := byRef[protocol.GraphRef{Kind: "repo", ID: p.Name}]; n != nil {
			n.Locations = append(n.Locations, protocol.GraphLocation{Machine: p.Hostname, Path: p.Path})
		}
	}
	sessions, err := s.Agents(protocol.QueryAgents{OnlineOnly: true, IncludeOwners: true})
	if err != nil {
		return g, err
	}
	for _, a := range sessions {
		if n := byRef[protocol.GraphRef{Kind: "agent", ID: a.AgentID}]; n != nil {
			n.SessionCount++
			if len(n.Sessions) < 5 {
				name := a.Profile.Name
				if name == "" {
					name = a.Title
				}
				n.Sessions = append(n.Sessions, protocol.GraphSession{ID: a.SessionID, Name: name, Machine: a.Hostname, Status: a.Status})
			}
		}
	}
	if ref == nil {
		return g, nil
	}
	out := protocol.Graph{Nodes: []protocol.Node{}, Edges: []protocol.Edge{}}
	keep := map[protocol.GraphRef]bool{*ref: true}
	if byRef[*ref] == nil {
		return out, nil
	}
	for _, e := range g.Edges {
		if protocol.RefMatches(e.FromRef, ref) || protocol.RefMatches(e.ToRef, ref) {
			out.Edges = append(out.Edges, e)
			keep[*e.FromRef] = true
			keep[*e.ToRef] = true
		}
	}
	for _, n := range g.Nodes {
		if keep[*n.Ref] {
			out.Nodes = append(out.Nodes, n)
		}
	}
	return out, nil
}

func validateEdgeRefs(e protocol.Edge) error {
	if e.FromRef == nil && e.ToRef == nil {
		return nil
	}
	if e.FromRef == nil || e.ToRef == nil {
		return reject("both edge endpoints must be typed")
	}
	f, t := e.EndpointKinds()
	if e.From == "*" {
		f = "special"
	}
	if e.FromRef.Kind != f || e.ToRef.Kind != t || e.FromRef.ID != e.From || e.ToRef.ID != e.To {
		return reject("endpoint kinds do not match %s", e.Kind)
	}
	return nil
}

// SetRefCharter preserves classification and owner approval in the mutation
// transaction. Role text cannot grant authority.
func (s *Store) SetRefCharter(ref protocol.GraphRef, attr, value, actor string, validate bool) error {
	if err := s.requireGraphRefs(); err != nil {
		return err
	}
	if err := ref.Validate(); err != nil {
		return err
	}
	if ref.Kind == "repo" {
		var exists bool
		if err := s.db.QueryRow(`SELECT EXISTS(SELECT 1 FROM repos WHERE id=? AND NOT EXISTS (SELECT 1 FROM archived_repos WHERE repo_id=repos.id))`, ref.ID).Scan(&exists); err != nil {
			return err
		}
		if !exists {
			return reject("unknown repo")
		}
		tx, err := s.db.Begin()
		if err != nil {
			return err
		}
		defer tx.Rollback()
		if err = setCharterKindTx(tx, ref.ID, attr, value, actor, s.now().UnixNano(), true); err != nil {
			return err
		}
		if validate {
			return nil
		}
		if err = tx.Commit(); err != nil {
			return err
		}
		if attr == "maturity" {
			s.refreshDeliveryTasks()
			s.settingsNotify()
		}
		return nil
	}
	if ref.Kind != "agent" {
		return reject("charter requires repo or agent")
	}
	if strings.TrimSpace(actor) == "" {
		return reject("actor required")
	}
	if _, err := protocol.CleanCharterValue(attr, value); err != nil {
		return err
	}
	var live bool
	if err := s.db.QueryRow(`SELECT EXISTS(SELECT 1 FROM agents WHERE id=? AND tombstone=0)`, ref.ID).Scan(&live); err != nil {
		return err
	}
	if !live {
		return fmt.Errorf("unknown agent")
	}
	if validate {
		tx, err := s.db.Begin()
		if err != nil {
			return err
		}
		defer tx.Rollback()
		return setAgentCharterTx(tx, ref.ID, attr, value, actor, s.now().UnixNano())
	}
	return s.SetAgentCharter(ref.ID, attr, value, actor)
}

// RefCharterHistory keeps history IDs in the shared graph history namespace.
func (s *Store) RefCharterHistory(ref protocol.GraphRef, limit int) ([]protocol.CharterChange, error) {
	if err := s.requireGraphRefs(); err != nil {
		return nil, err
	}
	if err := ref.Validate(); err != nil {
		return nil, err
	}
	if ref.Kind != "agent" && ref.Kind != "repo" {
		return nil, reject("invalid charter target")
	}
	if limit <= 0 || limit > 1000 {
		limit = 1000
	}
	rows, err := s.db.Query(`SELECT id,node,attr,old_value,new_value,at,actor FROM graph_history WHERE node=? AND type IN ('charter','agent_charter','lifecycle') AND target_kind=? ORDER BY id DESC LIMIT ?`, ref.ID, ref.Kind, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []protocol.CharterChange{}
	for rows.Next() {
		var h protocol.CharterChange
		h.Ref = &ref
		if err = rows.Scan(&h.ID, &h.Node, &h.Attr, &h.OldValue, &h.NewValue, &h.At, &h.By); err != nil {
			return nil, err
		}
		out = append(out, h)
	}
	return out, rows.Err()
}

func legacyRefEdge(e protocol.Edge) protocol.Edge {
	if e.FromRef != nil && e.FromRef.Kind == "agent" && e.FromRef.ID == "cto" {
		e.From = protocol.CTONodeName
	}
	if e.ToRef != nil && e.ToRef.Kind == "agent" && e.ToRef.ID == "cto" {
		e.To = protocol.CTONodeName
	}
	e.FromRef, e.ToRef = nil, nil
	return e
}

func (s *Store) RefEdgeHistory(ref protocol.GraphRef, limit int) ([]protocol.EdgeChange, error) {
	if err := s.requireGraphRefs(); err != nil {
		return nil, err
	}
	if err := ref.Validate(); err != nil {
		return nil, err
	}
	return s.edgeHistory("", &ref, limit)
}

func typedHistoryEdge(e *protocol.Edge) {
	f, t := e.EndpointKinds()
	if e.From == "*" {
		f = "special"
	}
	e.FromRef = &protocol.GraphRef{Kind: f, ID: e.From}
	e.ToRef = &protocol.GraphRef{Kind: t, ID: e.To}
	if f == "agent" && e.From == protocol.CTONodeName {
		e.FromRef.ID = "cto"
	}
	if t == "agent" && e.To == protocol.CTONodeName {
		e.ToRef.ID = "cto"
	}
}
