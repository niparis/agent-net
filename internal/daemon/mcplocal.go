package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// The daemon's part of the MCP registry: frames from the gateway (what to
// host, probe and invoke), and the local socket methods mcp_* that agents and
// the CLI use. Registry state lives in the gateway; the daemon forwards.

type waitKey struct{}

// withCallWait lets one gateway call wait longer than the default 20 s.
func withCallWait(ctx context.Context, d time.Duration) context.Context {
	return context.WithValue(ctx, waitKey{}, d)
}

func callWait(ctx context.Context) time.Duration {
	if d, ok := ctx.Value(waitKey{}).(time.Duration); ok {
		return d
	}
	return 20 * time.Second
}

// onMCPFrame handles a request from the gateway.
func (d *Daemon) onMCPFrame(ctx context.Context, l *link, h protocol.Header, raw []byte) {
	var data any
	var err error
	switch h.Type {
	case "mcp.sync":
		var p protocol.MCPSync
		if err = json.Unmarshal(raw, &p); err == nil {
			d.mcp.reconcile(p.Servers)
		}
	case "mcp.changed":
		d.broadcastLocal("mcp_changed")
		d.wakeTools()
	case "mcp.probe":
		var p protocol.MCPProbe
		if err = json.Unmarshal(raw, &p); err == nil {
			var tools []protocol.MCPTool
			if tools, err = d.mcp.probe(ctx, p.Server); err == nil {
				data = map[string]any{"tools": tools}
			}
		}
	case "mcp.invoke":
		var p protocol.MCPInvoke
		if err = json.Unmarshal(raw, &p); err == nil {
			var res json.RawMessage
			if res, err = d.mcp.invoke(ctx, p); err == nil {
				data = res
			}
		}
	}
	if h.Type == "mcp.sync" || h.Type == "mcp.changed" {
		// The gateway does not wait for these.
		return
	}
	if res, eerr := protocol.EncodeResult(h.ID, data, err); eerr == nil {
		l.write(ctx, res)
	}
}

// broadcastLocal tells every bound local client (not the CLI) that something
// changed. Clients then ask for what they need.
func (d *Daemon) broadcastLocal(method string) {
	if d.ln == nil {
		return
	}
	d.ln.mu.Lock()
	conns := make([]*localConn, 0, len(d.ln.conns))
	for c := range d.ln.conns {
		conns = append(conns, c)
	}
	d.ln.mu.Unlock()
	for _, c := range conns {
		c.mu.Lock()
		bound := c.ls != nil
		c.mu.Unlock()
		if bound {
			c.writeJSON(map[string]any{"method": method, "params": map[string]any{}})
		}
	}
}

// dispatchMCP serves the mcp_* methods. handled is false for other methods.
func (lc *localConn) dispatchMCP(ctx context.Context, client, method string, raw json.RawMessage) (res any, handled bool, err error) {
	if !strings.HasPrefix(method, "mcp_") {
		return nil, false, nil
	}
	d := lc.d
	handled = true
	switch method {
	case "mcp_list":
		res, err = d.call(ctx, "mcp.list", nil)

	case "mcp_tools":
		// Enabled servers with their tools: what a session registers. The
		// gateway filters them for the bound session's project.
		var sid string
		if sid, err = lc.boundID(); err != nil {
			return
		}
		res, err = d.call(ctx, "mcp.catalog", protocol.MCPCatalog{SessionID: sid})

	case "mcp_add":
		var p protocol.MCPAdd
		if json.Unmarshal(raw, &p) != nil {
			return nil, true, errors.New("bad mcp_add params")
		}
		if client == "cli" {
			p.AddedByHarness, p.AddedBySession = "cli", ""
		} else {
			// An agent's server is hosted on its own machine, whatever it says.
			p.On = ""
			lc.mu.Lock()
			ls := lc.ls
			lc.mu.Unlock()
			if ls == nil {
				return nil, true, errors.New("this method needs a bound session")
			}
			p.AddedBySession = d.currentSessionID(ls)
			p.AddedByHarness = ls.info.Harness
		}
		p.Server.NodeID, p.Server.Enabled = "", true
		if err = p.Server.Validate(); err != nil {
			return nil, true, err
		}
		res, err = d.call(withCallWait(ctx, 2*time.Minute), "mcp.add", p)

	case "mcp_remove", "mcp_test":
		var p protocol.MCPName
		if json.Unmarshal(raw, &p) != nil || p.Name == "" {
			return nil, true, errors.New(method + " needs name")
		}
		res, err = d.call(withCallWait(ctx, 2*time.Minute), strings.Replace(method, "mcp_", "mcp.", 1), p)

	case "mcp_enable":
		var p protocol.MCPSetEnabled
		if json.Unmarshal(raw, &p) != nil || p.Name == "" {
			return nil, true, errors.New("mcp_enable needs name and enabled")
		}
		res, err = d.call(ctx, "mcp.enable", p)

	case "mcp_call":
		var p protocol.MCPCall
		if json.Unmarshal(raw, &p) != nil || p.Server == "" || p.Tool == "" {
			return nil, true, errors.New("mcp_call needs server and tool")
		}
		// Who calls is decided here, never taken from the request: a bound
		// session calls as itself, and the owner's own command line calls
		// with the owner's authority. A command line run from inside an agent
		// session has neither and sees only global servers.
		p.SessionID, p.Owner = "", ""
		if client == "cli" {
			if lc.operatorGate(method) == nil {
				p.Owner = d.ownerSessionID()
			}
		} else if p.SessionID, err = lc.boundID(); err != nil {
			return nil, true, err
		}
		res, err = d.call(withCallWait(ctx, protocol.MCPMaxTimeout+30*time.Second), "mcp.call", p)

	default:
		return nil, true, errors.New("unknown method " + method)
	}
	return
}
