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
	id := d.ctoSessionID()
	if id == "" {
		id = "cto-unregistered:" + d.cfg.NodeID
	}
	return &localConn{d: d, client: "mcp", ls: &localSession{sessionID: id}}
}

func (d *Daemon) readCTOTool(ctx context.Context, name string, raw json.RawMessage) (any, error) {
	switch name {
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
	r := []rune(s)
	if len(r) > n {
		return string(r[:n])
	}
	return s
}

type ctoSessionRow struct {
	ID      string `json:"id"`
	Name    string `json:"name,omitempty"`
	Project string `json:"project,omitempty"`
	Status  string `json:"status,omitempty"`
	Task    string `json:"task,omitempty"`
}

func ctoSessionPage(agents []protocol.AgentView, limit, offset int) ctoList[ctoSessionRow] {
	rows := make([]ctoSessionRow, 0, len(agents))
	for _, a := range agents {
		name := a.Profile.Name
		if name == "" {
			name = a.Title
		}
		project := a.Repo.Project
		if project == "" {
			project = a.Profile.Project
		}
		rows = append(rows, ctoSessionRow{a.SessionID, ctoCut(name, 40), ctoCut(project, 40), ctoCut(a.Status, 20), ctoCut(a.Profile.Task, 60)})
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
