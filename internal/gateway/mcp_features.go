package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// Resources and prompts of registered servers (mcp.feature). The access rules
// are those of a tool call: the caller is the owner, or a session whose
// project may see the server now, and the server is enabled. A list without a
// server goes to every server the caller may see, at the same time; a host
// that is offline or fails is named in the answer, and the other servers still
// list. A read or a prompt goes to the one server it names.

func (g *Gateway) mcpFeature(ctx context.Context, nc *nodeConn, p protocol.MCPFeature) (any, error) {
	if err := p.Check(); err != nil {
		return nil, reject("%v", err)
	}
	if !protocol.MCPListOp(p.Op) {
		srv, err := g.mcpAccess(nc, p.Server, p.SessionID, p.Owner)
		if err != nil {
			return nil, err
		}
		hc := g.conn(srv.NodeID)
		if hc == nil {
			return nil, reject("host %s offline: MCP server %q runs there and is unavailable until it returns", srv.Hostname, p.Server)
		}
		inv := protocol.MCPFeatureInvoke{Op: p.Op, Server: p.Server, URI: p.URI, Name: p.Name, Arguments: p.Arguments,
			TimeoutSec: int(srv.Timeout() / time.Second)}
		data, err := hc.request(ctx, "mcp.feature", inv, srv.Timeout()+10*time.Second)
		if err != nil {
			return nil, reject("%s on %s: %v", p.Server, srv.Hostname, err)
		}
		return json.RawMessage(data), nil
	}

	var srvs []protocol.MCPServer
	if p.Server != "" {
		srv, err := g.mcpAccess(nc, p.Server, p.SessionID, p.Owner)
		if err != nil {
			return nil, err
		}
		srv.HostOnline = g.conn(srv.NodeID) != nil
		srvs = []protocol.MCPServer{srv}
	} else if g.validToolOwner(nc, p.Owner) {
		all, err := g.mcpList()
		if err != nil {
			return nil, err
		}
		for _, s := range all {
			if s.Enabled {
				srvs = append(srvs, s)
			}
		}
	} else {
		project, err := g.callerProject(nc, p.SessionID)
		if err != nil {
			return nil, err
		}
		if srvs, err = g.mcpCatalog(project); err != nil {
			return nil, err
		}
	}
	out := make([]protocol.MCPServerItems, len(srvs))
	var wg sync.WaitGroup
	for i, srv := range srvs {
		out[i] = protocol.MCPServerItems{Server: srv.Name, Host: srv.Hostname}
		hc := g.conn(srv.NodeID)
		if hc == nil {
			out[i].Error = "host " + srv.Hostname + " offline: the server is unavailable until it returns"
			continue
		}
		wg.Add(1)
		go func(i int, srv protocol.MCPServer) {
			defer wg.Done()
			inv := protocol.MCPFeatureInvoke{Op: p.Op, Server: srv.Name, TimeoutSec: int(protocol.MCPListTimeout / time.Second)}
			data, err := hc.request(ctx, "mcp.feature", inv, protocol.MCPListTimeout+5*time.Second)
			if err != nil {
				out[i].Error = err.Error()
				return
			}
			var r struct {
				Items json.RawMessage `json:"items"`
			}
			if json.Unmarshal(data, &r) != nil || len(r.Items) == 0 || r.Items[0] != '[' {
				out[i].Error = "unreadable answer from the host"
				return
			}
			out[i].Items = r.Items
		}(i, srv)
	}
	wg.Wait()
	capServerItems(out, protocol.MCPMaxListAnswerBytes)
	return map[string]any{"servers": out}, nil
}

// capServerItems keeps the items of all servers under max bytes, in order. A
// server past the limit keeps its name and gets the reason instead.
func capServerItems(out []protocol.MCPServerItems, max int) {
	total := 0
	for i := range out {
		if len(out[i].Items) == 0 {
			continue
		}
		if total+len(out[i].Items) > max {
			out[i].Items = nil
			out[i].Error = fmt.Sprintf("left out: the lists of all servers together are larger than %d bytes", max)
			continue
		}
		total += len(out[i].Items)
	}
}
