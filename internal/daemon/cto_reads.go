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
}

func (d *Daemon) ctoReadConn() *localConn {
	// The private CTO has no network session until design part 6. Keep its
	// caller distinct from the owner and every gateway session; reads requiring
	// a registered caller fail closed in the meantime. Never borrow the owner ID.
	var native string
	// Use the active private conversation as the caller. The prefix cannot be a
	// gateway session ID and cannot alias an ordinary session or the owner.
	_ = d.st.db.QueryRow(`SELECT CASE active_harness WHEN 'claude' THEN claude_session_id ELSE session_id END FROM cto WHERE id=1`).Scan(&native)
	return &localConn{d: d, client: "mcp", ls: &localSession{sessionID: "cto-private:" + d.cfg.NodeID + ":" + native}}
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
