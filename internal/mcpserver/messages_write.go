package mcpserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/niparis/agent-net/internal/protocol"
	"github.com/niparis/agent-net/internal/toolcatalog"
)

// The message write tools use the catalog's own input and output schemas. The
// SDK's low-level AddTool path is used so the advertised schema is exactly the
// catalog's, including its $defs, and this file validates both directions.

type messageSetIn struct {
	Team       string   `json:"team,omitempty"`
	Machine    string   `json:"machine,omitempty"`
	Project    string   `json:"project,omitempty"`
	Harness    string   `json:"harness,omitempty"`
	Kind       []string `json:"kind,omitempty"`
	OnlineOnly *bool    `json:"online_only,omitempty"`
}

type messageSendIn struct {
	To      string        `json:"to,omitempty"`
	ToSet   *messageSetIn `json:"to_set,omitempty"`
	Body    string        `json:"body"`
	Type    string        `json:"type,omitempty"`
	AsOwner bool          `json:"as_owner,omitempty"`
	Order   bool          `json:"order,omitempty"`
}

type messageReplyIn struct {
	Message        string `json:"message"`
	Body           string `json:"body"`
	Ack            bool   `json:"ack,omitempty"`
	Outcome        string `json:"outcome,omitempty"`
	Reason         string `json:"reason,omitempty"`
	RedirectTo     string `json:"redirect_to,omitempty"`
	Recommendation string `json:"recommendation,omitempty"`
}

// addMessageWriteTools registers agent_message_send and agent_message_reply.
func addMessageWriteTools(s *mcp.Server, run func(context.Context, string, any) (any, error)) {
	addCatalogTool(s, "agent_file_send", func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		var in protocol.FileSendRequest
		if err := decodeCatalogInput("agent_file_send", req, &in); err != nil {
			return toolError(err)
		}
		out, err := run(ctx, "file_send", in)
		if err != nil {
			return toolError(err)
		}
		return catalogResult("agent_file_send", out)
	})

	addCatalogTool(s, "agent_message_send", func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		var in messageSendIn
		if err := decodeCatalogInput("agent_message_send", req, &in); err != nil {
			return toolError(err)
		}
		if err := checkSendSelectors(in); err != nil {
			return toolError(err)
		}
		if in.Type == "" {
			in.Type = protocol.TypeRequest
		}
		var set map[string]any
		if in.ToSet != nil {
			set = map[string]any{}
			if in.ToSet.Team != "" {
				set["team"] = in.ToSet.Team
			}
			if in.ToSet.Machine != "" {
				set["machine"] = in.ToSet.Machine
			}
			if in.ToSet.Project != "" {
				set["project"] = in.ToSet.Project
			}
			if in.ToSet.Harness != "" {
				set["harness"] = in.ToSet.Harness
			}
			kind := in.ToSet.Kind
			if len(kind) == 0 {
				kind = []string{"agent"}
			}
			set["kind"] = kind
			online := true
			if in.ToSet.OnlineOnly != nil {
				online = *in.ToSet.OnlineOnly
			}
			set["online_only"] = online
		}
		out, err := sendCall(ctx, run, in.To, set, in.Body, in.Type, in.AsOwner, in.Order, true)
		if err != nil {
			return toolError(err)
		}
		return catalogResult("agent_message_send", out)
	})

	addCatalogTool(s, "agent_message_reply", func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		var in messageReplyIn
		if err := decodeCatalogInput("agent_message_reply", req, &in); err != nil {
			return toolError(err)
		}
		outcome, err := protocol.CleanOutcome(protocol.ReplyOutcome{
			Outcome:        in.Outcome,
			Reason:         in.Reason,
			RedirectTo:     in.RedirectTo,
			Recommendation: in.Recommendation,
		})
		if err != nil {
			return toolError(fmt.Errorf("%v. accept needs no extra fields; reject and escalate need reason; redirect needs reason and redirect_to; escalate needs recommendation", err))
		}
		if in.Ack && outcome.Outcome != "" {
			return toolError(errors.New("ack cannot be combined with outcome; ack closes the request without a reply"))
		}
		if strings.TrimSpace(in.Body) == "" && !in.Ack {
			return toolError(errors.New(`body is empty; pass a body, or set ack: true to close a request that needs no answer`))
		}
		out, err := replyCall(ctx, run, in, outcome, true)
		if err != nil {
			return toolError(err)
		}
		return catalogResult("agent_message_reply", out)
	})

	addCatalogTool(s, "agent_message_handoff", func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		var in messageHandoffIn
		if err := decodeCatalogInput("agent_message_handoff", req, &in); err != nil {
			return toolError(err)
		}
		out, err := opResult(run(ctx, "handoff", map[string]any{
			"message_id":      in.MessageID,
			"to_session":      in.ToSession,
			"note":            in.Note,
			"idempotency_key": protocol.NewID("k"),
		}))
		if err != nil {
			return toolError(err)
		}
		return catalogResult("agent_message_handoff", out)
	})
}

// messageHandoffIn is the input of agent_message_handoff.
type messageHandoffIn struct {
	MessageID string `json:"message_id"`
	ToSession string `json:"to_session"`
	Note      string `json:"note,omitempty"`
}

func checkSendSelectors(in messageSendIn) error {
	if in.To != "" && in.ToSet != nil {
		return errors.New(`choose exactly one of to and to_set; example: {"to":"s01","body":"Please review."}`)
	}
	if in.To == "" && in.ToSet == nil {
		return errors.New(`choose exactly one of to and to_set; example: {"to":"s01","body":"Please review."}`)
	}
	if in.ToSet != nil && in.ToSet.Team == "" && in.ToSet.Machine == "" && in.ToSet.Project == "" && in.ToSet.Harness == "" {
		return errors.New(`to_set needs a nonempty team, machine, project or harness filter; example: {"to_set":{"harness":"claude"},"body":"Please review."}`)
	}
	return nil
}

// addCatalogTool registers one catalog tool with the catalog's exact name,
// description and schemas, and an input validator built from that schema.
func addCatalogTool(s *mcp.Server, name string, h mcp.ToolHandler) {
	tool := catalogTool(name)
	if _, err := resolveCatalogSchema(tool.InputSchema); err != nil {
		panic("mcpserver: " + name + " input schema: " + err.Error())
	}
	annotations := &mcp.ToolAnnotations{
		ReadOnlyHint:   tool.Hints.ReadOnly,
		IdempotentHint: tool.Hints.Idempotent,
	}
	open := tool.Hints.OpenWorld
	annotations.OpenWorldHint = &open
	if tool.Hints.Destructive {
		destructive := true
		annotations.DestructiveHint = &destructive
	}
	s.AddTool(&mcp.Tool{
		Name:        tool.Name,
		Description: tool.Description,
		InputSchema: tool.InputSchema,
		Annotations: annotations,
	}, h)
}

func catalogTool(name string) toolcatalog.Tool {
	for _, tool := range toolcatalog.Tools {
		if tool.Name == name {
			return tool
		}
	}
	panic("mcpserver: no catalog tool " + name)
}

func resolveCatalogSchema(raw json.RawMessage) (*jsonschema.Resolved, error) {
	var schema jsonschema.Schema
	if err := json.Unmarshal(raw, &schema); err != nil {
		return nil, err
	}
	return schema.Resolve(nil)
}

// decodeCatalogInput validates the raw arguments against the catalog input
// schema, then decodes them into dst. Defaults are applied by the handlers.
func decodeCatalogInput(name string, req *mcp.CallToolRequest, dst any) error {
	var value any = map[string]any{}
	raw := json.RawMessage(nil)
	if req != nil && req.Params != nil {
		raw = req.Params.Arguments
	}
	if len(raw) > 0 && string(raw) != "null" {
		if err := json.Unmarshal(raw, &value); err != nil {
			return fmt.Errorf("arguments are not a JSON object: %v", err)
		}
	}
	schema, err := resolveCatalogSchema(catalogTool(name).InputSchema)
	if err != nil {
		return err
	}
	if err := schema.Validate(value); err != nil {
		return fmt.Errorf("%v. Example: %s", err, catalogExample(name))
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(encoded, dst); err != nil {
		return err
	}
	return nil
}

func catalogExample(name string) string {
	switch name {
	case "agent_file_send":
		return `{"to":"s01","paths":["/tmp/report.txt"],"note":"Please read."}`
	case "agent_message_send":
		return `{"to":"s01","body":"Please review."}`
	case "agent_message_reply":
		return `{"message":"m01","body":"Accepted.","outcome":"accept"}`
	}
	return "{}"
}

// catalogResult prunes newer fields, then validates against the catalog output schema
// and returns it with the same JSON as one text block.
func catalogResult(name string, value any) (*mcp.CallToolResult, error) {
	resolved, err := resolveCatalogSchema(catalogTool(name).OutputSchema)
	if err != nil {
		return toolError(fmt.Errorf("output schema: %v", err))
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return toolError(err)
	}
	encoded, err = toolcatalog.PruneOutput(catalogTool(name).OutputSchema, encoded)
	if err != nil {
		return toolError(err)
	}
	var normalized any
	if err := json.Unmarshal(encoded, &normalized); err != nil {
		return toolError(err)
	}
	if err := resolved.Validate(normalized); err != nil {
		return toolError(fmt.Errorf("%s output does not match the catalog schema: %v", name, err))
	}
	// Keep arbitrary JSON setting values exact on the wire, including integers
	// larger than float64 can represent. Validation used the decoded copy.
	if name == "agent_settings_read" {
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: string(encoded)}}, StructuredContent: json.RawMessage(encoded)}, nil
	}
	return &mcp.CallToolResult{
		Content:           []mcp.Content{&mcp.TextContent{Text: string(encoded)}},
		StructuredContent: normalized,
	}, nil
}

func toolError(err error) (*mcp.CallToolResult, error) {
	return &mcp.CallToolResult{
		IsError: true,
		Content: []mcp.Content{&mcp.TextContent{Text: err.Error()}},
	}, nil
}
