package protocol

// Claude Code asks the user to approve each MCP server it finds in a
// .mcp.json file before it uses it. A background session cannot answer that
// prompt, so the session never starts. The choice is stored in the launch
// folder's .claude/settings.local.json (enabledMcpjsonServers or
// disabledMcpjsonServers). OpFolderMCP records or undoes that choice on the
// machine that holds the folder (decision 0094).

// OpFolderMCP records the owner's choice for the .mcp.json servers Claude Code
// would load in a folder, or undoes a choice agent-net recorded. The CTO (on
// the owner's instruction) and the Inbox undo use it. It takes
// FolderMCPRequest and answers FolderMCPResult.
const OpFolderMCP = "folder_mcp"

// Frames that carry the choice to the machine that holds the folder. An older
// gateway or daemon answers "unknown frame type", so a choice is never
// downgraded to a plain folder trust.
const (
	FrameSessionMCPServers      = "session.mcp_servers"
	FrameSessionOwnerMCPServers = "session.owner_mcp_servers"
)

// Choices for the .mcp.json servers of a folder.
const (
	MCPApprove = "approve"
	MCPDisable = "disable"
)

// Reason codes of a failed session start that a caller can act on.
const (
	StartErrorMCPApprovalNeeded = "mcp_approval_needed"
)

// MCPServerInfo is one server of a .mcp.json file: its name, what it runs and
// the file that defines it.
type MCPServerInfo struct {
	Name    string `json:"name"`
	Command string `json:"command,omitempty"`
	File    string `json:"file"`
}

// FolderMCPRequest names the folder by a registered Project or by an absolute
// or ~ Path, on the machine NodeID (empty means this machine). Choice is
// "approve" or "disable". Undo takes back exactly what an earlier choice
// added; it needs only Path (or Project) and NodeID.
type FolderMCPRequest struct {
	Project string   `json:"project,omitempty"`
	Path    string   `json:"path,omitempty"`
	NodeID  string   `json:"node_id,omitempty"`
	Choice  string   `json:"mcp_servers,omitempty"`
	Undo    *MCPUndo `json:"undo,omitempty"`
}

// MCPUndo lists the changes of one choice. Names in Added were put in the list
// of the choice; names in Restored were taken out of the opposite list (an
// approval removes a refusal that would win) and go back there.
type MCPUndo struct {
	Choice   string   `json:"choice"`
	Added    []string `json:"added,omitempty"`
	Restored []string `json:"restored,omitempty"`
}

// MCPChoiceResult says what a choice did. Added holds only the names that were
// not in the list before, so an undo never removes a choice the owner made.
type MCPChoiceResult struct {
	Choice   string          `json:"choice"`
	Path     string          `json:"path"`
	File     string          `json:"settings_file"`
	Servers  []MCPServerInfo `json:"servers"`
	Added    []string        `json:"added,omitempty"`
	Restored []string        `json:"restored,omitempty"`
}

// FolderMCPResult answers OpFolderMCP.
type FolderMCPResult struct {
	MCPChoiceResult
	NodeID   string `json:"node_id,omitempty"`
	Hostname string `json:"hostname,omitempty"`
	Project  string `json:"project,omitempty"`
}

// SessionFolderMCPRelay is the choice sent to the machine that holds the folder.
type SessionFolderMCPRelay struct {
	Owner  string `json:"owner_session"`
	NodeID string `json:"node_id"`
	FolderMCPRequest
}

// SessionOwnerFolderMCP is the gateway's frame to that machine. FromNode is the
// machine the owner session belongs to.
type SessionOwnerFolderMCP struct {
	Owner    string `json:"owner_session"`
	FromNode string `json:"from_node"`
	FolderMCPRequest
}
