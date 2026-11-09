package protocol

// Claude Code permission proposals. An agent session proposes exact allow
// rules for named, registered repositories. The gateway stores the proposal
// and opens one owner-only ask. Only the owner's answer lets the gateway tell
// the target machines to write <repo>/.claude/settings.local.json. No agent,
// the CTO included, can apply a proposal without that answer.

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
	Error      string           `json:"error,omitempty"`
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
