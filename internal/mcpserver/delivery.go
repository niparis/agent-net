package mcpserver

// The delivery pipeline tools (decision 0057). Their contract is the catalog
// schema and the types in internal/protocol (delivery_pipeline.go and
// delivery_tools.go). Each call checks its arguments against the schema and
// sends them to its daemon method. The daemon fills in the caller's session,
// project, branch and commit, and asks the gateway.

import (
	"context"
	"encoding/json"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/niparis/agent-net/internal/protocol"
)

// deliveryTool registers one delivery tool that sends its arguments to the
// daemon method op and returns the daemon's answer as Out. The catalog output
// schema checks the answer before it is returned.
func deliveryTool[In, Out any](s *mcp.Server, call Caller, name, op string) {
	roAdd(s, name, func(ctx context.Context, _ *mcp.CallToolRequest, in In) (*mcp.CallToolResult, Out, error) {
		var out Out
		raw, err := call(ctx, op, in)
		if err != nil {
			return nil, out, err
		}
		err = json.Unmarshal(raw, &out)
		return nil, out, err
	})
}

// addDeliveryTools registers the delivery tools with the catalog's name,
// description, input schema and hints. The role filter removes the ones the
// caller may not use.
func addDeliveryTools(s *mcp.Server, call Caller) {
	deliveryTool[protocol.ChangeSubmitArgs, protocol.ChangeSubmitResult](s, call, "agent_change_submit", protocol.OpChangeSubmit)
	deliveryTool[protocol.StepClaimArgs, protocol.StepClaimResult](s, call, "agent_step_claim", protocol.OpStepClaim)
	deliveryTool[protocol.StepReportArgs, protocol.StepReportReply](s, call, "agent_step_report", protocol.OpStepReport)
	deliveryTool[protocol.ChangesFindArgs, protocol.ChangesFindResult](s, call, "agent_changes_find", protocol.OpChangesFind)
	deliveryTool[protocol.NudgePauseArgs, protocol.NudgeResult](s, call, "agent_nudge_pause", protocol.OpNudgePause)
	deliveryTool[protocol.NudgeResumeArgs, protocol.NudgeResult](s, call, "agent_nudge_resume", protocol.OpNudgeResume)
	deliveryTool[protocol.NudgePausesFindArgs, protocol.NudgeResult](s, call, "agent_nudge_pauses_find", protocol.OpNudgePausesFind)
	deliveryTool[protocol.NudgeStatsArgs, protocol.NudgeStatsResult](s, call, "agent_nudge_stats", protocol.OpNudgeStats)
	deliveryTool[protocol.FrictionReportArgs, protocol.FrictionReportResult](s, call, "agent_friction_report", protocol.OpFrictionReport)
	deliveryTool[protocol.FrictionMarkRequest, protocol.FrictionMarkResult](s, call, "agent_friction_mark", protocol.OpFrictionMarkLead)
	deliveryTool[protocol.AskSortArgs, protocol.AskSortResult](s, call, "agent_ask_sort", protocol.OpAskSort)
	deliveryTool[protocol.AskIntakeArgs, protocol.AskIntakeResult](s, call, "agent_ask_intake", protocol.OpAskIntake)
	deliveryTool[protocol.JudgmentRecordArgs, protocol.JudgmentRecordResult](s, call, "agent_owner_judgment_record", protocol.OpJudgmentRecord)
}

// dueIn is agent_due as the caller sends it. The item id is "id" here; the
// daemon gets it as due_id, because the gateway frame owns "id".
type dueIn struct {
	Action  string             `json:"action"`
	What    string             `json:"what,omitempty"`
	Due     string             `json:"due,omitempty"`
	After   *protocol.DueAfter `json:"after,omitempty"`
	Blocker string             `json:"blocker,omitempty"`
	ID      string             `json:"id,omitempty"`
}

// addDueTool registers agent_due through the catalog schema, so the public
// argument names stay as the catalog has them.
func addDueTool(s *mcp.Server, run func(context.Context, string, any) (any, error)) {
	addCatalogTool(s, "agent_due", func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		var in dueIn
		if err := decodeCatalogInput("agent_due", req, &in); err != nil {
			return toolError(err)
		}
		out, err := run(ctx, protocol.OpDue, protocol.DueArgs{Action: in.Action, What: in.What, Due: in.Due, After: in.After, Blocker: in.Blocker, ID: in.ID})
		if err != nil {
			return toolError(err)
		}
		return catalogResult("agent_due", out)
	})
}
