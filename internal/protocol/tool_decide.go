package protocol

import "encoding/json"

// MethodToolDecide is the local question "may this tool call run without
// asking the owner". It is not an operator method and changes nothing: the
// daemon answers allow from the scopes the session's role node holds, or gives
// no decision (decision 0165). It never answers block.
const MethodToolDecide = "tool_decide"

// ToolDecideRequest is MethodToolDecide. A plugin or MCP client is bound to its
// session and leaves Harness and NativeID empty. The command line (the Claude
// Code hook) names the session it runs for. Tool is the harness's own name for
// the tool, Input its arguments, and Folder the folder the call runs in.
type ToolDecideRequest struct {
	Harness  string          `json:"harness,omitempty"`
	NativeID string          `json:"native_id,omitempty"`
	Tool     string          `json:"tool"`
	Input    json.RawMessage `json:"input,omitempty"`
	Folder   string          `json:"folder,omitempty"`
}

// ToolDecideResult answers MethodToolDecide. Allow is true when a scope allows
// the call; false means no decision. Reason names the scope for the log.
type ToolDecideResult struct {
	Allow  bool   `json:"allow"`
	Reason string `json:"reason,omitempty"`
}
