package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strconv"

	"strings"
	"time"

	"github.com/niparis/agent-net/internal/gateway"
	"github.com/niparis/agent-net/internal/protocol"
	"github.com/niparis/agent-net/internal/toolcatalog"
)

// ctoTool is the only entry to the private CTO channel. Check the process
// tree on every call, including after a server restart.
func (lc *localConn) ctoTool(ctx context.Context, raw json.RawMessage) (any, error) {
	lc.mu.Lock()
	pid := lc.peerPID
	client := lc.client
	lc.mu.Unlock()
	if client != "cto-mcp" || !lc.d.isCTOPeer(pid) {
		return nil, errors.New("CTO tool requires the CTO server's process tree")
	}
	var q struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	}
	if json.Unmarshal(raw, &q) != nil {
		return nil, errors.New("bad CTO tool request")
	}
	var id string
	result, err := lc.d.runCTOTool(context.WithValue(ctx, dialogProposalKey{}, &id), q.Name, q.Arguments)
	if err != nil {
		return nil, err
	}
	if id == "" {
		return map[string]any{"kind": "result", "value": result}, nil
	}
	items, err := lc.d.proposalRunner().list()
	if err != nil {
		return nil, err
	}
	for _, item := range items {
		if item.ID == id {
			if item.Status != "open" {
				return nil, fmt.Errorf("proposal %s is %s, not open", item.ID, item.Status)
			}
			text := ProposalToolResult(id)
			message := fmt.Sprintf("Approve this proposal?\nTool: %s\nAction: %s\nArguments:\n%s\nReason: %s", item.Call.Tool, item.Call.Action, dialogArguments(item.Call.Args), item.Reason)
			if item.Call.Tool == "project_change" && item.Call.Action == "create" {
				text = "Project proposal " + id + " awaits the owner's approval in the dialog or Inbox."
				var start protocol.SessionStartRequest
				if json.Unmarshal(item.Call.Args, &start) == nil {
					model := firstNonEmpty(start.Model, start.Harness)
					if start.Harness == "claude" && start.Model == "claude-opus-5-5" {
						model = "Claude Opus"
					}
					name := filepath.Base(filepath.Clean(start.Path))
					if start.Remote != "" {
						message = fmt.Sprintf("%s -> %s, %s, clone of %s, %s, background. OK?", name, start.NodeID, start.Path, start.Remote, model)
					} else {
						message = fmt.Sprintf("%s -> %s, %s, %s, background. OK?", name, start.NodeID, start.Path, model)
					}
					if reason := ctoCut(cleanLine(item.Reason), 120); reason != "" {
						message += " " + reason
					}
				}
			}
			if item.Call.Tool == "session_change" && (item.Call.Action == "merger_start" || item.Call.Action == "merger_stop") {
				message = item.Reason
			}
			return map[string]any{"kind": "proposal", "proposal_id": id, "text": text, "message": message}, nil
		}
	}
	return nil, errors.New("proposal not found")
}

func dialogArguments(raw json.RawMessage) string {
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil {
		return "  " + string(raw)
	}
	keys := make([]string, 0, len(fields))
	for key := range fields {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var lines []string
	for _, key := range keys {
		value := string(fields[key])
		var str string
		if json.Unmarshal(fields[key], &str) == nil {
			if strings.ContainsAny(str, "\r\n") {
				value = strconv.Quote(str)
			} else {
				value = str
			}
		}
		lines = append(lines, "  "+key+": "+value)
	}
	return strings.Join(lines, "\n")
}

func (lc *localConn) ctoDialogAnswer(ctx context.Context, raw json.RawMessage) (any, error) {
	lc.mu.Lock()
	client, pid := lc.client, lc.peerPID
	lc.mu.Unlock()
	if client != "cto-mcp" || !lc.d.isCTOPeer(pid) {
		return nil, errors.New("CTO dialog requires the CTO server's process tree")
	}
	var q struct {
		ID      string `json:"id"`
		Approve *bool  `json:"approve"`
	}
	if json.Unmarshal(raw, &q) != nil || q.ID == "" || q.Approve == nil {
		return nil, errors.New("bad CTO dialog answer")
	}
	item, decided, err := lc.d.proposalRunner().decideForDialog(ctx, q.ID, *q.Approve)
	defer lc.d.syncProposalAsks(ctx)
	if err != nil {
		return nil, err
	}
	if !decided {
		return nil, nil
	}
	if *q.Approve && item.Status == "applied" && item.Call.Tool == "project_change" && item.Call.Action == "create" {
		var start protocol.StartedSession
		if json.Unmarshal(item.Result, &start) == nil && start.ID != "" {
			return lc.d.ctoProjectStartOutcome(ctx, start), nil
		}
	}
	return item.Note, nil
}

// decideForDialog decides a proposal and marks its note delivered under the
// note delivery lock. The dialog returns the outcome to the CTO as its tool
// result, so the notes loop must not send the same outcome as a second turn.
func (p proposalRunner) decideForDialog(ctx context.Context, id string, approve bool) (proposal, bool, error) {
	p.state.proposalDeliveryMu.Lock()
	defer p.state.proposalDeliveryMu.Unlock()
	decideErr := p.decide(ctx, id, approve)
	if decideErr != nil && decideErr.Error() == "proposal is not open" {
		return proposal{}, false, decideErr
	}
	items, err := p.list()
	if err != nil {
		return proposal{}, false, err
	}
	for _, item := range items {
		if item.ID == id && (decideErr == nil || item.Status == "failed") && item.Status != "open" {
			if err := p.markNoteDelivered(ctx, id); err != nil {
				return proposal{}, false, err
			}
			return item, true, nil
		}
	}
	return proposal{}, false, decideErr
}

// ctoProjectStartOutcome waits for the registered session before reporting it ready.
func (d *Daemon) ctoProjectStartOutcome(ctx context.Context, start protocol.StartedSession) string {
	ctx, cancel := context.WithTimeout(ctx, d.workerSettings("").StartTimeout)
	defer cancel()
	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()
	for start.Status == "starting" {
		select {
		case <-ctx.Done():
			return "Project creation was approved; the session is still starting."
		case <-ticker.C:
			var err error
			start, err = d.sessionStartGet(ctx, start.ID)
			if err != nil {
				return "Project creation was approved; could not check the session: " + cleanLine(err.Error())
			}
		}
	}
	if start.Status != "started" || start.SessionID == "" {
		return "Project creation was approved; the session did not start: " + cleanLine(firstNonEmpty(start.Error, start.Status))
	}
	name := cleanLine(start.Project)
	return fmt.Sprintf("%s is ready: session %s is waiting in tab 1 (Sessions). Press Enter on it to dive in.", name, name)
}

func (d *Daemon) runCTOToolRaw(ctx context.Context, name string, raw json.RawMessage) (any, error) {
	// The CTO server lists only the new names. An old name, including the
	// internal stored "message" call, is refused on the channel.
	if err := toolcatalog.RenamedError(name); err != nil {
		return nil, err
	}
	if name == "session_change" {
		if _, err := ctoClass(name, raw); err != nil {
			return nil, err
		}
		var p struct {
			Action string `json:"action"`
			protocol.SessionStartRequest
		}
		if json.Unmarshal(raw, &p) == nil && p.Action == "session_start" {
			project := strings.TrimSpace(p.Project)
			if p.Path != "" {
				project = p.Path
			}
			if project != "" {
				if rawProjects, err := d.call(ctx, "query.projects", protocol.QueryProjects{}); err == nil {
					var list protocol.ProjectsResult
					if json.Unmarshal(rawProjects, &list) == nil {
						for _, item := range list.Projects {
							if item.Name == project || item.Path == project {
								project = item.Name
								break
							}
						}
					}
				}
				admission, err := d.repoStartReservation(ctx, protocol.RepoStartReservation{Owner: d.ownerSessionID(), Ref: protocol.NewID("preview"), Project: project, Check: true})
				if err != nil {
					return nil, err
				}
				if admission.Status == "failed" {
					return map[string]any{"state": "failed", "result": map[string]any{"summary": admission.Error, "data": admission}}, nil
				}
			}
		}
	}
	return routeCTOTool(ctx, name, raw, d.Propose, d.executeImmediateCTOTool)
}

// executeCTOTool is used only for immediate reads/talk or by the approved runner.
func (d *Daemon) executeCTOTool(ctx context.Context, name string, raw json.RawMessage) (any, error) {
	if name == "cto_placement_memory" {
		return d.ctoPlacementMemory(ctx, raw)
	}
	if name == "cto_standing_orders" {
		return d.ctoStandingOrders(ctx, raw)
	}
	if ctoToolClasses[name] == ctoRead {
		return d.readCTOTool(ctx, name, raw)
	}

	var args map[string]any
	if len(raw) > 0 && string(raw) != "null" && json.Unmarshal(raw, &args) != nil {
		return nil, errors.New("bad CTO tool arguments")
	}
	if args == nil {
		args = map[string]any{}
	}
	// These operations reuse the same owner implementations after ctoTool has
	// checked the calling process tree. This connection is never exposed.
	lc := &localConn{d: d, client: "cli", cto: proposalActor(ctx) == ""}
	if ctoOwnerMethods[name] {
		res, _, err := lc.dispatchOwner(ctx, name, raw)
		return res, err
	}
	if ctoLocalMethods[name] {
		return lc.dispatch(ctx, name, raw)
	}
	switch name {
	case "settings_values_write":
		var q protocol.SettingsWrite
		if err := json.Unmarshal(raw, &q); err != nil {
			return nil, err
		}
		q.By = proposalActor(ctx)
		if q.By == "" {
			return nil, errors.New("settings write requires a CTO actor")
		}
		return d.call(ctx, "settings.write", q)
	case "settings_values_undo":
		var q protocol.SettingsUndo
		if err := json.Unmarshal(raw, &q); err != nil {
			return nil, err
		}
		q.By = proposalActor(ctx)
		if q.By == "" {
			return nil, errors.New("settings undo requires a CTO actor")
		}
		return d.call(ctx, "settings.undo", q)
	case "proposal_ask":
		var p struct {
			ID string `json:"id"`
		}
		if json.Unmarshal(raw, &p) != nil {
			return nil, errors.New("bad proposal_ask arguments")
		}
		p.ID = strings.TrimSpace(p.ID)
		if p.ID == "" {
			return nil, errors.New("proposal_ask needs id")
		}
		items, err := d.proposalRunner().list()
		if err != nil {
			return nil, err
		}
		for _, item := range items {
			if item.ID == p.ID {
				if item.Status != "open" {
					return nil, fmt.Errorf("proposal %s is %s, not open", item.ID, item.Status)
				}
				if dest, ok := ctx.Value(dialogProposalKey{}).(*string); ok {
					*dest = item.ID
				}
				return ProposalToolResult(item.ID), nil
			}
		}
		return nil, fmt.Errorf("unknown proposal %s", p.ID)
	case "agent_message_send":
		var p map[string]any
		if len(raw) > 0 && json.Unmarshal(raw, &p) != nil {
			return nil, errors.New("bad agent_message_send arguments")
		}
		if p == nil {
			p = map[string]any{}
		}
		p["receipt"] = true
		if p["idempotency_key"] == nil {
			p["idempotency_key"] = protocol.NewID("k")
		}
		b, _ := json.Marshal(p)
		return d.localSend(ctx, d.ctoReadConn(), b)
	case "agent_message_reply":
		var p map[string]any
		if len(raw) > 0 && json.Unmarshal(raw, &p) != nil {
			return nil, errors.New("bad agent_message_reply arguments")
		}
		if p == nil {
			p = map[string]any{}
		}
		if message, ok := p["message"]; ok {
			delete(p, "message")
			p["message_id"] = message
		}
		p["receipt"] = true
		if p["idempotency_key"] == nil {
			p["idempotency_key"] = protocol.NewID("k")
		}
		b, _ := json.Marshal(p)
		return d.localReply(ctx, d.ctoReadConn(), b)
	case "agent_owner_ask":
		var p struct {
			Kind           string   `json:"kind"`
			Text           string   `json:"text"`
			Recommendation string   `json:"recommendation"`
			Options        []string `json:"options"`
			Urgency        string   `json:"urgency"`
			Replaces       string   `json:"replaces"`
		}
		if json.Unmarshal(raw, &p) != nil || (strings.TrimSpace(p.Text) == "" && p.Replaces == "") {
			return nil, errors.New("agent_owner_ask needs kind, text and recommendation")
		}
		sid := d.ctoSessionID()
		if sid == "" {
			return nil, errors.New("CTO network session is not registered; start the CTO from the owner interface")
		}
		if p.Kind == "" && p.Replaces != "" {
			p.Kind = "question"
		}
		kind := map[string]string{"question": "question", "approval": "approval", "idea": "feature_idea"}[p.Kind]
		if kind == "" {
			return nil, errors.New("kind must be question, approval or idea")
		}
		value, err := d.call(ctx, "inbox.create", map[string]any{"session_id": sid, "kind": kind, "text": p.Text, "recommendation": p.Recommendation, "options": p.Options, "urgency": p.Urgency, "replaces": p.Replaces})
		if err != nil {
			return nil, err
		}
		var item struct {
			ID     string `json:"id"`
			State  string `json:"state"`
			Status string `json:"status"`
		}
		_ = json.Unmarshal(value, &item)
		state := item.State
		if state == "" {
			state = item.Status
		}
		return map[string]any{"item_id": item.ID, "kind": p.Kind, "state": state}, nil
	case "agent_learn":
		var p protocol.LearnRequest
		if json.Unmarshal(raw, &p) != nil {
			return nil, errors.New("bad agent_learn params")
		}
		sid := d.ownerSessionID()
		if sid == "" {
			return nil, errNotConnected
		}
		value, err := d.call(ctx, "learn.add", protocol.LearnFrame{LearnRequest: p, Reporter: sid})
		if err != nil {
			return nil, err
		}
		var out protocol.LearnResult
		if err := json.Unmarshal(value, &out); err != nil {
			return nil, err
		}
		return map[string]any{"recorded": out.Accepted, "id": strconv.FormatInt(out.ID, 10), "left_today": out.Remaining}, nil
	case "cto_ui_show":
		// The catalog tool shows a tab, or a notice when notice is set.
		var q struct {
			Tab    string `json:"tab"`
			Select string `json:"select"`
			Filter string `json:"filter"`
			Why    string `json:"why"`
			Notice string `json:"notice"`
		}
		if json.Unmarshal(raw, &q) != nil {
			return nil, errors.New("bad cto_ui_show request")
		}
		if q.Notice != "" {
			b, _ := json.Marshal(protocol.UIRequest{Text: q.Notice})
			return d.executeCTOTool(ctx, "ui_notice", b)
		}
		b, _ := json.Marshal(protocol.UIRequest{Tab: q.Tab, Select: q.Select, Filter: q.Filter, Why: q.Why})
		return d.executeCTOTool(ctx, "ui_show", b)
	case "ui_show", "ui_notice":
		var q protocol.UIRequest
		if json.Unmarshal(raw, &q) != nil {
			return nil, errors.New("bad UI request")
		}
		q.Tab, q.Select, q.Filter, q.Why, q.Text = cleanLine(q.Tab), cleanLine(q.Select), cleanLine(q.Filter), cleanLine(q.Why), cleanLine(q.Text)
		if name == "ui_show" {
			if q.Tab != "sessions" && q.Tab != "messages" && q.Tab != "quotas" && q.Tab != "graph" && q.Tab != "map" && q.Tab != "tools" {
				return nil, errors.New("ui_show tab must be sessions, messages, quotas, graph or tools")
			}
			q.Text = ""
			if q.Why == "" {
				q.Why = "Showing " + q.Tab
			}
			// CTO input cannot supply daemon-only notices or terminal commands.
			q = protocol.UIRequest{Tab: q.Tab, Select: q.Select, Filter: q.Filter, Why: q.Why}
		} else {
			if q.Text == "" {
				return nil, errors.New("ui_notice needs text")
			}
			q = protocol.UIRequest{Text: q.Text}
		}
		if d.broadcastCTOUI(q) == 0 {
			return "no TUI open", nil
		}
		return "shown", nil
	case "worker_stop":
		var p protocol.WorkerStopRequest
		if json.Unmarshal(raw, &p) != nil || p.Session == "" {
			return nil, errors.New("worker_stop needs session")
		}
		sid := d.ownerSessionID()
		if sid == "" {
			return nil, errNotConnected
		}
		_, err := d.call(withCallWait(ctx, 30*time.Second), "worker.stop", protocol.WorkerStopFrame{Session: p.Session, Requester: sid})
		if err != nil {
			return nil, tooOldGateway(err)
		}
		return protocol.WorkerStopResult{Session: p.Session, Stopped: true}, nil
	case "team_create", "team_rename", "team_purpose", "team_charter", "team_archive", "team_member_add", "team_member_remove":
		tw, err := d.resolveTeamWrite(ctx, name, raw)
		if err != nil {
			return nil, err
		}
		b, err := json.Marshal(tw)
		if err != nil {
			return nil, err
		}
		res, _, err := lc.dispatchOwner(ctx, protocol.OpTeamWrite, b)
		return res, err
	case "lead_set", "charter_set", "node_add", "node_remove", "edge_set", "edge_remove", "graph_undo", "node_settings_set", "repo_create", "repo_archive", "agent_create", "agent_tombstone":
		delete(args, "by")
		if name == "lead_set" || name == "repo_create" || name == "repo_archive" || name == "agent_create" || name == "agent_tombstone" {
			args["owner"] = d.ownerSessionID()
		}
		if name == "graph_undo" {
			args["history_id"] = args["id"]
			delete(args, "id")
		}
		args["by"] = "cto"
		if actor := proposalActor(ctx); actor != "" {
			args["by"] = actor
		}
		return d.ctoAttributedCall(ctx, strings.ReplaceAll(name, "_", "."), args)
	case "worker_profile_add", "worker_profile_remove":
		var p protocol.WorkerProfileRequest
		if len(raw) > 0 && string(raw) != "null" && json.Unmarshal(raw, &p) != nil {
			return nil, errors.New("bad worker profile request")
		}
		sid := d.ownerSessionID()
		if sid == "" {
			return nil, errNotConnected
		}
		action := map[string]string{"worker_profile_add": "worker.profile.add", "worker_profile_remove": "worker.profile.remove"}[name]
		return d.call(ctx, action, protocol.WorkerProfileFrame{WorkerProfileRequest: p, Requester: sid})
	case "protected_providers_set":
		var p protocol.ProtectedProviders
		if json.Unmarshal(raw, &p) != nil {
			return nil, errors.New("bad protected providers")
		}
		return d.protectedProviders(ctx, &p)
	case "worker_floors_set":
		var p protocol.WorkerFloors
		if json.Unmarshal(raw, &p) != nil {
			return nil, errors.New("bad worker floors")
		}
		return d.workerFloors(ctx, &p)
	case "worker_providers_set":
		var p protocol.WorkerProviders
		if json.Unmarshal(raw, &p) != nil {
			return nil, errors.New("bad worker providers")
		}
		return d.workerProviders(ctx, &p)
	case "score_set":
		var p protocol.ScoreSetRequest
		if json.Unmarshal(raw, &p) != nil {
			return nil, errors.New("bad score request")
		}
		return d.scores(ctx, &p)
	case "worker_start":
		var p protocol.WorkerStartRequest
		if json.Unmarshal(raw, &p) != nil {
			return nil, errors.New("bad worker request")
		}
		sid := d.ownerSessionID()
		if sid == "" {
			return nil, errNotConnected
		}
		return d.workerStartCall(ctx, sid, p, false, "", true)
	case "invite":
		if d.cfg.GatewayData == "" {
			return nil, errors.New("invite runs on the gateway machine")
		}
		lines, err := gateway.Invite(gateway.InviteOptions{Port: "7420", Data: d.cfg.GatewayData})
		if err != nil {
			return nil, fmt.Errorf("invite runs on the gateway machine: %w", err)
		}
		var b strings.Builder
		for _, l := range lines {
			if l[0] != "" {
				fmt.Fprintf(&b, "%s: ", l[0])
			}
			b.WriteString(l[1])
			b.WriteByte('\n')
		}
		b.WriteString("Valid for 10 minutes, works for one machine. Paste it in a shell on the machine to add.")
		return b.String(), nil
	}
	return nil, errors.New("unknown CTO tool")
}

// Every tool call is recorded, including reads, no-ops and failures. Graph
// history supplies the separate before-and-after diff for a successful change.
func (d *Daemon) runCTOTool(ctx context.Context, name string, raw json.RawMessage) (any, error) {
	// The audit keeps the concrete name, as it did before the action tools.
	auditName := name
	if concrete, _, _, err := ctoConcreteCall(name, raw); err == nil {
		auditName = concrete
	}
	switch name {
	case "agent_message_send", "agent_message_reply":
		// A plain send or reply keeps its short audit name; an as-owner one is
		// a proposal and keeps the tool name.
		var m struct {
			AsOwner bool `json:"as_owner"`
		}
		if json.Unmarshal(raw, &m) == nil && !m.AsOwner {
			auditName = map[string]string{"agent_message_send": "send", "agent_message_reply": "reply"}[name]
		}
	case "cto_ui_show":
		var u struct {
			Notice string `json:"notice"`
		}
		auditName = "ui_show"
		if json.Unmarshal(raw, &u) == nil && u.Notice != "" {
			auditName = "ui_notice"
		}
	}
	result, err := d.runCTOToolRaw(ctx, name, raw)
	var a struct {
		To      string `json:"to"`
		Body    string `json:"body"`
		Project string `json:"project"`
		Task    string `json:"task"`
		Node    string `json:"node"`
		Attr    string `json:"attr"`
		From    string `json:"from"`
		Kind    string `json:"kind"`
	}
	_ = json.Unmarshal(raw, &a)
	text := auditName
	if err != nil {
		text = fmt.Sprintf("failed %s: %v", auditName, err)
	} else {
		switch auditName {
		case "graph_get":
			text = "read graph"
		case "charter_history":
			text = "read charter history: " + a.Node
		case "edge_history":
			text = "read edge history: " + a.Node
		case "agent_sessions_find":
			text = "read sessions"
		case "agent_workers_find":
			text = "read workers"
		case "needs_you":
			text = "read needs you"
		case "agent_messages_find":
			text = "read feed"
		case "charter_set":
			text = fmt.Sprintf("charter %s: %s updated · u to undo", a.Node, a.Attr)
		case "edge_set":
			text = fmt.Sprintf("edge %s → %s set (%s) · u to undo", a.From, a.To, a.Kind)
		case "send":
			text = fmt.Sprintf("sent to %s", a.To)
		case "worker_start":
			verb := "started"
			if r, ok := result.(*protocol.WorkerStartResult); ok && (r.Status == "queued" || r.Status == "starting") {
				verb = r.Status
			}
			text = fmt.Sprintf("worker %s: %s", verb, a.Project)
		case "invite":
			text = "printed an invite line"
		}
	}
	if proposed, ok := result.(string); ok && strings.HasPrefix(proposed, "proposal p_") {
		text = auditName + ": " + proposed
	}
	logErr := d.recordCTOAction(text)
	if err != nil {
		return result, err
	}
	if logErr != nil {
		return result, logErr
	}
	return result, nil
}

// Send the capability check and the mutation on the same gateway link, so a
// reconnect to an older gateway cannot strip the attribution in between.
func (d *Daemon) ctoAttributedCall(ctx context.Context, typ string, params any) (json.RawMessage, error) {
	d.mu.Lock()
	l := d.lk
	d.mu.Unlock()
	if l == nil {
		return nil, errNotConnected
	}
	b, err := l.call(ctx, "cto.capabilities", nil)
	if err != nil {
		return nil, errors.New("gateway cannot verify CTO attribution; upgrade the gateway")
	}
	var caps struct {
		Attribution    bool `json:"attribution"`
		NetworkSession bool `json:"network_session"`
		GraphAuthority bool `json:"graph_authority"`
	}
	if json.Unmarshal(b, &caps) != nil || !caps.Attribution {
		return nil, errors.New("gateway cannot verify CTO attribution; upgrade the gateway")
	}
	if actor := proposalActor(ctx); (actor == "CTO (minor)" || actor == "CTO (owner window)") && !caps.GraphAuthority {
		return nil, errors.New("gateway cannot enforce CTO graph authority; upgrade the gateway")
	}
	if (typ == "msg.send" || typ == "cto.session.register") && !caps.NetworkSession {
		return nil, errors.New("gateway does not support the CTO network session; upgrade the gateway")
	}
	return l.call(ctx, typ, params)
}

func (d *Daemon) recordCTOAction(text string) error {
	// Empty session IDs are the not-yet-started conversation. The selection and
	// insert are one statement so a concurrent first send cannot lose an action.
	_, err := d.st.db.Exec(`INSERT INTO cto_actions(session_id,text,at,harness) VALUES(COALESCE((SELECT CASE active_harness WHEN 'claude' THEN claude_session_id ELSE session_id END FROM cto WHERE id=1),''),?,?,COALESCE((SELECT active_harness FROM cto WHERE id=1),'opencode'))`, text, time.Now().UnixNano())
	return err
}

// These allowlists are reachable only from ctoTool's process-tree check.
var ctoOwnerMethods = map[string]bool{
	"notification_settings": true, "onboarding_restart": true,
	"mcp_import": true, "mcp_native_remove": true, "tools_toggle": true, "tools_new_projects": true, "tools_plan": true, "tools_apply": true, "tools_undo": true,
	"inbox_answer": true, "outcome_overrule": true,
	"sessions_archive": true, "session_trust": true, "folder_trust": true, "folder_mcp": true, "session_policy": true, "session_rename": true, "node_rename": true, "machine_remove": true, "machine_rotate": true, "project_display_set": true, "integrations_set": true,
	"held_answer":  true,
	"playbook_set": true, "cto_settings": true, "cto_send": true, "cto_new": true,
}
var ctoLocalMethods = map[string]bool{
	"project_add": true, "project_remove": true,
	"mcp_add":    true,
	"mcp_remove": true, "mcp_test": true, "mcp_enable": true, "mcp_call": true,
}

// broadcastCTOUI sends only to connections that explicitly subscribed as an
// owner-gated TUI. Closed or failed connections do not count as open.
func (d *Daemon) broadcastCTOUI(q protocol.UIRequest) int {
	if d.ln == nil {
		return 0
	}
	d.ln.mu.Lock()
	conns := make([]*localConn, 0, len(d.ln.conns))
	for c := range d.ln.conns {
		conns = append(conns, c)
	}
	d.ln.mu.Unlock()
	count := 0
	for _, c := range conns {
		c.mu.Lock()
		subscribed := c.client == "cli" && c.tui
		c.mu.Unlock()
		if subscribed && c.writeJSON(map[string]any{"method": "cto_ui", "params": q}) == nil {
			count++
		}
	}
	return count
}

type ctoHarnessSummary struct {
	Name       string `json:"name"`
	Version    string `json:"version"`
	ModelCount int    `json:"model_count"`
}
type ctoNode struct {
	Name        string              `json:"name"`
	Online      bool                `json:"online"`
	Version     string              `json:"version"`
	Harnesses   []ctoHarnessSummary `json:"harnesses"`
	WorkerCount int                 `json:"worker_count"`
	Quota       string              `json:"quota"`
}
type ctoAgent struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Project string `json:"project"`
	Machine string `json:"machine"`
	Folder  string `json:"folder"`
	Harness string `json:"harness"`
	Model   string `json:"model"`
	Status  string `json:"status"`
	Task    string `json:"task"`
}

func compactCTONodes(in protocol.NodesInfo, agents []protocol.AgentView) any {
	counts := map[string]int{}
	for _, w := range protocol.WorkersOf(agents) {
		counts[w.NodeID]++
	}
	out := make([]ctoNode, 0, len(in.Nodes))
	for _, n := range in.Nodes {
		v := ctoNode{Name: n.Hostname, Online: n.Online, Version: n.Version, Harnesses: []ctoHarnessSummary{}, WorkerCount: counts[n.NodeID]}
		for _, h := range n.Harnesses {
			v.Harnesses = append(v.Harnesses, ctoHarnessSummary{h.Name, h.Version, len(h.Models)})
		}
		if n.Quotas != nil {
			var q []string
			for _, p := range n.Quotas.Providers {
				for _, w := range p.Windows {
					q = append(q, fmt.Sprintf("%s %s %.0f%%", p.Provider, w.ID, w.PercentUsed))
				}
			}
			v.Quota = strings.Join(q, ", ")
		}
		out = append(out, v)
	}
	return map[string]any{"nodes": out}
}
func compactCTOAgents(in []protocol.AgentView) any {
	out := make([]ctoAgent, 0, len(in))
	for _, a := range in {
		project := a.Repo.Project
		if project == "" {
			project = a.Profile.Project
		}
		name := a.Profile.Name
		if name == "" {
			name = a.Title
		}
		task := cleanLine(a.Profile.Task)
		if len(task) > 160 {
			task = task[:160]
		}
		out = append(out, ctoAgent{a.SessionID, name, project, a.Hostname, a.Cwd, a.Harness, a.Model, a.Status, task})
	}
	return map[string]any{"agents": out}
}

func ctoLimit(n int) int {
	if n <= 0 {
		return 20
	}
	if n > 50 {
		return 50
	}
	return n
}
func ctoPage[T any](items []T, limit int, before string, key func(T) string) ([]T, string) {
	sort.Slice(items, func(i, j int) bool { return key(items[i]) > key(items[j]) })
	limit = ctoLimit(limit)
	out := make([]T, 0, limit)
	next := ""
	for _, item := range items {
		k := key(item)
		if before != "" && k >= before {
			continue
		}
		if len(out) == limit {
			next = key(out[len(out)-1])
			break
		}
		out = append(out, item)
	}
	return out, next
}

// compactCTONeedsYou keeps only what the owner needs to identify and answer a wait.
func compactCTONeedsYou(agents []protocol.AgentView, now time.Time) any {
	type item struct {
		Kind       string `json:"kind"`
		SessionID  string `json:"session_id"`
		Name       string `json:"name"`
		Project    string `json:"project"`
		Machine    string `json:"machine"`
		Text       string `json:"text"`
		AgeSeconds int64  `json:"age_seconds"`
	}
	out := make([]item, 0)
	for _, a := range agents {
		if a.Waiting == nil {
			continue
		}
		name := a.Profile.Name
		if name == "" {
			name = a.Title
		}
		project := a.Repo.Project
		if project == "" {
			project = a.Profile.Project
		}
		text := []rune(a.Waiting.Text)
		if len(text) > 200 {
			text = text[:200]
		}
		age := int64(0)
		if a.Waiting.Since > 0 && now.Unix() > a.Waiting.Since {
			age = now.Unix() - a.Waiting.Since
		}
		out = append(out, item{a.Waiting.Kind, a.SessionID, name, project, a.Hostname, string(text), age})
	}
	return map[string]any{"agents": out}
}

// ctoPlacementMemory stores only advisory owner placement answers.
func (d *Daemon) ctoPlacementMemory(ctx context.Context, raw json.RawMessage) (any, error) {
	var q struct {
		Action    string `json:"action"`
		Purpose   string `json:"purpose"`
		Placement string `json:"placement"`
	}
	if json.Unmarshal(raw, &q) != nil {
		return nil, errors.New("bad placement memory")
	}
	q.Purpose, q.Placement = strings.TrimSpace(q.Purpose), strings.TrimSpace(q.Placement)
	switch q.Action {
	case "save":
		if q.Purpose == "" || q.Placement == "" || len(q.Purpose) > 512 || len(q.Placement) > 512 {
			return nil, errors.New("placement memory needs a purpose and placement of at most 512 bytes")
		}
		if _, err := d.st.db.ExecContext(ctx, `INSERT INTO cto_placements(purpose,placement) VALUES(?,?) ON CONFLICT(purpose) DO UPDATE SET placement=excluded.placement`, q.Purpose, q.Placement); err != nil {
			return nil, err
		}
	case "read":
	default:
		return nil, errors.New("placement memory action must be read or save")
	}
	rows, err := d.st.db.QueryContext(ctx, `SELECT purpose,placement FROM cto_placements ORDER BY purpose LIMIT 100`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	type entry struct {
		Purpose   string `json:"purpose"`
		Placement string `json:"placement"`
	}
	items := []entry{}
	for rows.Next() {
		var e entry
		if err := rows.Scan(&e.Purpose, &e.Placement); err != nil {
			return nil, err
		}
		items = append(items, e)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return map[string]any{"items": items}, nil
}
