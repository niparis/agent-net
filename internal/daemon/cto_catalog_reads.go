package daemon

// The shared catalog read tools as the CTO serves them. Each one derives its
// answer from the same ordinary-agent method an agent uses, through the CTO's
// read connection. The shapes follow the catalog output schemas; only the
// caller-scoped views (messages, MCP servers) need a little assembly here.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/niparis/agent-net/internal/protocol"
)

func (d *Daemon) readCTOCatalogTool(ctx context.Context, name string, raw json.RawMessage) (any, error) {
	switch name {
	case "agent_sessions_find", "agent_session_get", "agent_machines_find", "agent_machine_get", "agent_machine_log", "agent_projects_find", "agent_owner_judgments_find", "projects_layout":
		method := map[string]string{
			"agent_sessions_find": "sessions_find", "agent_session_get": "session_get",
			"agent_machines_find": "machines_find", "agent_machine_get": "machine_get", "agent_machine_log": "machine_log",
			"agent_projects_find": "projects_find", "agent_owner_judgments_find": "judgments_find", "projects_layout": "projects_layout",
		}[name]
		return d.ctoReadConn().dispatch(ctx, method, raw)
	case "agent_workers_find":
		return d.ctoCatalogWorkersFind(ctx, raw)
	case "agent_messages_find":
		return d.ctoCatalogMessagesFind(ctx, raw)
	case "agent_message_get":
		return d.ctoCatalogMessageGet(ctx, raw)
	case "agent_settings_read":
		var q protocol.SettingsReadRequest
		if len(raw) > 0 && json.Unmarshal(raw, &q) != nil {
			return nil, errors.New("bad settings arguments")
		}
		rows, err := d.readSettings(ctx, q, false)
		if err != nil {
			return nil, err
		}
		return map[string]any{"settings": rows}, nil
	case "agent_graph_read":
		return d.ctoCatalogGraphRead(ctx, raw)
	case "agent_mcp_servers_find":
		return d.ctoCatalogMCPServersFind(ctx, raw)
	case "agent_worker_profiles_find":
		return d.ctoCatalogWorkerProfilesFind(ctx, raw)
	}
	return nil, errors.New("CTO read has no ordinary-agent source")
}

func ctoCatalogLimit(n int) int {
	if n <= 0 {
		return 25
	}
	if n > 100 {
		return 100
	}
	return n
}

func ctoCatalogOffset(cursor string) (int, error) {
	if cursor == "" {
		return 0, nil
	}
	offset, err := strconv.Atoi(cursor)
	if err != nil || offset < 0 {
		return 0, errors.New("unknown or malformed cursor. Start again without cursor and use next_cursor from that result")
	}
	return offset, nil
}

func ctoCatalogPage(rows []map[string]any, limit int, cursor string) ([]map[string]any, string, error) {
	offset, err := ctoCatalogOffset(cursor)
	if err != nil {
		return nil, "", err
	}
	if offset > len(rows) {
		offset = len(rows)
	}
	end := offset + limit
	if end > len(rows) {
		end = len(rows)
	}
	next := ""
	if end < len(rows) {
		next = strconv.Itoa(end)
	}
	return rows[offset:end], next, nil
}

func ctoCatalogRef(id, name string) map[string]any {
	if name == "" {
		name = id
	}
	return map[string]any{"id": id, "name": name}
}

func ctoCatalogTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}

func ctoCatalogUnix(seconds int64) string {
	if seconds <= 0 {
		return ""
	}
	return time.Unix(seconds, 0).UTC().Format(time.RFC3339)
}

func ctoCatalogAgo(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	d := time.Since(t)
	if d < 0 {
		d = 0
	}
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	}
}

func ctoCatalogRunTime(startedAt int64, running bool) string {
	if !running || startedAt <= 0 {
		return ""
	}
	d := time.Since(time.Unix(startedAt, 0))
	if d < 0 {
		d = 0
	}
	switch {
	case d < time.Minute:
		return "<1m"
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh %dm", int(d.Hours()), int(d.Minutes())%60)
	default:
		return fmt.Sprintf("%dd %dh", int(d.Hours()/24), int(d.Hours())%24)
	}
}

func ctoCatalogCut(s string, max int) (string, bool) {
	if len(s) <= max {
		return s, false
	}
	end := max
	for end > 0 && !utf8.RuneStart(s[end]) {
		end--
	}
	return s[:end], true
}

func ctoCatalogPreview(body string, max int) (string, bool) {
	cut := false
	if i := strings.IndexByte(body, '\n'); i >= 0 {
		body, cut = body[:i], true
	}
	body = strings.TrimRight(body, "\r")
	short, wasCut := ctoCatalogCut(body, max)
	return short, cut || wasCut
}

func ctoCatalogChunk(body string, offset, max int) (string, int) {
	if offset >= len(body) {
		return "", len(body)
	}
	end := offset + max
	if end >= len(body) {
		return body[offset:], len(body)
	}
	for end > offset && !utf8.RuneStart(body[end]) {
		end--
	}
	return body[offset:end], end
}

func (d *Daemon) ctoCatalogNames(ctx context.Context) (map[string]string, []protocol.AgentView) {
	names := map[string]string{}
	value, err := d.ctoReadConn().dispatch(ctx, "list", json.RawMessage(`{"online_only":false}`))
	if err != nil {
		return names, nil
	}
	var result protocol.AgentsResult
	if ctoDecode(value, &result) != nil {
		return names, nil
	}
	for _, agent := range result.Agents {
		if agent.Profile.Name != "" {
			names[agent.SessionID] = agent.Profile.Name
		}
	}
	return names, result.Agents
}

func ctoCatalogName(names map[string]string, id, fallback string) string {
	if name := names[id]; name != "" {
		return name
	}
	if fallback != "" {
		return fallback
	}
	return id
}

func (d *Daemon) ctoCatalogWorkersFind(ctx context.Context, raw json.RawMessage) (any, error) {
	var q struct {
		StartedBy string `json:"started_by"`
		State     string `json:"state"`
		Project   string `json:"project"`
		Machine   string `json:"machine"`
		Limit     int    `json:"limit"`
		Cursor    string `json:"cursor"`
		Detail    string `json:"detail"`
	}
	if len(raw) > 0 && string(raw) != "null" && json.Unmarshal(raw, &q) != nil {
		return nil, errors.New("bad agent_workers_find arguments")
	}
	if q.StartedBy == "" {
		q.StartedBy = "anyone"
	}
	if q.State == "" {
		q.State = "running"
	}
	if q.Detail == "" {
		q.Detail = "concise"
	}
	if q.StartedBy != "me" && q.StartedBy != "anyone" {
		return nil, errors.New(`started_by must be me or anyone. Example: {"started_by":"anyone"}`)
	}
	if q.State != "running" && q.State != "ended" && q.State != "any" {
		return nil, errors.New(`state must be running, ended or any. Example: {"state":"any"}`)
	}
	if q.Detail != "concise" && q.Detail != "full" {
		return nil, errors.New(`detail must be concise or full. Example: {"detail":"full"}`)
	}
	limit := ctoCatalogLimit(q.Limit)
	caller := d.ctoSessionID()
	names, agents := d.ctoCatalogNames(ctx)
	rows := make([]map[string]any, 0)
	type keyed struct {
		row map[string]any
		key string
	}
	var keys []keyed
	for _, agent := range agents {
		if agent.Worker == nil {
			continue
		}
		if q.StartedBy == "me" && agent.Worker.Owner != caller {
			continue
		}
		if q.State == "running" && !agent.Online || q.State == "ended" && agent.Online {
			continue
		}
		if q.Project != "" && !strings.EqualFold(agent.Worker.Project, q.Project) {
			continue
		}
		machine := agent.Hostname
		if machine == "" {
			machine = agent.NodeID
		}
		if q.Machine != "" && !strings.EqualFold(machine, q.Machine) && agent.NodeID != q.Machine {
			continue
		}
		difficulty := agent.Worker.DifficultyUsed
		if difficulty == "" {
			difficulty = agent.Worker.DifficultyAsked
		}
		state := "ended"
		if agent.Online {
			state = "running"
		}
		row := map[string]any{
			"id": agent.SessionID, "name": ctoCatalogName(names, agent.SessionID, ""),
			"task": agent.Worker.Task, "machine": machine, "project": agent.Worker.Project,
			"harness": agent.Harness, "model": agent.Model, "difficulty": difficulty,
			"caller": ctoCatalogRef(agent.Worker.Owner, ctoCatalogName(names, agent.Worker.Owner, agent.Worker.Owner)),
			"state":  state, "run_time": ctoCatalogRunTime(agent.Worker.StartedAt, agent.Online),
		}
		if q.Detail == "full" {
			row["started"] = ctoCatalogUnix(agent.Worker.StartedAt)
		}
		rows = append(rows, row)
		keys = append(keys, keyed{row, fmt.Sprintf("%020d|%s", agent.Worker.StartedAt, agent.SessionID)})
	}
	sort.SliceStable(keys, func(i, j int) bool { return keys[i].key > keys[j].key })
	for i := range keys {
		rows[i] = keys[i].row
	}
	page, next, err := ctoCatalogPage(rows, limit, q.Cursor)
	if err != nil {
		return nil, err
	}
	out := map[string]any{"items": page, "total": len(rows)}
	if next != "" {
		out["next_cursor"] = next
		out["note"] = "More workers match. Call again with next_cursor for the next page, or narrow the filters."
	}
	return out, nil
}

func (d *Daemon) ctoCatalogMessagesFind(ctx context.Context, raw json.RawMessage) (any, error) {
	var q struct {
		Conversation string `json:"conversation"`
		With         string `json:"with"`
		Direction    string `json:"direction"`
		Since        string `json:"since"`
		Limit        int    `json:"limit"`
		Cursor       string `json:"cursor"`
		Detail       string `json:"detail"`
	}
	if len(raw) > 0 && string(raw) != "null" && json.Unmarshal(raw, &q) != nil {
		return nil, errors.New("bad agent_messages_find arguments")
	}
	if q.Direction == "" {
		q.Direction = "any"
	}
	if q.Direction != "in" && q.Direction != "out" && q.Direction != "any" {
		return nil, errors.New(`direction must be in, out or any. Example: {"direction":"in"}`)
	}
	if q.Detail == "" {
		q.Detail = "concise"
	}
	if q.Detail != "concise" && q.Detail != "full" {
		return nil, errors.New(`detail must be concise or full. Example: {"detail":"full"}`)
	}
	limit := ctoCatalogLimit(q.Limit)
	var since time.Time
	var err error
	if q.Since != "" {
		since, err = time.Parse(time.RFC3339, q.Since)
		if err != nil {
			return nil, fmt.Errorf("since must be an RFC 3339 time, not %q. Example: {\"since\":\"2026-01-01T12:00:00Z\"}", q.Since)
		}
	}
	caller := d.ctoSessionID()
	names, _ := d.ctoCatalogNames(ctx)
	var messages []protocol.Message
	seen := map[string]bool{}
	offset := 0
	for page := 0; page < 200; page++ {
		params, _ := json.Marshal(map[string]any{"conversation_id": q.Conversation, "limit": 200, "offset": offset})
		value, err := d.ctoReadConn().dispatch(ctx, "messages", params)
		if err != nil {
			return nil, err
		}
		var result struct {
			Messages []protocol.Message `json:"messages"`
			Total    int                `json:"total"`
		}
		if err := ctoDecode(value, &result); err != nil {
			return nil, err
		}
		added := 0
		for _, message := range result.Messages {
			if seen[message.ID] {
				continue
			}
			seen[message.ID] = true
			messages = append(messages, message)
			added++
		}
		if len(result.Messages) == 0 || added == 0 || len(messages) >= result.Total {
			break
		}
		offset += len(result.Messages)
	}
	type keyed struct {
		row map[string]any
		key string
	}
	var keys []keyed
	for _, message := range messages {
		if q.Direction == "in" && message.To != caller || q.Direction == "out" && message.From != caller {
			continue
		}
		if q.With != "" && !ctoCatalogMessageWith(message, q.With, names) {
			continue
		}
		if !since.IsZero() && message.CreatedAt.Before(since) {
			continue
		}
		text, truncated := ctoCatalogPreview(message.Body, 200)
		rows := map[string]any{
			"id": message.ID, "conversation": message.ConversationID,
			"from": ctoCatalogRef(message.From, ctoCatalogName(names, message.From, message.FromName)),
			"to":   ctoCatalogRef(message.To, ctoCatalogName(names, message.To, "")),
			"type": message.Type, "state": message.State,
			"time": ctoCatalogTime(message.CreatedAt), "ago": ctoCatalogAgo(message.CreatedAt),
			"text": text, "truncated": truncated,
		}
		keys = append(keys, keyed{rows, fmt.Sprintf("%020d|%s", message.CreatedAt.UnixNano(), message.ID)})
	}
	sort.SliceStable(keys, func(i, j int) bool { return keys[i].key > keys[j].key })
	rows := make([]map[string]any, 0, len(keys))
	for _, k := range keys {
		rows = append(rows, k.row)
	}
	page, next, err := ctoCatalogPage(rows, limit, q.Cursor)
	if err != nil {
		return nil, err
	}
	out := map[string]any{"items": page, "total": len(rows)}
	if next != "" {
		out["next_cursor"] = next
		out["note"] = "More messages match. Call again with next_cursor for the next page, or narrow the filters."
	}
	return out, nil
}

func ctoCatalogMessageWith(message protocol.Message, wanted string, names map[string]string) bool {
	if message.From == wanted || message.To == wanted {
		return true
	}
	for _, candidate := range []string{message.FromName, names[message.From], names[message.To]} {
		if candidate != "" && strings.EqualFold(candidate, wanted) {
			return true
		}
	}
	return false
}

func (d *Daemon) ctoCatalogMessageGet(ctx context.Context, raw json.RawMessage) (any, error) {
	var q struct {
		Message    string `json:"message"`
		BodyCursor string `json:"body_cursor"`
		MaxBytes   int    `json:"max_bytes"`
		File       string `json:"file"`
		FileCursor string `json:"file_cursor"`
	}
	if len(raw) > 0 && string(raw) != "null" && json.Unmarshal(raw, &q) != nil {
		return nil, errors.New("bad agent_message_get arguments")
	}
	if strings.TrimSpace(q.Message) == "" {
		return nil, errors.New(`message is required. Example: {"message":"m_1234"}`)
	}
	maxBytes := q.MaxBytes
	if maxBytes == 0 {
		maxBytes = 8192
	}
	if maxBytes < 256 || maxBytes > 16384 {
		return nil, errors.New(`max_bytes must be between 256 and 16384. Example: {"max_bytes":8192}`)
	}
	params, _ := json.Marshal(map[string]any{"message_id": q.Message})
	value, err := d.ctoReadConn().dispatch(ctx, "message_status", params)
	if err != nil {
		return nil, err
	}
	var result protocol.MessageResult
	if err := ctoDecode(value, &result); err != nil {
		return nil, err
	}
	message := result.Message
	if message.ID == "" {
		return nil, fmt.Errorf("unknown message %q", q.Message)
	}
	offset, err := ctoCatalogOffset(q.BodyCursor)
	if err != nil {
		return nil, err
	}
	if offset > len(message.Body) {
		return nil, fmt.Errorf("the body cursor is stale or unknown for message %q: call again without body_cursor", message.ID)
	}
	names, _ := d.ctoCatalogNames(ctx)
	chunk, next := ctoCatalogChunk(message.Body, offset, maxBytes)
	text, truncated := ctoCatalogPreview(message.Body, 200)
	out := map[string]any{
		"message": map[string]any{
			"id": message.ID, "conversation": message.ConversationID,
			"from": ctoCatalogRef(message.From, ctoCatalogName(names, message.From, message.FromName)),
			"to":   ctoCatalogRef(message.To, ctoCatalogName(names, message.To, "")),
			"type": message.Type, "state": message.State,
			"time": ctoCatalogTime(message.CreatedAt), "ago": ctoCatalogAgo(message.CreatedAt),
			"text": text, "truncated": truncated, "body": chunk,
			"outcome": "", "reason": "",
		},
		"body_truncated": next < len(message.Body),
	}
	if next < len(message.Body) {
		out["next_body_cursor"] = strconv.Itoa(next)
	}
	if len(message.Files) > 0 {
		files := make([]map[string]any, 0, len(message.Files))
		for _, f := range message.Files {
			files = append(files, map[string]any{"name": f.Name, "size": f.Size})
		}
		out["files"] = files
	}
	if q.File == "" && q.FileCursor != "" {
		return nil, fmt.Errorf(`file_cursor needs file. Example: {"message":%q,"file":"notes.md"}`, message.ID)
	}
	if q.File != "" {
		fileOffset, err := ctoCatalogOffset(q.FileCursor)
		if err != nil {
			return nil, err
		}
		params, _ := json.Marshal(protocol.MessageFileRequest{MessageID: message.ID, File: q.File, Offset: int64(fileOffset), MaxBytes: maxBytes})
		value, err := d.ctoReadConn().dispatch(ctx, "message_file", params)
		if err != nil {
			return nil, err
		}
		var page protocol.MessageFileResult
		if err := ctoDecode(value, &page); err != nil {
			return nil, err
		}
		file := map[string]any{"name": page.Name, "size": page.Size, "type": page.Type}
		if page.Text != "" {
			file["text"] = page.Text
		}
		if page.NextOffset > 0 {
			file["next_file_cursor"] = strconv.FormatInt(page.NextOffset, 10)
		}
		out["file"] = file
	}
	return out, nil
}

// ctoCharterGuide is the CTO's guide to writing charters and edges. The CTO
// reads it with agent_graph_read view guide; it is kept out of the schemas.
func ctoCharterGuide() string {
	return protocol.CharterGuideText() + "\n\nEdges: depends goes from consumer to provider; deploys from deployer to app. Delivery owner edges accepts, reviews, tests, tests_after_merge, approves, approves_deploy, merges, releases, delivery_deploys and verifies go from the owner node to the repo. Request rules: " + protocol.EdgeRulesText() + "."
}

func (d *Daemon) ctoCatalogGraphRead(ctx context.Context, raw json.RawMessage) (any, error) {
	var q struct {
		Ref    *protocol.GraphRef `json:"ref"`
		Typed  bool               `json:"typed"`
		View   string             `json:"view"`
		Node   string             `json:"node"`
		Root   string             `json:"root"`
		Limit  int                `json:"limit"`
		Cursor string             `json:"cursor"`
	}
	if len(raw) > 0 && string(raw) != "null" && json.Unmarshal(raw, &q) != nil {
		return nil, errors.New("bad agent_graph_read arguments")
	}
	switch q.View {
	case "":
	case "guide":
		return map[string]any{"nodes": []any{}, "edges": []any{}, "note": ctoCharterGuide()}, nil
	default:
		return nil, errors.New(`unknown agent_graph_read view; the only view is guide, for example {"view":"guide"}`)
	}
	limit := ctoCatalogLimit(q.Limit)
	if strings.TrimSpace(q.Node) != "" || q.Ref != nil {
		page, err := d.agentGraph(ctx, protocol.PublicGraphQuery{Node: q.Node, Ref: q.Ref, Typed: q.Typed, Root: q.Root})
		if err != nil {
			return nil, err
		}
		out := map[string]any{"nodes": ctoCatalogGraphNodes(page.Nodes), "edges": ctoCatalogGraphEdges(page.Edges)}
		if page.Note != "" {
			out["note"] = page.Note
		}
		if len(page.Nodes) == 0 {
			out["note"] = fmt.Sprintf("No graph node is named %q. Read the graph without node to list the names.", q.Node)
		}
		return out, nil
	}
	var nodes []map[string]any
	var edges []map[string]any
	offset := 0
	for page := 0; page < 1000; page++ {
		got, err := d.agentGraph(ctx, protocol.PublicGraphQuery{Offset: offset, Limit: protocol.PublicGraphMaxLimit, Typed: q.Typed, Root: q.Root})
		if err != nil {
			return nil, err
		}
		if page == 0 {
			edges = ctoCatalogGraphEdges(got.Edges)
		}
		nodes = append(nodes, ctoCatalogGraphNodes(got.Nodes)...)
		if got.Next <= 0 || len(got.Nodes) == 0 {
			break
		}
		offset = got.Next
	}
	sort.SliceStable(nodes, func(i, j int) bool {
		if nodes[i]["name"] != nodes[j]["name"] {
			return nodes[i]["name"].(string) < nodes[j]["name"].(string)
		}
		return fmt.Sprint(nodes[i]["ref"]) < fmt.Sprint(nodes[j]["ref"])
	})
	if nodes == nil {
		nodes = []map[string]any{}
	}
	start, err := ctoCatalogOffset(q.Cursor)
	if err != nil {
		return nil, err
	}
	if start > len(nodes) {
		start = len(nodes)
	}
	end := start + limit
	if end > len(nodes) {
		end = len(nodes)
	}
	if edges == nil {
		edges = []map[string]any{}
	}
	out := map[string]any{"nodes": nodes[start:end], "edges": edges}
	if end < len(nodes) {
		out["next_cursor"] = strconv.Itoa(end)
		out["note"] = "More graph nodes match. Call again with next_cursor for the next page."
	}
	if out["nodes"] == nil {
		out["nodes"] = []map[string]any{}
	}
	return out, nil
}

func ctoCatalogGraphNodes(nodes []protocol.PublicGraphNode) []map[string]any {
	out := make([]map[string]any, 0, len(nodes))
	for _, node := range nodes {
		purpose, maturity := "", node.Maturity
		if node.Charter != nil {
			purpose = node.Charter.Purpose
			if node.Charter.Maturity != "" {
				maturity = node.Charter.Maturity
			}
		}
		rules := map[string]string{}
		for _, rule := range node.Rules {
			rules[rule.Name] = rule.Value
		}
		row := map[string]any{"name": node.Name, "kind": node.Kind, "maturity": maturity, "purpose": purpose, "rules": rules}
		if node.Active != nil {
			row["active"] = *node.Active
		}
		if node.Ref != nil {
			row["ref"] = node.Ref
			row["owner"] = node.Owner
			row["lead"] = node.Lead
			row["max_agents"] = node.MaxAgents
			row["agent_count"] = node.AgentCount
			row["over_max"] = node.OverMax
			row["role"] = node.Role
		}
		if charter := ctoCatalogCharter(node.Charter); charter != nil {
			row["charter"] = charter
		}
		if node.ReportsTo != "" {
			row["reports_to"] = node.ReportsTo
		}
		if node.LeadSession != nil {
			row["lead_session"] = node.LeadSession
		}
		if len(node.Sessions) > 0 {
			row["sessions"] = node.Sessions
		}
		if len(node.Scopes) > 0 {
			row["scopes"] = node.Scopes
		}
		out = append(out, row)
	}
	return out
}

// ctoCatalogCharter is the whole public charter: the CTO reads it before it
// proposes a charter change. Empty attributes are left out.
func ctoCatalogCharter(c *protocol.Charter) map[string]any {
	if c == nil {
		return nil
	}
	out := map[string]any{}
	if c.Sources != nil {
		out["sources"] = c.Sources
	}
	for key, value := range map[string]string{"purpose": c.Purpose, "feature_requests": c.FeatureRequests, "maturity": c.Maturity, "maturity_note": c.MaturityNote, "escalate": c.Escalate, "rules": c.Rules} {
		if strings.TrimSpace(value) != "" {
			out[key] = value
		}
	}
	for key, value := range map[string]string{"accepts": c.Accepts, "refuses": c.Refuses} {
		if items := protocol.SplitCharterList(value); len(items) > 0 {
			out[key] = items
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func ctoCatalogGraphEdges(edges []protocol.PublicGraphEdge) []map[string]any {
	out := make([]map[string]any, 0, len(edges))
	for _, edge := range edges {
		var from, to any = edge.From, edge.To
		if edge.FromRef != nil {
			from = edge.FromRef
		}
		if edge.ToRef != nil {
			to = edge.ToRef
		}
		out = append(out, map[string]any{"from": from, "to": to, "kind": edge.Kind, "mode": edge.Mode, "rule": edge.Rule})
	}
	return out
}

func (d *Daemon) ctoCatalogMCPServersFind(ctx context.Context, raw json.RawMessage) (any, error) {
	var q struct {
		UsableOnly *bool  `json:"usable_only"`
		Limit      int    `json:"limit"`
		Cursor     string `json:"cursor"`
		Detail     string `json:"detail"`
	}
	if len(raw) > 0 && string(raw) != "null" && json.Unmarshal(raw, &q) != nil {
		return nil, errors.New("bad agent_mcp_servers_find arguments")
	}
	usableOnly := true
	if q.UsableOnly != nil {
		usableOnly = *q.UsableOnly
	}
	if q.Detail == "" {
		q.Detail = "concise"
	}
	if q.Detail != "concise" && q.Detail != "full" {
		return nil, errors.New(`detail must be concise or full. Example: {"detail":"full"}`)
	}
	limit := ctoCatalogLimit(q.Limit)
	project := d.ctoCatalogProject(ctx)
	value, err := d.ctoReadConn().dispatch(ctx, "mcp_list", nil)
	if err != nil {
		return nil, err
	}
	var result struct {
		Servers []protocol.MCPServer `json:"servers"`
	}
	if err := ctoDecode(value, &result); err != nil {
		return nil, err
	}
	rows := make([]map[string]any, 0, len(result.Servers))
	for _, server := range result.Servers {
		usable := server.Enabled && server.HostOnline && ctoCatalogMCPScopeAllows(server, project)
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
		rows = append(rows, map[string]any{"name": server.Name, "machine": machine, "enabled": server.Enabled,
			"health": server.Health, "usable_by_you": usable, "tools": names})
	}
	sort.SliceStable(rows, func(i, j int) bool {
		a := rows[i]["name"].(string) + "\x00" + rows[i]["machine"].(string)
		b := rows[j]["name"].(string) + "\x00" + rows[j]["machine"].(string)
		return a < b
	})
	page, next, err := ctoCatalogPage(rows, limit, q.Cursor)
	if err != nil {
		return nil, err
	}
	out := map[string]any{"items": page, "total": len(rows)}
	if next != "" {
		out["next_cursor"] = next
		out["note"] = "More MCP servers match. Call again with next_cursor for the next page."
	}
	return out, nil
}

func (d *Daemon) ctoCatalogProject(ctx context.Context) string {
	value, err := d.ctoReadConn().dispatch(ctx, "info", nil)
	if err != nil {
		return ""
	}
	var info protocol.OperatorInfo
	if ctoDecode(value, &info) != nil {
		return ""
	}
	id := d.ctoSessionID()
	for _, session := range info.Sessions {
		if session.SessionID == id {
			return session.Repo.Project
		}
	}
	return ""
}

func ctoCatalogMCPScopeAllows(server protocol.MCPServer, project string) bool {
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

func (d *Daemon) ctoCatalogWorkerProfilesFind(ctx context.Context, raw json.RawMessage) (any, error) {
	var q struct {
		Limit  int    `json:"limit"`
		Cursor string `json:"cursor"`
		Detail string `json:"detail"`
	}
	if len(raw) > 0 && string(raw) != "null" && json.Unmarshal(raw, &q) != nil {
		return nil, errors.New("bad agent_worker_profiles_find arguments")
	}
	if q.Detail == "" {
		q.Detail = "concise"
	}
	if q.Detail != "concise" && q.Detail != "full" {
		return nil, errors.New(`detail must be concise or full. Example: {"detail":"full"}`)
	}
	limit := ctoCatalogLimit(q.Limit)
	value, err := d.ctoReadConn().dispatch(ctx, protocol.OpWorkerProfiles, nil)
	if err != nil {
		return nil, err
	}
	var profiles []protocol.WorkerProfile
	if err := ctoDecode(value, &profiles); err != nil {
		var wrapped struct {
			Profiles []protocol.WorkerProfile `json:"profiles"`
		}
		if err2 := ctoDecode(value, &wrapped); err2 != nil {
			return nil, err
		}
		profiles = wrapped.Profiles
	}
	rows := make([]map[string]any, 0, len(profiles))
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
		sort.Strings(on)
		row := map[string]any{"name": profile.Name, "harness": profile.Harness, "model": profile.Model,
			"effort": profile.Effort, "available_on": on}
		if profile.Score != nil {
			row["score"] = *profile.Score
			row["score_source"] = profile.ScoreSource
		}
		rows = append(rows, row)
	}
	sort.SliceStable(rows, func(i, j int) bool { return rows[i]["name"].(string) < rows[j]["name"].(string) })
	page, next, err := ctoCatalogPage(rows, limit, q.Cursor)
	if err != nil {
		return nil, err
	}
	out := map[string]any{"items": page, "total": len(rows)}
	if next != "" {
		out["next_cursor"] = next
		out["note"] = "More worker profiles match. Call again with next_cursor for the next page."
	}
	return out, nil
}
