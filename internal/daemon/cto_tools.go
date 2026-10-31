package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"

	"strings"
	"time"

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
	return lc.d.runCTOTool(ctx, q.Name, q.Arguments)
}

func (d *Daemon) runCTOToolRaw(ctx context.Context, name string, raw json.RawMessage) (any, error) {
	var args map[string]any
	if len(raw) > 0 && string(raw) != "null" && json.Unmarshal(raw, &args) != nil {
		return nil, errors.New("bad CTO tool arguments")
	}
	if args == nil {
		args = map[string]any{}
	}
	// These operations reuse the same owner implementations after ctoTool has
	// checked the calling process tree. This connection is never exposed.
	lc := &localConn{d: d, client: "cli", cto: true}
	if ctoOwnerMethods[name] {
		res, _, err := lc.dispatchOwner(ctx, name, raw)
		return res, err
	}
	if ctoLocalMethods[name] && name != "nodes" && name != "projects" {
		return lc.dispatch(ctx, name, raw)
	}
	switch name {
	case "tools_list":
		data, err := d.call(ctx, "query.tools_items", json.RawMessage(raw))
		if err != nil {
			return nil, err
		}
		var page protocol.ToolsPage
		if err = json.Unmarshal(data, &page); err != nil {
			return nil, err
		}
		needs, err := d.call(ctx, "query.tools_needs", struct{}{})
		if err != nil {
			return nil, err
		}
		var advisory []protocol.ToolNeed
		if err = json.Unmarshal(needs, &advisory); err != nil {
			return nil, err
		}
		return map[string]any{"items": page.Items, "next": page.Next, "unmet_needs": advisory}, nil
	case "ui_show", "ui_notice":
		var q protocol.UIRequest
		if json.Unmarshal(raw, &q) != nil {
			return nil, errors.New("bad UI request")
		}
		q.Tab, q.Select, q.Filter, q.Why, q.Text = cleanLine(q.Tab), cleanLine(q.Select), cleanLine(q.Filter), cleanLine(q.Why), cleanLine(q.Text)
		if name == "ui_show" {
			if q.Tab != "sessions" && q.Tab != "messages" && q.Tab != "quotas" && q.Tab != "map" {
				return nil, errors.New("ui_show tab must be sessions, messages, quotas or map")
			}
			q.Text = ""
			if q.Why == "" {
				q.Why = "Showing " + q.Tab
			}
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
	case "reply":
		var p struct {
			MessageID string `json:"message_id"`
			Body      string `json:"body"`
		}
		if json.Unmarshal(raw, &p) != nil || p.MessageID == "" || p.Body == "" {
			return nil, errors.New("reply needs message_id and body")
		}
		sid := d.ownerSessionID()
		if sid == "" {
			return nil, errNotConnected
		}
		data, err := d.call(ctx, "query.message", protocol.QueryMessage{MessageID: p.MessageID})
		if err != nil {
			return nil, err
		}
		var parent protocol.MessageResult
		if err := json.Unmarshal(data, &parent); err != nil {
			return nil, err
		}
		if parent.Message.To != sid {
			return nil, errors.New("message was not sent to the owner")
		}
		return d.send(ctx, protocol.MsgSend{Sender: sid, Recipient: parent.Message.From, Type: protocol.TypeReply, Body: p.Body, ParentID: p.MessageID, FromCTO: true})
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
	case "graph_get", "charter_set", "charter_history", "node_add", "node_remove", "edge_set", "edge_remove", "edge_history", "graph_undo":
		delete(args, "by")
		if name == "graph_undo" {
			args["history_id"] = args["id"]
			delete(args, "id")
		}
		args["by"] = "cto"
		if name != "graph_get" && name != "charter_history" && name != "edge_history" {
			return d.ctoAttributedCall(ctx, strings.ReplaceAll(name, "_", "."), args)
		}
		return d.call(ctx, strings.ReplaceAll(name, "_", "."), args)
	case "nodes":
		data, err := d.call(ctx, "query.nodes", nil)
		if err != nil {
			return nil, err
		}
		var nodes protocol.NodesInfo
		if err = json.Unmarshal(data, &nodes); err != nil {
			return nil, err
		}
		var opt struct {
			Models *struct {
				Machine string `json:"machine"`
				Harness string `json:"harness"`
			} `json:"models"`
		}
		if err = json.Unmarshal(raw, &opt); err != nil && len(raw) > 0 {
			return nil, err
		}
		if opt.Models != nil {
			for _, n := range nodes.Nodes {
				if n.Hostname == opt.Models.Machine || n.NodeID == opt.Models.Machine {
					for _, h := range n.Harnesses {
						if h.Name == opt.Models.Harness {
							return map[string]any{"machine": n.Hostname, "harness": h.Name, "models": h.Models}, nil
						}
					}
				}
			}
			return nil, errors.New("machine or harness not found")
		}
		data, err = d.call(ctx, "query.agents", protocol.QueryAgents{IncludeOwners: true})
		if err != nil {
			return nil, err
		}
		var agents protocol.AgentsResult
		if err = json.Unmarshal(data, &agents); err != nil {
			return nil, err
		}
		return compactCTONodes(nodes, agents.Agents), nil
	case "agents":
		data, err := d.call(ctx, "query.agents", args)
		if err != nil {
			return nil, err
		}
		var agents protocol.AgentsResult
		if err = json.Unmarshal(data, &agents); err != nil {
			return nil, err
		}
		return compactCTOAgents(agents.Agents), nil
	case "feed":
		var p struct {
			Limit  int   `json:"limit"`
			Before int64 `json:"before"`
		}
		if len(raw) > 0 && json.Unmarshal(raw, &p) != nil {
			return nil, errors.New("bad pagination")
		}
		limit := ctoLimit(p.Limit)
		data, err := d.call(ctx, "query.events", protocol.QueryEvents{Limit: limit + 1, BeforeID: p.Before})
		if err != nil {
			return nil, err
		}
		var r protocol.EventsResult
		if err = json.Unmarshal(data, &r); err != nil {
			return nil, err
		}
		for i, j := 0, len(r.Events)-1; i < j; i, j = i+1, j-1 {
			r.Events[i], r.Events[j] = r.Events[j], r.Events[i]
		}
		next := int64(0)
		if len(r.Events) > limit {
			r.Events = r.Events[:limit]
			next = r.Events[len(r.Events)-1].ID
		}
		return map[string]any{"events": r.Events, "next_before": next}, nil
	case "projects":
		var p struct {
			Name   string `json:"name"`
			Limit  int    `json:"limit"`
			Before string `json:"before"`
		}
		if len(raw) > 0 && json.Unmarshal(raw, &p) != nil {
			return nil, errors.New("bad pagination")
		}
		data, err := d.call(ctx, "query.projects", protocol.QueryProjects{Name: p.Name})
		if err != nil {
			return nil, err
		}
		var r protocol.ProjectsResult
		if err = json.Unmarshal(data, &r); err != nil {
			return nil, err
		}
		page, next := ctoPage(r.Projects, p.Limit, p.Before, func(v protocol.ProjectView) string { return v.Name + "|" + v.NodeID + "|" + v.Path })
		return map[string]any{"projects": page, "next_before": next}, nil
	case "workers":
		var p struct {
			Limit  int    `json:"limit"`
			Before string `json:"before"`
		}
		if len(raw) > 0 && json.Unmarshal(raw, &p) != nil {
			return nil, errors.New("bad pagination")
		}
		r, err := d.workersView(ctx, protocol.WorkersQuery{Recent: true})
		if err != nil {
			return nil, err
		}
		page, next := ctoPage(r.Workers, p.Limit, p.Before, func(v protocol.WorkerView) string { return fmt.Sprintf("%020d|%s", v.StartedAt, v.SessionID) })
		return map[string]any{"workers": page, "next_before": next}, nil
	case "needs_you":
		data, err := d.call(ctx, "query.agents", protocol.QueryAgents{OnlineOnly: true})
		if err != nil {
			return nil, err
		}
		var a protocol.AgentsResult
		if err = json.Unmarshal(data, &a); err != nil {
			return nil, err
		}
		return compactCTONeedsYou(a.Agents, time.Now()), nil
	case "send":
		var p struct {
			To   string `json:"to"`
			Body string `json:"body"`
		}
		if json.Unmarshal(raw, &p) != nil || p.To == "" || p.Body == "" {
			return nil, errors.New("send needs to and body")
		}
		sid := d.ownerSessionID()
		if sid == "" {
			return nil, errNotConnected
		}
		to, err := d.resolveTo(ctx, p.To)
		if err != nil {
			return nil, err
		}
		return d.send(ctx, protocol.MsgSend{Sender: sid, Recipient: to, Type: protocol.TypeRequest, Body: p.Body, FromCTO: true})
	case "worker_profile_list", "worker_profile_add", "worker_profile_remove":
		var p protocol.WorkerProfileRequest
		if len(raw) > 0 && string(raw) != "null" && json.Unmarshal(raw, &p) != nil {
			return nil, errors.New("bad worker profile request")
		}
		sid := d.ownerSessionID()
		if sid == "" {
			return nil, errNotConnected
		}
		action := map[string]string{"worker_profile_list": "worker.profile.list", "worker_profile_add": "worker.profile.add", "worker_profile_remove": "worker.profile.remove"}[name]
		return d.call(ctx, action, protocol.WorkerProfileFrame{WorkerProfileRequest: p, Requester: sid})
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
	}
	return nil, errors.New("unknown CTO tool")
}

// Every tool call is recorded, including reads, no-ops and failures. Graph
// history supplies the separate before-and-after diff for a successful change.
func (d *Daemon) runCTOTool(ctx context.Context, name string, raw json.RawMessage) (any, error) {
	result, err := d.runCTOToolRaw(ctx, name, raw)
	var a struct {
		To      string `json:"to"`
		Body    string `json:"body"`
		Project string `json:"project"`
		Task    string `json:"task"`
		Node    string `json:"node"`
		Attr    string `json:"attr"`
	}
	_ = json.Unmarshal(raw, &a)
	text := name
	if err != nil {
		text = fmt.Sprintf("failed %s: %v", name, err)
	} else {
		switch name {
		case "graph_get":
			text = "read graph"
		case "charter_history":
			text = "read charter history: " + a.Node
		case "edge_history":
			text = "read edge history: " + a.Node
		case "agents":
			text = "read agents"
		case "workers":
			text = "read workers"
		case "needs_you":
			text = "read needs you"
		case "feed":
			text = "read feed"
		case "charter_set":
			text = fmt.Sprintf("charter_set %s.%s", a.Node, a.Attr)
		case "send":
			text = fmt.Sprintf("sent to %s: %s", a.To, a.Body)
		case "worker_start":
			verb := "started"
			if r, ok := result.(*protocol.WorkerStartResult); ok && r.Status == "queued" {
				verb = "queued"
			}
			text = fmt.Sprintf("%s worker %s: %s", verb, a.Project, a.Task)
		}
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
		Attribution bool `json:"attribution"`
	}
	if json.Unmarshal(b, &caps) != nil || !caps.Attribution {
		return nil, errors.New("gateway cannot verify CTO attribution; upgrade the gateway")
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
	"tools_plan": true, "tools_apply": true, "tools_history": true,
	"inbox_list": true, "inbox_answer": true, "outcome_inbox": true, "outcome_overrule": true,
	"usage": true, "usage_top": true, "counts": true, "graph_view": true, "session_text": true, "session_enter": true,
	"session_trust": true, "session_policy": true, "node_rename": true, "integrations_get": true, "integrations_set": true,
	"held_list": true, "held_answer": true, "node_settings_get": true, "node_settings_set": true,
	"playbook_get": true, "playbook_set": true, "cto_settings": true, "cto_enter": true, "cto_send": true, "cto_new": true, "cto_events": true, "cto_graph_history": true,
}
var ctoLocalMethods = map[string]bool{
	"info": true, "nodes": true, "projects": true, "project_add": true, "project_remove": true,
	"get": true, "messages": true, "message_status": true, "mcp_list": true, "mcp_add": true,
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
