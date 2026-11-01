package daemon

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/niparis/agent-net/internal/protocol"
)

// These are daemon methods used by the matching ordinary MCP tools. A read
// without a source fails closed. Do not use an owner/CLI connection here.
var ctoReadSources = map[string]string{
	"agents": "list", "nodes": "nodes", "projects": "projects", "get": "get",
	"messages": "messages", "message_status": "message_status", "mcp_list": "mcp_list",
	"worker_profile_list": protocol.OpWorkerProfiles,
	// The owner made the whole graph public to agents. The CTO reads the same
	// public view with its own caller identity.
	"graph": "agent_graph",
}

func (d *Daemon) ctoReadConn() *localConn {
	id := d.ctoSessionID()
	if id == "" {
		id = "cto-unregistered:" + d.cfg.NodeID
	}
	return &localConn{d: d, client: "mcp", ls: &localSession{sessionID: id}}
}

func (d *Daemon) readCTOTool(ctx context.Context, name string, raw json.RawMessage) (any, error) {
	switch name {
	case "charter_guide":
		return protocol.CharterGuideText(), nil
	case "cto_events":
		var q struct {
			Index int `json:"index"`
		}
		if len(raw) > 0 && json.Unmarshal(raw, &q) != nil {
			return nil, errors.New("bad CTO timeline arguments")
		}
		return d.ctoEvents(ctx, q.Index)
	}
	source, ok := ctoReadSources[name]
	if !ok {
		return nil, errors.New("CTO read has no ordinary-agent source")
	}
	return d.ctoReadConn().dispatch(ctx, source, raw)
}
