package mcpserver

// The CTO server also serves the shared catalog tools tagged for the CTO role.
// They go through the CTO's daemon channel: reads are ctoRead, send, reply,
// owner_ask and learn are ctoTalk. The output schema is not advertised; each
// structured result is validated against it before it is returned.

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/niparis/agent-net/internal/toolcatalog"
)

func addCTOSharedTools(s *mcp.Server, call Caller, addTool ctoToolAdder) {
	for _, entry := range toolcatalog.ForRole(toolcatalog.RoleCTO) {
		if ctoV2Tools[entry.Name] {
			continue
		}
		input := entry.InputSchema
		if len(entry.CTOInputSchema) > 0 {
			input = entry.CTOInputSchema
		}
		var inputSchema any
		if err := json.Unmarshal(input, &inputSchema); err != nil {
			panic("mcpserver: " + entry.Name + " input schema: " + err.Error())
		}
		tool := &mcp.Tool{Name: entry.Name, Description: entry.Description, InputSchema: inputSchema}
		if entry.AlwaysLoad {
			tool.Meta = mcp.Meta{"anthropic/alwaysLoad": true}
		}
		name := entry.Name
		resolved, err := resolveCTOCatalogSchema(input)
		if err != nil {
			panic("mcpserver: " + name + " input schema: " + err.Error())
		}
		addTool(tool, func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			args, err := applyCTOCatalogInput(name, resolved, req)
			if err != nil {
				return toolError(err)
			}
			raw, err := call(ctx, name, args)
			if err != nil {
				return toolError(err)
			}
			var value any
			if err := json.Unmarshal(raw, &value); err != nil {
				return toolError(fmt.Errorf("%s: unreadable daemon answer: %w", name, err))
			}
			return catalogResult(name, value)
		})
	}
}

func resolveCTOCatalogSchema(raw json.RawMessage) (*jsonschema.Resolved, error) {
	var schema jsonschema.Schema
	if err := json.Unmarshal(raw, &schema); err != nil {
		return nil, err
	}
	return schema.Resolve(nil)
}

func applyCTOCatalogInput(name string, resolved *jsonschema.Resolved, req *mcp.CallToolRequest) (json.RawMessage, error) {
	var value any = map[string]any{}
	raw := json.RawMessage(nil)
	if req != nil && req.Params != nil {
		raw = req.Params.Arguments
	}
	if len(raw) > 0 && string(raw) != "null" {
		if err := json.Unmarshal(raw, &value); err != nil {
			return nil, fmt.Errorf("%s arguments are not a JSON object: %v", name, err)
		}
	}
	if err := resolved.ApplyDefaults(&value); err != nil {
		return nil, err
	}
	if err := resolved.Validate(&value); err != nil {
		return nil, fmt.Errorf("%v. Example: %s", err, catalogExample(name))
	}
	return json.Marshal(value)
}
