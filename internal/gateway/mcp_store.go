package gateway

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"errors"
	"sort"

	"github.com/niparis/agent-net/internal/protocol"
)

// The MCP registry. A row keeps how to start or reach a server (never a
// secret: headers name an env var or a file), who hosts it, who added it, and
// the last health and tool list its host reported.

// specOf is the part of a server that is stored as JSON in the spec column.
func specOf(s protocol.MCPServer) []byte {
	keep := protocol.MCPServer{Transport: s.Transport, Command: s.Command, Args: s.Args, Env: s.Env,
		URL: s.URL, Headers: s.Headers, TimeoutSec: s.TimeoutSec}
	b, _ := json.Marshal(keep)
	return b
}

const mcpCols = `m.name, m.node_id, COALESCE(n.hostname, ''), m.spec, m.enabled, m.added_by_node,
	m.added_by_session, m.added_by_harness, m.added_at, m.health, m.health_detail, m.health_at, m.tools`

func scanMCP(sc interface{ Scan(...any) error }) (protocol.MCPServer, error) {
	var s protocol.MCPServer
	var spec, tools string
	var enabled int
	err := sc.Scan(&s.Name, &s.NodeID, &s.Hostname, &spec, &enabled, &s.AddedByNode, &s.AddedBySession,
		&s.AddedByHarness, &s.AddedAt, &s.Health, &s.HealthDetail, &s.HealthAt, &tools)
	if err != nil {
		return s, err
	}
	var kept protocol.MCPServer
	if err := json.Unmarshal([]byte(spec), &kept); err != nil {
		return s, err
	}
	s.Transport, s.Command, s.Args, s.Env = kept.Transport, kept.Command, kept.Args, kept.Env
	s.URL, s.Headers, s.TimeoutSec = kept.URL, kept.Headers, kept.TimeoutSec
	s.Enabled = enabled == 1
	if err := json.Unmarshal([]byte(tools), &s.Tools); err != nil {
		return s, err
	}
	return s, nil
}

// AddMCPServer stores a new server. The name must be free.
func (s *Store) AddMCPServer(srv protocol.MCPServer) error {
	if err := srv.Validate(); err != nil {
		return reject("%v", err)
	}
	sort.Slice(srv.Tools, func(i, j int) bool { return srv.Tools[i].Name < srv.Tools[j].Name })
	tools, _ := json.Marshal(srv.Tools)
	if srv.Tools == nil {
		tools = []byte("[]")
	}
	if srv.Health == "" {
		srv.Health = protocol.HealthUnknown
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	_, err = tx.Exec(`INSERT INTO mcp_servers (name, node_id, spec, enabled, added_by_node, added_by_session,
		added_by_harness, added_at, health, health_detail, health_at, tools) VALUES (?,?,?,?,?,?,?,?,?,?,?,?)`,
		srv.Name, srv.NodeID, specOf(srv), 1, srv.AddedByNode, srv.AddedBySession, srv.AddedByHarness,
		s.now().UnixNano(), srv.Health, srv.HealthDetail, s.now().UnixNano(), tools)
	if err != nil {
		if _, ok, _ := s.MCPServer(srv.Name); ok {
			return reject("an MCP server named %q already exists", srv.Name)
		}
		return err
	}
	// A new server is a tool item with no assignment, which means global.
	if err = ensureMCPItemTx(tx, srv.Name); err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	s.Event("mcp.added", srv.AddedByNode, srv.AddedBySession, "", srv.Name+" on "+srv.NodeID)
	return nil
}

// MCPServer returns one server by name.
func (s *Store) MCPServer(name string) (protocol.MCPServer, bool, error) {
	srv, err := scanMCP(s.db.QueryRow(`SELECT `+mcpCols+` FROM mcp_servers m LEFT JOIN nodes n ON n.id = m.node_id
		WHERE m.name = ?`, name))
	if errors.Is(err, sql.ErrNoRows) {
		return srv, false, nil
	}
	return srv, err == nil, err
}

// MCPServers lists every server, by name.
func (s *Store) MCPServers() ([]protocol.MCPServer, error) {
	rows, err := s.db.Query(`SELECT ` + mcpCols + ` FROM mcp_servers m LEFT JOIN nodes n ON n.id = m.node_id ORDER BY m.name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []protocol.MCPServer
	for rows.Next() {
		srv, err := scanMCP(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, srv)
	}
	return out, rows.Err()
}

// RemoveMCPServer deletes a server and reports whether it existed.
func (s *Store) RemoveMCPServer(name, byNode, bySession string) (bool, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	if err = forgetMCPItemTx(tx, name); err != nil {
		return false, err
	}
	res, err := tx.Exec(`DELETE FROM mcp_servers WHERE name = ?`, name)
	if err != nil {
		return false, err
	}
	if err = tx.Commit(); err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	if n == 1 {
		s.Event("mcp.removed", byNode, bySession, "", name)
	}
	return n == 1, nil
}

// SetMCPEnabled turns a server on or off and reports whether it existed.
func (s *Store) SetMCPEnabled(name string, enabled bool, byNode, bySession string) (bool, error) {
	v := 0
	if enabled {
		v = 1
	}
	res, err := s.db.Exec(`UPDATE mcp_servers SET enabled = ? WHERE name = ?`, v, name)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	if n == 1 {
		kind := "mcp.disabled"
		if enabled {
			kind = "mcp.enabled"
		}
		s.Event(kind, byNode, bySession, "", name)
	}
	return n == 1, nil
}

// SetMCPReport records a host's health and tool list for a server. It returns
// whether the tool list changed. When tools is nil the cached list is kept
// (a failed check does not wipe it).
func (s *Store) SetMCPReport(name, health, detail string, tools []protocol.MCPTool) (toolsChanged bool, err error) {
	srv, ok, err := s.MCPServer(name)
	if err != nil || !ok {
		return false, err
	}
	if tools == nil {
		tools = srv.Tools
	}
	sort.Slice(tools, func(i, j int) bool { return tools[i].Name < tools[j].Name })
	old, _ := json.Marshal(srv.Tools)
	cur, _ := json.Marshal(tools)
	if len(tools) == 0 {
		cur = []byte("[]")
	}
	if len(srv.Tools) == 0 {
		old = []byte("[]")
	}
	toolsChanged = !bytes.Equal(old, cur)
	_, err = s.db.Exec(`UPDATE mcp_servers SET health = ?, health_detail = ?, health_at = ?, tools = ? WHERE name = ?`,
		health, detail, s.now().UnixNano(), cur, name)
	return toolsChanged, err
}

// NodeByHostOrID finds a node by its ID or, if that fails, by host name. A host
// name shared by several nodes is refused.
func (s *Store) NodeByHostOrID(s2 string) (string, error) {
	var id string
	err := s.db.QueryRow(`SELECT id FROM nodes WHERE id = ?`, s2).Scan(&id)
	if err == nil {
		return id, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return "", err
	}
	rows, err := s.db.Query(`SELECT id FROM nodes WHERE hostname = ?`, s2)
	if err != nil {
		return "", err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var x string
		rows.Scan(&x)
		ids = append(ids, x)
	}
	switch len(ids) {
	case 0:
		return "", reject("no node or host named %q", s2)
	case 1:
		return ids[0], nil
	}
	return "", reject("%d nodes are named %q; use a node ID", len(ids), s2)
}
