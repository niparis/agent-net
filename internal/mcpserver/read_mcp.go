package mcpserver

// The shared MCP server and worker-profile read tools: agent_mcp_servers_find
// and agent_worker_profiles_find (tool redesign task 2b).

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/niparis/agent-net/internal/protocol"
)

type roMCPFindIn struct {
	UsableOnly *bool  `json:"usable_only"`
	Limit      int    `json:"limit"`
	Cursor     string `json:"cursor"`
	Detail     string `json:"detail"`
}

type roMCPRow struct {
	Name        string   `json:"name"`
	Machine     string   `json:"machine"`
	Enabled     bool     `json:"enabled"`
	Health      string   `json:"health"`
	UsableByYou bool     `json:"usable_by_you"`
	Tools       []string `json:"tools"`
	key         string
}

type roMCPFindOut struct {
	Items      []roMCPRow `json:"items"`
	Total      int        `json:"total"`
	NextCursor string     `json:"next_cursor,omitempty"`
	Note       string     `json:"note,omitempty"`
}

type roProfileFindIn struct {
	Limit  int    `json:"limit"`
	Cursor string `json:"cursor"`
	Detail string `json:"detail"`
}

type roProfileRow struct {
	Name        string   `json:"name"`
	Harness     string   `json:"harness"`
	Model       string   `json:"model"`
	Effort      string   `json:"effort"`
	AvailableOn []string `json:"available_on"`
	key         string
}

type roProfileFindOut struct {
	Items      []roProfileRow `json:"items"`
	Total      int            `json:"total"`
	NextCursor string         `json:"next_cursor,omitempty"`
	Note       string         `json:"note,omitempty"`
}

func addMCPReadTools(s *mcp.Server, call Caller) {
	roAdd(s, "agent_mcp_servers_find", func(ctx context.Context, _ *mcp.CallToolRequest, in roMCPFindIn) (*mcp.CallToolResult, roMCPFindOut, error) {
		out, err := roFindMCPServers(ctx, call, in)
		return nil, out, err
	})
	roAdd(s, "agent_worker_profiles_find", func(ctx context.Context, _ *mcp.CallToolRequest, in roProfileFindIn) (*mcp.CallToolResult, roProfileFindOut, error) {
		out, err := roFindWorkerProfiles(ctx, call, in)
		return nil, out, err
	})
}

func roFindMCPServers(ctx context.Context, call Caller, in roMCPFindIn) (roMCPFindOut, error) {
	usableOnly := true
	if in.UsableOnly != nil {
		usableOnly = *in.UsableOnly
	}
	if _, err := roDetail(in.Detail); err != nil {
		return roMCPFindOut{}, err
	}
	limit, err := roLimit(in.Limit)
	if err != nil {
		return roMCPFindOut{}, err
	}
	caller, err := roWhoAmI(ctx, call)
	if err != nil {
		return roMCPFindOut{}, err
	}
	filters := roFilterKey(struct {
		UsableOnly bool
		Detail     string
	}{usableOnly, in.Detail})
	after, err := roDecodeCursor(in.Cursor, "agent_mcp_servers_find", caller.SessionID, filters)
	if err != nil {
		return roMCPFindOut{}, err
	}
	servers, err := roLoadMCPServers(ctx, call)
	if err != nil {
		return roMCPFindOut{}, err
	}
	rows := make([]roMCPRow, 0, len(servers))
	for _, server := range servers {
		usable := server.Enabled && server.HostOnline && roMCPScopeAllows(server, caller.Project)
		if usableOnly && !usable {
			continue
		}
		machine := server.Hostname
		if machine == "" {
			machine = server.NodeID
		}
		names := make([]string, 0, len(server.Tools))
		for _, tool := range server.Tools {
			names = append(names, tool.Name)
		}
		sort.Strings(names)
		rows = append(rows, roMCPRow{Name: server.Name, Machine: machine, Enabled: server.Enabled,
			Health: server.Health, UsableByYou: usable, Tools: names, key: server.Name + "\x00" + machine})
	}
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].key < rows[j].key })
	page, next := roWindow(rows, limit, after, false, func(row roMCPRow) string { return row.key })
	out := roMCPFindOut{Items: page, Total: len(rows)}
	if next != "" {
		out.NextCursor = roEncodeCursor(roCursor{Tool: "agent_mcp_servers_find", Caller: caller.SessionID, Filters: filters, After: next})
		out.Note = roPageNote("MCP servers")
	}
	return out, nil
}

// roLoadMCPServers is the one MCP server read. agent_mcp_servers_find and the
// agent_mcp_list alias both go through it.
func roLoadMCPServers(ctx context.Context, call Caller) ([]protocol.MCPServer, error) {
	raw, err := call(ctx, "mcp_list", nil)
	if err != nil {
		return nil, err
	}
	var result struct {
		Servers []protocol.MCPServer `json:"servers"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, err
	}
	return result.Servers, nil
}

// roMCPScopeAllows says whether the owner assigned this server to the caller's
// project. An empty scope is the global default.
func roMCPScopeAllows(server protocol.MCPServer, project string) bool {
	switch server.Scope {
	case "", "global":
		return true
	case "projects":
		for _, assigned := range server.Projects {
			if assigned == project {
				return true
			}
		}
		return false
	default:
		return false
	}
}

func roFindWorkerProfiles(ctx context.Context, call Caller, in roProfileFindIn) (roProfileFindOut, error) {
	if _, err := roDetail(in.Detail); err != nil {
		return roProfileFindOut{}, err
	}
	limit, err := roLimit(in.Limit)
	if err != nil {
		return roProfileFindOut{}, err
	}
	filters := roFilterKey(struct{ Detail string }{in.Detail})
	after, err := roDecodeCursor(in.Cursor, "agent_worker_profiles_find", roCursorCaller(ctx), filters)
	if err != nil {
		return roProfileFindOut{}, err
	}
	profiles, err := roLoadWorkerProfiles(ctx, call)
	if err != nil {
		return roProfileFindOut{}, err
	}
	rows := make([]roProfileRow, 0, len(profiles))
	for _, profile := range profiles {
		var on []string
		for _, machine := range profile.MachineAvailability {
			if machine.Available {
				on = append(on, machine.Machine)
			}
		}
		if len(on) == 0 && profile.Available != nil && *profile.Available {
			on = append(on, profile.MachinesChecked...)
		}
		rows = append(rows, roProfileRow{Name: profile.Name, Harness: profile.Harness, Model: profile.Model,
			Effort: profile.Effort, AvailableOn: roUniqueSorted(on), key: profile.Name})
	}
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].key < rows[j].key })
	page, next := roWindow(rows, limit, after, false, func(row roProfileRow) string { return row.key })
	out := roProfileFindOut{Items: page, Total: len(rows)}
	if next != "" {
		out.NextCursor = roEncodeCursor(roCursor{Tool: "agent_worker_profiles_find", Caller: roCursorCaller(ctx), Filters: filters, After: next})
		out.Note = roPageNote("worker profiles")
	}
	return out, nil
}

// roLoadWorkerProfiles is the one worker profile read. agent_worker_profiles_find
// and the agent_worker_profiles alias both go through it.
func roLoadWorkerProfiles(ctx context.Context, call Caller) ([]protocol.WorkerProfile, error) {
	raw, err := call(ctx, "worker_profiles", struct{}{})
	if err != nil {
		return nil, err
	}
	return roDecodeProfiles(raw)
}

// roDecodeProfiles accepts the gateway's array and a wrapped object, so an
// older or newer answer still reads.
func roDecodeProfiles(raw json.RawMessage) ([]protocol.WorkerProfile, error) {
	var profiles []protocol.WorkerProfile
	if err := json.Unmarshal(raw, &profiles); err == nil && profiles != nil {
		return profiles, nil
	}
	var wrapped struct {
		Profiles []protocol.WorkerProfile `json:"profiles"`
	}
	if err := json.Unmarshal(raw, &wrapped); err != nil {
		return nil, fmt.Errorf("worker profiles: %w", err)
	}
	return wrapped.Profiles, nil
}
