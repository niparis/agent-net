package operator

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/niparis/agent-net/internal/protocol"
)

// The Tools tab reads and changes skills and MCP servers through these
// methods. They are owner-only on the daemon (decision 0027). They are not part
// of the Operator interface: the tab asks for them when the source has them.

func decodeCall[T any](raw json.RawMessage, err error) (T, error) {
	var v T
	if err != nil {
		return v, err
	}
	err = json.Unmarshal(raw, &v)
	return v, err
}

// ToolsItems reads every page of tools_items.
func (s *Client) ToolsItems(ctx context.Context) ([]protocol.ToolItem, error) {
	var items []protocol.ToolItem
	offset := 0
	for {
		page, err := decodeCall[protocol.ToolsPage](s.action(ctx, "tools_items", map[string]int{"offset": offset}))
		if err != nil {
			return nil, err
		}
		items = append(items, page.Items...)
		if page.Next == 0 {
			return items, nil
		}
		if page.Next <= offset {
			return nil, errors.New("invalid tools page cursor")
		}
		offset = page.Next
	}
}

// ToolsAccepted lists the keys of the differences the owner accepted.
func (s *Client) ToolsAccepted(ctx context.Context) ([]string, error) {
	return decodeCall[[]string](s.action(ctx, "tools_accepted", struct{}{}))
}

// ToolsAccept accepts a difference as it is, or takes the mark off.
func (s *Client) ToolsAccept(ctx context.Context, key string, accepted bool) error {
	_, err := s.action(ctx, "tools_accept", protocol.ToolAccept{Key: key, Accepted: accepted})
	return err
}

// ToolsNeeds lists the charters' needs lines.
func (s *Client) ToolsNeeds(ctx context.Context) ([]protocol.ToolNeed, error) {
	return decodeCall[[]protocol.ToolNeed](s.action(ctx, "tools_needs", struct{}{}))
}

// ToolsPlan proposes a plan. It changes nothing.
func (s *Client) ToolsPlan(ctx context.Context, req protocol.ToolPlanRequest) (protocol.ToolPlan, error) {
	return decodeCall[protocol.ToolPlan](s.action(ctx, "tools_plan", req))
}

// ToolsApply applies a plan the owner has seen.
func (s *Client) ToolsApply(ctx context.Context, planID string) (protocol.ToolApplyResult, error) {
	return decodeCall[protocol.ToolApplyResult](s.action(ctx, "tools_apply", protocol.ToolPlanID{ID: planID}))
}

// ToolsImport copies one observed skill into the agent-net store.
func (s *Client) ToolsImport(ctx context.Context, imp protocol.ToolImport) error {
	_, err := s.action(ctx, "tools_import", imp)
	return err
}

// MCPCandidates lists the native MCP entries that can move to the gateway.
func (s *Client) MCPCandidates(ctx context.Context) ([]protocol.MCPImportCandidate, error) {
	var all []protocol.MCPImportCandidate
	offset := 0
	for {
		page, err := decodeCall[protocol.MCPImportCandidatesPage](s.action(ctx, "mcp_import_candidates", protocol.MCPImportCandidatesRequest{Offset: offset}))
		if err != nil {
			return nil, err
		}
		all = append(all, page.Candidates...)
		if page.Next <= offset {
			return all, nil
		}
		offset = page.Next
	}
}

// MCPImportPlan plans the move of one native entry to the gateway.
func (s *Client) MCPImportPlan(ctx context.Context, candidate string) (protocol.MCPImportPlan, error) {
	return decodeCall[protocol.MCPImportPlan](s.action(ctx, "mcp_import", protocol.MCPImportRequest{Candidate: candidate}))
}

// MCPImportApply applies an import plan.
func (s *Client) MCPImportApply(ctx context.Context, planID string) (protocol.MCPImportResult, error) {
	return decodeCall[protocol.MCPImportResult](s.action(ctx, "mcp_import", protocol.MCPImportRequest{PlanID: planID}))
}

// MCPNativeRemovePlan plans the removal of an imported native entry.
func (s *Client) MCPNativeRemovePlan(ctx context.Context, candidate string) (protocol.MCPNativeRemovePlan, error) {
	return decodeCall[protocol.MCPNativeRemovePlan](s.action(ctx, "mcp_native_remove", protocol.MCPNativeRemoveRequest{Candidate: candidate}))
}

// MCPNativeRemoveApply applies a removal plan.
func (s *Client) MCPNativeRemoveApply(ctx context.Context, planID string) (protocol.MCPNativeRemoveResult, error) {
	return decodeCall[protocol.MCPNativeRemoveResult](s.action(ctx, "mcp_native_remove", protocol.MCPNativeRemoveRequest{PlanID: planID}))
}
