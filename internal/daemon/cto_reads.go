package daemon

import (
	"context"
	"encoding/json"
	"errors"
)

// Each view is derived from ordinary dispatch with the CTO session, or from
// the CTO's own proposal store and static guidance.
var ctoReadSources = map[string]string{
	"cto_proposals_find":  "own proposals,asks.list",
	"agent_sessions_find": "sessions_find", "agent_session_get": "session_get",
	"agent_machines_find": "machines_find", "agent_machine_get": "machine_get", "agent_machine_log": "machine_log",
	"agent_projects_find": "projects_find", "agent_workers_find": "list,get",
	"agent_messages_find": "messages", "agent_message_get": "messages,message_status,message_file",
	"agent_graph_read": "agent_graph", "agent_settings_read": "settings_read", "agent_mcp_servers_find": "mcp_list",
	"agent_worker_profiles_find": "worker_profiles", "agent_owner_judgments_find": "judgments_find", "projects_layout": "projects_layout",
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
	case "agent_sessions_find", "agent_session_get", "agent_machines_find", "agent_machine_get", "agent_machine_log", "agent_projects_find", "agent_workers_find", "agent_messages_find", "agent_message_get", "agent_graph_read", "agent_settings_read", "agent_mcp_servers_find", "agent_worker_profiles_find", "agent_owner_judgments_find", "projects_layout":
		return d.readCTOCatalogTool(ctx, name, raw)
	case "cto_proposals_find":
		var q struct {
			ID     string `json:"id"`
			Kind   string `json:"kind"`
			Limit  int    `json:"limit"`
			Offset int    `json:"offset"`
		}
		if len(raw) > 0 && string(raw) != "null" && json.Unmarshal(raw, &q) != nil {
			return nil, errors.New("bad proposals arguments")
		}
		if q.ID != "" {
			p, ok, err := d.proposalRunner().get(q.ID)
			if err != nil {
				return nil, err
			}
			if !ok {
				return nil, errors.New("unknown proposal")
			}
			return map[string]any{"id": p.ID, "tool": p.Call.Tool, "action": p.Call.Action, "arguments": p.Call.Args, "reason": p.Reason, "created_at": p.CreatedAt, "status": p.Status, "note": p.Note}, nil
		}
		kind, err := ctoOwnerKindFilter(q.Kind)
		if err != nil {
			return nil, err
		}
		rows, err := d.ctoOwnerItemRows(ctx, kind)
		if err != nil {
			return nil, err
		}
		return ctoOffsetPage(rows, q.Limit, q.Offset), nil
	}
	return nil, errors.New("CTO read has no ordinary-agent source")
}

type ctoList[T any] struct {
	Items []T `json:"items"`
	Total int `json:"total"`
	Next  int `json:"next,omitempty"`
}

func ctoOffsetPage[T any](items []T, limit, offset int) ctoList[T] {
	if offset < 0 {
		offset = 0
	}
	if offset > len(items) {
		offset = len(items)
	}
	end := offset + ctoLimit(limit)
	if end > len(items) {
		end = len(items)
	}
	page := ctoList[T]{Items: items[offset:end], Total: len(items)}
	if end < len(items) {
		page.Next = end
	}
	return page
}

func ctoCut(s string, n int) string {
	s = cleanLine(s)
	end, size := 0, 0
	for i, r := range s {
		encoded, _ := json.Marshal(string(r))
		size += len(encoded) - 2
		if size > n {
			break
		}
		end = i + len(string(r))
	}
	return s[:end]
}

func ctoDecode(value any, dst any) error {
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	if data, ok := value.(json.RawMessage); ok {
		raw = data
	}
	return json.Unmarshal(raw, dst)
}
