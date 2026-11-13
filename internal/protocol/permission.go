package protocol

// Claude Code permission proposals. An agent session proposes exact allow
// rules for named, registered repositories. The gateway stores the proposal
// and opens one owner-only ask. Only the owner's answer lets the gateway tell
// the target machines to write <repo>/.claude/settings.local.json. No agent
// can apply a proposal without that answer. The one other path is the owner's
// direct instruction in the CTO chat: the CTO applies it with his quoted words
// as the record (decision 0159).

// The two proposal kinds. A permissions proposal carries allow rules for
// repositories; a trust proposal carries folders on one machine.
const (
	PermissionKindRules = "permissions"
	PermissionKindTrust = "trust"
)

// FramePermissionApply tells a daemon to merge rules into one repository, or to
// trust one folder, through the owner's existing trust writer. The gateway
// sends it, and only after the owner answered the ask.
const FramePermissionApply = "permission.apply"

// FramePermissionRevoke tells a daemon to remove the exact rules or trust one
// approval added, and nothing else.
const FramePermissionRevoke = "permission.revoke"

// FramePermissionTrustAsk lets a daemon tell the gateway that a local start
// answered needs_trust, so the gateway raises the owner-only trust ask. It
// writes nothing.
const FramePermissionTrustAsk = "permission.trust_ask"

// PermissionTrustAsk is the daemon's report of one folder that needs Claude
// Code trust. Session and Owner name who hit it; the gateway adds the machine.
type PermissionTrustAsk struct {
	Session string `json:"session,omitempty"`
	Owner   string `json:"owner,omitempty"`
	Path    string `json:"path"`
	Project string `json:"project,omitempty"`
}

// PermissionProposeRequest is agent_permission_propose: exact Claude Code
// permission rules for repositories, or exact folders to trust on one machine.
type PermissionProposeRequest struct {
	Kind    string   `json:"kind,omitempty"`
	Rules   []string `json:"rules,omitempty"`
	Targets []string `json:"targets"`
	Machine string   `json:"machine,omitempty"`
	Reason  string   `json:"reason"`
}

// PermissionProposeFrame is the daemon frame "permission.propose". The daemon
// adds the caller.
type PermissionProposeFrame struct {
	PermissionProposeRequest
	SessionID string `json:"session_id"`
}

// PermissionTarget is one registered repository location a proposal applies
// to. Name is the project name the caller used.
type PermissionTarget struct {
	Name     string `json:"name"`
	NodeID   string `json:"node_id"`
	Hostname string `json:"hostname,omitempty"`
	Path     string `json:"path"`
}

// PermissionProposeResult answers agent_permission_propose.
type PermissionProposeResult struct {
	ID      string             `json:"id"`
	Status  string             `json:"status"`
	Targets []PermissionTarget `json:"targets,omitempty"`
}

// PermissionProposal is one stored proposal. Status is pending, approved,
// rejected, applied, partial (some targets failed), failed or undone.
type PermissionProposal struct {
	ID           string                  `json:"id"`
	Kind         string                  `json:"kind"`
	Rules        []string                `json:"rules,omitempty"`
	Targets      []PermissionTarget      `json:"targets"`
	Reason       string                  `json:"reason"`
	ProposedBy   string                  `json:"proposed_by"`
	ProposedName string                  `json:"proposed_name,omitempty"`
	Status       string                  `json:"status"`
	CreatedAt    int64                   `json:"created_at"`
	DecidedAt    int64                   `json:"decided_at,omitempty"`
	DecidedBy    string                  `json:"decided_by,omitempty"`
	Results      []PermissionApplyResult `json:"results,omitempty"`
	DeskLogID    int64                   `json:"desk_log_id,omitempty"`
	DedupeKey    string                  `json:"dedupe_key,omitempty"`
	// OwnerWords quotes the owner's chat instruction when the CTO applied the
	// proposal on his words instead of a Desk answer (decision 0159).
	OwnerWords string `json:"owner_words,omitempty"`
}

// PermissionApplyFrame asks one daemon to add rules to one repository's
// .claude/settings.local.json permissions.allow list, or to trust one folder.
type PermissionApplyFrame struct {
	ProposalID string           `json:"proposal_id"`
	Kind       string           `json:"kind"`
	Target     PermissionTarget `json:"target"`
	Rules      []string         `json:"rules,omitempty"`
}

// PermissionRevokeFrame asks one daemon to remove exactly the rules, or the
// trust, an earlier approval added.
type PermissionRevokeFrame struct {
	ProposalID string   `json:"proposal_id"`
	Kind       string   `json:"kind"`
	Path       string   `json:"path"`
	Rules      []string `json:"rules,omitempty"`
}

// PermissionApplyResult is the outcome for one target. Status is applied,
// already_present, removed, absent or failed. TrustAdded says an approval
// created the folder trust, so only then does the undo remove it.
type PermissionApplyResult struct {
	Target     PermissionTarget `json:"target"`
	Kind       string           `json:"kind,omitempty"`
	Status     string           `json:"status"`
	Added      []string         `json:"added,omitempty"`
	Removed    []string         `json:"removed,omitempty"`
	TrustAdded bool             `json:"trust_added,omitempty"`
	// File is the settings file a rules apply wrote or read.
	File  string `json:"file,omitempty"`
	Error string `json:"error,omitempty"`
}

// FramePermissionOwnerApply is the gateway frame behind the owner operation
// permission_apply (decision 0159). The owner's direct instruction is his
// approval: the gateway stores his quoted words as the record and applies
// through the same per-machine frames as a Desk approval. Only the owner's own
// operator path, or the CTO's process-checked one, reaches it.
const FramePermissionOwnerApply = "permission.owner_apply"

// FramePermissionGet reads one stored proposal for the owner or the CTO.
const FramePermissionGet = "permission.get"

// PermissionOwnerApplyRequest is cto_op permission_apply. Give id alone to
// approve a proposal that waits on the Desk, or kind, rules, targets and
// machine as agent_permission_propose takes them for a new grant.
type PermissionOwnerApplyRequest struct {
	ID         string   `json:"id,omitempty" jsonschema:"a pending proposal (pp_...) to approve; give it alone"`
	Kind       string   `json:"kind,omitempty" jsonschema:"permissions (default) or trust"`
	Rules      []string `json:"rules,omitempty" jsonschema:"exact Claude Code allow rules, for kind permissions"`
	Targets    []string `json:"targets,omitempty" jsonschema:"registered repo names for permissions, or folders for trust"`
	Machine    string   `json:"machine,omitempty" jsonschema:"the machine of the folders, for kind trust"`
	OwnerWords string   `json:"owner_words" jsonschema:"a short quote of the owner's instruction in this chat; stored as the record"`
}

// PermissionOwnerApplyFrame is FramePermissionOwnerApply. The daemon adds the
// owner session it checked, the session that acted and who it was. The request
// is nested because a frame's own id is a header field.
type PermissionOwnerApplyFrame struct {
	Request PermissionOwnerApplyRequest `json:"request"`
	Owner   string                      `json:"owner"`
	Session string                      `json:"session,omitempty"`
	By      string                      `json:"by,omitempty"`
}

// PermissionGetFrame is FramePermissionGet.
type PermissionGetFrame struct {
	ProposalID string `json:"proposal_id"`
	Owner      string `json:"owner"`
}

// PermissionUndoRequest asks the owner's undo of one approval.
type PermissionUndoRequest struct {
	ID string `json:"id"`
}

// PermissionUndoFrame is the gateway frame "permission.undo". Owner is the
// owner session of the machine that asked, checked by the gateway.
type PermissionUndoFrame struct {
	ProposalID string `json:"proposal_id"`
	Owner      string `json:"owner"`
}

// PermissionUndoResult answers an undo.
type PermissionUndoResult struct {
	ID      string                  `json:"id"`
	Status  string                  `json:"status"`
	Results []PermissionApplyResult `json:"results"`
}
