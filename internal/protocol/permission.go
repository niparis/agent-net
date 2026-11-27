package protocol

// Claude Code permission grants. Only the owner's direct instruction grants a
// permission rule or a folder trust: the owner at the command line, or the CTO
// with his quoted words. An agent that needs a permission asks the chief of
// staff or the CTO with a plain message. The gateway tells each target machine
// to write <repo>/.claude/settings.local.json, or to trust the folder, and
// stores nothing.

// The two grant kinds. A permissions grant carries allow rules for
// repositories; a trust grant carries folders on one machine.
const (
	PermissionKindRules = "permissions"
	PermissionKindTrust = "trust"
)

// FramePermissionApply tells a daemon to merge rules into one repository, or to
// trust one folder, through the owner's existing trust writer. The gateway
// sends it only for a grant on the owner's verified words.
const FramePermissionApply = "permission.apply"

// FramePermissionTrustAsk lets a daemon tell the gateway that a local start
// answered needs_trust, so the gateway asks the chief of staff. It writes
// nothing.
const FramePermissionTrustAsk = "permission.trust_ask"

// PermissionTrustAsk is the daemon's report of one folder that needs Claude
// Code trust. Session and Owner name who hit it; the gateway adds the machine.
type PermissionTrustAsk struct {
	Session string `json:"session,omitempty"`
	Owner   string `json:"owner,omitempty"`
	Path    string `json:"path"`
	Project string `json:"project,omitempty"`
	// MCPServers names the servers of the folder's own .mcp.json that the
	// trust also approves.
	MCPServers []string `json:"mcp_servers,omitempty"`
}

// PermissionTarget is one registered repository location, or one folder, a
// grant applies to. Name is the project name the caller used.
type PermissionTarget struct {
	Name     string `json:"name"`
	NodeID   string `json:"node_id"`
	Hostname string `json:"hostname,omitempty"`
	Path     string `json:"path"`
	// MCPServers names the servers of the folder's own .mcp.json that a trust
	// grant also approves. Empty means the machine approves the servers it
	// finds in that file.
	MCPServers []string `json:"mcp_servers,omitempty"`
}

// PermissionGrant answers permission_apply: what was granted, on whose words,
// and the outcome on each target. Status is applied, partial (some targets
// failed) or failed.
type PermissionGrant struct {
	ID         string                  `json:"id"`
	Kind       string                  `json:"kind"`
	Rules      []string                `json:"rules,omitempty"`
	Targets    []PermissionTarget      `json:"targets"`
	By         string                  `json:"by"`
	OwnerWords string                  `json:"owner_words"`
	Status     string                  `json:"status"`
	Results    []PermissionApplyResult `json:"results"`
}

// PermissionApplyFrame asks one daemon to add rules to one repository's
// .claude/settings.local.json permissions.allow list, or to trust one folder.
type PermissionApplyFrame struct {
	GrantID string           `json:"grant_id"`
	Kind    string           `json:"kind"`
	Target  PermissionTarget `json:"target"`
	Rules   []string         `json:"rules,omitempty"`
}

// PermissionApplyResult is the outcome for one target. Status is applied,
// already_present or failed. Added names exactly the rules this grant wrote,
// and TrustAdded says the grant created the folder trust.
type PermissionApplyResult struct {
	Target     PermissionTarget `json:"target"`
	Kind       string           `json:"kind,omitempty"`
	Status     string           `json:"status"`
	Added      []string         `json:"added,omitempty"`
	TrustAdded bool             `json:"trust_added,omitempty"`
	// MCPAdded and MCPRestored are the servers of a trust grant's folder
	// .mcp.json that it approved or took out of disabledMcpjsonServers.
	MCPAdded    []string `json:"mcp_added,omitempty"`
	MCPRestored []string `json:"mcp_restored,omitempty"`
	// File is the settings file a rules apply wrote or read.
	File  string `json:"file,omitempty"`
	Error string `json:"error,omitempty"`
}

// FramePermissionOwnerApply is the gateway frame behind the owner operation
// permission_apply. The owner's direct instruction is his approval. Only the
// owner's own operator path, or the CTO's process-checked one, reaches it.
const FramePermissionOwnerApply = "permission.owner_apply"

// PermissionOwnerApplyRequest is cto_op permission_apply.
type PermissionOwnerApplyRequest struct {
	Kind       string   `json:"kind,omitempty" jsonschema:"permissions (default) or trust"`
	Rules      []string `json:"rules,omitempty" jsonschema:"exact Claude Code allow rules, for kind permissions"`
	Targets    []string `json:"targets,omitempty" jsonschema:"registered repo names for permissions, or folders for trust"`
	Machine    string   `json:"machine,omitempty" jsonschema:"the machine of the folders, for kind trust"`
	OwnerWords string   `json:"owner_words" jsonschema:"a short quote of the owner's instruction in this chat; kept in the log"`
}

// PermissionOwnerApplyFrame is FramePermissionOwnerApply. The daemon adds the
// owner session it checked, the session that acted and who it was. The request
// is nested because a frame's own id is a header field.
type PermissionOwnerApplyFrame struct {
	Request PermissionOwnerApplyRequest `json:"request"`
	Owner   string                      `json:"owner"`
	Session string                      `json:"session,omitempty"`
	By      string                      `json:"by,omitempty"`
	// WordsVerified is set by the daemon when the quote is the owner's own:
	// he typed it at the command line, or it was found in what he typed in the
	// CTO chat. The gateway applies nothing without it.
	WordsVerified bool `json:"words_verified,omitempty"`
}
