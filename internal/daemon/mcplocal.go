package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

const mcpAdminOwnerOnly = "adding or removing a shared MCP server is owner-only: ask the owner to do it in the TUI, or ask the CTO to use mcp_change"

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
		// A node hears mcp.sync after every reconnect and whenever the servers
		// it hosts change: a fresh catalog read must follow.
		d.catalog.invalidate()
	case "mcp.changed":
		// Drop the kept catalog before the sessions hear the push, so their
		// next read comes from the gateway.
		d.catalog.invalidate()
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
	case "mcp.feature":
		var p protocol.MCPFeatureInvoke
		if err = json.Unmarshal(raw, &p); err == nil {
			var res json.RawMessage
			if res, err = d.mcp.feature(ctx, p); err == nil {
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

// broadcastLocal tells every bound local client (not the CLI, and including the thread-less connection of a Codex process) that something
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
		// The thread-less connection of a Codex process lists tools too.
		bound := c.ls != nil || c.client == "mcp"
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
		//
		// The one connection of a Codex process that has no thread (it lists
		// tools for every thread of the app server) is not bound. It has no
		// project, so it gets the global servers only.
		var sid, project string
		lc.mu.Lock()
		unboundCodex := lc.ls == nil && lc.client == "mcp"
		lc.mu.Unlock()
		if !unboundCodex {
			if sid, err = lc.boundID(); err != nil {
				return
			}
			project = lc.sessionProject()
		}
		res, err = d.mcpTools(ctx, sid, project)

	case "mcp_add":
		if client != "cli" {
			return nil, true, errors.New(mcpAdminOwnerOnly)
		}
		var p protocol.MCPAdd
		if json.Unmarshal(raw, &p) != nil {
			return nil, true, errors.New("bad mcp_add params")
		}
		p.AddedByHarness, p.AddedBySession = "cli", ""
		p.Server.NodeID, p.Server.Enabled = "", true
		if err = p.Server.Validate(); err != nil {
			return nil, true, err
		}
		res, err = d.call(withCallWait(ctx, 2*time.Minute), "mcp.add", p)

	case "mcp_remove", "mcp_test":
		if method == "mcp_remove" && client != "cli" {
			return nil, true, errors.New(mcpAdminOwnerOnly)
		}
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

	case "mcp_feature":
		// Resources and prompts: the caller is decided as for mcp_call. The
		// one connection of a Codex process that has no thread is not bound;
		// like its mcp_tools, it gets the global servers only.
		var p protocol.MCPFeature
		if json.Unmarshal(raw, &p) != nil {
			return nil, true, errors.New("bad mcp_feature params")
		}
		if err = p.Check(); err != nil {
			return nil, true, err
		}
		p.SessionID, p.Owner = "", ""
		lc.mu.Lock()
		unboundCodex := lc.ls == nil && lc.client == "mcp"
		lc.mu.Unlock()
		switch {
		case client == "cli":
			if lc.operatorGate(method) == nil {
				p.Owner = d.ownerSessionID()
			}
		case !unboundCodex:
			if p.SessionID, err = lc.boundID(); err != nil {
				return nil, true, err
			}
		}
		res, err = d.call(withCallWait(ctx, protocol.MCPMaxTimeout+30*time.Second), "mcp.feature", p)

	default:
		return nil, true, errors.New("unknown method " + method)
	}
	return
}

// sessionProject returns the project of the connection's bound session, as the
// daemon knows it, or "" for an unbound connection.
func (lc *localConn) sessionProject() string {
	lc.mu.Lock()
	ls := lc.ls
	lc.mu.Unlock()
	if ls == nil {
		return ""
	}
	lc.d.mu.Lock()
	defer lc.d.mu.Unlock()
	return ls.info.Repo.Project
}

// mcpTools answers a session's tool-list read from the kept catalog, keyed by
// the project that decides what the gateway lists. It reads the gateway only
// when no answer is kept for that project.
func (d *Daemon) mcpTools(ctx context.Context, sid, project string) (json.RawMessage, error) {
	key := "global"
	switch {
	case sid == "":
		// An unbound Codex connection: global servers only.
	case project != "":
		key = "project:" + project
	default:
		// The gateway may still give this session a project from its profile.
		// Keep the answer to itself, because the daemon cannot know it.
		key = "session:" + sid
	}
	return d.catalog.getOrLoad(ctx, key, func(ctx context.Context) (json.RawMessage, error) {
		return d.call(ctx, "mcp.catalog", protocol.MCPCatalog{SessionID: sid})
	})
}
