package gateway

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"

	"github.com/niparis/agent-net/internal/protocol"
)

// Scoped federation. A server in the MCP registry is also a tool item (kind
// "mcp", hash "federated-<added_at>"), so the owner assigns it through the
// same plans, revisions and audit as a skill. The newest revision says which
// sessions may see and call it. A server with no revision is global: that is
// how every server added before scoping existed keeps working. The hash holds
// the time the server was added, so a server removed and added again under the
// same name is a new item and does not inherit the old scope.

const fedHashPrefix = "federated-"

// isFederatedHash reports whether an item hash belongs to a registry server.
func isFederatedHash(hash string) bool { return strings.HasPrefix(hash, fedHashPrefix) }

// ensureMCPItemTx makes sure the server has its tool identity.
func ensureMCPItemTx(tx interface {
	Exec(string, ...any) (sql.Result, error)
}, name string) error {
	_, err := tx.Exec(`INSERT OR IGNORE INTO tool_identities(id,kind,name,hash)
		SELECT json_array('mcp',name,'federated-'||added_at),'mcp',name,'federated-'||added_at FROM mcp_servers WHERE name=?`, name)
	return err
}

// MCPItemID returns the tool item ID of a registered server.
func (s *Store) MCPItemID(name string) (string, bool, error) {
	var id string
	err := s.db.QueryRow(`SELECT i.id FROM tool_identities i JOIN mcp_servers m ON m.name=i.name
		AND i.kind='mcp' AND i.hash='federated-'||m.added_at WHERE m.name=?`, name).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	return id, err == nil, err
}

// MCPAssignment returns the server's current scope: the newest revision, or
// global when the owner never assigned it.
func (s *Store) MCPAssignment(name string) (protocol.ToolAssignment, error) {
	all, err := s.MCPAssignments()
	if err != nil {
		return protocol.ToolAssignment{}, err
	}
	if a, ok := all[name]; ok {
		return a, nil
	}
	return protocol.ToolAssignment{Scope: "global"}, nil
}

// MCPAssignments returns the newest revision of every assigned server, by name.
func (s *Store) MCPAssignments() (map[string]protocol.ToolAssignment, error) {
	rows, err := s.db.Query(`SELECT m.name, r.assignment FROM tool_revisions r
		JOIN tool_identities i ON i.id=r.item JOIN mcp_servers m ON m.name=i.name
		AND i.kind='mcp' AND i.hash='federated-'||m.added_at
		WHERE r.revision=(SELECT MAX(x.revision) FROM tool_revisions x WHERE x.item=r.item)`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]protocol.ToolAssignment{}
	for rows.Next() {
		var name, raw string
		if err = rows.Scan(&name, &raw); err != nil {
			return nil, err
		}
		var a protocol.ToolAssignment
		if err = json.Unmarshal([]byte(raw), &a); err != nil {
			// An unreadable assignment must not widen access.
			a = protocol.ToolAssignment{Scope: "uninstalled"}
		}
		out[name] = a
	}
	return out, rows.Err()
}

// mcpVisible reports whether a session of the given project may see and call
// a server with this scope. A session with no project sees only global ones.
func mcpVisible(a protocol.ToolAssignment, project string) bool {
	switch a.Scope {
	case "global", "":
		return true
	case "projects":
		if project == "" {
			return false
		}
		for _, p := range a.Projects {
			if p == project {
				return true
			}
		}
	}
	return false
}

// SessionProject returns the project of a session, as the PROJECT column
// resolves it, and whether the session exists on that node.
func (s *Store) SessionProject(sessionID, nodeID string) (string, bool, error) {
	var project string
	err := s.db.QueryRow(`SELECT COALESCE(NULLIF(s.repo_project,''),p.project,'') FROM sessions s
		LEFT JOIN profiles p ON p.session_id=s.id WHERE s.id=? AND s.node_id=?`, sessionID, nodeID).Scan(&project)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	return project, err == nil, err
}

// isFederatedItem reports whether a tool item is a registry server.
func (s *Store) isFederatedItem(item string) bool {
	var hash string
	err := s.db.QueryRow(`SELECT hash FROM tool_identities WHERE id=? AND kind='mcp'`, item).Scan(&hash)
	return err == nil && isFederatedHash(hash)
}

// callerProject finds the project of the session that makes a request. The
// gateway reads it from its own session record. The session must live on the
// node that sent the frame, so a node cannot speak for another node's session.
// An empty session ID is a caller with no project. Nothing in the request can
// name a project.
func (g *Gateway) callerProject(nc *nodeConn, sessionID string) (string, error) {
	if sessionID == "" {
		return "", nil
	}
	project, ok, err := g.store.SessionProject(sessionID, nc.id)
	if err != nil {
		return "", err
	}
	if !ok {
		return "", reject("session %s is not a session of this node", sessionID)
	}
	return project, nil
}

// mcpCatalog lists the enabled servers a session of this project may see,
// with the host state. Tools of a server whose host is offline stay listed:
// the assignment is kept, and a call says the host is offline.
func (g *Gateway) mcpCatalog(project string) ([]protocol.MCPServer, error) {
	srvs, err := g.mcpList()
	if err != nil {
		return nil, err
	}
	out := []protocol.MCPServer{}
	for _, s := range srvs {
		if s.Enabled && mcpVisible(protocol.ToolAssignment{Scope: s.Scope, Projects: s.Projects}, project) {
			out = append(out, s)
		}
	}
	return out, nil
}

// federatedTargets is the plan of a registry server. Nothing is copied to any
// machine: the assignment is enforced at the gateway. The one target names the
// host so the owner sees which machine runs the server. It does not depend on
// whether the host is online, so a plan does not go stale when the host drops;
// an offline host shows in the outcome of the apply.
func (g *Gateway) federatedTargets(name string) ([]protocol.ToolPlanTarget, error) {
	srv, ok, err := g.store.MCPServer(name)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, errors.New("tool item not found")
	}
	return []protocol.ToolPlanTarget{{NodeID: srv.NodeID, Machine: srv.Hostname, Action: "federate", State: "pending"}}, nil
}

// mcpScopeChanged tells every node to refresh its sessions' tools when an
// applied or undone item is a registry server. Sessions then read the catalog
// again: those that gained or lost tools get tools/list_changed, the others
// see no difference.
func (g *Gateway) mcpScopeChanged(ctx context.Context, item string) {
	if g.store.isFederatedItem(item) {
		g.mcpChanged(ctx, "")
	}
}

// fillFederatedItems adds scope, host and state to the registry servers in a
// tool listing, and decides which of them match the filter. A server that is
// assigned takes effect at the gateway at once, so it counts as applied.
func (s *Store) fillFederatedItems(all map[string]*protocol.ToolItem, f protocol.ToolsFilter, matched map[string]bool, online func(string) bool) error {
	assigned, err := s.MCPAssignments()
	if err != nil {
		return err
	}
	srvs, err := s.MCPServers()
	if err != nil {
		return err
	}
	for _, srv := range srvs {
		id, ok, err := s.MCPItemID(srv.Name)
		if err != nil {
			return err
		}
		item := all[id]
		if !ok || item == nil {
			continue
		}
		a, ok := assigned[srv.Name]
		if !ok {
			a = protocol.ToolAssignment{Scope: "global"}
		}
		item.Scope, item.ScopeProjects = a.Scope, a.Projects
		item.Host, item.HostOnline = srv.Hostname, online(srv.NodeID)
		item.Global = a.Scope == "global"
		if a.Scope == "projects" {
			item.Projects = append([]string{}, a.Projects...)
		}
		item.Applied = "applied"
		if !item.HostOnline {
			item.State = protocol.InventoryStale
		}
		kindOK := f.Kind == "" || f.Kind == protocol.InventoryMCP
		projectOK := f.Project == ""
		for _, p := range a.Projects {
			projectOK = projectOK || p == f.Project
		}
		matched[id] = kindOK && projectOK && f.Harness == "" && (f.State == "" || f.State == item.State)
	}
	return nil
}

// forgetMCPItemTx drops the plans, revisions and identity of a removed server.
// The audit trail stays.
func forgetMCPItemTx(tx *sql.Tx, name string) error {
	if _, err := tx.Exec(`DELETE FROM mcp_imports WHERE name=?`, name); err != nil {
		return err
	}
	var id string
	err := tx.QueryRow(`SELECT i.id FROM tool_identities i JOIN mcp_servers m ON m.name=i.name
		AND i.kind='mcp' AND i.hash='federated-'||m.added_at WHERE m.name=?`, name).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, q := range []string{
		`DELETE FROM tool_results WHERE item=?`, `DELETE FROM tool_plans WHERE item=?`,
		`DELETE FROM tool_revisions WHERE item=?`, `DELETE FROM tool_identities WHERE id=?`,
	} {
		if _, err = tx.Exec(q, id); err != nil {
			return err
		}
	}
	return nil
}

// sessionsHoldingNative lists the online sessions of one harness on one node
// that may have loaded a native MCP entry: all of them, or only those of the
// project when project is not empty. Owner sessions are never listed.
func (s *Store) sessionsHoldingNative(nodeID, harness, project string) ([]string, error) {
	rows, err := s.db.Query(`SELECT s.id FROM sessions s LEFT JOIN profiles p ON p.session_id=s.id
		WHERE s.node_id=? AND s.harness=? AND s.harness<>? AND s.online=1
		AND (?='' OR COALESCE(NULLIF(s.repo_project,''),p.project,'')=?) ORDER BY s.id`, nodeID, harness, protocol.OwnerHarness, project, project)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}
