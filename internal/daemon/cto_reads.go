package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// Each view is derived from ordinary dispatch with the CTO session, or from
// the CTO's own proposal store and static guidance.
var ctoReadSources = map[string]string{
	"overview": "list,nodes,own proposals", "sessions": "list", "session": "get", "proposals": "own proposals", "cto_proposals_find": "own proposals", "feed": "messages", "messages": "messages,message_status", "graph": "agent_graph", "machines": "nodes", "projects": "projects", "workers": "list,get", "tools": "mcp_list", "settings": "settings_read,worker_profiles",
	"agent_sessions_find": "sessions_find", "agent_session_get": "session_get",
	"agent_machines_find": "machines_find", "agent_machine_get": "machine_get",
	"agent_projects_find": "projects_find", "agent_workers_find": "list,get",
	"agent_messages_find": "messages", "agent_message_get": "messages,message_status",
	"agent_graph_read": "agent_graph", "agent_settings_read": "settings_read", "agent_mcp_servers_find": "mcp_list",
	"agent_worker_profiles_find": "worker_profiles",
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
	case "agent_sessions_find", "agent_session_get", "agent_machines_find", "agent_machine_get", "agent_projects_find", "agent_workers_find", "agent_messages_find", "agent_message_get", "agent_graph_read", "agent_settings_read", "agent_mcp_servers_find", "agent_worker_profiles_find":
		return d.readCTOCatalogTool(ctx, name, raw)
	case "workers":
		var q struct {
			ID     string `json:"id"`
			Limit  int    `json:"limit"`
			Offset int    `json:"offset"`
		}
		if len(raw) > 0 && json.Unmarshal(raw, &q) != nil {
			return nil, errors.New("bad worker arguments")
		}
		value, err := d.ctoReadConn().dispatch(ctx, "list", nil)
		if err != nil {
			return nil, err
		}
		var result protocol.AgentsResult
		if err = ctoDecode(value, &result); err != nil {
			return nil, err
		}
		rows := ctoWorkerRows(result.Agents)
		if q.ID != "" {
			for _, r := range rows {
				if r.ID == q.ID {
					b, _ := json.Marshal(protocol.QueryAgent{SessionID: q.ID})
					return d.ctoReadConn().dispatch(ctx, "get", b)
				}
			}
			return nil, errors.New("unknown worker")
		}
		return ctoOffsetPage(rows, q.Limit, q.Offset), nil
	case "tools":
		var q struct {
			View   string `json:"view"`
			Limit  int    `json:"limit"`
			Offset int    `json:"offset"`
		}
		if len(raw) > 0 && json.Unmarshal(raw, &q) != nil {
			return nil, errors.New("bad tools arguments")
		}
		if q.View != "" && q.View != "mcp" {
			return nil, errors.New("unknown tools view")
		}
		value, err := d.ctoReadConn().dispatch(ctx, "mcp_list", nil)
		if err != nil {
			return nil, err
		}
		var result struct {
			Servers []protocol.MCPServer `json:"servers"`
		}
		if err = ctoDecode(value, &result); err != nil {
			return nil, err
		}
		type row struct {
			Name    string `json:"name"`
			Machine string `json:"machine"`
			Enabled bool   `json:"enabled"`
			Health  string `json:"health"`
		}
		rows := make([]row, 0, len(result.Servers))
		for _, v := range result.Servers {
			rows = append(rows, row{ctoCut(v.Name, 80), ctoCut(v.Hostname, 80), v.Enabled, ctoCut(v.Health, 80)})
		}
		return ctoOffsetPage(rows, q.Limit, q.Offset), nil
	case "settings":
		var q struct {
			Area   string `json:"area"`
			Key    string `json:"key"`
			Repo   string `json:"repo"`
			Limit  int    `json:"limit"`
			Offset int    `json:"offset"`
		}
		if len(raw) > 0 && json.Unmarshal(raw, &q) != nil {
			return nil, errors.New("bad settings arguments")
		}
		if q.Area == "values" {
			return d.readSettings(ctx, protocol.SettingsReadRequest{Key: q.Key, Repo: q.Repo}, false)
		}
		if q.Area != "worker_profiles" {
			return nil, errors.New("settings area unavailable to ordinary sessions")
		}
		value, err := d.ctoReadConn().dispatch(ctx, protocol.OpWorkerProfiles, nil)
		if err != nil {
			return nil, err
		}
		var profiles []protocol.WorkerProfile
		if err = ctoDecode(value, &profiles); err != nil {
			return nil, err
		}
		type row struct {
			Name    string `json:"name"`
			Harness string `json:"harness"`
			Model   string `json:"model"`
			Effort  string `json:"effort,omitempty"`
		}
		rows := make([]row, 0, len(profiles))
		for _, v := range profiles {
			rows = append(rows, row{ctoCut(v.Name, 80), ctoCut(v.Harness, 80), ctoCut(v.Model, 80), ctoCut(v.Effort, 80)})
		}
		return ctoOffsetPage(rows, q.Limit, q.Offset), nil

	case "graph":
		var q struct {
			View   string `json:"view"`
			Node   string `json:"node"`
			Limit  int    `json:"limit"`
			Offset int    `json:"offset"`
		}
		if len(raw) > 0 && json.Unmarshal(raw, &q) != nil {
			return nil, errors.New("bad graph arguments")
		}
		if q.View == "guide" {
			return protocol.CharterGuideText() + "\n\nEdges: depends goes from consumer to provider; governs from architect to component; deploys from deployer to app. Governance modes are advisor (advises), arbiter (arbitrates), and owner (decides). Request rules: normal delivers with ordinary peer guidance; auto delivers with peer trust; ask holds for the owner; never refuses.", nil
		}
		if q.View != "" {
			return nil, errors.New("unknown graph view")
		}
		page, err := d.agentGraph(ctx, protocol.PublicGraphQuery{Node: q.Node, Offset: q.Offset, Limit: q.Limit})
		if err != nil {
			return nil, err
		}
		if q.Node != "" {
			if page.Total == 0 {
				return nil, errors.New("unknown public graph node")
			}
			return map[string]any{"node": page.Nodes[0].Name, "items": page.Nodes, "edges": page.Edges, "total": page.Total, "edge_total": page.EdgeTotal}, nil
		}
		return ctoGraphPage{Items: page.Nodes, Edges: page.Edges, Total: page.Total, Next: page.Next, EdgeTotal: page.EdgeTotal}, nil
	case "projects":
		var q struct {
			Name   string `json:"name"`
			Limit  int    `json:"limit"`
			Offset int    `json:"offset"`
		}
		if len(raw) > 0 && json.Unmarshal(raw, &q) != nil {
			return nil, errors.New("bad project arguments")
		}
		b, _ := json.Marshal(protocol.QueryProjects{Name: q.Name})
		value, err := d.ctoReadConn().dispatch(ctx, "projects", b)
		if err != nil {
			return nil, err
		}
		var result protocol.ProjectsResult
		if err = ctoDecode(value, &result); err != nil {
			return nil, err
		}
		return ctoOffsetPage(ctoProjectRows(result.Projects), q.Limit, q.Offset), nil
	case "machines":
		var q struct {
			Machine string `json:"machine"`
			Harness string `json:"harness"`
			Limit   int    `json:"limit"`
			Offset  int    `json:"offset"`
		}
		if len(raw) > 0 && json.Unmarshal(raw, &q) != nil {
			return nil, errors.New("bad machine arguments")
		}
		value, err := d.ctoReadConn().dispatch(ctx, "nodes", nil)
		if err != nil {
			return nil, err
		}
		var result protocol.NodesInfo
		if err = ctoDecode(value, &result); err != nil {
			return nil, err
		}
		type row struct {
			ID        string `json:"id"`
			Name      string `json:"name"`
			Online    bool   `json:"online"`
			Harnesses int    `json:"harnesses"`
		}
		rows := make([]row, 0, len(result.Nodes))
		for _, n := range result.Nodes {
			if q.Machine != "" && (q.Machine == n.NodeID || q.Machine == n.Hostname) {
				if q.Harness == "" {
					return row{n.NodeID, ctoCut(n.Hostname, 80), n.Online, len(n.Harnesses)}, nil
				}
				for _, h := range n.Harnesses {
					if h.Name == q.Harness {
						return ctoOffsetPage(h.Models, q.Limit, q.Offset), nil
					}
				}
				return nil, errors.New("unknown harness")
			}
			rows = append(rows, row{n.NodeID, ctoCut(n.Hostname, 80), n.Online, len(n.Harnesses)})
		}
		if q.Machine != "" {
			return nil, errors.New("unknown machine")
		}
		return ctoOffsetPage(rows, q.Limit, q.Offset), nil

	case "proposals", "cto_proposals_find":
		var q struct {
			ID     string `json:"id"`
			Limit  int    `json:"limit"`
			Offset int    `json:"offset"`
		}
		if len(raw) > 0 && json.Unmarshal(raw, &q) != nil {
			return nil, errors.New("bad proposals arguments")
		}
		items, err := d.proposalRunner().list()
		if err != nil {
			return nil, err
		}
		type row struct {
			ID     string `json:"id"`
			Tool   string `json:"tool"`
			Action string `json:"action,omitempty"`
			Status string `json:"status"`
			Reason string `json:"reason,omitempty"`
		}
		rows := make([]row, 0, len(items))
		for _, p := range items {
			r := row{p.ID, p.Call.Tool, p.Call.Action, p.Status, ctoCut(p.Reason, 80)}
			if q.ID == p.ID {
				return map[string]any{"id": p.ID, "tool": p.Call.Tool, "action": p.Call.Action, "arguments": p.Call.Args, "reason": p.Reason, "created_at": p.CreatedAt, "status": p.Status, "note": p.Note}, nil
			}
			rows = append(rows, r)
		}
		if q.ID != "" {
			return nil, errors.New("unknown proposal")
		}
		return ctoOffsetPage(rows, q.Limit, q.Offset), nil
	case "feed", "messages":
		var q struct {
			MessageID      string `json:"message_id"`
			ConversationID string `json:"conversation_id"`
			Limit          int    `json:"limit"`
			Offset         int    `json:"offset"`
		}
		if len(raw) > 0 && json.Unmarshal(raw, &q) != nil {
			return nil, errors.New("bad messages arguments")
		}
		if q.MessageID != "" && name == "messages" {
			b, _ := json.Marshal(map[string]string{"message_id": q.MessageID})
			return d.ctoReadConn().dispatch(ctx, "message_status", b)
		}
		limit := ctoLimit(q.Limit)
		if q.Offset < 0 {
			q.Offset = 0
		}
		b, _ := json.Marshal(protocol.QueryMessages{ConversationID: q.ConversationID, Limit: limit, Offset: q.Offset})
		value, err := d.ctoReadConn().dispatch(ctx, "messages", b)
		if err != nil {
			return nil, err
		}
		return ctoDecodeMessagePage(value, q.Offset, name == "feed")

	case "sessions", "overview":
		var q struct {
			Limit  int `json:"limit"`
			Offset int `json:"offset"`
		}
		if len(raw) > 0 && json.Unmarshal(raw, &q) != nil {
			return nil, errors.New("bad list arguments")
		}
		result, err := d.ctoReadConn().dispatch(ctx, "list", raw)
		if err != nil {
			return nil, err
		}
		var agents protocol.AgentsResult
		if err = ctoDecode(result, &agents); err != nil {
			return nil, err
		}
		if name == "sessions" {
			return ctoSessionPage(agents.Agents, q.Limit, q.Offset), nil
		}
		nodes, err := d.ctoReadConn().dispatch(ctx, "nodes", nil)
		if err != nil {
			return nil, err
		}
		var machines protocol.NodesInfo
		if err = ctoDecode(nodes, &machines); err != nil {
			return nil, err
		}
		online, machineOnline := 0, 0
		for _, a := range agents.Agents {
			if a.Online {
				online++
			}
		}
		for _, n := range machines.Nodes {
			if n.Online {
				machineOnline++
			}
		}
		running := 0
		for _, worker := range protocol.WorkersOf(agents.Agents) {
			if worker.Online {
				running++
			}
		}
		proposals, err := d.proposalRunner().list()
		if err != nil {
			return nil, err
		}
		open := 0
		for _, proposal := range proposals {
			if proposal.Status == "open" {
				open++
			}
		}
		return map[string]any{"sessions_online": online, "machines_online": machineOnline, "workers_running": running, "proposals_open": open}, nil
	case "session":
		return d.ctoReadConn().dispatch(ctx, "get", raw)
	}
	return nil, errors.New("CTO read has no ordinary-agent source")
}

type ctoList[T any] struct {
	Items []T `json:"items"`
	Total int `json:"total"`
	Next  int `json:"next,omitempty"`
}

// ctoGraphPage is the paged public graph: one line per node with its charter
// and maturity, and the compact edges with kind, mode and request rule.
type ctoGraphPage struct {
	Items     []protocol.PublicGraphNode `json:"items"`
	Edges     []protocol.PublicGraphEdge `json:"edges"`
	Total     int                        `json:"total"`
	Next      int                        `json:"next,omitempty"`
	EdgeTotal int                        `json:"edge_total"`
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

type ctoSessionRow struct {
	ID     string `json:"id"`
	Name   string `json:"name,omitempty"`
	Status string `json:"status,omitempty"`
	Task   string `json:"task,omitempty"`
}

func ctoSessionPage(agents []protocol.AgentView, limit, offset int) ctoList[ctoSessionRow] {
	rows := make([]ctoSessionRow, 0, len(agents))
	for _, a := range agents {
		name := a.Profile.Name
		if name == "" {
			name = a.Title
		}
		rows = append(rows, ctoSessionRow{a.SessionID, ctoCut(name, 18), ctoCut(a.Status, 12), ctoCut(a.Profile.Task, 40)})
	}
	return ctoOffsetPage(rows, limit, offset)
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

type ctoConversationRow struct {
	ID   string `json:"id"`
	From string `json:"from"`
	To   string `json:"to"`
	Type string `json:"type"`
	Time string `json:"time"`
	Text string `json:"text,omitempty"`
}

func ctoConversationRows(messages []protocol.Message, feed bool) []ctoConversationRow {
	rows := make([]ctoConversationRow, 0, len(messages))
	for _, m := range messages {
		row := ctoConversationRow{ID: m.ID, From: m.From, To: m.To, Type: m.Type, Time: m.CreatedAt.Format(time.RFC3339)}
		if !feed {
			row.Text = ctoCut(m.Body, 80)
		}
		rows = append(rows, row)
	}
	return rows
}

type ctoProjectRow struct {
	Name     string `json:"name"`
	Display  string `json:"display,omitempty"`
	Machine  string `json:"machine"`
	Sessions int    `json:"sessions"`
	Online   int    `json:"online"`
}

func ctoProjectRows(projects []protocol.ProjectView) []ctoProjectRow {
	rows := make([]ctoProjectRow, 0, len(projects))
	for _, p := range projects {
		rows = append(rows, ctoProjectRow{ctoCut(p.Name, 80), ctoCut(p.DisplayName, 80), ctoCut(p.Hostname, 80), p.Sessions, p.Online})
	}
	return rows
}

type ctoWorkerRow struct {
	ID      string `json:"id"`
	Project string `json:"project,omitempty"`
	Status  string `json:"status,omitempty"`
	Task    string `json:"task,omitempty"`
}

func ctoWorkerRows(agents []protocol.AgentView) []ctoWorkerRow {
	workers := protocol.WorkersOf(agents)
	rows := make([]ctoWorkerRow, 0, len(workers))
	for _, w := range workers {
		rows = append(rows, ctoWorkerRow{w.SessionID, ctoCut(w.Project, 80), ctoCut(w.Status, 30), ctoCut(w.Task, 80)})
	}
	return rows
}

func ctoDecodeMessagePage(value any, offset int, feed bool) (ctoList[ctoConversationRow], error) {
	var result struct {
		Messages []protocol.Message `json:"messages"`
		Total    *int               `json:"total"`
	}
	if err := ctoDecode(value, &result); err != nil {
		return ctoList[ctoConversationRow]{}, err
	}
	if result.Total == nil {
		return ctoList[ctoConversationRow]{}, errors.New("gateway does not support message pagination; upgrade the gateway")
	}
	page := ctoList[ctoConversationRow]{Items: ctoConversationRows(result.Messages, feed), Total: *result.Total}
	if offset+len(result.Messages) < *result.Total {
		page.Next = offset + len(result.Messages)
	}
	return page, nil
}
