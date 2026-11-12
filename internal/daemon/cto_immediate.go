package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/niparis/agent-net/internal/protocol"
)

// executeImmediateCTOTool runs classified mutations through the same concrete
// owner handlers used for approved calls and records a durable Inbox notice.
func (d *Daemon) executeImmediateCTOTool(ctx context.Context, name string, raw json.RawMessage) (any, error) {
	class, err := ctoClass(name, raw)
	if err != nil {
		return nil, err
	}
	if name == "settings_change" {
		concrete, args, _, e := ctoConcreteCall(name, raw)
		if e != nil {
			return nil, e
		}
		if concrete == "owner_operation" {
			return d.executeCTOOperator(ctx, args)
		}
	}
	if name == "mcp_change" {
		concrete, args, _, e := ctoConcreteCall(name, raw)
		if e != nil {
			return nil, e
		}
		if strings.HasPrefix(concrete, "apps.") {
			return d.executeCTOApps(ctx, concrete, args)
		}
	}
	if class != ctoMinor {
		return d.executeCTOTool(ctx, name, raw)
	}
	concrete, args, _, err := ctoConcreteCall(name, raw)
	if err != nil {
		return nil, err
	}
	var fields map[string]json.RawMessage
	if len(args) > 0 && json.Unmarshal(args, &fields) != nil {
		return nil, fmt.Errorf("bad CTO arguments")
	}
	if fields == nil {
		fields = map[string]json.RawMessage{}
	}
	var reason string
	if value, ok := fields["reason"]; ok && json.Unmarshal(value, &reason) != nil {
		return nil, fmt.Errorf("reason must be a string")
	}
	if concrete != "settings_values_write" && concrete != "protected_providers_set" {
		delete(fields, "reason")
	}
	if concrete == "session_start" {
		// trust:true trusts the folder as its own audited action, then starts.
		var on bool
		if value, ok := fields["trust"]; ok {
			if json.Unmarshal(value, &on) != nil {
				return nil, fmt.Errorf("trust must be a boolean")
			}
			delete(fields, "trust")
		}
		if on {
			if err := d.ctoTrustBeforeStart(ctx, fields); err != nil {
				return nil, err
			}
		}
	}
	args, err = json.Marshal(fields)
	if err != nil {
		return nil, err
	}
	if name == "graph_change" {
		d.ctoGraph.mu.Lock()
		defer d.ctoGraph.mu.Unlock()
	}

	var last int64
	if name == "graph_change" {
		raw, e := d.call(ctx, "cto.graph_latest", nil)
		if e != nil {
			return nil, e
		}
		var latest struct {
			ID int64 `json:"id"`
		}
		if e = json.Unmarshal(raw, &latest); e != nil {
			return nil, e
		}
		last = latest.ID
	}

	var settingsBefore int64
	if concrete == "settings_values_write" || concrete == "settings_values_undo" {
		snapshot, e := d.call(ctx, "settings.snapshot", nil)
		if e != nil {
			return nil, e
		}
		var state struct {
			Revision int64 `json:"revision"`
		}
		if e = json.Unmarshal(snapshot, &state); e != nil {
			return nil, e
		}
		settingsBefore = state.Revision
	}
	var previousProtected protocol.ProtectedProviders
	var previousProviders protocol.WorkerProviders
	var previousIntegrations protocol.IntegrationsResult
	var previousScores []protocol.ModelScore
	var previousPlaybook protocol.PlaybookGetResult
	var previousToolProjects protocol.ToolProjectState
	var oldProfiles []protocol.WorkerProfile
	var haveProviders, haveProfiles, haveIntegrations, haveScores, havePlaybook, haveToolProjects bool
	switch concrete {
	case "protected_providers_set":
		previousProtected, err = d.protectedProviders(ctx, nil)
		haveProviders = err == nil
	case "worker_providers_set":
		previousProviders, err = d.workerProviders(ctx, nil)
		haveProviders = err == nil
	case "score_set":
		previousScores, err = d.scores(ctx, nil)
		haveScores = err == nil
	case "integrations_set":
		previousIntegrations, err = d.integrationsGet(ctx)
		haveIntegrations = err == nil && previousIntegrations.Set
	case "playbook_set":
		var q protocol.PlaybookSetRequest
		_ = json.Unmarshal(args, &q)
		before, e := d.call(ctx, "playbook.get", protocol.PlaybookGetRequest{Model: q.Model, Harness: q.Harness})
		if e == nil {
			havePlaybook = json.Unmarshal(before, &previousPlaybook) == nil
		}
		if previousPlaybook.Note != nil && !strings.EqualFold(previousPlaybook.Note.Harness, q.Harness) {
			previousPlaybook.Note = nil
		}
	case "tools_new_projects":
		var q protocol.ToolNewProjectsRequest
		_ = json.Unmarshal(args, &q)
		before, e := d.call(ctx, "query.tools_projects", map[string]string{"owner": d.ownerSessionID()})
		if e == nil {
			var states []protocol.ToolProjectState
			if json.Unmarshal(before, &states) == nil {
				for _, state := range states {
					if state.Item == q.Item {
						previousToolProjects, haveToolProjects = state, true
						break
					}
				}
			}
		}
	case "worker_profile_add", "worker_profile_remove":
		list, e := d.call(ctx, "worker.profile.list", protocol.WorkerProfileFrame{Requester: d.ownerSessionID()})
		if e == nil {
			haveProfiles = json.Unmarshal(list, &oldProfiles) == nil
		}
	}

	var oldName string
	var oldTrust bool
	var oldMachine protocol.NodeView
	var oldDisplay protocol.ProjectDisplayResult
	var oldServer protocol.MCPServer
	var haveMachine, haveDisplay, haveServer bool
	if concrete == "session_rename" || concrete == "session_trust" {
		var q protocol.SessionRenameRequest
		_ = json.Unmarshal(args, &q)
		view, e := d.agentView(ctx, q.SessionID)
		if e != nil {
			return nil, e
		}
		oldName, oldTrust = view.Profile.Name, view.PeerTrust
	}
	switch concrete {
	case "node_rename":
		var q protocol.NodeRenameRequest
		_ = json.Unmarshal(args, &q)
		before, e := d.call(ctx, "query.nodes", nil)
		if e != nil {
			return nil, e
		}
		var nodes protocol.NodesInfo
		if e = json.Unmarshal(before, &nodes); e != nil {
			return nil, e
		}
		for _, node := range nodes.Nodes {
			if node.NodeID == q.NodeID {
				oldMachine, haveMachine = node, true
				break
			}
		}
	case "project_display_set":
		var q protocol.ProjectDisplayRequest
		_ = json.Unmarshal(args, &q)
		if before, e := d.call(ctx, "project.display.get", q); e == nil {
			haveDisplay = json.Unmarshal(before, &oldDisplay) == nil
		}
	case "mcp_enable":
		var q protocol.MCPSetEnabled
		_ = json.Unmarshal(args, &q)
		if before, e := d.call(ctx, "mcp.list", nil); e == nil {
			var list struct {
				Servers []protocol.MCPServer `json:"servers"`
			}
			if json.Unmarshal(before, &list) == nil {
				for _, srv := range list.Servers {
					if srv.Name == q.Name {
						oldServer = srv
						haveServer = true
						break
					}
				}
			}
		}
	}
	ctx = context.WithValue(ctx, proposalActorKey{}, "cto")
	result, err := d.executeApprovedCTOTool(ctx, concrete, args)
	if err != nil {
		return nil, err
	}
	text := "CTO applied " + strings.ReplaceAll(concrete, "_", " ")
	for _, key := range []string{"team", "name", "node", "project", "path", "session", "session_id", "old_session", "server", "node_id"} {
		var value string
		if json.Unmarshal(fields[key], &value) == nil && strings.TrimSpace(value) != "" {
			text += " " + cleanLine(value)
			break
		}
	}
	if concrete == "session_replace" {
		var q protocol.SessionReplaceRequest
		if json.Unmarshal(args, &q) == nil {
			text = fmt.Sprintf("CTO moved the work of %s to %s", cleanLine(q.OldSession), cleanLine(q.NewSession))
		}
	}
	var options []string
	diff := ""
	switch {
	case name == "graph_change":
		history, e := d.ctoGraphHistory(ctx, last)
		if e != nil {
			return result, e
		}
		if len(history) > 0 {
			h := history[len(history)-1]
			b, _ := json.Marshal(protocol.GraphUndoRequest{ID: h.ID})
			options = []string{"graph_undo", string(b)}
			diff = fmt.Sprintf("%s: %s -> %s", h.Node, h.OldValue, h.NewValue)
		}
	case concrete == "session_rename":
		var q protocol.SessionRenameRequest
		_ = json.Unmarshal(args, &q)
		text += " " + q.SessionID + " to " + q.Name
		diff = oldName + " -> " + q.Name
		q.Name = oldName
		b, _ := json.Marshal(q)
		options = []string{"session_rename", string(b)}
	case concrete == "session_stop" || concrete == "session_resume" || concrete == "session_restart":
		done, _ := result.(*ctoSessionControlResult)
		if done != nil {
			text = fmt.Sprintf("CTO %s %s (%s)", map[string]string{"stop": "stopped", "resume": "resumed", "restart": "restarted"}[done.Action], done.Name, done.SessionID)
			if done.Action == "stop" {
				// The conversation is kept, so resume is the undo. Resume and
				// restart have none: the session is already running again.
				undo, _ := json.Marshal(protocol.SessionResumeRequest{SessionID: done.SessionID})
				options = []string{protocol.OpSessionResume, string(undo)}
			}
			diff = done.Note
		}
	case concrete == "session_trust":
		var q protocol.SessionTrustRequest
		_ = json.Unmarshal(args, &q)
		diff = fmt.Sprintf("trust: %t -> %t", oldTrust, q.On)
		q.On = oldTrust
		undo, _ := json.Marshal(q)
		options = []string{protocol.OpSessionTrust, string(undo)}
	case concrete == "folder_trust":
		var res protocol.FolderTrustResult
		b, _ := json.Marshal(result)
		_ = json.Unmarshal(b, &res)
		where := cleanLine(firstNonEmpty(res.Hostname, res.NodeID))
		if where == "" {
			where = "this machine"
		}
		if res.Added {
			text = fmt.Sprintf("CTO trusted %s for Claude Code on %s", cleanLine(res.Path), where)
			diff = "hasTrustDialogAccepted: false -> true"
			// Only trust agent-net added is removed. A folder the owner had
			// trusted already is left alone and has no undo.
			undo, _ := json.Marshal(protocol.FolderTrustRequest{Path: res.Path, NodeID: res.NodeID, Remove: true})
			options = []string{protocol.OpFolderTrust, string(undo)}
		} else {
			text = fmt.Sprintf("CTO checked Claude Code trust for %s on %s: already trusted, unchanged", cleanLine(res.Path), where)
			diff = "already trusted before: unchanged"
		}
	case concrete == "folder_mcp":
		var res protocol.FolderMCPResult
		b, _ := json.Marshal(result)
		_ = json.Unmarshal(b, &res)
		where := cleanLine(firstNonEmpty(res.Hostname, res.NodeID))
		if where == "" {
			where = "this machine"
		}
		verb := map[string]string{protocol.MCPApprove: "approved", protocol.MCPDisable: "disabled"}[res.Choice]
		text = fmt.Sprintf("CTO %s the .mcp.json servers for Claude Code in %s on %s", verb, cleanLine(res.Path), where)
		diff, options = mcpChoiceAudit(res.MCPChoiceResult, res.NodeID)
	case concrete == "process_stop":
		var res protocol.ProcessStopResult
		b, _ := json.Marshal(result)
		_ = json.Unmarshal(b, &res)
		where := cleanLine(firstNonEmpty(res.Hostname, res.NodeID))
		if where == "" {
			where = "the machine"
		}
		counts := map[string]int{}
		lines := []string{"No undo: a stopped process cannot be brought back."}
		for _, p := range res.Processes {
			counts[p.Outcome]++
			lines = append(lines, processOutcomeLine(p))
		}
		text = fmt.Sprintf("CTO stopped processes on %s (no undo): %d stopped by TERM, %d by KILL, %d refused, %d not found",
			where, counts[protocol.ProcessStoppedTerm], counts[protocol.ProcessStoppedKill], counts[protocol.ProcessRefused], counts[protocol.ProcessNotFound])
		if n := counts[protocol.ProcessFailed]; n > 0 {
			text += fmt.Sprintf(", %d failed", n)
		}
		diff = strings.Join(lines, "\n")
	case concrete == "session_start":
		var res protocol.StartedSession
		b, _ := json.Marshal(result)
		if json.Unmarshal(b, &res) == nil && res.MCPChoice != nil {
			// The choice made for the start is audited like a separate one.
			diff, options = mcpChoiceAudit(*res.MCPChoice, res.NodeID)
			text += " (" + res.MCPChoice.Choice + " .mcp.json servers)"
		}
	case concrete == "node_rename":
		if haveMachine {
			var q protocol.NodeRenameRequest
			_ = json.Unmarshal(args, &q)
			var renamed protocol.NodeRenameResult
			b, _ := json.Marshal(result)
			_ = json.Unmarshal(b, &renamed)
			diff = oldMachine.Hostname + " -> " + renamed.Hostname
			q.Name = oldMachine.DisplayName
			undo, _ := json.Marshal(q)
			options = []string{protocol.OpNodeRename, string(undo)}
		}
	case concrete == "project_display_set":
		if haveDisplay {
			var q protocol.ProjectDisplayRequest
			_ = json.Unmarshal(args, &q)
			diff = oldDisplay.Name + " -> " + q.Name
			undo, _ := json.Marshal(protocol.ProjectDisplayRequest{Project: q.Project, Name: oldDisplay.Name})
			options = []string{protocol.OpProjectDisplaySet, string(undo)}
		}

	case concrete == "mcp_enable":
		if haveServer {
			var q protocol.MCPSetEnabled
			_ = json.Unmarshal(args, &q)
			diff = fmt.Sprintf("%s enabled: %t -> %t", q.Name, oldServer.Enabled, q.Enabled)
			q.Enabled = oldServer.Enabled
			undo, _ := json.Marshal(q)
			options = []string{"mcp_enable", string(undo)}
		}
	case concrete == "settings_values_write" || concrete == "settings_values_undo":
		b, _ := json.Marshal(result)
		var written protocol.SettingsResult
		if json.Unmarshal(b, &written) == nil && written.Revision > settingsBefore {
			undo, _ := json.Marshal(protocol.SettingsUndo{Revision: written.Revision})
			options = []string{"settings_undo", string(undo)}
			if historyRaw, e := d.call(ctx, "settings.history", map[string]any{"revision": written.Revision, "owner": true}); e == nil {
				var history []protocol.SettingsHistory
				if json.Unmarshal(historyRaw, &history) == nil {
					var lines []string
					for _, h := range history {
						lines = append(lines, fmt.Sprintf("%s (%s %s): %s -> %s", h.Key, h.Level, h.Scope, h.Old, h.New))
					}
					diff = strings.Join(lines, "\n")
				}
			}
		}
	case concrete == "protected_providers_set":
		var next protocol.ProtectedProviders
		_ = json.Unmarshal(args, &next)
		diff = fmt.Sprintf("protected providers: %v -> %v", previousProtected.Providers, next.Providers)
		if haveProviders && previousProtected.Configured {
			undo, _ := json.Marshal(previousProtected)
			options = []string{protocol.OpProtectedProvidersSet, string(undo)}
		}
	case concrete == "worker_providers_set":
		var next protocol.WorkerProviders
		_ = json.Unmarshal(args, &next)
		diff = fmt.Sprintf("worker providers: %v -> %v", previousProviders.Providers, next.Providers)
		if haveProviders {
			undo, _ := json.Marshal(previousProviders)
			options = []string{protocol.OpWorkerProvidersSet, string(undo)}
		}
	case concrete == "score_set":
		if haveScores {
			var q protocol.ScoreSetRequest
			_ = json.Unmarshal(args, &q)
			next := "default"
			if q.Score != nil {
				next = fmt.Sprint(*q.Score)
			}
			q.Score = nil
			previous := "default"
			for _, score := range previousScores {
				if strings.EqualFold(score.Match, q.Model) {
					value := score.Score
					q.Score = &value
					previous = fmt.Sprint(value)
					break
				}
			}
			text += " " + cleanLine(q.Model)
			diff = fmt.Sprintf("%s: %s -> %s", q.Model, previous, next)
			undo, _ := json.Marshal(q)
			options = []string{protocol.OpScoresSet, string(undo)}
		}
	case concrete == "integrations_set":
		if haveIntegrations {
			var next protocol.Integrations
			_ = json.Unmarshal(args, &next)
			before, _ := json.Marshal(previousIntegrations.Integrations)
			after, _ := json.Marshal(next)
			diff = fmt.Sprintf("%s -> %s", before, after)
			options = []string{protocol.OpIntegrationsSet, string(before)}
		}
	case concrete == "playbook_set":
		if havePlaybook {
			var q protocol.PlaybookSetRequest
			_ = json.Unmarshal(args, &q)
			next, before := q.Note, ""
			if old := previousPlaybook.Note; old == nil {
				q.Note, q.Pin = "", nil
			} else {
				before = old.Note
				q.Note = old.Note
				pinned := old.Pinned
				q.Pin = &pinned
			}
			text += " " + cleanLine(q.Model)
			diff = before + " -> " + next
			if previousPlaybook.Note == nil || previousPlaybook.Note.Source == protocol.PlaybookSourceOwner {
				undo, _ := json.Marshal(q)
				options = []string{protocol.OpPlaybookSet, string(undo)}
			}
		}
	case concrete == "tools_new_projects":
		if haveToolProjects {
			var q protocol.ToolNewProjectsRequest
			_ = json.Unmarshal(args, &q)
			diff = fmt.Sprintf("new projects: %t -> %t", previousToolProjects.NewProjects, q.On)
			q.On = previousToolProjects.NewProjects
			undo, _ := json.Marshal(q)
			options = []string{"tools_new_projects", string(undo)}
		}
	case concrete == "worker_profile_add" || concrete == "worker_profile_remove":
		var q protocol.WorkerProfileRequest
		_ = json.Unmarshal(args, &q)
		if concrete == "worker_profile_add" {
			b, _ := json.Marshal(result)
			var added protocol.WorkerProfile
			if json.Unmarshal(b, &added) == nil && added.Name != "" {
				q.Name = added.Name
			}
		}
		text += " " + q.Name
		var old *protocol.WorkerProfile
		for i := range oldProfiles {
			if strings.EqualFold(oldProfiles[i].Name, q.Name) {
				old = &oldProfiles[i]
				break
			}
		}
		if haveProfiles && old != nil {
			undo, _ := json.Marshal(protocol.WorkerProfileRequest{Name: old.Name, Harness: old.Harness, Model: old.Model, Effort: old.Effort})
			options = []string{protocol.OpWorkerProfileAdd, string(undo)}
			diff = fmt.Sprintf("%s: %s %s %s -> %s %s %s", old.Name, old.Harness, old.Model, old.Effort, q.Harness, q.Model, q.Effort)
		} else if haveProfiles && concrete == "worker_profile_add" && q.Name != "" {
			undo, _ := json.Marshal(protocol.WorkerProfileRequest{Name: q.Name})
			options = []string{protocol.OpWorkerProfileRemove, string(undo)}
		}

	case concrete == "tools_apply" || concrete == "tools_undo":
		b, _ := json.Marshal(result)
		var applied protocol.ToolApplyResult
		if json.Unmarshal(b, &applied) == nil && applied.Item != "" {
			text += " " + cleanLine(applied.Item)
			var target string
			if concrete == "tools_apply" {
				var q protocol.ToolPlanID
				_ = json.Unmarshal(args, &q)
				target = applied.Item
				// Group plans return the last member's revision. Their undo
				// takes the group plan ID rather than a member ID.
				if applied.Source != "plan:"+q.ID {
					target = q.ID
				}
			} else {
				var q protocol.ToolUndoRequest
				_ = json.Unmarshal(args, &q)
				if q.Item == applied.Item {
					target = q.Item
				}
			}
			if target != "" {
				undo, _ := json.Marshal(protocol.ToolUndoRequest{Item: target})
				options = []string{"tools_undo", string(undo)}
			}
			var lines []string
			for _, t := range applied.Targets {
				lines = append(lines, fmt.Sprintf("%s: %s (%s)", t.Machine, t.Action, t.State))
			}
			diff = strings.Join(lines, "\n")
		}
	case concrete == "mcp_native_remove":
		b, _ := json.Marshal(result)
		var removed protocol.MCPNativeRemoveResult
		if json.Unmarshal(b, &removed) == nil && removed.State == protocol.MCPNativeRemoved && removed.PlanID != "" {
			undo, _ := json.Marshal(protocol.MCPNativeRemoveRequest{UndoPlanID: removed.PlanID})
			options = []string{"mcp_native_remove", string(undo)}
			text += " " + cleanLine(removed.Server)
			diff = "removed native configuration for " + removed.Server
		}
	case concrete == "mcp_add":
		var q struct {
			Server struct {
				Name string `json:"name"`
			} `json:"server"`
			On string `json:"on"`
		}
		if json.Unmarshal(args, &q) == nil && q.Server.Name != "" {
			undo, _ := json.Marshal(map[string]string{"name": q.Server.Name, "on": q.On})
			options = []string{"mcp_remove", string(undo)}
			text += " " + q.Server.Name
		}
	case concrete == "sessions_archive":
		b, _ := json.Marshal(result)
		var archived protocol.SessionsArchiveResult
		_ = json.Unmarshal(b, &archived)
		ids := []string{}
		for _, s := range archived.Sessions {
			ids = append(ids, s.SessionID)
		}
		if len(ids) > 0 {
			undo, _ := json.Marshal(protocol.SessionsArchiveRequest{IDs: ids, Restore: true})
			options = []string{"sessions_archive", string(undo)}
		}
		text = fmt.Sprintf("CTO archived %d sessions", len(ids))
	}
	if reason = cleanLine(reason); reason != "" {
		text += ": " + reason
	}
	_, noticeErr := d.call(ctx, "inbox.create", map[string]any{"session_id": d.ctoSessionID(), "kind": "check", "text": text, "recommendation": diff, "options": options})
	if noticeErr != nil {
		return result, fmt.Errorf("%s; could not add Inbox notice: %w", text, noticeErr)
	}
	return result, nil
}

func (d *Daemon) ctoGraphHistory(ctx context.Context, after int64) ([]protocol.CTOGraphChange, error) {
	raw, err := d.call(ctx, "cto.graph_history", map[string]any{"after": after})
	if err != nil {
		return nil, err
	}
	var history []protocol.CTOGraphChange
	err = json.Unmarshal(raw, &history)
	return history, err
}

// mcpChoiceAudit describes a recorded MCP server choice for the Inbox: the
// servers with what they run, the file written, and, when the choice changed
// anything, an undo that takes back exactly the names it added.
func mcpChoiceAudit(res protocol.MCPChoiceResult, node string) (string, []string) {
	var lines []string
	for _, s := range res.Servers {
		line := "server " + cleanLine(s.Name)
		if s.Command != "" {
			line += ": " + cleanLine(s.Command)
		}
		lines = append(lines, line+" (from "+cleanLine(s.File)+")")
	}
	list := map[string]string{protocol.MCPApprove: claudeMCPEnabledKey, protocol.MCPDisable: claudeMCPDisabledKey}[res.Choice]
	if len(res.Added) == 0 && len(res.Restored) == 0 {
		lines = append(lines, "already recorded before: unchanged")
		return strings.Join(lines, "\n"), nil
	}
	lines = append(lines, fmt.Sprintf("%s: added %s in %s", list, strings.Join(res.Added, ", "), cleanLine(res.File)))
	if len(res.Restored) > 0 {
		lines = append(lines, fmt.Sprintf("%s: removed %s (a refusal would win)", claudeMCPDisabledKey, strings.Join(res.Restored, ", ")))
	}
	undo, _ := json.Marshal(protocol.FolderMCPRequest{Path: res.Path, NodeID: node,
		Undo: &protocol.MCPUndo{Choice: res.Choice, Added: res.Added, Restored: res.Restored}})
	return strings.Join(lines, "\n"), []string{protocol.OpFolderMCP, string(undo)}
}
