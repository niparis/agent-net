package mcpserver

// The message read tools: agent_messages_find and agent_message_get (tool
// redesign task 2b).

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/niparis/agent-net/internal/protocol"
)

type roMessagesFindIn struct {
	Conversation string `json:"conversation"`
	With         string `json:"with"`
	Direction    string `json:"direction"`
	Since        string `json:"since"`
	Limit        int    `json:"limit"`
	Cursor       string `json:"cursor"`
	Detail       string `json:"detail"`
}

type roMessageRow struct {
	ID           string           `json:"id"`
	Conversation string           `json:"conversation"`
	From         roSessionRefType `json:"from"`
	To           roSessionRefType `json:"to"`
	Type         string           `json:"type"`
	State        string           `json:"state"`
	Time         string           `json:"time"`
	Ago          string           `json:"ago,omitempty"`
	Text         string           `json:"text"`
	Truncated    bool             `json:"truncated"`
	Outcome      string           `json:"outcome,omitempty"`
	Reason       string           `json:"reason,omitempty"`
	key          string
}

type roMessagesFindOut struct {
	Items      []roMessageRow `json:"items"`
	Total      int            `json:"total"`
	NextCursor string         `json:"next_cursor,omitempty"`
	Note       string         `json:"note,omitempty"`
}

// roMessageDetail is the message_get row. The output schema requires the
// outcome and reason fields even when they are empty.
type roMessageDetail struct {
	ID           string           `json:"id"`
	Conversation string           `json:"conversation"`
	From         roSessionRefType `json:"from"`
	To           roSessionRefType `json:"to"`
	Type         string           `json:"type"`
	State        string           `json:"state"`
	Time         string           `json:"time"`
	Ago          string           `json:"ago,omitempty"`
	Text         string           `json:"text"`
	Truncated    bool             `json:"truncated"`
	Body         string           `json:"body"`
	Outcome      string           `json:"outcome"`
	Reason       string           `json:"reason"`
}

type roMessageGetIn struct {
	Message    string `json:"message"`
	BodyCursor string `json:"body_cursor"`
	MaxBytes   int    `json:"max_bytes"`
}

type roMessageGetOut struct {
	Message        roMessageDetail `json:"message"`
	NextBodyCursor string          `json:"next_body_cursor,omitempty"`
	BodyTruncated  bool            `json:"body_truncated"`
}

func addMessageReadTools(s *mcp.Server, call Caller) {
	roAdd(s, "agent_messages_find", func(ctx context.Context, _ *mcp.CallToolRequest, in roMessagesFindIn) (*mcp.CallToolResult, roMessagesFindOut, error) {
		out, err := roFindMessages(ctx, call, in)
		return nil, out, err
	})
	roAdd(s, "agent_message_get", func(ctx context.Context, _ *mcp.CallToolRequest, in roMessageGetIn) (*mcp.CallToolResult, roMessageGetOut, error) {
		out, err := roGetMessage(ctx, call, in)
		return nil, out, err
	})
}

func roFindMessages(ctx context.Context, call Caller, in roMessagesFindIn) (roMessagesFindOut, error) {
	direction := in.Direction
	if direction == "" {
		direction = "any"
	}
	switch direction {
	case "in", "out", "any":
	default:
		return roMessagesFindOut{}, fmt.Errorf("direction must be in, out or any, not %q. Example: {\"direction\":\"in\"}", in.Direction)
	}
	if _, err := roDetail(in.Detail); err != nil {
		return roMessagesFindOut{}, err
	}
	limit, err := roLimit(in.Limit)
	if err != nil {
		return roMessagesFindOut{}, err
	}
	var since time.Time
	if in.Since != "" {
		since, err = time.Parse(time.RFC3339, in.Since)
		if err != nil {
			return roMessagesFindOut{}, fmt.Errorf("since must be an RFC 3339 time, not %q. Example: {\"since\":\"2026-01-01T12:00:00Z\"}", in.Since)
		}
	}
	caller, err := roWhoAmI(ctx, call)
	if err != nil {
		return roMessagesFindOut{}, err
	}
	filters := roFilterKey(struct {
		Conversation, With, Direction, Since, Detail string
	}{in.Conversation, in.With, direction, in.Since, in.Detail})
	after, err := roDecodeCursor(in.Cursor, "agent_messages_find", caller.SessionID, filters)
	if err != nil {
		return roMessagesFindOut{}, err
	}
	messages, err := roAllMessages(ctx, call, in.Conversation)
	if err != nil {
		return roMessagesFindOut{}, err
	}
	// Names make the rows readable. A failure to list agents is not fatal:
	// the sender's own name still comes with the message.
	agents, _ := roAgentList(ctx, call)
	names := roNames(agents, caller.Sessions)

	rows := make([]roMessageRow, 0, len(messages))
	for _, message := range messages {
		if direction == "in" && message.To != caller.SessionID || direction == "out" && message.From != caller.SessionID {
			continue
		}
		if in.With != "" && !roMessageWith(message, in.With, names) {
			continue
		}
		if !since.IsZero() && message.CreatedAt.Before(since) {
			continue
		}
		text, truncated := roPreview(message.Body, 200)
		rows = append(rows, roMessageRow{
			ID: message.ID, Conversation: message.ConversationID,
			From: roSessionRef(message.From, roName(names, message.From, message.FromName)),
			To:   roSessionRef(message.To, roName(names, message.To, "")),
			Type: message.Type, State: message.State,
			Time: roTimeText(message.CreatedAt), Ago: roAgoText(message.CreatedAt),
			Text: text, Truncated: truncated,
			key: fmt.Sprintf("%020d|%s", message.CreatedAt.UnixNano(), message.ID),
		})
	}
	sort.SliceStable(rows, func(i, j int) bool {
		return roMessageKey(rows[i]) > roMessageKey(rows[j])
	})
	page, next := roWindow(rows, limit, after, true, roMessageKey)
	out := roMessagesFindOut{Items: page, Total: len(rows)}
	if next != "" {
		out.NextCursor = roEncodeCursor(roCursor{Tool: "agent_messages_find", Caller: caller.SessionID, Filters: filters, After: next})
		out.Note = roPageNote("messages")
	}
	return out, nil
}

func roMessageKey(row roMessageRow) string { return row.key }

func roMessageWith(message protocol.Message, wanted string, names map[string]string) bool {
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

// roAllMessages reads this session's messages a gateway page at a time, so the
// filters can be applied to the whole snapshot.
func roAllMessages(ctx context.Context, call Caller, conversation string) ([]protocol.Message, error) {
	var out []protocol.Message
	seen := map[string]bool{}
	offset := 0
	for page := 0; page < 200; page++ {
		raw, err := call(ctx, "messages", map[string]any{"conversation_id": conversation, "limit": 200, "offset": offset})
		if err != nil {
			return nil, err
		}
		var result struct {
			Messages []protocol.Message `json:"messages"`
			Total    int                `json:"total"`
		}
		if err := json.Unmarshal(raw, &result); err != nil {
			return nil, err
		}
		added := 0
		for _, message := range result.Messages {
			if seen[message.ID] {
				continue
			}
			seen[message.ID] = true
			out = append(out, message)
			added++
		}
		if len(result.Messages) == 0 || added == 0 || len(out) >= result.Total || offset+len(result.Messages) >= result.Total {
			break
		}
		offset += len(result.Messages)
		if len(out) >= roMaxMessages {
			break
		}
	}
	return out, nil
}

func roGetMessage(ctx context.Context, call Caller, in roMessageGetIn) (roMessageGetOut, error) {
	if strings.TrimSpace(in.Message) == "" {
		return roMessageGetOut{}, fmt.Errorf("message is required. Example: {\"message\":\"m_1234\"}")
	}
	maxBytes := in.MaxBytes
	if maxBytes == 0 {
		maxBytes = 8192
	}
	if maxBytes < 256 || maxBytes > 16384 {
		return roMessageGetOut{}, fmt.Errorf("max_bytes must be between 256 and 16384. Example: {\"max_bytes\":8192}")
	}
	caller, err := roWhoAmI(ctx, call)
	if err != nil {
		return roMessageGetOut{}, err
	}
	raw, err := call(ctx, "message_status", map[string]any{"message_id": in.Message})
	if err != nil {
		return roMessageGetOut{}, err
	}
	var result protocol.MessageResult
	if err := json.Unmarshal(raw, &result); err != nil {
		return roMessageGetOut{}, err
	}
	message := result.Message
	if message.ID == "" {
		return roMessageGetOut{}, fmt.Errorf("unknown message %q", in.Message)
	}
	version := message.CreatedAt.UTC().Format(time.RFC3339Nano)
	filters := "message=" + message.ID + ";version=" + version
	after, err := roDecodeCursor(in.BodyCursor, "agent_message_get", caller.SessionID, filters)
	if err != nil {
		return roMessageGetOut{}, err
	}
	offset := 0
	if after != "" {
		offset, err = strconv.Atoi(after)
		if err != nil || offset < 0 || offset > len(message.Body) {
			return roMessageGetOut{}, fmt.Errorf("the body cursor is stale or unknown for message %q: call again without body_cursor", message.ID)
		}
	}
	chunk, next := roChunk(message.Body, offset, maxBytes)
	text, truncated := roPreview(message.Body, 200)
	names := roNames(nil, caller.Sessions)
	out := roMessageGetOut{
		Message: roMessageDetail{
			ID: message.ID, Conversation: message.ConversationID,
			From: roSessionRef(message.From, roName(names, message.From, message.FromName)),
			To:   roSessionRef(message.To, roName(names, message.To, "")),
			Type: message.Type, State: message.State,
			Time: roTimeText(message.CreatedAt), Ago: roAgoText(message.CreatedAt),
			Text: text, Truncated: truncated, Body: chunk,
		},
		BodyTruncated: next < len(message.Body),
	}
	if next < len(message.Body) {
		out.NextBodyCursor = roEncodeCursor(roCursor{Tool: "agent_message_get", Caller: caller.SessionID, Filters: filters, After: strconv.Itoa(next)})
	}
	return out, nil
}
