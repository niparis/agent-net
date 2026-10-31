package mcpserver

import (
	"context"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/niparis/agent-net/internal/protocol"
)

type profileAddIn struct {
	Name    string `json:"name,omitempty" jsonschema:"Optional profile name. Reuse an existing name to replace its model or effort; the harness and resolved model form the default name."`
	Harness string `json:"harness" jsonschema:"Installed worker harness: opencode, pi, codex or claude."`
	Model   string `json:"model" jsonschema:"Model ID or loose model name to resolve against installed harness model lists."`
	Effort  string `json:"effort,omitempty" jsonschema:"Optional model reasoning or thinking effort."`
}
type profileRemoveIn struct {
	Name string `json:"name" jsonschema:"Name of the registered worker profile to remove."`
}

func addWorkerProfileTools(s *mcp.Server, run func(context.Context, string, any) (any, error)) {
	mcp.AddTool(s, &mcp.Tool{Name: "agent_worker_profiles", Description: "List the network's registered worker profiles. Workers cannot use this tool."}, func(ctx context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, any, error) {
		out, err := run(ctx, protocol.OpWorkerProfiles, struct{}{})
		return nil, out, err
	})
	mcp.AddTool(s, &mcp.Tool{Name: "agent_worker_profile_add", Description: "Register a worker harness and model when the owner asks. Reusing a profile name replaces its model or effort. The model must be listed by an installed harness; workers cannot use this tool."}, func(ctx context.Context, _ *mcp.CallToolRequest, in profileAddIn) (*mcp.CallToolResult, any, error) {
		out, err := run(ctx, protocol.OpWorkerProfileAdd, protocol.WorkerProfileRequest{Name: in.Name, Harness: in.Harness, Model: in.Model, Effort: in.Effort})
		return nil, out, err
	})
	mcp.AddTool(s, &mcp.Tool{Name: "agent_worker_profile_remove", Description: "Remove a registered worker profile when the owner asks. Workers cannot use this tool."}, func(ctx context.Context, _ *mcp.CallToolRequest, in profileRemoveIn) (*mcp.CallToolResult, any, error) {
		out, err := run(ctx, protocol.OpWorkerProfileRemove, protocol.WorkerProfileRequest{Name: in.Name})
		return nil, out, err
	})
}
