package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/niparis/agent-net/internal/protocol"
)

// ctoTool is the only entry to the private CTO channel. Check the process
// tree on every call, including after a server restart.
func (lc *localConn) ctoTool(ctx context.Context, raw json.RawMessage) (any, error) {
	lc.mu.Lock()
	pid := lc.peerPID
	client := lc.client
	lc.mu.Unlock()
	if client != "cto-mcp" || !lc.d.isCTOPeer(pid) {
		return nil, errors.New("CTO tool requires the CTO server's process tree")
	}
	var q struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	}
	if json.Unmarshal(raw, &q) != nil {
		return nil, errors.New("bad CTO tool request")
	}
	return lc.d.runCTOTool(ctx, q.Name, q.Arguments)
}

func (d *Daemon) runCTOTool(ctx context.Context, name string, raw json.RawMessage) (any, error) {
	var args map[string]any
	if len(raw) > 0 && string(raw) != "null" && json.Unmarshal(raw, &args) != nil {
		return nil, errors.New("bad CTO tool arguments")
	}
	if args == nil {
		args = map[string]any{}
	}
	switch name {
	case "graph_get", "charter_set", "charter_history", "node_add", "node_remove", "edge_set", "edge_remove", "edge_history", "graph_undo":
		delete(args, "by")
		if name == "graph_undo" {
			args["history_id"] = args["id"]
			delete(args, "id")
		}
		args["by"] = "cto"
		if name != "graph_get" && name != "charter_history" && name != "edge_history" {
			return d.ctoAttributedCall(ctx, strings.ReplaceAll(name, "_", "."), args)
		}
		return d.call(ctx, strings.ReplaceAll(name, "_", "."), args)
	case "agents":
		return d.call(ctx, "query.agents", args)
	case "feed":
		return d.call(ctx, "query.events", args)
	case "workers":
		return d.workersView(ctx, protocol.WorkersQuery{Recent: true})
	case "needs_you":
		data, err := d.call(ctx, "query.agents", protocol.QueryAgents{OnlineOnly: true})
		if err != nil {
			return nil, err
		}
		var a protocol.AgentsResult
		if err = json.Unmarshal(data, &a); err != nil {
			return nil, err
		}
		out := []protocol.AgentView{}
		for _, v := range a.Agents {
			if v.Waiting != nil {
				out = append(out, v)
			}
		}
		return protocol.AgentsResult{Agents: out}, nil
	case "send":
		var p struct {
			To   string `json:"to"`
			Body string `json:"body"`
		}
		if json.Unmarshal(raw, &p) != nil || p.To == "" || p.Body == "" {
			return nil, errors.New("send needs to and body")
		}
		sid := d.ownerSessionID()
		if sid == "" {
			return nil, errNotConnected
		}
		to, err := d.resolveTo(ctx, p.To)
		if err != nil {
			return nil, err
		}
		return d.send(ctx, protocol.MsgSend{Sender: sid, Recipient: to, Type: protocol.TypeRequest, Body: p.Body, FromCTO: true})
	case "worker_start":
		var p protocol.WorkerStartRequest
		if json.Unmarshal(raw, &p) != nil {
			return nil, errors.New("bad worker request")
		}
		sid := d.ownerSessionID()
		if sid == "" {
			return nil, errNotConnected
		}
		return d.workerStartCall(ctx, sid, p, false, "", true)
	}
	return nil, errors.New("unknown CTO tool")
}

// Send the capability check and the mutation on the same gateway link, so a
// reconnect to an older gateway cannot strip the attribution in between.
func (d *Daemon) ctoAttributedCall(ctx context.Context, typ string, params any) (json.RawMessage, error) {
	d.mu.Lock()
	l := d.lk
	d.mu.Unlock()
	if l == nil {
		return nil, errNotConnected
	}
	b, err := l.call(ctx, "cto.capabilities", nil)
	if err != nil {
		return nil, errors.New("gateway cannot verify CTO attribution; upgrade the gateway")
	}
	var caps struct {
		Attribution bool `json:"attribution"`
	}
	if json.Unmarshal(b, &caps) != nil || !caps.Attribution {
		return nil, errors.New("gateway cannot verify CTO attribution; upgrade the gateway")
	}
	return l.call(ctx, typ, params)
}
