package protocol

import (
	"strconv"
	"strings"
)

const (
	OpSessionStop     = "session_stop"
	OpSessionResume   = "session_resume"
	OpSessionStart    = "session_start"
	OpSessionStartGet = "session_start_get"
)

// Gateway frames that relay an owner session action to the machine that holds
// the project or the session. They come from the owner's machine and carry the
// owner session ID. The gateway checks it and asks the target machine; that
// machine checks the flag again against the gateway.
const (
	FrameSessionStart              = "session.start"
	FrameSessionProposalStart      = "session.proposal_start"
	FrameSessionProposalClone      = "session.proposal_clone"
	FrameSessionResume             = "session.resume"
	FrameSessionWake               = "session.wake"
	FrameSessionWakeRebind         = "session.wake_rebind"
	FrameSessionStop               = "session.stop"
	FrameSessionGet                = "session.get"
	FrameSessionOwnerStart         = "session.owner_start"
	FrameSessionOwnerProposalStart = "session.owner_proposal_start"
	FrameSessionOwnerProposalClone = "session.owner_proposal_clone"
	FrameSessionOwnerResume        = "session.owner_resume"
	FrameSessionOwnerStop          = "session.owner_stop"
	FrameSessionOwnerGet           = "session.owner_get"
)

// The Folder choices of the New session form. Empty means FolderProject, so
// a request from an older TUI still starts in the project folder. FolderWorktree
// makes a new git worktree next to the project and starts the session there.
const (
	FolderProject  = "project"
	FolderWorktree = "worktree"
)

// OwnerWorktreeProject is the project part of an owner session's worktree
// branch. A branch name cannot hold spaces or every character git refuses, so
// those become a single hyphen.
func OwnerWorktreeProject(project string) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.TrimSpace(project) {
		ok := r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_' || r == '.'
		if ok {
			b.WriteRune(r)
			dash = false
			continue
		}
		if !dash {
			b.WriteByte('-')
			dash = true
		}
	}
	name := b.String()
	for strings.Contains(name, "..") {
		name = strings.ReplaceAll(name, "..", ".")
	}
	name = strings.Trim(name, "-.")
	if name == "" {
		name = "session"
	}
	return name
}

// OwnerWorktreeBranchPrefix is the start of the automatic branch names of a
// project's owner worktrees: agent-net/<project>-.
func OwnerWorktreeBranchPrefix(project string) string {
	return "agent-net/" + OwnerWorktreeProject(project) + "-"
}

// OwnerWorktreeBranch is agent-net/<project>-<n>, the branch of an owner
// session's worktree.
func OwnerWorktreeBranch(project string, n int) string {
	return OwnerWorktreeBranchPrefix(project) + strconv.Itoa(n)
}

// SessionStartRequest starts an owner session in a known project.
// Empty Harness means Claude; empty Model keeps the harness default. NodeID
// picks the machine when the project is on another one; empty means the local
// machine. Folder is the folder choice of the form and is carried to the
// machine that runs the start. Branch names the new worktree's branch; empty
// asks the machine for the lowest free agent-net/<project>-<n>.
type SessionStartRequest struct {
	// AgentID selects the lasting Agent. Empty keeps the Repo owner default.
	AgentID      string `json:"agent_id,omitempty"`
	Remote       string `json:"remote,omitempty"`
	Merger       bool   `json:"merger,omitempty"`
	Project      string `json:"project"`
	Harness      string `json:"harness,omitempty"`
	Model        string `json:"model,omitempty"`
	FirstMessage string `json:"first_message,omitempty"`
	NodeID       string `json:"node_id,omitempty"`
	Folder       string `json:"folder,omitempty"`
	Branch       string `json:"branch,omitempty"`
	// Path is a folder typed in the Project field. It must be absolute or
	// start with ~, and it is checked on the machine that runs the start.
	// Project is empty for a path start.
	Path string `json:"path,omitempty"`
	// Create is the owner's yes to creating Path with git init when it does
	// not exist. Without it the start answers status "needs_create".
	Create bool `json:"create,omitempty"`
	// TrustClaude is the owner's yes to trusting an existing folder for
	// Claude Code. Without it an untrusted folder answers "needs_trust".
	TrustClaude bool `json:"trust_claude,omitempty"`
	// CTOProposal asks the destination to enforce proposal-specific folder limits.
	CTOProposal bool `json:"cto_proposal,omitempty"`
	// HerdrSocket names the owner's local Herdr server, if the TUI is in one.
	HerdrSocket string `json:"herdr_socket,omitempty"`
	HerdrPaneID string `json:"herdr_pane_id,omitempty"`
}

// SessionStartRelay is "session.start" from the owner's machine. The gateway
// fills the target when NodeID is empty by resolving the project.
type SessionStartRelay struct {
	Owner  string `json:"owner_session"`
	NodeID string `json:"node_id,omitempty"`
	SessionStartRequest
}

// SessionRelay is "session.resume", "session.stop" or "session.get" from the
// owner's machine.
type SessionRelay struct {
	Owner     string `json:"owner_session"`
	NodeID    string `json:"node_id,omitempty"`
	SessionID string `json:"session_id,omitempty"`
	StartID   string `json:"start_id,omitempty"`
}

// SessionOwnerRequest is the gateway's frame to the machine that owns the
// project or session. FromNode is the machine the owner session belongs to.
// The target checks both fields against the gateway before it acts.
type SessionOwnerRequest struct {
	Owner    string `json:"owner_session"`
	FromNode string `json:"from_node"`
	SessionStartRequest
	SessionID string `json:"session_id,omitempty"`
	StartID   string `json:"start_id,omitempty"`
}

type SessionStartQuery struct {
	ID string `json:"id"`
}

// StartedSession is separate from a worker. It has no owner agent, brief,
// deadline or cleanup rules. SessionID is filled after normal registration.
type StartedSession struct {
	AgentID    string `json:"agent_id,omitempty"`
	Type       string `json:"type,omitempty"`
	Background bool   `json:"background,omitempty"`
	ID         string `json:"id"`
	Status     string `json:"status"` // starting, started, failed
	NodeID     string `json:"node_id"`
	Hostname   string `json:"hostname,omitempty"`
	Harness    string `json:"harness"`
	Project    string `json:"project"`
	Path       string `json:"path"`
	Branch     string `json:"branch,omitempty"`
	Model      string `json:"model,omitempty"`
	// EnterNote says how Enter reaches a session on another machine, for the
	// result line after a start with a first message. Empty on this machine.
	EnterNote string     `json:"enter_note,omitempty"`
	StartedAt int64      `json:"started_at"`
	JobID     string     `json:"job_id,omitempty"`
	NativeID  string     `json:"native_id,omitempty"`
	SessionID string     `json:"session_id,omitempty"`
	Error     string     `json:"error,omitempty"`
	EnterPlan *EnterPlan `json:"enter_plan,omitempty"`
}

// SessionResumeRequest identifies an offline conversation. NodeID is the
// machine that holds it, when the caller knows it; empty lets the daemon or
// the gateway resolve it.
type SessionResumeRequest struct {
	SessionID   string `json:"session_id"`
	NodeID      string `json:"node_id,omitempty"`
	HerdrSocket string `json:"herdr_socket,omitempty"`
	HerdrPaneID string `json:"herdr_pane_id,omitempty"`
}

// SessionStopResult describes the harness stop without removing the conversation.
type SessionStopResult struct {
	Note string `json:"note"`
}

// SessionAgentReservation is an owner-authorized selection for a future session.
// NativeID may be empty for validation before launch.
type SessionAgentReservation struct {
	Owner    string `json:"owner_session"`
	AgentID  string `json:"agent_id"`
	Harness  string `json:"harness"`
	NativeID string `json:"native_id,omitempty"`
}
