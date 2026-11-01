package protocol

const (
	OpSessionResume   = "session_resume"
	OpSessionStart    = "session_start"
	OpSessionStartGet = "session_start_get"
)

// SessionStartRequest starts an owner session in a known local project.
// Empty Harness means Claude; empty Model keeps the harness default.
type SessionStartRequest struct {
	Project      string `json:"project"`
	Harness      string `json:"harness,omitempty"`
	Model        string `json:"model,omitempty"`
	FirstMessage string `json:"first_message,omitempty"`
}

type SessionStartQuery struct {
	ID string `json:"id"`
}

// StartedSession is separate from a worker. It has no owner agent, brief,
// deadline or cleanup rules. SessionID is filled after normal registration.
type StartedSession struct {
	ID        string     `json:"id"`
	Status    string     `json:"status"` // starting, started, failed
	NodeID    string     `json:"node_id"`
	Harness   string     `json:"harness"`
	Project   string     `json:"project"`
	Path      string     `json:"path"`
	Branch    string     `json:"branch,omitempty"`
	Model     string     `json:"model,omitempty"`
	StartedAt int64      `json:"started_at"`
	JobID     string     `json:"job_id,omitempty"`
	NativeID  string     `json:"native_id,omitempty"`
	SessionID string     `json:"session_id,omitempty"`
	Error     string     `json:"error,omitempty"`
	EnterPlan *EnterPlan `json:"enter_plan,omitempty"`
}

// SessionResumeRequest identifies an offline conversation.
type SessionResumeRequest struct {
	SessionID string `json:"session_id"`
}
