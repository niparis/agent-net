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
	Worker   string `json:"worker" jsonschema:"the finished worker's session ID, as its end notice names it"`
	Kind     string `json:"kind,omitempty" jsonschema:"rating (default) for a 1 to 5 score, or defect_found_later for a defect found after the hand-back"`
	Score    int    `json:"score,omitempty" jsonschema:"1 to 5 for the result; leave it out for a defect_found_later report"`
	Severity int    `json:"severity,omitempty" jsonschema:"1 to 4, required on a defect_found_later report: how much rework the defect adds"`
	Note     string `json:"note,omitempty" jsonschema:"one optional line on why, or the one-line defect; it is cut to one short line"`
}

func addRateTool(s *mcp.Server, run func(context.Context, string, any) (any, error)) {
	mcp.AddTool(s, &mcp.Tool{Name: "agent_rate",
		Description: "Rate a finished worker you started, when its end notice asks: 1 to 5 for the result, and one optional line on why. " +
			"Set kind defect_found_later to report a defect found later, after the hand-back or after merge: no score, severity 1 to 4, and one line on what is wrong. " +
			"Each defect report adds its severity in points of rework to that worker's task, and a worker may take several. " +
			"A rating is taken once per worker. Only the session that started the worker may rate it or report on it; the owner may also report a defect. " +
			"The record is stored with the worker's model, harness, effort, difficulty and you; it never carries the task text. " +
			"A report for a worker you did not start is refused."},
		func(ctx context.Context, _ *mcp.CallToolRequest, in rateIn) (*mcp.CallToolResult, any, error) {
			out, err := run(ctx, protocol.OpRate, protocol.RateRequest{Worker: in.Worker, Kind: in.Kind, Score: in.Score, Severity: in.Severity, Note: in.Note})
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
				kind, _ := r["kind"].(string)
				score, _ := r["score"].(float64)
				model, _ := r["model"].(string)
				difficulty, _ := r["difficulty"].(string)
				text := fmt.Sprintf("Recorded rating %d for worker %s", int(score), worker)
				if kind == protocol.RateKindDefectLater {
					severity, _ := r["severity"].(float64)
					text = fmt.Sprintf("Recorded defect found later (severity %d) on worker %s", int(severity), worker)
				}
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
