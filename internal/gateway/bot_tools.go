package gateway

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/auth"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/niparis/agent-net/internal/protocol"
	"github.com/niparis/agent-net/internal/settingsview"
	"github.com/niparis/agent-net/internal/toolcatalog"
)

func (g *Gateway) botMCPServer() *mcp.Server {
	version := g.opts.Version
	if version == "" {
		version = "dev"
	}
	server := mcp.NewServer(&mcp.Implementation{Name: "agent-net", Version: version}, nil)
	for _, entry := range toolcatalog.ForRole(toolcatalog.RoleBot) {
		var schema jsonschema.Schema
		if err := json.Unmarshal(entry.InputSchema, &schema); err != nil {
			panic(err)
		}
		resolved, err := schema.Resolve(nil)
		if err != nil {
			panic(err)
		}
		open := entry.Hints.OpenWorld
		server.AddTool(&mcp.Tool{Name: entry.Name, Description: entry.Description, InputSchema: entry.InputSchema,
			Annotations: &mcp.ToolAnnotations{ReadOnlyHint: entry.Hints.ReadOnly, IdempotentHint: entry.Hints.Idempotent, OpenWorldHint: &open}},
			func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
				info := auth.TokenInfoFromContext(ctx)
				if info == nil || info.UserID == "" {
					return botToolError(fmt.Errorf("missing app identity"))
				}
				for _, kind := range []string{"call", "message"} {
					if kind == "message" && entry.Name != "agent_message_send" && entry.Name != "agent_message_reply" {
						continue
					}
					if ok, retry := g.limiter.AllowApp(info.UserID, kind); !ok {
						seconds := int64((retry-1)/time.Second) + 1
						g.logLine("apps.rate_limit", "app", info.UserID, "kind", kind)
						return botToolError(fmt.Errorf("Rate limit: try again in %d seconds", seconds))
					}
				}
				var in map[string]any
				if req.Params == nil || len(req.Params.Arguments) == 0 {
					in = map[string]any{}
				} else if err := json.Unmarshal(req.Params.Arguments, &in); err != nil || in == nil {
					return botToolError(fmt.Errorf("arguments must be a JSON object"))
				}
				if err := resolved.Validate(in); err != nil {
					return botToolError(err)
				}
				var sid string
				if err := g.store.db.QueryRow(`SELECT id FROM sessions WHERE node_id=? AND harness='bot' AND native_id=?`, info.UserID, info.UserID).Scan(&sid); err != nil {
					return botToolError(err)
				}
				out, err := g.botToolCall(ctx, sid, entry.Name, in)
				if err != nil {
					return botToolError(err)
				}
				raw, err := json.Marshal(out)
				if err != nil {
					return botToolError(err)
				}
				return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: string(raw)}}, StructuredContent: out}, nil
			})
	}
	return server
}

func botToolError(err error) (*mcp.CallToolResult, error) {
	return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: err.Error()}}}, nil
}

func botDecode(in map[string]any, dst any) error {
	raw, err := json.Marshal(in)
	if err != nil {
		return err
	}
	return json.Unmarshal(raw, dst)
}

func (g *Gateway) botToolCall(ctx context.Context, sid, name string, in map[string]any) (any, error) {
	switch name {
	case "agent_sessions_find":
		var p protocol.SessionsFindRequest
		if err := botDecode(in, &p); err != nil {
			return nil, err
		}
		p.Caller = sid
		return g.store.FindSessions(p)
	case "agent_session_get":
		var p protocol.SessionGetRequest
		if err := botDecode(in, &p); err != nil {
			return nil, err
		}
		p.Caller = sid
		return g.store.GetSession(p)
	case "agent_machines_find":
		var p protocol.MachinesFindRequest
		if err := botDecode(in, &p); err != nil {
			return nil, err
		}
		p.Caller = sid
		return g.FindMachines(p)
	case "agent_machine_get":
		var p protocol.MachineGetRequest
		if err := botDecode(in, &p); err != nil {
			return nil, err
		}
		p.Caller = sid
		return g.MachineGet(p)
	case "agent_projects_find":
		var p protocol.ProjectsFindRequest
		if err := botDecode(in, &p); err != nil {
			return nil, err
		}
		p.Caller = sid
		return g.store.FindProjects(p)
	case "agent_settings_read":
		var q protocol.SettingsReadRequest
		if err := botDecode(in, &q); err != nil {
			return nil, err
		}
		snap, err := g.store.SettingsSnapshot()
		if err != nil {
			return nil, err
		}
		history, err := g.store.SettingsHistory(q.Key, 0, false)
		if err != nil {
			return nil, err
		}
		rows, err := settingsview.Views(snap, history, q, false)
		if err != nil {
			return nil, err
		}
		return map[string]any{"settings": rows}, nil
	case "agent_graph_read":
		return g.botGraph(sid, in)
	case "agent_messages_find":
		return g.botMessages(sid, in)
	case "agent_message_get":
		return g.botMessageGet(sid, in)
	case "agent_message_send", "agent_message_reply":
		return g.botMessageSend(ctx, sid, name, in)
	}
	return nil, fmt.Errorf("unknown bot tool %q", name)
}

type botRef struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}
type botReceipt struct {
	MessageID string `json:"message_id"`
	To        botRef `json:"to"`
	State     string `json:"state"`
}

func (g *Gateway) botMessageSend(ctx context.Context, sid, tool string, in map[string]any) (any, error) {
	if _, ok := in["to_set"]; ok {
		return nil, fmt.Errorf("bots cannot send to a group")
	}
	if _, ok := in["as_owner"]; ok {
		return nil, fmt.Errorf("bots cannot send as the owner")
	}
	var p struct {
		To      string `json:"to"`
		Message string `json:"message"`
		Body    string `json:"body"`
		Type    string `json:"type"`
		protocol.ReplyOutcome
	}
	if err := botDecode(in, &p); err != nil {
		return nil, err
	}
	var recipient string
	if tool == "agent_message_reply" {
		parent, ok, err := g.store.Message(p.Message)
		if err != nil {
			return nil, err
		}
		if !ok || parent.To != sid {
			return nil, fmt.Errorf("unknown received message %q", p.Message)
		}
		recipient = parent.From
		p.Type = protocol.TypeReply
	} else {
		to, err := g.store.GetSession(protocol.SessionGetRequest{Session: p.To, Caller: sid})
		if err != nil {
			return nil, err
		}
		recipient = to.Session.ID
		if p.Type == "" {
			p.Type = protocol.TypeRequest
		}
		if p.Type != protocol.TypeRequest && p.Type != protocol.TypeNotification {
			return nil, fmt.Errorf("type must be request or notification")
		}
	}
	target, ok, err := g.store.Agent(recipient)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, fmt.Errorf("unknown recipient session %q", recipient)
	}
	if target.Worker != nil && !target.Online {
		return nil, fmt.Errorf("worker %s has ended; a bot cannot start it again", recipient)
	}
	msg, created, err := g.store.StoreMessage(protocol.MsgSend{Sender: sid, Recipient: recipient, Type: p.Type, Body: p.Body, ParentID: p.Message, ReplyOutcome: p.ReplyOutcome})
	if err != nil {
		return nil, err
	}
	if created && msg.State != protocol.StateHeld {
		g.dispatch(ctx, msg)
	}
	if updated, ok, err := g.store.Message(msg.ID); err == nil && ok {
		msg = updated
	}
	name := g.store.displayName(recipient)
	if name == "" {
		name = recipient
	}
	receipt := botReceipt{msg.ID, botRef{recipient, name}, msg.State}
	if tool == "agent_message_reply" {
		return receipt, nil
	}
	return map[string]any{"sent": []botReceipt{receipt}}, nil
}

func (g *Gateway) botGraph(sid string, in map[string]any) (any, error) {
	var p struct {
		Node   string `json:"node"`
		Limit  int    `json:"limit"`
		Cursor string `json:"cursor"`
	}
	if err := botDecode(in, &p); err != nil {
		return nil, err
	}
	graph, err := g.store.GraphFor(p.Node)
	if err != nil {
		return nil, err
	}
	// Project the complete graph through the same public view as local agents.
	all := protocol.PublicGraph(graph, protocol.PublicGraphQuery{Node: p.Node, Limit: protocol.PublicGraphMaxLimit})
	if p.Node == "" {
		for all.Next > 0 {
			page := protocol.PublicGraph(graph, protocol.PublicGraphQuery{Offset: all.Next, Limit: protocol.PublicGraphMaxLimit})
			all.Nodes = append(all.Nodes, page.Nodes...)
			all.Next = page.Next
		}
	}
	paging, err := newReadPaging(sid, p.Cursor, readQueryHash(struct{ Node string }{p.Node}), p.Limit)
	if err != nil {
		return nil, err
	}
	start, end, next := paging.page(len(all.Nodes))
	nodes := make([]map[string]any, 0, end-start)
	for _, n := range all.Nodes[start:end] {
		purpose, maturity := "", n.Maturity
		rules := map[string]string{}
		for _, r := range n.Rules {
			rules[r.Name] = r.Value
		}
		row := map[string]any{"name": n.Name, "kind": n.Kind, "maturity": maturity, "purpose": purpose, "rules": rules}
		if n.Charter != nil {
			c := n.Charter
			row["purpose"] = c.Purpose
			if c.Maturity != "" {
				row["maturity"] = c.Maturity
			}
			row["charter"] = protocol.ReadCharter{Purpose: c.Purpose, Accepts: charterList(c.Accepts), Refuses: charterList(c.Refuses), FeatureRequests: c.FeatureRequests}
		}
		nodes = append(nodes, row)
	}
	edges := make([]map[string]any, 0, len(all.Edges))
	for _, e := range all.Edges {
		edges = append(edges, map[string]any{"from": e.From, "to": e.To, "kind": e.Kind, "mode": e.Mode, "rule": e.Rule})
	}
	out := map[string]any{"nodes": nodes, "edges": edges}
	if next != "" {
		out["next_cursor"] = next
		out["note"] = pageNote(end-start, len(all.Nodes), next)
	}
	return out, nil
}

func botCut(s string, max int) (string, bool) {
	if len(s) <= max {
		return s, false
	}
	end := max
	for end > 0 && !utf8.RuneStart(s[end]) {
		end--
	}
	return s[:end], true
}
func (g *Gateway) botMessageRow(m protocol.Message) map[string]any {
	from := m.FromName
	if from == "" {
		from = m.From
	}
	to := g.store.displayName(m.To)
	if to == "" {
		to = m.To
	}
	line := strings.SplitN(m.Body, "\n", 2)[0]
	text, truncated := botCut(strings.TrimRight(line, "\r"), 200)
	truncated = truncated || len(line) < len(m.Body)
	return map[string]any{"id": m.ID, "conversation": m.ConversationID, "from": botRef{m.From, from}, "to": botRef{m.To, to}, "type": m.Type, "state": m.State, "time": m.CreatedAt.UTC().Format(time.RFC3339), "text": text, "truncated": truncated}
}
func (g *Gateway) botMarkRead(sid, id string) error {
	_, err := g.store.db.Exec(`UPDATE messages SET state='delivered' WHERE id=? AND recipient=? AND state='stored'`, id, sid)
	return err
}
func (g *Gateway) botMessages(sid string, in map[string]any) (any, error) {
	var p struct {
		Conversation string `json:"conversation"`
		With         string `json:"with"`
		Direction    string `json:"direction"`
		Since        string `json:"since"`
		Detail       string `json:"detail"`
		Limit        int    `json:"limit"`
		Cursor       string `json:"cursor"`
	}
	if err := botDecode(in, &p); err != nil {
		return nil, err
	}
	var since time.Time
	var err error
	if p.Since != "" {
		since, err = time.Parse(time.RFC3339, p.Since)
		if err != nil {
			return nil, err
		}
	}
	query := readQueryHash(struct{ Conversation, With, Direction, Since, Detail string }{p.Conversation, p.With, p.Direction, p.Since, p.Detail})
	paging, err := newReadPaging(sid, p.Cursor, query, p.Limit)
	if err != nil {
		return nil, err
	}
	var matched []protocol.Message
	for offset := 0; ; offset += 200 {
		batch, total, err := g.store.MessagesPageForBot(sid, p.Conversation, offset)
		if err != nil {
			return nil, err
		}
		for _, m := range batch {
			if p.Direction == "in" && m.To != sid || p.Direction == "out" && m.From != sid || !since.IsZero() && m.CreatedAt.Before(since) {
				continue
			}
			if p.With != "" && !equalFoldAny(p.With, m.From, m.To, m.FromName, g.store.displayName(m.To)) {
				continue
			}
			matched = append(matched, m)
		}
		if offset+len(batch) >= total || len(batch) == 0 {
			break
		}
	}
	start, end, next := paging.page(len(matched))
	rows := make([]map[string]any, 0, end-start)
	for _, m := range matched[start:end] {
		rows = append(rows, g.botMessageRow(m))
		if m.To == sid {
			if err := g.botMarkRead(sid, m.ID); err != nil {
				return nil, err
			}
		}
	}
	out := map[string]any{"items": rows, "total": len(matched)}
	if next != "" {
		out["next_cursor"] = next
		out["note"] = pageNote(end-start, len(matched), next)
	}
	return out, nil
}
func (s *Store) MessagesPageForBot(sid, conv string, offset int) ([]protocol.Message, int, error) {
	node, err := s.SessionNode(sid)
	if err != nil {
		return nil, 0, err
	}
	return s.MessagesPage(node, protocol.QueryMessages{SessionID: sid, ConversationID: conv, Limit: 200, Offset: offset})
}
func (g *Gateway) botMessageGet(sid string, in map[string]any) (any, error) {
	var p struct {
		Message    string `json:"message"`
		BodyCursor string `json:"body_cursor"`
		MaxBytes   int    `json:"max_bytes"`
	}
	if err := botDecode(in, &p); err != nil {
		return nil, err
	}
	m, ok, err := g.store.Message(p.Message)
	if err != nil {
		return nil, err
	}
	if !ok || m.From != sid && m.To != sid {
		return nil, fmt.Errorf("unknown message %q", p.Message)
	}
	if p.MaxBytes == 0 {
		p.MaxBytes = 8192
	}
	query := readQueryHash(struct {
		ID, Version string
		MaxBytes    int
	}{m.ID, m.CreatedAt.UTC().Format(time.RFC3339Nano), p.MaxBytes})
	paging, err := newReadPaging(sid, p.BodyCursor, query, p.MaxBytes)
	if err != nil {
		return nil, err
	}
	// Body offsets are bytes, and can exceed the normal list page limit.
	offset := paging.Offset
	if offset > len(m.Body) || offset < len(m.Body) && !utf8.RuneStart(m.Body[offset]) {
		return nil, fmt.Errorf("invalid body cursor")
	}
	chunk, truncated := botCut(m.Body[offset:], p.MaxBytes)
	row := g.botMessageRow(m)
	row["body"] = chunk
	row["outcome"] = ""
	row["reason"] = ""
	var outcome, reason string
	err = g.store.db.QueryRow(`SELECT outcome,reason FROM graph_outcomes WHERE message_id=? OR reply_id=? ORDER BY at DESC LIMIT 1`, m.ID, m.ID).Scan(&outcome, &reason)
	if err != nil && err != sql.ErrNoRows {
		return nil, err
	}
	if err == nil {
		row["outcome"] = outcome
		row["reason"] = reason
	}
	if m.To == sid {
		if err := g.botMarkRead(sid, m.ID); err != nil {
			return nil, err
		}
	}
	out := map[string]any{"message": row, "body_truncated": truncated}
	if truncated {
		out["next_body_cursor"] = encodeReadCursor(readCursor{V: 1, Caller: sid, Query: paging.Query, Offset: offset + len(chunk)})
	}
	return out, nil
}
