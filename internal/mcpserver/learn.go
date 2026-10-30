package mcpserver

import (
	"context"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/niparis/agent-net/internal/protocol"
)

// agent_learn records one finding for the network's playbook (decision 0022).

type learnIn struct {
	SubjectKind string   `json:"subject_kind" jsonschema:"model, harness, model_harness, project, mcp or machine"`
	Subject     string   `json:"subject" jsonschema:"the model, harness, model@harness, project, MCP server or machine the finding is about"`
	Finding     string   `json:"finding" jsonschema:"what you learned, in one or two sentences: only what would change how someone picks or briefs this model, harness or project next time. Never a routine result. At most 500 characters"`
	Evidence    []string `json:"evidence,omitempty" jsonschema:"session or worker IDs that show it (optional, at most 20)"`
}

func addLearnTool(s *mcp.Server, run func(context.Context, string, any) (any, error)) {
	mcp.AddTool(s, &mcp.Tool{Name: "agent_learn",
		Description: "Record one thing the network should know before it picks or briefs a model, harness, project, MCP server or machine next time. " +
			"Report only what would change that choice or brief, never a routine result or a status update. " +
			"A finding is quiet: it is not a message, it does not appear in Needs you, and it changes nothing until the gateway promotes a pattern to the playbook. " +
			"At most 3 findings per session per day. subject_kind is model, harness, model_harness (subject model@harness), project, mcp or machine."},
		func(ctx context.Context, _ *mcp.CallToolRequest, in learnIn) (*mcp.CallToolResult, any, error) {
			out, err := run(ctx, protocol.OpLearn, protocol.LearnRequest{SubjectKind: in.SubjectKind, Subject: in.Subject,
				Finding: in.Finding, Evidence: in.Evidence})
			if err != nil {
				return nil, nil, err
			}
			if r, ok := out.(map[string]any); ok {
				accepted, _ := r["accepted"].(bool)
				if !accepted {
					reason, _ := r["reason"].(string)
					if reason != "" {
						return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "Not recorded: " + reason}}}, out, nil
					}
					return nil, out, nil
				}
				id, _ := r["id"].(float64)
				status, _ := r["status"].(string)
				left, _ := r["remaining"].(float64)
				return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{
					Text: fmt.Sprintf("Recorded finding %d (%s). %d left today.", int64(id), status, int(left))}}}, out, nil
			}
			return nil, out, nil
		})
}
