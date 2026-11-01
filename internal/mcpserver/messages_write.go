package mcpserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/niparis/agent-net/internal/protocol"
	"github.com/niparis/agent-net/internal/toolcatalog"
)

// The message write tools use the catalog's own input and output schemas. The
// SDK's low-level AddTool path is used so the advertised schema is exactly the
// catalog's, including its $defs, and this file validates both directions.

type messageSetIn struct {
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
}

type messageReplyIn struct {
	Message        string `json:"message"`
	Body           string `json:"body"`
	Outcome        string `json:"outcome,omitempty"`
	Reason         string `json:"reason,omitempty"`
	RedirectTo     string `json:"redirect_to,omitempty"`
	Recommendation string `json:"recommendation,omitempty"`
}

// addMessageWriteTools registers agent_message_send and agent_message_reply.
func addMessageWriteTools(s *mcp.Server, run func(context.Context, string, any) (any, error)) {
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
		params := map[string]any{
			"body":            in.Body,
			"type":            in.Type,
			"as_owner":        in.AsOwner,
			"receipt":         true,
			"idempotency_key": protocol.NewID("k"),
		}
		if in.ToSet != nil {
			set := map[string]any{}
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
			params["to_set"] = set
		} else {
			params["to"] = in.To
		}
		out, err := run(ctx, "send", params)
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
		out, err := run(ctx, "reply", map[string]any{
			"message_id":      in.Message,
			"body":            in.Body,
			"outcome":         outcome.Outcome,
			"reason":          outcome.Reason,
			"redirect_to":     outcome.RedirectTo,
			"recommendation":  outcome.Recommendation,
			"receipt":         true,
			"idempotency_key": protocol.NewID("k"),
		})
		if err != nil {
			return toolError(err)
		}
		return catalogResult("agent_message_reply", out)
	})
}

func checkSendSelectors(in messageSendIn) error {
	if in.To != "" && in.ToSet != nil {
		return errors.New(`choose exactly one of to and to_set; example: {"to":"s01","body":"Please review."}`)
	}
	if in.To == "" && in.ToSet == nil {
		return errors.New(`choose exactly one of to and to_set; example: {"to":"s01","body":"Please review."}`)
	}
	if in.ToSet != nil && in.ToSet.Machine == "" && in.ToSet.Project == "" && in.ToSet.Harness == "" {
		return errors.New(`to_set needs a nonempty machine, project or harness filter; example: {"to_set":{"harness":"claude"},"body":"Please review."}`)
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
	case "agent_message_send":
		return `{"to":"s01","body":"Please review."}`
	case "agent_message_reply":
		return `{"message":"m01","body":"Accepted.","outcome":"accept"}`
	}
	return "{}"
}

// catalogResult validates a structured value against the catalog output schema
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
	var normalized any
	if err := json.Unmarshal(encoded, &normalized); err != nil {
		return toolError(err)
	}
	if err := resolved.Validate(normalized); err != nil {
		return toolError(fmt.Errorf("%s output does not match the catalog schema: %v", name, err))
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
