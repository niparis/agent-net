package protocol

// Removal of an imported native MCP entry from the harness config file. The
// owner plans it, applies it and may undo it. The machine that owns the file
// makes the edit; the gateway never sees file content.

// Operations of node.mcp_native_edit.
const (
	MCPNativePlan  = "plan"
	MCPNativeApply = "apply"
	MCPNativeUndo  = "undo"
)

// MCPNativeEditRequest asks the host to look at, edit or restore one native
// config file. Plan and apply name the entry the last scan saw; the host takes
// the project root from its own scan. Undo names a backup the host made.
type MCPNativeEditRequest struct {
	Op       string `json:"op"`
	Harness  string `json:"harness,omitempty"`
	Scope    string `json:"scope,omitempty"`
	Project  string `json:"project,omitempty"`
	Location string `json:"location,omitempty"`
	Name     string `json:"name,omitempty"`
	// FileHash is the hash the plan saw. Apply edits only a file that still has it.
	FileHash string `json:"file_hash,omitempty"`
	// PostHash and BackupID come from the apply result. Undo restores only a
	// file that still has PostHash.
	PostHash string `json:"post_hash,omitempty"`
	BackupID string `json:"backup_id,omitempty"`
}

// MCPNativeEditResult is the host's answer. Conflict is a plain reason the
// edit did not happen; the file was not changed.
type MCPNativeEditResult struct {
	Conflict string `json:"conflict,omitempty"`
	// FileHash is the hash of the file now, or after a plan.
	FileHash string `json:"file_hash,omitempty"`
	// Committed is true when the file is tracked by Git in a repository.
	Committed bool   `json:"committed,omitempty"`
	PostHash  string `json:"post_hash,omitempty"`
	BackupID  string `json:"backup_id,omitempty"`
}

// MCPNativeRemoveRequest is the owner's request. Exactly one field is set:
// Candidate makes a plan, PlanID applies it, UndoPlanID undoes an applied one.
type MCPNativeRemoveRequest struct {
	Candidate  string `json:"candidate,omitempty"`
	PlanID     string `json:"plan_id,omitempty"`
	UndoPlanID string `json:"undo_plan_id,omitempty"`
}

// MCPNativeRemovePlan is what a removal would do, shown before the owner applies it.
type MCPNativeRemovePlan struct {
	ID        string             `json:"id"`
	Candidate MCPImportCandidate `json:"candidate"`
	// Server is the federated server that replaces the native entry.
	Server   string `json:"server"`
	FileHash string `json:"file_hash"`
	// Gaps say what the native entry can do that the federation does not.
	Gaps      []string `json:"gaps"`
	Conflicts []string `json:"conflicts"`
	State     string   `json:"state"`
	Actor     string   `json:"actor"`
	At        int64    `json:"at"`
	// Set when applied.
	PostHash string `json:"post_hash,omitempty"`
	BackupID string `json:"backup_id,omitempty"`
}

// States of a native removal.
const (
	MCPNativeRemoved  = "removed"
	MCPNativeRestored = "restored"
	MCPNativeRefused  = "refused"
)

// MCPNativeRemoveResult answers an apply or an undo.
type MCPNativeRemoveResult struct {
	PlanID string `json:"plan_id"`
	Server string `json:"server"`
	State  string `json:"state"`
	Reason string `json:"reason,omitempty"`
	// Notes tell the owner what to do next, for example reload the session.
	Notes []string `json:"notes,omitempty"`
}
