package mcpserver

import (
	"context"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/niparis/agent-net/internal/protocol"
)

type profileAddIn struct {
	Name     string   `json:"name,omitempty" jsonschema:"Optional profile name. Reuse an existing name to replace its model or effort; the harness and resolved model form the default name."`
	Harness  string   `json:"harness" jsonschema:"Installed worker harness: opencode, pi, codex or claude."`
	Model    string   `json:"model" jsonschema:"Model ID or loose model name to resolve against the live harness model list."`
	Effort   string   `json:"effort,omitempty" jsonschema:"Optional model reasoning or thinking effort."`
	Machines []string `json:"machines,omitempty" jsonschema:"Optional machines the profile is for. Empty checks every online machine with the harness."`
	Test     bool     `json:"test,omitempty" jsonschema:"Run a short real start to test the model before saving. False only checks the live model list."`
}
type profileRemoveIn struct {
	Name string `json:"name" jsonschema:"Name of the registered worker profile to remove."`
}

func addWorkerProfileTools(s *mcp.Server, run func(context.Context, string, any) (any, error)) {
	mcp.AddTool(s, &mcp.Tool{Name: "agent_worker_profile_add", Description: "Register a worker harness and model when the owner asks. Reusing a profile name replaces its model or effort. The model is checked against the harness's live model list on each chosen machine; workers cannot use this tool."}, func(ctx context.Context, _ *mcp.CallToolRequest, in profileAddIn) (*mcp.CallToolResult, any, error) {
		out, err := run(ctx, protocol.OpWorkerProfileAdd, protocol.WorkerProfileRequest{Name: in.Name, Harness: in.Harness, Model: in.Model, Effort: in.Effort, Machines: in.Machines, Test: in.Test})
		return nil, out, err
	})
	mcp.AddTool(s, &mcp.Tool{Name: "agent_worker_profile_remove", Description: "Remove a registered worker profile when the owner asks. Workers cannot use this tool."}, func(ctx context.Context, _ *mcp.CallToolRequest, in profileRemoveIn) (*mcp.CallToolResult, any, error) {
		out, err := run(ctx, protocol.OpWorkerProfileRemove, protocol.WorkerProfileRequest{Name: in.Name})
		return nil, out, err
	})
}
