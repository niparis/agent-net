package mcpserver

// The worker read tool: agent_workers_find (tool redesign task 2b). Ordinary
// agents default to the workers they started; the CTO catalog default is
// anyone.

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type roWorkersFindIn struct {
	StartedBy string `json:"started_by"`
	State     string `json:"state"`
	Project   string `json:"project"`
	Machine   string `json:"machine"`
	Limit     int    `json:"limit"`
	Cursor    string `json:"cursor"`
	Detail    string `json:"detail"`
}

type roWorkerRow struct {
	ID         string           `json:"id"`
	Name       string           `json:"name"`
	Task       string           `json:"task"`
	Machine    string           `json:"machine"`
	Project    string           `json:"project"`
	Harness    string           `json:"harness"`
	Model      string           `json:"model"`
	Difficulty string           `json:"difficulty"`
	Caller     roSessionRefType `json:"caller"`
	State      string           `json:"state"`
	RunTime    string           `json:"run_time"`
	Started    string           `json:"started,omitempty"`
	Branch     string           `json:"branch,omitempty"`
	key        string
}

type roWorkersFindOut struct {
	Items      []roWorkerRow `json:"items"`
	Total      int           `json:"total"`
	NextCursor string        `json:"next_cursor,omitempty"`
	Note       string        `json:"note,omitempty"`
}

func addWorkerReadTools(s *mcp.Server, call Caller) {
	roAdd(s, "agent_workers_find", func(ctx context.Context, _ *mcp.CallToolRequest, in roWorkersFindIn) (*mcp.CallToolResult, roWorkersFindOut, error) {
		out, err := roFindWorkers(ctx, call, in)
		return nil, out, err
	})
}

func roFindWorkers(ctx context.Context, call Caller, in roWorkersFindIn) (roWorkersFindOut, error) {
	startedBy := in.StartedBy
	if startedBy == "" {
		startedBy = "me"
	}
	switch startedBy {
	case "me", "anyone":
	default:
		return roWorkersFindOut{}, fmt.Errorf("started_by must be me or anyone, not %q. Example: {\"started_by\":\"anyone\"}", in.StartedBy)
	}
	state := in.State
	if state == "" {
		state = "running"
	}
	switch state {
	case "running", "ended", "any":
	default:
		return roWorkersFindOut{}, fmt.Errorf("state must be running, ended or any, not %q. Example: {\"state\":\"any\"}", in.State)
	}
	full, err := roDetail(in.Detail)
	if err != nil {
		return roWorkersFindOut{}, err
	}
	limit, err := roLimit(in.Limit)
	if err != nil {
		return roWorkersFindOut{}, err
	}
	caller, err := roWhoAmI(ctx, call)
	if err != nil {
		return roWorkersFindOut{}, err
	}
	agents, err := roAgentList(ctx, call)
	if err != nil {
		return roWorkersFindOut{}, err
	}
	names := roNames(agents, caller.Sessions)

	filters := roFilterKey(struct {
		StartedBy, State, Project, Machine, Detail string
	}{startedBy, state, in.Project, in.Machine, in.Detail})
	after, err := roDecodeCursor(in.Cursor, "agent_workers_find", caller.SessionID, filters)
	if err != nil {
		return roWorkersFindOut{}, err
	}

	rows := make([]roWorkerRow, 0, len(agents))
	for _, agent := range agents {
		if agent.Worker == nil {
			continue
		}
		if startedBy == "me" && agent.Worker.Owner != caller.SessionID {
			continue
		}
		if state == "running" && !agent.Online || state == "ended" && agent.Online {
			continue
		}
		if in.Project != "" && !strings.EqualFold(agent.Worker.Project, in.Project) {
			continue
		}
		machine := agent.Hostname
		if machine == "" {
			machine = agent.NodeID
		}
		if in.Machine != "" && !strings.EqualFold(machine, in.Machine) && agent.NodeID != in.Machine {
			continue
		}
		difficulty := agent.Worker.DifficultyUsed
		if difficulty == "" {
			difficulty = agent.Worker.DifficultyAsked
		}
		name := agent.Profile.Name
		if name == "" {
			name = agent.SessionID
		}
		row := roWorkerRow{
			ID: agent.SessionID, Name: name, Task: agent.Worker.Task, Machine: machine,
			Project: agent.Worker.Project, Harness: agent.Harness, Model: agent.Model,
			Difficulty: difficulty,
			Caller:     roSessionRef(agent.Worker.Owner, roName(names, agent.Worker.Owner, agent.Worker.Owner)),
			State:      "ended",
			RunTime:    roRunTime(agent.Worker.StartedAt, agent.Online),
			key:        fmt.Sprintf("%020d|%s", agent.Worker.StartedAt, agent.SessionID),
		}
		if agent.Online {
			row.State = "running"
		}
		if full {
			row.Started = roUnixText(agent.Worker.StartedAt)
		}
		rows = append(rows, row)
	}
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].key > rows[j].key })
	page, next := roWindow(rows, limit, after, true, func(row roWorkerRow) string { return row.key })
	out := roWorkersFindOut{Items: page, Total: len(rows)}
	if next != "" {
		out.NextCursor = roEncodeCursor(roCursor{Tool: "agent_workers_find", Caller: caller.SessionID, Filters: filters, After: next})
		out.Note = roPageNote("workers")
	}
	return out, nil
}
