package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"

	"github.com/niparis/agent-net/internal/mcpserver"
	"github.com/niparis/agent-net/internal/protocol"
)

// executeCTOOperator fronts the same owner handlers as the TUI. The operation
// table is explicit and excludes consent completion and UI acknowledgements.
func (d *Daemon) executeCTOOperator(ctx context.Context, raw json.RawMessage) (any, error) {
	var p struct {
		Method    string          `json:"method"`
		Arguments json.RawMessage `json:"arguments"`
		Reason    string          `json:"reason"`
	}
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, err
	}
	read, ok := protocol.CTOOperatorMethods()[p.Method]
	if !ok {
		return nil, fmt.Errorf("unknown owner operation %q", p.Method)
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil {
		return nil, fmt.Errorf("bad owner operation")
	}
	delete(fields, "reason")
	checked, err := json.Marshal(fields)
	if err != nil {
		return nil, err
	}
	if err := mcpserver.CTOArguments("owner_operation", checked); err != nil {
		return nil, err
	}
	var arguments map[string]json.RawMessage
	if json.Unmarshal(p.Arguments, &arguments) != nil || arguments == nil {
		return nil, fmt.Errorf("operator arguments must be an object")
	}
	// Attribution is supplied by the daemon, never by tool arguments.
	ctx = context.WithValue(ctx, proposalActorKey{}, "cto")
	lc := &localConn{d: d, client: "cli", cto: true}
	call := func(method string, args json.RawMessage) (any, error) {
		if method == "session_enter" || method == "cto_enter" {
			var q struct {
				SessionID string `json:"session_id"`
				UINodeID  string `json:"ui_node_id"`
			}
			if err := json.Unmarshal(args, &q); err != nil {
				return nil, err
			}
			return d.requestOwnerEnter(ctx, method, q.SessionID, q.UINodeID)
		}

		result, handled, err := lc.dispatchOwner(ctx, method, args)
		if !handled {
			result, handled, err = lc.dispatchActions(ctx, "cli", method, args)
		}
		if !handled {
			return lc.dispatch(ctx, method, args)
		}
		return result, err
	}
	graphChange := p.Method == "team_write" || p.Method == "delivery_owner_set" || p.Method == "merger_set"
	if graphChange {
		d.ctoGraph.mu.Lock()
		defer d.ctoGraph.mu.Unlock()
	}
	var plan []ownerUndoCall
	if !read {
		plan, err = d.operatorUndoBefore(ctx, p.Method, p.Arguments, call)
		if err != nil {
			return nil, err
		}
	}
	var settingsBefore int64
	if p.Method == "merger_set" {
		data, e := d.call(ctx, "settings.snapshot", nil)
		if e != nil {
			return nil, e
		}
		var snapshot struct {
			Revision int64 `json:"revision"`
		}
		if e = json.Unmarshal(data, &snapshot); e != nil {
			return nil, e
		}
		settingsBefore = snapshot.Revision
	}
	var graphBefore int64
	if graphChange {
		data, e := d.call(ctx, "cto.graph_latest", nil)
		if e != nil {
			return nil, e
		}
		var v struct {
			ID int64 `json:"id"`
		}
		if e = json.Unmarshal(data, &v); e != nil {
			return nil, e
		}
		graphBefore = v.ID
	}
	return runOwnerOperation(p.Method, p.Arguments, p.Reason, call, func(result any) ([]ownerUndoCall, error) {
		if graphChange {
			history, e := d.ctoGraphHistory(ctx, graphBefore)
			if e != nil {
				return nil, e
			}
			for i := len(history) - 1; i >= 0; i-- {
				plan = append(plan, undoCall("graph_undo", protocol.GraphUndoRequest{ID: history[i].ID}))
			}
		}
		if p.Method == "merger_set" {
			data, e := d.call(ctx, "settings.history", map[string]any{"key": "delivery.pipeline.merge.requires", "owner": true})
			if e != nil {
				return nil, e
			}
			var history []protocol.SettingsHistory
			if e = json.Unmarshal(data, &history); e != nil {
				return nil, e
			}
			var q protocol.MergerSetRequest
			if e = json.Unmarshal(p.Arguments, &q); e != nil {
				return nil, e
			}
			revisions := map[int64]bool{}
			for _, h := range history {
				if h.Revision > settingsBefore && h.Actor == "cto" && h.Scope == q.Project && h.Reason == "enable merger pipeline" {
					revisions[h.Revision] = true
				}
			}
			var ids []int64
			for id := range revisions {
				ids = append(ids, id)
			}
			sort.Slice(ids, func(i, j int) bool { return ids[i] > ids[j] })
			for _, id := range ids {
				plan = append(plan, undoCall("settings_undo", protocol.SettingsUndo{Revision: id}))
			}
		}
		data, e := json.Marshal(result)
		if e != nil {
			return nil, e
		}
		switch p.Method {
		case "sessions_archive":
			var saved protocol.SessionsArchiveResult
			if e := json.Unmarshal(data, &saved); e != nil {
				return nil, e
			}
			var q protocol.SessionsArchiveRequest
			if e := json.Unmarshal(p.Arguments, &q); e != nil {
				return nil, e
			}
			var ids []string
			for _, v := range saved.Sessions {
				ids = append(ids, v.SessionID)
			}
			if len(ids) > 0 {
				plan = append(plan, undoCall("sessions_archive", protocol.SessionsArchiveRequest{IDs: ids, Restore: !q.Restore}))
			}
		case "mcp_coverage_apply":
			var applied protocol.MCPCoverageResult
			if json.Unmarshal(data, &applied) == nil && applied.PlanID != "" {
				plan = append(plan, undoCall("mcp_coverage_undo", protocol.MCPCoverageUndoRequest{PlanID: applied.PlanID}))
			}
		case "learned_answer":
			var request struct {
				ID     int64  `json:"suggestion_id"`
				Action string `json:"action"`
			}
			if json.Unmarshal(p.Arguments, &request) == nil && request.Action == "accept" && request.ID < 0 {
				// A charter draft: the answer is the stored row, and it names the
				// graph entry the accept wrote.
				var row struct {
					HistoryID int64 `json:"history_id"`
				}
				if json.Unmarshal(data, &row) == nil && row.HistoryID != 0 {
					plan = append(plan, undoCall("graph_undo", protocol.GraphUndoRequest{ID: row.HistoryID}))
				}
			} else if json.Unmarshal(p.Arguments, &request) == nil && request.Action == "accept" {
				list, e := call("learned_list", json.RawMessage(`{}`))
				if e != nil {
					return nil, e
				}
				raw, e := json.Marshal(list)
				if e != nil {
					return nil, e
				}
				var items []protocol.LearnedSuggestion
				if e = json.Unmarshal(raw, &items); e != nil {
					return nil, e
				}
				for _, item := range items {
					if item.ID == request.ID && item.HistoryID != 0 {
						plan = append(plan, undoCall("graph_undo", protocol.GraphUndoRequest{ID: item.HistoryID}))
					}
				}
			}
		}
		return plan, nil
	}, d.recordCTOAction, func(text string, options []string) error {
		// The gateway already logs a permission grant with the owner's words,
		// each target's result and its undo. A second line would repeat it.
		if p.Method == protocol.OpPermissionApply {
			return nil
		}
		_, e := d.call(ctx, "inbox.create", map[string]any{"session_id": d.ctoSessionID(), "kind": "check", "text": text, "options": options})
		return e
	})
}
