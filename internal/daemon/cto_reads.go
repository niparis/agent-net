package daemon

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/niparis/agent-net/internal/mcpserver"
	"github.com/niparis/agent-net/internal/protocol"
)

// Each view is derived from ordinary dispatch with the CTO session, from the
// shared reads of mcpserver.ServeRead run for that session, or from the CTO's
// own proposal store and static guidance. A read whose sources depend on its
// detail names the extra source after a semicolon.
var ctoReadSources = map[string]string{
	"cto_proposals_find":  "own proposals,asks.list",
	"cto_intake_find":     protocol.FrameIntakeFind,
	"agent_sessions_find": "sessions_find",
	"agent_machines_find": "machines_find", "agent_machine_log": "machine_log",
	"agent_projects_find": "projects_find", "agent_workers_find": protocol.MethodWorkersFind,
	"agent_messages_find": protocol.MethodMessagesFind,
	"agent_graph_read":    mcpserver.ReadGraph, "agent_settings_read": "settings_read", "agent_mcp_servers_find": protocol.MethodMCPServersFind,
	"agent_worker_profiles_find": mcpserver.ReadWorkerProfiles, "agent_owner_judgments_find": "judgments_find", "projects_layout": "projects_layout",
	"agent_protected_providers_get": protocol.OpProtectedProvidersGet,
}

// ctoDispatchReads are the catalog reads that are one ordinary daemon method.
var ctoDispatchReads = map[string]string{
	"agent_sessions_find": "sessions_find", "agent_machines_find": "machines_find", "agent_machine_log": "machine_log",
	"agent_projects_find": "projects_find", "agent_owner_judgments_find": "judgments_find", "projects_layout": "projects_layout",
}

// ctoSharedReads are the catalog reads that filter and page. They run the
// ordinary agent's code (decision 0176), so the CTO gets the agent's rows and
// cursors.
var ctoSharedReads = map[string]string{
	"agent_workers_find": protocol.MethodWorkersFind, "agent_messages_find": protocol.MethodMessagesFind,
	"agent_mcp_servers_find": protocol.MethodMCPServersFind, "agent_graph_read": mcpserver.ReadGraph,
	"agent_worker_profiles_find": mcpserver.ReadWorkerProfiles,
}

func (d *Daemon) readCTOCatalogTool(ctx context.Context, name string, raw json.RawMessage) (any, error) {
	if method := ctoDispatchReads[name]; method != "" {
		return d.ctoReadConn().dispatch(ctx, method, raw)
	}
	switch name {
	case "agent_protected_providers_get":
		return d.protectedProviders(ctx, nil)
	case "agent_settings_read":
		var q protocol.SettingsReadRequest
		if len(raw) > 0 && json.Unmarshal(raw, &q) != nil {
			return nil, errors.New("bad settings arguments")
		}
		rows, err := d.readSettings(ctx, q, false)
		if err != nil {
			return nil, err
		}
		return map[string]any{"settings": rows}, nil
	case "agent_graph_read":
		if guide, ok, err := ctoGraphGuide(raw); ok || err != nil {
			return guide, err
		}
	case "agent_workers_find":
		// The CTO's default is every worker, not the ones it started.
		raw = ctoDefaultArg(raw, "started_by", "anyone")
	}
	if method := ctoSharedReads[name]; method != "" {
		return d.ctoReadConn().serveRead(ctx, method, raw)
	}
	return nil, errors.New("CTO read has no ordinary-agent source")
}

// ctoGraphGuide answers agent_graph_read view guide: the CTO's guide to
// writing charters and edges, kept out of the schemas. ok is false for an
// ordinary graph read.
func ctoGraphGuide(raw json.RawMessage) (any, bool, error) {
	var q struct {
		View string `json:"view"`
	}
	if len(raw) > 0 && string(raw) != "null" && json.Unmarshal(raw, &q) != nil {
		return nil, false, errors.New("bad agent_graph_read arguments")
	}
	switch q.View {
	case "":
		return nil, false, nil
	case "guide":
		note := protocol.CharterGuideText() + "\n\nEdges: depends goes from consumer to provider; deploys from deployer to app. Delivery owner edges accepts, reviews, tests, tests_after_merge, approves, approves_deploy, merges, releases, delivery_deploys and verifies go from the owner node to the repo. Request rules: " + protocol.EdgeRulesText() + "."
		return map[string]any{"nodes": []any{}, "edges": []any{}, "note": note}, true, nil
	}
	return nil, false, errors.New(`unknown agent_graph_read view; the only view is guide, for example {"view":"guide"}`)
}

// ctoDefaultArg sets one argument the CTO leaves out. Bad JSON is passed on
// unchanged, so the shared read reports it.
func ctoDefaultArg(raw json.RawMessage, key, value string) json.RawMessage {
	args := map[string]any{}
	if len(raw) > 0 && string(raw) != "null" && json.Unmarshal(raw, &args) != nil {
		return raw
	}
	if _, ok := args[key]; ok {
		return raw
	}
	args[key] = value
	out, err := json.Marshal(args)
	if err != nil {
		return raw
	}
	return out
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
	case "agent_sessions_find", "agent_machines_find", "agent_machine_log", "agent_projects_find", "agent_workers_find", "agent_messages_find", "agent_graph_read", "agent_settings_read", "agent_mcp_servers_find", "agent_worker_profiles_find", "agent_owner_judgments_find", "projects_layout", "agent_protected_providers_get":
		return d.readCTOCatalogTool(ctx, name, raw)
	case "cto_intake_find":
		var q protocol.IntakeFindArgs
		if len(raw) > 0 && string(raw) != "null" && json.Unmarshal(raw, &q) != nil {
			return nil, errors.New("bad cto_intake_find arguments")
		}
		value, err := d.call(ctx, protocol.FrameIntakeFind, q)
		if err != nil {
			return nil, err
		}
		var out protocol.IntakeFindResult
		return out, ctoDecode(value, &out)
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
		if draft, ok := ctoCharterDraftID(q.ID); ok {
			return d.ctoCharterDraft(ctx, draft)
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
