package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"

	"strings"
	"time"

	"github.com/niparis/agent-net/internal/gateway"
	"github.com/niparis/agent-net/internal/protocol"
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
			return map[string]any{"kind": "proposal", "proposal_id": id, "text": ProposalToolResult(id), "message": fmt.Sprintf("Approve this proposal?\nTool: %s\nAction: %s\nArguments:\n%s\nReason: %s", item.Call.Tool, item.Call.Action, dialogArguments(item.Call.Args), item.Reason)}, nil
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
	err := lc.d.proposalRunner().decide(ctx, q.ID, *q.Approve)
	if err != nil && err.Error() == "proposal is not open" {
		return nil, err
	}
	items, listErr := lc.d.proposalRunner().list()
	if listErr != nil {
		return nil, listErr
	}
	for _, item := range items {
		if item.ID == q.ID && (err == nil || item.Status == "failed") && item.Status != "open" {
			return item.Note, nil
		}
	}
	return nil, err
}

func (d *Daemon) runCTOToolRaw(ctx context.Context, name string, raw json.RawMessage) (any, error) {
	if isCTOGraphTool(name) {
		return d.routeCTOGraph(ctx, name, raw)
	}
	return routeCTOTool(ctx, name, raw, d.Propose, d.executeCTOTool)
}

// executeCTOTool is used only for immediate reads/talk or by the approved runner.
func (d *Daemon) executeCTOTool(ctx context.Context, name string, raw json.RawMessage) (any, error) {
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
		}
		if json.Unmarshal(raw, &p) != nil || strings.TrimSpace(p.Text) == "" {
			return nil, errors.New("agent_owner_ask needs kind, text and recommendation")
		}
		sid := d.ctoSessionID()
		if sid == "" {
			return nil, errors.New("CTO network session is not registered; start the CTO from the owner interface")
		}
		kind := map[string]string{"question": "question", "approval": "approval", "idea": "feature_idea"}[p.Kind]
		if kind == "" {
			return nil, errors.New("kind must be question, approval or idea")
		}
		value, err := d.call(ctx, "inbox.create", map[string]any{"session_id": sid, "kind": kind, "text": p.Text, "recommendation": p.Recommendation, "options": p.Options, "urgency": p.Urgency})
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
	case "message":
		var q struct {
			Action string `json:"action"`
		}
		if err := json.Unmarshal(raw, &q); err != nil {
			return nil, err
		}
		if q.Action != "send" && q.Action != "reply" {
			return nil, errors.New("message needs action send or reply")
		}
		return d.executeCTOTool(ctx, q.Action, raw)
	case "ask_owner":
		var p struct {
			Question       string   `json:"question"`
			Options        []string `json:"options"`
			Recommendation string   `json:"recommendation"`
			Urgency        string   `json:"urgency"`
		}
		if json.Unmarshal(raw, &p) != nil || strings.TrimSpace(p.Question) == "" {
			return nil, errors.New("ask_owner needs a question")
		}
		sid := d.ctoSessionID()
		if sid == "" {
			return nil, errors.New("CTO network session is not registered; start the CTO from the owner interface")
		}
		return d.call(ctx, "inbox.create", map[string]any{"session_id": sid, "kind": "question", "text": p.Question, "recommendation": p.Recommendation})
	case "ui":
		var q struct {
			Action string `json:"action"`
			Tab    string `json:"tab"`
			Select string `json:"select"`
			Filter string `json:"filter"`
			Why    string `json:"why"`
			Text   string `json:"text"`
		}
		if json.Unmarshal(raw, &q) != nil {
			return nil, errors.New("bad UI request")
		}
		req := protocol.UIRequest{Tab: cleanLine(q.Tab), Select: cleanLine(q.Select), Filter: cleanLine(q.Filter), Why: cleanLine(q.Why), Text: cleanLine(q.Text)}
		switch q.Action {
		case "show":
			if req.Tab != "sessions" && req.Tab != "messages" && req.Tab != "quotas" && req.Tab != "graph" && req.Tab != "map" && req.Tab != "tools" {
				return nil, errors.New("ui show tab must be sessions, messages, quotas, graph or tools")
			}
			req.Text = ""
			if req.Why == "" {
				req.Why = "Showing " + req.Tab
			}
		case "notice":
			if req.Text == "" {
				return nil, errors.New("ui notice needs text")
			}
			req = protocol.UIRequest{Text: req.Text}
		default:
			return nil, errors.New("ui needs action show or notice")
		}
		if d.broadcastCTOUI(req) == 0 {
			return "no TUI open", nil
		}
		return "shown", nil
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
	case "send", "reply":
		p, err := d.ctoMessage(ctx, name, raw)
		if err != nil {
			return nil, err
		}
		return d.send(ctx, p)
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
	case "learn":
		var p protocol.LearnRequest
		if json.Unmarshal(raw, &p) != nil {
			return nil, errors.New("bad learn params")
		}
		sid := d.ownerSessionID()
		if sid == "" {
			return nil, errNotConnected
		}
		return d.call(ctx, "learn.add", protocol.LearnFrame{LearnRequest: p, Reporter: sid})
	case "charter_set", "node_add", "node_remove", "edge_set", "edge_remove", "graph_undo", "node_settings_set":
		delete(args, "by")
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
	case "message":
		var m struct {
			Action  string `json:"action"`
			AsOwner bool   `json:"as_owner"`
		}
		if json.Unmarshal(raw, &m) == nil && !m.AsOwner && (m.Action == "send" || m.Action == "reply") {
			auditName = m.Action
		}
	case "ui":
		var u struct {
			Action string `json:"action"`
		}
		if json.Unmarshal(raw, &u) == nil && u.Action == "show" {
			auditName = "ui_show"
		} else if json.Unmarshal(raw, &u) == nil && u.Action == "notice" {
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
		Mode    string `json:"mode"`
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
		case "sessions":
			text = "read sessions"
		case "workers":
			text = "read workers"
		case "needs_you":
			text = "read needs you"
		case "feed":
			text = "read feed"
		case "charter_set":
			text = fmt.Sprintf("charter %s: %s updated · u to undo", a.Node, a.Attr)
		case "edge_set":
			text = fmt.Sprintf("edge %s → %s set (%s, %s) · u to undo", a.From, a.To, a.Kind, a.Mode)
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
	"mcp_import": true, "mcp_native_remove": true, "tools_plan": true, "tools_apply": true, "tools_undo": true,
	"inbox_answer": true, "outcome_overrule": true,
	"session_trust": true, "session_policy": true, "session_rename": true, "node_rename": true, "project_display_set": true, "integrations_set": true,
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
