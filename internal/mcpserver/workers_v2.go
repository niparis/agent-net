package mcpserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/niparis/agent-net/internal/protocol"
	"github.com/niparis/agent-net/internal/toolcatalog"
)

// addRedesignedTools registers the agent and worker contracts directly from
// the shared catalog. The daemon methods remain the existing implementations.
func addRedesignedTools(s *mcp.Server, call Caller, run func(context.Context, string, any) (any, error)) {
	for _, name := range []string{"agent_worker_start", "agent_worker_stop", "agent_worker_rate", "agent_handback", "agent_announce", "agent_owner_ask", "agent_learn", "agent_charter_draft", "agent_project_add", "agent_mcp_test"} {
		entry := redesignedCatalogTool(name)
		t := &mcp.Tool{Name: entry.Name, Description: entry.Description, InputSchema: json.RawMessage(entry.InputSchema)}
		s.AddTool(t, func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			var args map[string]any
			if err := json.Unmarshal(req.Params.Arguments, &args); err != nil {
				return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: err.Error()}}}, nil
			}
			out, err := redesignedCall(ctx, call, name, args, run)
			if err != nil {
				return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: err.Error()}}}, nil
			}
			return catalogResult(name, out)
		})
	}
	if os.Getenv("AGENT_NET_WORKER") != "" {
		// These are agent-only capabilities.
		s.RemoveTools("agent_worker_start", "agent_worker_stop", "agent_worker_rate", "agent_owner_ask",
			"agent_project_add", "agent_announce", "agent_charter_draft")
	}
}

func redesignedCatalogTool(name string) *toolcatalog.Tool {
	for i := range toolcatalog.Tools {
		if toolcatalog.Tools[i].Name == name {
			return &toolcatalog.Tools[i]
		}
	}
	panic("missing tool catalog entry: " + name)
}

func redesignedCall(ctx context.Context, call Caller, name string, a map[string]any, run func(context.Context, string, any) (any, error)) (map[string]any, error) {
	str := func(k string) string { v, _ := a[k].(string); return v }
	integer := func(k string) int { v, _ := a[k].(float64); return int(v) }
	switch name {
	case "agent_worker_start":
		task, continuation := str("task"), str("continue")
		if (task == "") == (continuation == "") {
			return nil, errors.New(`set exactly one of task or continue, for example {"task":"Fix the login bug"}`)
		}
		touches, _ := a["touches"].([]any)
		var areas []string
		for _, v := range touches {
			if x, ok := v.(string); ok {
				areas = append(areas, x)
			}
		}
		in := startIn{Touches: areas, Project: str("project"), Task: task, Title: str("title"), Harness: str("harness"), Difficulty: str("difficulty"), Model: str("model"), Provider: str("provider"), Effort: str("effort"), Machine: str("machine"), Isolation: str("isolation"), Base: str("base"), MCPServers: str("mcp_servers"), Continue: continuation}
		if v, ok := a["auto_continue"].(bool); ok {
			in.AutoContinue = &v
		}
		if in.Harness == "" && in.Model != "" && in.Model != "auto" {
			in.Harness = "opencode"
		}
		m, err := workerStartCall(ctx, run, protocol.WorkerStartRequest{NotFamily: stringSlice(a["not_family"]), Project: in.Project, Machine: in.Machine, Harness: in.Harness, Model: in.Model, Provider: in.Provider, Effort: in.Effort, Difficulty: in.Difficulty, Touches: in.Touches, Task: in.Task, Title: in.Title, Isolation: in.Isolation, Base: in.Base, MCPServers: in.MCPServers, Fixes: str("fixes"), FixesSeverity: integer("fixes_severity"), Continue: in.Continue, AutoContinue: in.AutoContinue})
		if err != nil {
			return nil, err
		}
		out := map[string]any{}
		// The model note says which model was chosen and why; keep it.
		if note, ok := m["model_note"].(string); ok && note != "" {
			out["model_note"] = note
		}
		// The judge's difficulty, worded for the caller to tell its user.
		if line, ok := m["owner_line"].(string); ok && line != "" {
			out["owner_line"] = line
		}
		// Set only when the caller named the model instead of the picker.
		if r, ok := m["reminder"].(string); ok && r != "" {
			out["reminder"] = r
		}
		if q, ok := m["queue_id"].(string); ok {
			out["queued"] = map[string]any{"queue_id": q, "position": m["position"], "reason": firstValue(m, "queue_reason", "reason")}
			return out, nil
		}
		// The daemon's answer does not repeat the request: fill the task from
		// it. The difficulty comes from the worker record, never from the
		// caller's ignored field.
		if m["task"] == nil {
			m["task"] = in.Task
		}
		if m["difficulty"] == nil {
			m["difficulty"] = "normal"
		}
		worker := filledWorker(ctx, call, m)
		harness, _ := m["harness"].(string)
		model, _ := m["model"].(string)
		selection := protocol.ParseModelSelection(harness, "", model, "")
		worker["model"] = selection.Model
		worker["provider"], worker["effort"] = selection.Provider, selection.Effort
		if selection.Provider == "" {
			worker["provider"] = protocol.ModelVendor(harness, selection.Model)
		}
		worker["model_selection"] = selection.Display()

		out["worker"] = worker
		return out, nil
	case "agent_worker_stop":
		m, err := workerStopCall(ctx, run, str("worker"))
		if err != nil {
			return nil, err
		}
		m["state"] = "stopped"
		return map[string]any{"worker": workerOutput(m)}, nil
	case "agent_worker_rate":
		kind := str("kind")
		score, severity := integer("score"), integer("severity")
		if kind == "rating" && score == 0 {
			return nil, errors.New(`score is required for rating, for example {"kind":"rating","score":4}`)
		}
		if kind == "defect_found_later" && severity == 0 {
			return nil, errors.New(`severity is required for defect_found_later, for example {"kind":"defect_found_later","severity":2,"note":"Visible annoyance"}`)
		}
		if kind != "rating" && kind != "defect_found_later" {
			return nil, errors.New(`kind must be rating or defect_found_later`)
		}
		m, err := rateCall(ctx, run, protocol.RateRequest{Worker: str("worker"), Kind: kind, Score: score, Severity: severity, Note: str("note")})
		if err != nil {
			return nil, err
		}
		worker := map[string]any{"session_id": m["worker"], "model": m["model"], "harness": m["harness"], "difficulty": m["difficulty"]}
		result := map[string]any{"recorded": m["accepted"], "worker": filledWorker(ctx, call, worker), "kind": kind}
		if score != 0 {
			result["score"] = score
		}
		if severity != 0 {
			result["severity"] = severity
		}
		return result, nil
	case "agent_handback":
		var complete *bool
		if v, ok := a["complete"].(bool); ok {
			complete = &v
		}
		m, err := handbackCall(ctx, run, protocol.WorkerHandbackRequest{Report: str("report"), Status: str("status"), Complete: complete, Files: stringSlice(a["files"])})
		if err != nil {
			return nil, err
		}
		delivered := m["sent"] != nil && !boolValue(m["queued"])
		return map[string]any{"delivered": delivered, "status": m["status"], "complete": m["complete"]}, nil
	case "agent_announce":
		m, err := announceCall(ctx, run, announceIn{Task: str("task"), Project: str("project"), Expertise: str("expertise"), Capabilities: stringSlice(a["capabilities"]), Waiting: str("waiting")})
		if err != nil {
			return nil, err
		}
		// The daemon answers a bare protocol.Profile. An older or wrapped
		// answer may put it under "profile"; accept both shapes and keep only
		// the profile fields the catalog output schema allows.
		if p, ok := m["profile"].(map[string]any); ok && p != nil {
			m = p
		}
		return map[string]any{"profile": profileOutput(m)}, nil
	case "agent_owner_ask":
		kind := str("kind")
		replaces := str("replaces")
		if kind == "" && replaces != "" {
			kind = "question"
		}
		m, err := ownerAskCall(ctx, run, kind, str("text"), str("recommendation"), stringSlice(a["options"]), str("urgency"), replaces)
		if err != nil {
			return nil, err
		}
		if m["kind"] == nil {
			m["kind"] = kind
		}
		top := m
		// A question only marks the session as waiting; it has no inbox item.
		// Never return null: the result must match the catalog schema, and an
		// error here would make agents ask again.
		if nested, ok := m["item"].(map[string]any); ok {
			m = nested
		}
		item, _ := m["item_id"].(string)
		if item == "" {
			item, _ = m["id"].(string)
		}
		state, _ := m["state"].(string)
		if state == "" {
			state, _ = m["status"].(string)
		}
		if state == "" {
			state = "waiting"
		}
		out := map[string]any{"item_id": item, "kind": m["kind"], "state": state}
		// The team lead takes the question: say so, and what to do meanwhile.
		if red, ok := top["redirected"].(map[string]any); ok {
			out["state"] = "sent_to_lead"
			if id, _ := red["item_id"].(string); id != "" {
				out["item_id"] = id
			}
			if note, _ := red["note"].(string); note != "" {
				out["note"] = note
			}
		}
		return out, nil
	case "agent_learn":
		m, err := learnCall(ctx, run, protocol.LearnRequest{SubjectKind: str("subject_kind"), Subject: str("subject"), Finding: str("finding"), Evidence: stringSlice(a["evidence"])})
		if err != nil {
			return nil, err
		}
		id := m["id"]
		if id == nil {
			id = ""
		}
		if n, ok := id.(float64); ok {
			id = fmt.Sprint(int64(n))
		}
		remaining := m["remaining"]
		if remaining == nil {
			remaining = float64(0)
		}
		return map[string]any{"recorded": m["accepted"], "id": id, "left_today": remaining}, nil
	case "agent_charter_draft":
		m, err := charterDraftCall(ctx, run, protocol.CharterDraftRequest{Level: str("level"), Attr: str("attr"), Value: str("value"), Team: str("team")})
		if err != nil {
			return nil, err
		}
		return map[string]any{"id": m["id"], "status": m["status"]}, nil
	case "agent_project_add":
		m, err := projectAddCall(ctx, run, protocol.ProjectAddRequest{Paths: stringSlice(a["paths"]), Dir: str("dir"), Scan: boolValue(a["scan"]), Depth: integer("depth"), DryRun: boolValue(a["dry_run"])})
		if err != nil {
			return nil, err
		}
		if m["projects"] == nil {
			var projects []any
			for _, key := range []string{"added", "existing"} {
				rows, _ := m[key].([]any)
				for _, row := range rows {
					p, _ := row.(map[string]any)
					if p == nil {
						continue
					}
					name, _ := p["name"].(string)
					folder, _ := p["path"].(string)
					projects = append(projects, map[string]any{"name": name, "display_name": name, "machine": m["machine"], "folder": folder, "remote": p["remote"], "default_branch": p["default_branch"], "sessions_online": 0, "sessions_total": 0, "source": "registered"})
				}
			}
			m["projects"] = projects
		}
		if m["dry_run"] == nil {
			m["dry_run"] = boolValue(a["dry_run"])
		}
		return map[string]any{"projects": m["projects"], "dry_run": m["dry_run"]}, nil
	case "agent_mcp_test":
		m, err := mcpTestCall(ctx, run, str("server"))
		if err != nil {
			return nil, err
		}
		return map[string]any{"ok": m["ok"], "tools": m["tools"], "detail": m["detail"]}, nil
	}
	return nil, fmt.Errorf("unknown redesigned tool %q", name)
}

// profileOutput turns the daemon's protocol.Profile into the announce output
// shape. It ignores unknown keys and always fills the fields the schema
// requires, including function and an empty capabilities list.
func profileOutput(m map[string]any) map[string]any {
	raw, _ := json.Marshal(m)
	var p protocol.Profile
	_ = json.Unmarshal(raw, &p)
	capabilities := p.Capabilities
	if capabilities == nil {
		capabilities = []string{}
	}
	return map[string]any{"name": p.Name, "function": p.Function, "project": p.Project,
		"capabilities": capabilities, "task": p.Task, "expertise": p.Expertise}
}

// filledWorker returns the worker record the read tools would show. The
// daemon's start and rate answers carry only a session ID, so read the record
// from the gateway; when it is not listed yet, keep what the answer had and
// fill the caller from the calling session.
func filledWorker(ctx context.Context, call Caller, m map[string]any) map[string]any {
	out := workerOutput(m)
	id, _ := out["id"].(string)
	if row, ok := roWorkerByID(ctx, call, id); ok {
		// The start, stop and rate results declare the short worker record;
		// the assessment belongs to agent_workers_find with detail full.
		full := workerRowMap(row)
		delete(full, "assessment")
		return full
	}
	if caller, ok := callerRef(ctx, call); ok {
		out["caller"] = map[string]any{"id": caller.ID, "name": caller.Name}
	}
	return out
}

func workerOutput(m map[string]any) map[string]any {
	id := m["session_id"]
	if id == nil {
		id = m["worker"]
	}
	if id == nil {
		id = m["session"]
	}
	if id == nil {
		id = ""
	}
	name := m["name"]
	if name == nil {
		name = id
	}
	caller := m["caller"]
	if caller == nil {
		caller = map[string]any{"id": "", "name": ""}
	}
	out := map[string]any{"id": id, "name": name, "task": m["task"], "title": m["title"], "machine": firstValue(m, "machine", "hostname"), "project": m["project"], "harness": m["harness"], "model": m["model"], "difficulty": m["difficulty"], "caller": caller, "state": firstValue(m, "state", "status"), "run_time": firstValue(m, "run_time", "runtime")}

	for _, k := range []string{"started", "branch"} {
		if v := m[k]; v != nil {
			out[k] = v
		}
	}
	for k, v := range out {
		if v == nil {
			out[k] = ""
		}
	}
	return out
}
func firstValue(m map[string]any, keys ...string) any {
	for _, k := range keys {
		if v := m[k]; v != nil {
			return v
		}
	}
	return ""
}
func stringSlice(v any) []string {
	a, _ := v.([]any)
	out := make([]string, 0, len(a))
	for _, x := range a {
		if s, ok := x.(string); ok {
			out = append(out, s)
		}
	}
	return out
}
func boolValue(v any) bool { b, _ := v.(bool); return b }

func firstNonEmptyString(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}
