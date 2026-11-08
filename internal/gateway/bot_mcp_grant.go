package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/niparis/agent-net/internal/protocol"
)

// A bot can be granted the use of one shared MCP server ("mcp server <name>").
// It then sees that server's allowed tools in tools/list, named
// `<server>__<tool>` like the sessions see them, and calls them through the
// same path as a session: the gateway forwards the call to the host daemon,
// which holds the server's credentials. The grants are read from the database
// on every request, so a removal takes effect at once. Ungranted tools stay
// unknown, as before.

// AppGrants returns the grants of an active app.
func (s *Store) AppGrants(id string) ([]string, error) {
	var raw string
	if err := s.db.QueryRow("SELECT grants FROM apps WHERE id=? AND revoked_at IS NULL", id).Scan(&raw); err != nil {
		return nil, err
	}
	var grants []string
	return grants, json.Unmarshal([]byte(raw), &grants)
}

// botGrantedServers returns the enabled shared servers the app may use.
func (g *Gateway) botGrantedServers(appID string) ([]protocol.MCPServer, error) {
	grants, err := g.store.AppGrants(appID)
	if err != nil {
		return nil, err
	}
	var out []protocol.MCPServer
	for _, grant := range grants {
		name, ok := protocol.ParseMCPServerGrant(grant)
		if !ok {
			continue
		}
		srv, found, err := g.store.MCPServer(name)
		if err != nil {
			return nil, err
		}
		if found && srv.Enabled {
			srv.HostOnline = g.conn(srv.NodeID) != nil
			out = append(out, srv)
		}
	}
	return out, nil
}

func botRequestApp(req mcp.Request) string {
	if extra := req.GetExtra(); extra != nil && extra.TokenInfo != nil {
		return extra.TokenInfo.UserID
	}
	return ""
}

// botGrantTools adds the granted servers' tools to tools/list and answers
// their tools/call. Everything else goes on to the bot server unchanged.
func (g *Gateway) botGrantTools() mcp.Middleware {
	return func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
			switch method {
			case "tools/list":
				res, err := next(ctx, method, req)
				list, ok := res.(*mcp.ListToolsResult)
				if err != nil || !ok {
					return res, err
				}
				appID := botRequestApp(req)
				if appID == "" {
					return res, nil
				}
				servers, err := g.botGrantedServers(appID)
				if err != nil {
					return res, nil
				}
				for _, srv := range servers {
					for _, t := range srv.AllowedOnly(srv.Tools) {
						desc := fmt.Sprintf("[%s on %s] %s", srv.Name, srv.Hostname, t.Description)
						if !srv.HostOnline {
							desc = fmt.Sprintf("[%s on %s, unavailable: host %s offline] %s", srv.Name, srv.Hostname, srv.Hostname, t.Description)
						}
						list.Tools = append(list.Tools, &mcp.Tool{Name: srv.Name + "__" + t.Name, Description: desc, InputSchema: botObjectSchema(t.InputSchema)})
					}
				}
				return list, nil
			case "tools/call":
				call, ok := req.(*mcp.CallToolRequest)
				if !ok || call.Params == nil {
					break
				}
				server, tool, found := strings.Cut(call.Params.Name, "__")
				appID := botRequestApp(req)
				if !found || appID == "" {
					break
				}
				servers, err := g.botGrantedServers(appID)
				if err != nil {
					break
				}
				for _, srv := range servers {
					if srv.Name == server && srv.ToolAllowed(tool) {
						return g.botMCPServerCall(ctx, appID, srv, tool, call.Params.Arguments)
					}
				}
			}
			return next(ctx, method, req)
		}
	}
}

// botMCPServerCall counts the call against the app's call limit, then sends it
// to the host daemon the way a session's call goes.
func (g *Gateway) botMCPServerCall(ctx context.Context, appID string, srv protocol.MCPServer, tool string, args json.RawMessage) (mcp.Result, error) {
	if err := g.botAllow(appID, "call"); err != nil {
		return botToolError(err)
	}
	g.logLine("apps.mcp_server_call", "app", appID, "server", srv.Name, "tool", safeAppLog(tool))
	data, err := g.mcpForward(ctx, srv, protocol.MCPCall{Server: srv.Name, Tool: tool, Arguments: args}, false)
	if err != nil {
		return botToolError(err)
	}
	var res mcp.CallToolResult
	if err := json.Unmarshal(data.(json.RawMessage), &res); err != nil {
		return botToolError(fmt.Errorf("unreadable answer from the MCP server: %v", err))
	}
	return &res, nil
}

// botObjectSchema makes sure the schema a client sees is an object schema.
func botObjectSchema(raw json.RawMessage) any {
	var m map[string]any
	if json.Unmarshal(raw, &m) != nil || m == nil || m["type"] != "object" {
		return map[string]any{"type": "object", "properties": map[string]any{}}
	}
	return m
}
