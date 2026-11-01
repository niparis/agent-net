package protocol

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
	FrameSessionStart       = "session.start"
	FrameSessionResume      = "session.resume"
	FrameSessionStop        = "session.stop"
	FrameSessionGet         = "session.get"
	FrameSessionOwnerStart  = "session.owner_start"
	FrameSessionOwnerResume = "session.owner_resume"
	FrameSessionOwnerStop   = "session.owner_stop"
	FrameSessionOwnerGet    = "session.owner_get"
)

// SessionStartRequest starts an owner session in a known project.
// Empty Harness means Claude; empty Model keeps the harness default. NodeID
// picks the machine when the project is on another one; empty means the local
// machine. Folder is the folder choice of the form and is carried to the
// machine that runs the start (folder creation comes in a later part).
type SessionStartRequest struct {
	Project      string `json:"project"`
	Harness      string `json:"harness,omitempty"`
	Model        string `json:"model,omitempty"`
	FirstMessage string `json:"first_message,omitempty"`
	NodeID       string `json:"node_id,omitempty"`
	Folder       string `json:"folder,omitempty"`
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
	ID       string `json:"id"`
	Status   string `json:"status"` // starting, started, failed
	NodeID   string `json:"node_id"`
	Hostname string `json:"hostname,omitempty"`
	Harness  string `json:"harness"`
	Project  string `json:"project"`
	Path     string `json:"path"`
	Branch   string `json:"branch,omitempty"`
	Model    string `json:"model,omitempty"`
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
	SessionID string `json:"session_id"`
	NodeID    string `json:"node_id,omitempty"`
}

// SessionStopResult describes the harness stop without removing the conversation.
type SessionStopResult struct {
	Note string `json:"note"`
}
