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
	addCatalogTool(s, "agent_worker_profile_add", func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		var in profileAddIn
		if err := decodeCatalogInput("agent_worker_profile_add", req, &in); err != nil {
			return toolError(err)
		}
		out, err := run(ctx, protocol.OpWorkerProfileAdd, protocol.WorkerProfileRequest{
			Name:     in.Name,
			Harness:  in.Harness,
			Model:    in.Model,
			Effort:   in.Effort,
			Machines: in.Machines,
			Test:     in.Test,
		})
		if err != nil {
			return toolError(err)
		}
		return catalogResult("agent_worker_profile_add", out)
	})
	addCatalogTool(s, "agent_worker_profile_remove", func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		var in profileRemoveIn
		if err := decodeCatalogInput("agent_worker_profile_remove", req, &in); err != nil {
			return toolError(err)
		}
		out, err := run(ctx, protocol.OpWorkerProfileRemove, protocol.WorkerProfileRequest{Name: in.Name})
		if err != nil {
			return toolError(err)
		}
		return catalogResult("agent_worker_profile_remove", out)
	})
}
