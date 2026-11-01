package mcpserver

// Old-name aliases for the tool redesign, kept for one release (task 3c).
//
// An alias keeps the old name, the old arguments and the old result shape,
// but it runs the same operation as its new tool: it calls the shared op in
// ops.go or the same data loader the new tool uses, then renders the old
// result type. Authorization is not widened: the daemon still checks the
// caller, and a worker still cannot reach agent_start (it is removed from a
// worker's list, and the daemon refuses it).
//
// Same-name operations from the catalog (agent_announce, agent_handback,
// agent_learn, agent_project_add, agent_mcp_test) are the new tools already.
// They stay one tool, so they are not marked deprecated here.

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"strconv"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/niparis/agent-net/internal/protocol"
)

type aliasRun = func(context.Context, string, any) (any, error)

type askOwnerIn struct {
	Question string   `json:"question" jsonschema:"question in one line"`
	Options  []string `json:"options,omitempty" jsonschema:"up to four short answers"`
	Urgency  string   `json:"urgency,omitempty" jsonschema:"normal or high"`
}

type approvalIn struct {
	Action         string `json:"action" jsonschema:"the exact action"`
	Recommendation string `json:"recommendation" jsonschema:"your recommendation"`
}

type ideaIn struct {
	Text           string `json:"text" jsonschema:"the idea"`
	Recommendation string `json:"recommendation" jsonschema:"your recommendation"`
}

func deprecated(newTool, rest string) string {
	return "Deprecated: use " + newTool + ". This name is removed in the next release. " + rest
}

// addAliasTools registers the old tool names that the next release removes.
func addAliasTools(s *mcp.Server, call Caller, run aliasRun) {
	mcp.AddTool(s, &mcp.Tool{Name: "agent_list", Description: deprecated("agent_sessions_find",
		"List agent sessions on the network, with their name, function, task, expertise, project and status. "+AskPeerRule)},
		func(ctx context.Context, _ *mcp.CallToolRequest, in listIn) (*mcp.CallToolResult, any, error) {
			out, err := aliasList(ctx, run, in)
			return nil, out, err
		})
	mcp.AddTool(s, &mcp.Tool{Name: "agent_get", Description: deprecated("agent_session_get",
		"Show one agent session by session_id.")},
		func(ctx context.Context, _ *mcp.CallToolRequest, in getIn) (*mcp.CallToolResult, any, error) {
			out, err := aliasGet(ctx, run, in)
			return nil, out, err
		})
	mcp.AddTool(s, &mcp.Tool{Name: "agent_nodes", Description: deprecated("agent_machines_find",
		"Read one machine's models, quotas, track record or playbook with agent_machine_get.")},
		func(ctx context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, any, error) {
			out, err := aliasNodes(ctx, run)
			if err != nil {
				return nil, nil, err
			}
			return aliasText(out), out, nil
		})
	mcp.AddTool(s, &mcp.Tool{Name: "agent_project_list", Description: deprecated("agent_projects_find",
		"List the projects of the agent network: one row per repository per machine.")},
		func(ctx context.Context, _ *mcp.CallToolRequest, in projectListIn) (*mcp.CallToolResult, any, error) {
			out, err := aliasProjects(ctx, run, in)
			return nil, out, err
		})
	mcp.AddTool(s, &mcp.Tool{Name: "agent_messages", Description: deprecated("agent_messages_find",
		"List recent messages you sent or received, newest first, with their delivery state.")},
		func(ctx context.Context, _ *mcp.CallToolRequest, in messagesIn) (*mcp.CallToolResult, any, error) {
			out, err := aliasMessages(ctx, call, in)
			return nil, out, err
		})
	mcp.AddTool(s, &mcp.Tool{Name: "agent_message_status", Description: deprecated("agent_message_get",
		"Show the state of one message (stored, dispatched, delivered, replied, failed, expired).")},
		func(ctx context.Context, _ *mcp.CallToolRequest, in statusIn) (*mcp.CallToolResult, any, error) {
			out, err := roLoadMessage(ctx, call, in.MessageID)
			return nil, out, err
		})
	mcp.AddTool(s, &mcp.Tool{Name: "agent_send", Description: deprecated("agent_message_send",
		"Send a message to another agent session. The reply comes back as a message in your own session. "+
			AskPeerRule+" "+RelayRule+peerNote)},
		func(ctx context.Context, _ *mcp.CallToolRequest, in sendIn) (*mcp.CallToolResult, any, error) {
			out, err := sendCall(ctx, run, in.To, nil, in.Body, in.MessageType, false, false)
			return nil, out, err
		})
	mcp.AddTool(s, &mcp.Tool{Name: "agent_reply", Description: deprecated("agent_message_reply",
		"Reply to a message you received from another agent, by its message_id.")},
		func(ctx context.Context, _ *mcp.CallToolRequest, in replyIn) (*mcp.CallToolResult, any, error) {
			out, err := replyCall(ctx, run, messageReplyIn{Message: in.MessageID, Body: in.Body,
				Outcome: in.Outcome, Reason: in.Reason, RedirectTo: in.RedirectTo,
				Recommendation: in.Recommendation}, in.ReplyOutcome, false)
			return nil, out, err
		})
	mcp.AddTool(s, &mcp.Tool{Name: "agent_graph", Description: deprecated("agent_graph_read",
		"Read the component graph, read-only: every node's charter and rules, and every edge's kind, mode and request rule. "+
			"With node, read one node and its edges. Without node, offset and limit page the list; total counts the nodes.")},
		func(ctx context.Context, _ *mcp.CallToolRequest, in graphIn) (*mcp.CallToolResult, any, error) {
			out, err := aliasGraph(ctx, call, in)
			return nil, out, err
		})
	mcp.AddTool(s, &mcp.Tool{Name: "agent_mcp_list", Description: deprecated("agent_mcp_servers_find",
		"List the registered MCP servers: machine, transport, auth source, enabled, health and tool names.")},
		func(ctx context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, any, error) {
			servers, err := roLoadMCPServers(ctx, call)
			if err != nil {
				return nil, nil, err
			}
			return nil, summarize(struct {
				Servers []protocol.MCPServer `json:"servers"`
			}{servers}, ""), nil
		})
	mcp.AddTool(s, &mcp.Tool{Name: "agent_worker_profiles", Description: deprecated("agent_worker_profiles_find",
		"List the network's registered worker profiles, with per-machine model resolution.")},
		func(ctx context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, any, error) {
			raw, err := call(ctx, "worker_profiles", struct{}{})
			if err != nil {
				return nil, nil, err
			}
			out, err := decodeAny(raw)
			return nil, out, err
		})
	mcp.AddTool(s, &mcp.Tool{Name: "agent_start", Description: deprecated("agent_worker_start",
		"Start a worker in a project and give it a task, or continue an ended worker with continue.")},
		func(ctx context.Context, _ *mcp.CallToolRequest, in startIn) (*mcp.CallToolResult, any, error) {
			return aliasStart(ctx, run, in)
		})
	mcp.AddTool(s, &mcp.Tool{Name: "agent_stop", Description: deprecated("agent_worker_stop",
		"Stop a worker you started, or cancel a queued start.")},
		func(ctx context.Context, _ *mcp.CallToolRequest, in stopIn) (*mcp.CallToolResult, any, error) {
			out, err := workerStopCall(ctx, run, in.Session)
			return nil, out, err
		})
	mcp.AddTool(s, &mcp.Tool{Name: "agent_rate", Description: deprecated("agent_worker_rate",
		"Rate a finished worker you started from 1 to 5, or report a defect found later with severity 1 to 4.")},
		func(ctx context.Context, _ *mcp.CallToolRequest, in rateIn) (*mcp.CallToolResult, any, error) {
			return aliasRate(ctx, run, in)
		})
	mcp.AddTool(s, &mcp.Tool{Name: "agent_ask_owner", Description: deprecated("agent_owner_ask",
		"It asks with kind question and shows the question in Needs you.")},
		func(ctx context.Context, _ *mcp.CallToolRequest, in askOwnerIn) (*mcp.CallToolResult, any, error) {
			out, err := ownerAskCall(ctx, run, "question", in.Question, "", in.Options, in.Urgency)
			return nil, out, err
		})
	mcp.AddTool(s, &mcp.Tool{Name: "agent_request_approval", Description: deprecated("agent_owner_ask",
		"It asks with kind approval for one exact action.")},
		func(ctx context.Context, _ *mcp.CallToolRequest, in approvalIn) (*mcp.CallToolResult, any, error) {
			out, err := ownerAskCall(ctx, run, "approval", in.Action, in.Recommendation, nil, "")
			return nil, out, err
		})
	mcp.AddTool(s, &mcp.Tool{Name: "agent_propose_idea", Description: deprecated("agent_owner_ask",
		"It sends with kind idea to the owner's inbox.")},
		func(ctx context.Context, _ *mcp.CallToolRequest, in ideaIn) (*mcp.CallToolResult, any, error) {
			out, err := ownerAskCall(ctx, run, "idea", in.Text, in.Recommendation, nil, "")
			return nil, out, err
		})
}

// aliasList calls the old list method, so the legacy answer is exactly what it
// was: the full AgentView with charter details, pending requests and repo
// information. This name is removed next release, so it keeps its own read.
func aliasList(ctx context.Context, run aliasRun, in listIn) (any, error) {
	online := true
	if in.OnlineOnly != nil {
		online = *in.OnlineOnly
	}
	return run(ctx, "list", map[string]any{"harness": in.Harness, "capability": in.Capability,
		"project": in.Project, "online_only": online, "text": in.Text})
}

// aliasGet calls the old get method, so the legacy answer keeps the full
// AgentView.
func aliasGet(ctx context.Context, run aliasRun, in getIn) (any, error) {
	return run(ctx, "get", in)
}

// aliasNodes reads every machine category, so models and quotas are kept.
func aliasNodes(ctx context.Context, run aliasRun) (protocol.NodesInfo, error) {
	machines, err := aliasCollect(ctx, run, "agent_machines_find", map[string]any{"detail": "full", "limit": roMaxLimit})
	if err != nil {
		return protocol.NodesInfo{}, err
	}
	info := protocol.NodesInfo{}
	seenPlaybook := map[string]bool{}
	for _, raw := range machines {
		var machine protocol.MachineRow
		if err := aliasDecode(raw, &machine); err != nil {
			return protocol.NodesInfo{}, err
		}
		node := protocol.NodeView{NodeID: machine.ID, Hostname: machine.Name, OS: machine.OS,
			Version: machine.Version, Online: machine.Online}
		if t, err := time.Parse(time.RFC3339, machine.LastSeen); err == nil {
			node.LastSeen = t.Unix()
		}
		for _, harness := range machine.Harnesses {
			node.Harnesses = append(node.Harnesses, protocol.HarnessInfo{Name: harness.Name, Version: harness.Version})
		}
		models, err := aliasMachineCategory(ctx, run, machine.ID, "models")
		if err != nil {
			return protocol.NodesInfo{}, err
		}
		for _, rawModel := range models {
			var model protocol.MachineModelRow
			if err := aliasDecode(rawModel, &model); err != nil {
				return protocol.NodesInfo{}, err
			}
			for i := range node.Harnesses {
				if node.Harnesses[i].Name == model.Harness {
					node.Harnesses[i].Models = append(node.Harnesses[i].Models, model.ID)
				}
			}
		}
		quotas, err := aliasMachineCategory(ctx, run, machine.ID, "quotas")
		if err != nil {
			return protocol.NodesInfo{}, err
		}
		if len(quotas) > 0 {
			node.Quotas = &protocol.Quotas{}
			for _, rawQuota := range quotas {
				var quota protocol.MachineQuotaRow
				if err := aliasDecode(rawQuota, &quota); err != nil {
					return protocol.NodesInfo{}, err
				}
				provider := -1
				for i := range node.Quotas.Providers {
					if node.Quotas.Providers[i].Provider == quota.Provider {
						provider = i
					}
				}
				if provider < 0 {
					node.Quotas.Providers = append(node.Quotas.Providers, protocol.QuotaProvider{Provider: quota.Provider})
					provider = len(node.Quotas.Providers) - 1
				}
				window := protocol.QuotaWindow{ID: quota.Window, PercentUsed: quota.PercentUsed}
				if t, err := time.Parse(time.RFC3339, quota.ResetsAt); err == nil {
					window.ResetsAt = t.Unix()
				}
				node.Quotas.Providers[provider].Windows = append(node.Quotas.Providers[provider].Windows, window)
			}
		}
		tracks, err := aliasMachineCategory(ctx, run, machine.ID, "track_record")
		if err != nil {
			return protocol.NodesInfo{}, err
		}
		for _, rawTrack := range tracks {
			var track protocol.MachineTrackRow
			if err := aliasDecode(rawTrack, &track); err != nil {
				return protocol.NodesInfo{}, err
			}
			node.Tracks = append(node.Tracks, protocol.TrackRecord{Harness: track.Harness, Model: track.Model,
				Runs: track.Runs, Complete: track.Complete, Partial: track.Partial, Failed: track.Failed,
				MedianRunSeconds: int64(track.MedianMinutes * 60),
				Resumable:        protocol.HarnessCanResume(track.Harness)})
		}
		notes, err := aliasMachineCategory(ctx, run, machine.ID, "playbook")
		if err != nil {
			return protocol.NodesInfo{}, err
		}
		for _, rawNote := range notes {
			var note protocol.MachinePlaybookRow
			if err := aliasDecode(rawNote, &note); err != nil {
				return protocol.NodesInfo{}, err
			}
			key := note.Subject + "\x00" + note.Note
			if seenPlaybook[key] {
				continue
			}
			seenPlaybook[key] = true
			info.Playbook = append(info.Playbook, protocol.PlaybookNote{Subject: note.Subject, Note: note.Note})
		}
		info.Nodes = append(info.Nodes, node)
	}
	if info.Nodes == nil {
		info.Nodes = []protocol.NodeView{}
	}
	return info, nil
}

func aliasMachineCategory(ctx context.Context, run aliasRun, machine, category string) ([]map[string]any, error) {
	var items []map[string]any
	cursor := ""
	for page := 0; page < 1000; page++ {
		in := map[string]any{"machine": machine, "include": []string{category}, "limit": roMaxLimit}
		if cursor != "" {
			in["cursor"] = cursor
		}
		out, err := readToolCall(ctx, run, "agent_machine_get", in)
		if err != nil {
			return nil, err
		}
		m, _ := out.(map[string]any)
		categoryPage, _ := m[category].(map[string]any)
		if categoryPage == nil {
			break
		}
		if rows, ok := categoryPage["items"].([]any); ok {
			for _, row := range rows {
				if item, ok := row.(map[string]any); ok {
					items = append(items, item)
				}
			}
		}
		next, _ := categoryPage["next_cursor"].(string)
		if next == "" {
			break
		}
		cursor = next
	}
	return items, nil
}

// aliasText adds the track and playbook lines the old agent_nodes text had.
func aliasText(info protocol.NodesInfo) *mcp.CallToolResult {
	raw, _ := json.Marshal(info)
	text := string(raw)
	for _, node := range info.Nodes {
		for i := range node.Tracks {
			text += "\n" + node.Hostname + ": " + node.Tracks[i].Line()
		}
	}
	for i := range info.Playbook {
		text += "\n" + info.Playbook[i].Line()
	}
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: text}}}
}

// aliasProjects calls the old projects method, so the legacy answer keeps the
// full ProjectView (worktrees, harnesses, permissions and registration data).
func aliasProjects(ctx context.Context, run aliasRun, in projectListIn) (any, error) {
	return run(ctx, "projects", protocol.QueryProjects{Name: in.Name})
}

// aliasMessages reads every message through the new tool's loader and returns
// the old {messages, total} answer.
func aliasMessages(ctx context.Context, call Caller, in messagesIn) (map[string]any, error) {
	messages, err := roAllMessages(ctx, call, in.ConversationID)
	if err != nil {
		return nil, err
	}
	sort.SliceStable(messages, func(i, j int) bool {
		if !messages[i].CreatedAt.Equal(messages[j].CreatedAt) {
			return messages[i].CreatedAt.After(messages[j].CreatedAt)
		}
		return messages[i].ID > messages[j].ID
	})
	total := len(messages)
	limit := in.Limit
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}
	if len(messages) > limit {
		messages = messages[:limit]
	}
	return map[string]any{"messages": messages, "total": total}, nil
}

// aliasGraph renders agent_graph_read as the old PublicGraphPage.
func aliasGraph(ctx context.Context, call Caller, in graphIn) (protocol.PublicGraphPage, error) {
	if in.Node != "" {
		read, err := roReadGraph(ctx, call, roGraphReadIn{Node: in.Node})
		if err != nil {
			return protocol.PublicGraphPage{}, err
		}
		page := protocol.PublicGraphPage{Nodes: aliasGraphNodes(read.Nodes), Edges: aliasGraphEdges(read.Edges)}
		page.Total = len(page.Nodes)
		page.EdgeTotal = len(page.Edges)
		return page, nil
	}
	nodes, edges, err := roAllGraphNodes(ctx, call)
	if err != nil {
		return protocol.PublicGraphPage{}, err
	}
	offset := in.Offset
	if offset < 0 {
		offset = 0
	}
	if offset > len(nodes) {
		offset = len(nodes)
	}
	limit := in.Limit
	if limit <= 0 {
		limit = protocol.PublicGraphDefaultLimit
	}
	if limit > protocol.PublicGraphMaxLimit {
		limit = protocol.PublicGraphMaxLimit
	}
	end := offset + limit
	if end > len(nodes) {
		end = len(nodes)
	}
	page := protocol.PublicGraphPage{Nodes: aliasGraphNodes(nodes[offset:end]), Edges: aliasGraphEdges(edges),
		Total: len(nodes), EdgeTotal: len(edges)}
	if end < len(nodes) {
		page.Next = end
	}
	return page, nil
}

func aliasGraphNodes(nodes []roGraphNode) []protocol.PublicGraphNode {
	out := make([]protocol.PublicGraphNode, 0, len(nodes))
	for _, node := range nodes {
		public := protocol.PublicGraphNode{Name: node.Name, Kind: node.Kind, Maturity: node.Maturity,
			Charter: &protocol.Charter{Purpose: node.Purpose}}
		names := make([]string, 0, len(node.Rules))
		for name := range node.Rules {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			public.Rules = append(public.Rules, protocol.PublicRule{Name: name, Value: node.Rules[name]})
		}
		out = append(out, public)
	}
	return out
}

func aliasGraphEdges(edges []roGraphEdge) []protocol.PublicGraphEdge {
	out := make([]protocol.PublicGraphEdge, 0, len(edges))
	for _, edge := range edges {
		out = append(out, protocol.PublicGraphEdge{From: edge.From, To: edge.To, Kind: edge.Kind, Mode: edge.Mode, Rule: edge.Rule})
	}
	return out
}

// aliasStart keeps the old start result and its permission note.
func aliasStart(ctx context.Context, run aliasRun, in startIn) (*mcp.CallToolResult, any, error) {
	if len(in.Touches) == 0 && in.Continue == "" {
		return nil, nil, errors.New(protocol.WorkerTouchesError)
	}
	if in.Harness == "" && in.Continue == "" && in.Model != "" && in.Model != "auto" {
		in.Harness = "opencode"
	}
	out, err := workerStartCall(ctx, run, protocol.WorkerStartRequest{Project: in.Project, Machine: in.Machine,
		Harness: in.Harness, Model: in.Model, Difficulty: in.Difficulty, Touches: in.Touches, Task: in.Task,
		Isolation: in.Isolation, Base: in.Base, Continue: in.Continue, AutoContinue: in.AutoContinue})
	if err != nil {
		return nil, nil, err
	}
	level, _ := out["permissions"].(string)
	harness, _ := out["harness"].(string)
	raw, _ := json.Marshal(out)
	text := string(raw) + "\n" + protocol.WorkerPermissionsText(level, harness)
	var result protocol.WorkerStartResult
	if json.Unmarshal(raw, &result) == nil {
		if result.Track != nil {
			text += "\n" + result.Track.Line()
		}
		for i := range result.Playbooks {
			text += "\n" + result.Playbooks[i].Line()
		}
	}
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: text}}}, out, nil
}

// aliasRate keeps the old rating result and its one-line answer.
func aliasRate(ctx context.Context, run aliasRun, in rateIn) (*mcp.CallToolResult, any, error) {
	out, err := rateCall(ctx, run, protocol.RateRequest{Worker: in.Worker, Kind: in.Kind, Score: in.Score,
		Severity: in.Severity, Note: in.Note})
	if err != nil {
		return nil, nil, err
	}
	accepted, _ := out["accepted"].(bool)
	if !accepted {
		reason, _ := out["reason"].(string)
		if reason != "" {
			return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "Not recorded: " + reason}}}, out, nil
		}
		return nil, out, nil
	}
	worker, _ := out["worker"].(string)
	kind, _ := out["kind"].(string)
	score, _ := out["score"].(float64)
	model, _ := out["model"].(string)
	difficulty, _ := out["difficulty"].(string)
	text := "Recorded rating " + strconv.Itoa(int(score)) + " for worker " + worker
	if kind == protocol.RateKindDefectLater {
		severity, _ := out["severity"].(float64)
		text = "Recorded defect found later (severity " + strconv.Itoa(int(severity)) + ") on worker " + worker
	}
	if model != "" {
		text += " (" + model
		if difficulty != "" {
			text += ", " + difficulty
		}
		text += ")"
	}
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: text + "."}}}, out, nil
}

// aliasCollect reads every page of one new read tool.
func aliasCollect(ctx context.Context, run aliasRun, tool string, in map[string]any) ([]map[string]any, error) {
	var items []map[string]any
	cursor := ""
	for page := 0; page < 1000; page++ {
		q := map[string]any{}
		for k, v := range in {
			q[k] = v
		}
		if cursor != "" {
			q["cursor"] = cursor
		}
		out, err := readToolCall(ctx, run, tool, q)
		if err != nil {
			return nil, err
		}
		m, _ := out.(map[string]any)
		if m == nil {
			break
		}
		if rows, ok := m["items"].([]any); ok {
			for _, row := range rows {
				if item, ok := row.(map[string]any); ok {
					items = append(items, item)
				}
			}
		}
		next, _ := m["next_cursor"].(string)
		if next == "" {
			break
		}
		cursor = next
	}
	return items, nil
}

func aliasDecode(in any, dst any) error {
	raw, err := json.Marshal(in)
	if err != nil {
		return err
	}
	return json.Unmarshal(raw, dst)
}

func containsString(list []string, want string) bool {
	for _, item := range list {
		if item == want {
			return true
		}
	}
	return false
}
