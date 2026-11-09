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
	deliveryTool[protocol.StepReportArgs, protocol.StepReportResult](s, call, "agent_step_report", protocol.OpStepReport)
	deliveryTool[protocol.ChangesFindArgs, protocol.ChangesFindResult](s, call, "agent_changes_find", protocol.OpChangesFind)
	deliveryTool[protocol.AskSortArgs, protocol.AskSortResult](s, call, "agent_ask_sort", protocol.OpAskSort)
	deliveryTool[protocol.AskIntakeArgs, protocol.AskIntakeResult](s, call, "agent_ask_intake", protocol.OpAskIntake)
}
