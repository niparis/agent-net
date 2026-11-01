package mcpserver

import (
	"context"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/niparis/agent-net/internal/protocol"
)

// agent_rate records a caller's rating of a finished worker (difficulty
// calibration, part 4).

type rateIn struct {
	Worker string `json:"worker" jsonschema:"the finished worker's session ID, as its end notice names it"`
	Score  int    `json:"score" jsonschema:"1 to 5 for the result"`
	Note   string `json:"note,omitempty" jsonschema:"one optional line on why; it is cut to one short line"`
}

func addRateTool(s *mcp.Server, run func(context.Context, string, any) (any, error)) {
	mcp.AddTool(s, &mcp.Tool{Name: "agent_rate",
		Description: "Rate a finished worker you started, when its end notice asks: 1 to 5 for the result, and one optional line on why. " +
			"Only the session that started the worker may rate it, and each worker takes one rating. " +
			"The rating is stored with the worker's model, harness, effort, difficulty and you; it never carries the task text. " +
			"A rating for a worker you did not start is refused."},
		func(ctx context.Context, _ *mcp.CallToolRequest, in rateIn) (*mcp.CallToolResult, any, error) {
			out, err := run(ctx, protocol.OpRate, protocol.RateRequest{Worker: in.Worker, Score: in.Score, Note: in.Note})
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
				worker, _ := r["worker"].(string)
				score, _ := r["score"].(float64)
				model, _ := r["model"].(string)
				difficulty, _ := r["difficulty"].(string)
				text := fmt.Sprintf("Recorded rating %d for worker %s", int(score), worker)
				if model != "" {
					text += " (" + model
					if difficulty != "" {
						text += ", " + difficulty
					}
					text += ")"
				}
				return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: text + "."}}}, out, nil
			}
			return nil, out, nil
		})
}
