package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// This file is the MCP registry on the wire. A daemon asks the gateway to add,
// list, test, enable, disable or remove servers, and to call a tool. The
// gateway never talks to an MCP server: the hosting node's daemon does. The
// gateway asks it (mcp.probe, mcp.invoke) and tells it what to run (mcp.sync).
// Every connected node hears mcp.changed when the registry or a tool list
// changes, so its sessions can refresh their tools.

const probeTimeout = 30 * time.Second

// request is nc.call with a time limit.
func (nc *nodeConn) request(ctx context.Context, typ string, params any, wait time.Duration) (json.RawMessage, error) {
	ctx, cancel := context.WithTimeout(ctx, wait)
	defer cancel()
	data, err := nc.call(ctx, typ, params)
	if errors.Is(err, context.DeadlineExceeded) {
		return nil, errors.New("host node did not answer in time")
	}
	return data, err
}

func (g *Gateway) conn(nodeID string) *nodeConn {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.conns[nodeID]
}

// serveMCP answers one mcp.* frame. It runs in its own goroutine because it
// may wait for another node while this node's read loop must keep going.
func (g *Gateway) serveMCP(ctx context.Context, nc *nodeConn, h protocol.Header, raw []byte) {
	data, err := g.handleMCP(ctx, nc, h.Type, raw)
	if err != nil {
		var rj Rejection
		if !errors.As(err, &rj) {
			g.logLine("error", "node", nc.id, "where", h.Type, "err", err)
		}
	}
	reply, eerr := protocol.EncodeResult(h.ID, data, err)
	if eerr != nil {
		g.logLine("error", "where", "encode result", "err", eerr)
		return
	}
	nc.write(ctx, reply)
}

func (g *Gateway) handleMCP(ctx context.Context, nc *nodeConn, typ string, raw []byte) (any, error) {
	st := g.store
	switch typ {
	case "mcp.add":
		p, err := decode[protocol.MCPAdd](raw)
		if err != nil {
			return nil, reject("bad mcp.add")
		}
		return g.mcpAdd(ctx, nc, p)

	case "mcp.import":
		p, err := decode[struct {
			protocol.MCPImportRequest
			Actor string `json:"actor"`
			Owner string `json:"owner"`
		}](raw)
		if err != nil || (p.Actor != "owner" && p.Actor != "cto") || !g.validToolOwner(nc, p.Owner) {
			return nil, reject("owner authority required")
		}
		if (p.Candidate == "") == (p.PlanID == "") {
			return nil, reject("import needs a candidate or a plan_id")
		}
		if p.Candidate != "" {
			return g.planMCPImport(ctx, p.Candidate, p.Actor)
		}
		return g.applyMCPImport(ctx, p.PlanID, p.Actor, p.Owner)

	case "mcp.native_remove":
		p, err := decode[struct {
			protocol.MCPNativeRemoveRequest
			Actor string `json:"actor"`
			Owner string `json:"owner"`
		}](raw)
		if err != nil || (p.Actor != "owner" && p.Actor != "cto") || !g.validToolOwner(nc, p.Owner) {
			return nil, reject("owner authority required")
		}
		n := 0
		for _, v := range []string{p.Candidate, p.PlanID, p.UndoPlanID} {
			if v != "" {
				n++
			}
		}
		if n != 1 {
			return nil, reject("native removal needs one of candidate, plan_id or undo_plan_id")
		}
		switch {
		case p.Candidate != "":
			return g.planMCPNativeRemove(ctx, p.Candidate, p.Actor)
		case p.PlanID != "":
			return g.applyMCPNativeRemove(ctx, p.PlanID, p.Actor)
		}
		return g.undoMCPNativeRemove(ctx, p.UndoPlanID, p.Actor)

	case "mcp.list":
		srvs, err := g.mcpList()
		if err != nil {
			return nil, err
		}
		return map[string]any{"servers": srvs}, nil

	case "mcp.catalog":
		p, err := decode[protocol.MCPCatalog](raw)
		if err != nil {
			return nil, reject("bad mcp.catalog")
		}
		project, err := g.callerProject(nc, p.SessionID)
		if err != nil {
			return nil, err
		}
		srvs, err := g.mcpCatalog(project)
		if err != nil {
			return nil, err
		}
		return map[string]any{"servers": srvs}, nil

	case "mcp.remove":
		p, err := decode[protocol.MCPName](raw)
		if err != nil {
			return nil, reject("bad mcp.remove")
		}
		srv, ok, err := st.MCPServer(p.Name)
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, reject("no MCP server named %q", p.Name)
		}
		if _, err := st.RemoveMCPServer(p.Name, nc.id, ""); err != nil {
			return nil, err
		}
		g.mcpChanged(ctx, srv.NodeID)
		return map[string]any{"removed": p.Name}, nil

	case "mcp.enable":
		p, err := decode[protocol.MCPSetEnabled](raw)
		if err != nil {
			return nil, reject("bad mcp.enable")
		}
		srv, ok, err := st.MCPServer(p.Name)
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, reject("no MCP server named %q", p.Name)
		}
		if _, err := st.SetMCPEnabled(p.Name, p.Enabled, nc.id, ""); err != nil {
			return nil, err
		}
		g.mcpChanged(ctx, srv.NodeID)
		return map[string]any{"name": p.Name, "enabled": p.Enabled}, nil

	case "mcp.test":
		p, err := decode[protocol.MCPName](raw)
		if err != nil {
			return nil, reject("bad mcp.test")
		}
		return g.mcpTest(ctx, p.Name)

	case "mcp.report":
		p, err := decode[protocol.MCPReport](raw)
		if err != nil {
			return nil, reject("bad mcp.report")
		}
		srv, ok, err := st.MCPServer(p.Name)
		if err != nil {
			return nil, err
		}
		if !ok || srv.NodeID != nc.id {
			return nil, reject("MCP server %q is not hosted on this node", p.Name)
		}
		changed, err := st.SetMCPReport(p.Name, p.Health, p.Detail, p.Tools)
		if err != nil {
			return nil, err
		}
		if changed {
			g.mcpChanged(ctx, "")
		}
		return nil, nil

	case "mcp.call":
		p, err := decode[protocol.MCPCall](raw)
		if err != nil {
			return nil, reject("bad mcp.call")
		}
		return g.mcpCall(ctx, nc, p)
	}
	return nil, reject("unknown frame type %q", typ)
}

func (g *Gateway) mcpList() ([]protocol.MCPServer, error) {
	srvs, err := g.store.MCPServers()
	if err != nil {
		return nil, err
	}
	assigned, err := g.store.MCPAssignments()
	if err != nil {
		return nil, err
	}
	for i := range srvs {
		srvs[i].HostOnline = g.conn(srvs[i].NodeID) != nil
		srvs[i].Scope, srvs[i].Projects = "global", nil
		if a, ok := assigned[srvs[i].Name]; ok {
			srvs[i].Scope, srvs[i].Projects = a.Scope, a.Projects
		}
	}
	return srvs, nil
}

func (g *Gateway) mcpAdd(ctx context.Context, nc *nodeConn, p protocol.MCPAdd) (any, error) {
	srv := p.Server
	if err := srv.Validate(); err != nil {
		return nil, reject("%v", err)
	}
	host := nc.id
	if p.On != "" {
		id, err := g.store.NodeByHostOrID(p.On)
		if err != nil {
			return nil, err
		}
		host = id
	}
	if _, ok, err := g.store.MCPServer(srv.Name); err != nil {
		return nil, err
	} else if ok {
		return nil, reject("an MCP server named %q already exists", srv.Name)
	}
	hc := g.conn(host)
	if hc == nil {
		return nil, reject("the host node is offline, so the server cannot be checked")
	}
	srv.NodeID, srv.Enabled = host, true
	srv.AddedByNode, srv.AddedBySession, srv.AddedByHarness = nc.id, p.AddedBySession, p.AddedByHarness
	srv.Tools, srv.Health, srv.HealthDetail = nil, "", ""

	tools, err := probe(ctx, hc, srv)
	if err != nil {
		g.store.Event("mcp.refused", nc.id, p.AddedBySession, "", srv.Name+": "+err.Error())
		return nil, reject("could not use the server: %v", err)
	}
	srv.Tools, srv.Health = tools, protocol.HealthOK
	if err := g.store.AddMCPServer(srv); err != nil {
		return nil, err
	}
	g.mcpChanged(ctx, host)
	out, _, err := g.store.MCPServer(srv.Name)
	if err != nil {
		return nil, err
	}
	out.HostOnline = true
	return map[string]any{"server": out}, nil
}

// probe asks the host to connect to the server and list its tools.
func probe(ctx context.Context, hc *nodeConn, srv protocol.MCPServer) ([]protocol.MCPTool, error) {
	data, err := hc.request(ctx, "mcp.probe", protocol.MCPProbe{Server: srv}, probeTimeout+5*time.Second)
	if err != nil {
		return nil, err
	}
	var r struct {
		Tools []protocol.MCPTool `json:"tools"`
	}
	if err := json.Unmarshal(data, &r); err != nil {
		return nil, err
	}
	return r.Tools, nil
}

func (g *Gateway) mcpTest(ctx context.Context, name string) (any, error) {
	srv, ok, err := g.store.MCPServer(name)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, reject("no MCP server named %q", name)
	}
	hc := g.conn(srv.NodeID)
	if hc == nil {
		g.store.SetMCPReport(name, protocol.HealthError, "host node is offline", nil)
		return nil, reject("the host node %s is offline", srv.Hostname)
	}
	tools, err := probe(ctx, hc, srv)
	if err != nil {
		g.store.SetMCPReport(name, protocol.HealthError, err.Error(), nil)
		return map[string]any{"ok": false, "detail": err.Error()}, nil
	}
	changed, _ := g.store.SetMCPReport(name, protocol.HealthOK, "", tools)
	if changed {
		g.mcpChanged(ctx, "")
	}
	return map[string]any{"ok": true, "tools": tools}, nil
}

func (g *Gateway) mcpCall(ctx context.Context, nc *nodeConn, p protocol.MCPCall) (any, error) {
	srv, ok, err := g.store.MCPServer(p.Server)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, reject("no MCP server named %q", p.Server)
	}
	// Authorize here, on every call, from the gateway's own records: a client
	// that cached the tool earlier gets no more than its project may see now.
	if !g.validToolOwner(nc, p.Owner) {
		project, err := g.callerProject(nc, p.SessionID)
		if err != nil {
			return nil, err
		}
		a, err := g.store.MCPAssignment(p.Server)
		if err != nil {
			return nil, err
		}
		if !mcpVisible(a, project) {
			g.store.Event("mcp.refused", nc.id, p.SessionID, "", p.Server+": not assigned to project "+project)
			if project == "" {
				return nil, reject("MCP server %q is not assigned to this project (the session has no project)", p.Server)
			}
			return nil, reject("MCP server %q is not assigned to this project", p.Server)
		}
	}
	if !srv.Enabled {
		return nil, reject("MCP server %q is disabled", p.Server)
	}
	if !srv.ToolAllowed(p.Tool) {
		return nil, reject("tool %q is not exposed by MCP server %q", p.Tool, p.Server)
	}
	if len(p.Arguments) > protocol.MCPMaxArgsBytes {
		return nil, reject("arguments are larger than %d bytes", protocol.MCPMaxArgsBytes)
	}
	hc := g.conn(srv.NodeID)
	if hc == nil {
		return nil, reject("host %s offline: MCP server %q runs there and is unavailable until it returns", srv.Hostname, p.Server)
	}
	inv := protocol.MCPInvoke{Server: p.Server, Tool: p.Tool, Arguments: p.Arguments,
		TimeoutSec: int(srv.Timeout() / time.Second)}
	data, err := hc.request(ctx, "mcp.invoke", inv, srv.Timeout()+10*time.Second)
	if err != nil {
		return nil, reject("%s on %s: %v", p.Server, srv.Hostname, err)
	}
	return json.RawMessage(data), nil
}

// sendMCPSync tells a node which enabled servers it must run. nodeID "" means
// every connected node.
func (g *Gateway) sendMCPSync(ctx context.Context, nc *nodeConn) {
	srvs, err := g.store.MCPServers()
	if err != nil {
		g.logLine("error", "where", "mcp sync", "err", err)
		return
	}
	var mine []protocol.MCPServer
	for _, s := range srvs {
		if s.NodeID == nc.id && s.Enabled {
			s.Tools = nil
			mine = append(mine, s)
		}
	}
	frame, err := protocol.EncodeRequest("mcp.sync", protocol.NewID("d"), protocol.MCPSync{Servers: mine})
	if err == nil {
		nc.write(ctx, frame)
	}
}

// mcpChanged re-syncs the host (when given) and tells every node to refresh.
func (g *Gateway) mcpChanged(ctx context.Context, hostID string) {
	if hostID != "" {
		if hc := g.conn(hostID); hc != nil {
			g.sendMCPSync(ctx, hc)
		}
	}
	g.mu.Lock()
	conns := make([]*nodeConn, 0, len(g.conns))
	for _, c := range g.conns {
		conns = append(conns, c)
	}
	g.mu.Unlock()
	for _, c := range conns {
		if frame, err := protocol.EncodeRequest("mcp.changed", protocol.NewID("d"), nil); err == nil {
			c.write(ctx, frame)
		}
	}
}
