package gateway

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/niparis/agent-net/internal/protocol"
)

type proposalValidationKey struct{}

func validatingProposal(ctx context.Context) bool {
	v, _ := ctx.Value(proposalValidationKey{}).(bool)
	return v
}

// Proposal validation never probes an MCP server, starts a worker, edits a
// native file or saves a plan. Database-only changes use rollback transactions.
func (g *Gateway) validateCTOProposal(ctx context.Context, nc *nodeConn, raw []byte) (any, error) {
	var q struct {
		Tool  string          `json:"tool"`
		Args  json.RawMessage `json:"args"`
		Owner string          `json:"owner"`
	}
	if json.Unmarshal(raw, &q) != nil || !g.validToolOwner(nc, q.Owner) {
		return nil, reject("owner authority required")
	}
	ctx = context.WithValue(ctx, proposalValidationKey{}, true)
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(q.Args, &fields); err != nil {
		return nil, err
	}
	str := func(key string) string { var v string; _ = json.Unmarshal(fields[key], &v); return v }
	st := g.store
	switch q.Tool {
	case "charter_set":
		var p protocol.CharterSetRequest
		if err := json.Unmarshal(q.Args, &p); err != nil {
			return nil, err
		}
		return g.handle(ctx, nc, "charter.validate", q.Args)
	case "edge_set":
		return g.handle(ctx, nc, "edge.validate", q.Args)
	case "machine_remove", "machine_rotate":
		return nil, g.checkMachineAction(str("node_id"), q.Tool == "machine_rotate")
	case "node_rename":
		return g.handle(ctx, nc, "node.rename.validate", q.Args)
	case "repo_create", "repo_archive", "agent_create", "agent_tombstone":
		var p protocol.GraphLifecycleRequest
		if err := json.Unmarshal(q.Args, &p); err != nil {
			return nil, err
		}
		return nil, st.GraphLifecycle(strings.ReplaceAll(q.Tool, "_", "."), p, "owner", true)
	case "node_add":
		return nil, st.AddArchitect(str("name"), "owner", true)
	case "node_remove":
		return nil, st.RemoveArchitect(str("name"), "owner", true)
	case "edge_remove":
		var p protocol.Edge
		if err := json.Unmarshal(q.Args, &p); err != nil {
			return nil, err
		}
		return nil, st.RemoveEdge(p, "owner", true)
	case "graph_undo":
		var p protocol.GraphUndoRequest
		if err := json.Unmarshal(q.Args, &p); err != nil {
			return nil, err
		}
		if p.ID < 0 {
			return nil, reject("bad graph.undo")
		}
		return nil, st.GraphUndo(p.ID, "owner", true)
	case "node_settings_set":
		return nil, st.SetNodeSetting(str("node"), str("setting"), str("value"), "owner", true)
	case "session_rename":
		return st.RenameSession(str("session_id"), str("name"), true)
	case "project_display_set":
		return st.SetProjectDisplayName(str("project"), str("name"), true)

	case "session_trust":
		var p protocol.SessionTrust
		if err := json.Unmarshal(q.Args, &p); err != nil {
			return nil, err
		}
		ok, err := st.SetPeerTrust(p.SessionID, p.On, true)
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, reject("unknown or untrustable session")
		}
		return nil, nil
	case "session_policy":
		return g.handle(ctx, nc, "session.policy", q.Args)
	case "held_answer":
		return st.AnswerHeld(str("message_id"), str("action"), true)
	case "inbox_answer":
		return st.AnswerInbox(str("id"), str("answer"), true)
	case "outcome_overrule":
		return st.ResolveOutcome(str("reply_id"), q.Owner, str("action"), true)
	case "worker_start":
		var p protocol.WorkerStartRequest
		if err := json.Unmarshal(q.Args, &p); err != nil {
			return nil, err
		}
		return g.workerStart(ctx, nc, protocol.WorkerStartFrame{WorkerStartRequest: p, Owner: q.Owner, FromCTO: true}, true)
	case "worker_stop":
		return g.workerStop(ctx, nc, protocol.WorkerStopFrame{Session: str("session"), Requester: q.Owner}, true)
	case "worker_profile_add", "worker_profile_remove":
		var p protocol.WorkerProfileRequest
		if err := json.Unmarshal(q.Args, &p); err != nil {
			return nil, err
		}
		return g.workerProfileAction(nc.id, strings.ReplaceAll(q.Tool, "_", "."), protocol.WorkerProfileFrame{WorkerProfileRequest: p, Requester: q.Owner}, true)
	case "mcp_add":
		var p protocol.MCPAdd
		if err := json.Unmarshal(q.Args, &p); err != nil {
			return nil, err
		}
		p.Server.NodeID = ""
		p.Server.Enabled = true
		return g.mcpAdd(ctx, nc, p, true)
	case "mcp_remove", "mcp_test", "mcp_enable":
		srv, ok, err := st.MCPServer(str("name"))
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, reject("no MCP server named %q", str("name"))
		}
		if q.Tool == "mcp_test" && g.conn(srv.NodeID) == nil {
			return nil, reject("host node is offline")
		}
		return nil, nil
	case "mcp_call":
		var p protocol.MCPCall
		if err := json.Unmarshal(q.Args, &p); err != nil {
			return nil, err
		}
		p.Owner = q.Owner
		p.SessionID = ""
		return g.mcpCall(ctx, nc, p, true)
	case "tools_toggle":
		var p protocol.ToolToggleRequest
		if err := json.Unmarshal(q.Args, &p); err != nil {
			return nil, err
		}
		return g.toggleTool(ctx, p, "owner")
	case "tools_new_projects":
		var p protocol.ToolNewProjectsRequest
		if err := json.Unmarshal(q.Args, &p); err != nil {
			return nil, err
		}
		return g.setToolNewProjects(ctx, p, "owner")
	case "tools_plan":
		var p protocol.ToolPlanRequest
		if err := json.Unmarshal(q.Args, &p); err != nil {
			return nil, err
		}
		return g.planTool(ctx, p, "owner")
	case "tools_apply":
		return g.applyTool(ctx, str("plan_id"), "owner")
	case "tools_undo":
		return g.undoTool(ctx, str("item"), "owner")
	case "mcp_import":
		if (str("candidate") == "") == (str("plan_id") == "") {
			return nil, reject("import needs a candidate or plan_id")
		}
		if str("candidate") != "" {
			return g.planMCPImport(ctx, str("candidate"), "owner")
		}
		return g.applyMCPImport(ctx, str("plan_id"), "owner", q.Owner)
	case "mcp_native_remove":
		count := 0
		for _, key := range []string{"candidate", "plan_id", "undo_plan_id"} {
			if str(key) != "" {
				count++
			}
		}
		if count != 1 {
			return nil, reject("native removal needs one of candidate, plan_id or undo_plan_id")
		}
		if str("candidate") != "" {
			return g.planMCPNativeRemove(ctx, str("candidate"), "owner")
		}
		if str("plan_id") != "" {
			return g.applyMCPNativeRemove(ctx, str("plan_id"), "owner", q.Owner)
		}
		return g.undoMCPNativeRemove(ctx, str("undo_plan_id"), "owner")
	case "playbook_set":
		var p protocol.PlaybookSetRequest
		if err := json.Unmarshal(q.Args, &p); err != nil {
			return nil, err
		}
		_, _, _, err := protocol.CleanPlaybook(p.Model, p.Harness, p.Note)
		return nil, err
	case "send", "message", "reply":
		if str("body") == "" || len(str("body")) > protocol.MaxBodyBytes {
			return nil, reject("message needs a body within the message limit")
		}
		if q.Tool == "reply" || str("action") == "reply" {
			m, ok, err := st.Message(str("message_id"))
			if err != nil {
				return nil, err
			}
			var cto string
			if err := st.db.QueryRow(`SELECT s.id FROM sessions s JOIN owner_settings o ON o.key='cto_session' AND o.value=s.id WHERE s.node_id=?`, nc.id).Scan(&cto); err != nil {
				return nil, reject("CTO network session is not registered on this node")
			}
			if !ok || m.To != cto {
				return nil, reject("message was not sent to the CTO")
			}
			return nil, nil
		}
		a, ok, err := st.Agent(str("to"))
		if err != nil {
			return nil, err
		}
		if !ok || protocol.IsOwner(a.Harness) {
			return nil, reject("unknown or unsuitable recipient")
		}
		return nil, nil

	case "project_remove":
		node := nc.id
		if on := str("on"); on != "" {
			var err error
			if node, err = st.NodeByHostOrID(on); err != nil {
				return nil, err
			}
		}
		return st.RemoveProject(node, str("name"), str("path"), nc.id, true)

	default:
		return nil, reject("unsupported proposal tool %q", q.Tool)
	}
}
