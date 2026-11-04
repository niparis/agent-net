package mcpserver

// The delivery pipeline tools (decision 0056). Their contract is the catalog
// schema and the types in internal/protocol (delivery_pipeline.go). The daemon
// part is not built yet, so each call checks its arguments against the schema
// and answers that the tool is not built yet, without calling the daemon. The
// daemon part replaces deliveryNotBuilt with calls to protocol.OpChangeSubmit,
// protocol.OpStepReport and protocol.OpChangesFind.

import (
	"context"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func deliveryNotBuilt(name string) error {
	return fmt.Errorf("%s is not built yet: agent-net does not record delivery changes in this version. Deliver as your rules say", name)
}

// deliveryHandler validates the arguments, then refuses: not built yet.
func deliveryHandler(name string) mcp.ToolHandler {
	return func(_ context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		if _, err := catalogInput(name, req); err != nil {
			return toolError(err)
		}
		return toolError(deliveryNotBuilt(name))
	}
}

// addDeliveryTools registers the delivery tools with the catalog's name,
// description, input schema and hints. The role filter removes the ones the
// caller may not use.
func addDeliveryTools(s *mcp.Server) {
	for _, name := range []string{"agent_change_submit", "agent_step_report", "agent_changes_find"} {
		spec := roSpec(name)
		annotations := &mcp.ToolAnnotations{ReadOnlyHint: spec.Hints.ReadOnly, IdempotentHint: spec.Hints.Idempotent, DestructiveHint: roBool(spec.Hints.Destructive)}
		s.AddTool(&mcp.Tool{Name: spec.Name, Description: spec.Description, InputSchema: spec.InputSchema, Annotations: annotations}, deliveryHandler(name))
	}
}
