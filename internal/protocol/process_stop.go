package protocol

// OpProcessStop stops processes on a machine, by process ID. The owner at the
// keyboard and the CTO (on the owner's instruction) use it; no agent tool
// calls it. It takes ProcessStopRequest and answers ProcessStopResult.
// Nothing can undo it (decision 0102).
const OpProcessStop = "process_stop"

// Frames that carry a process stop to the machine that runs the processes.
// The gateway checks the owner session and asks that machine, as it does for a
// folder trust. The target checks the owner flag again.
const (
	FrameSessionStopProcess      = "session.stop_process"
	FrameSessionOwnerStopProcess = "session.owner_stop_process"
)

// What happened to one process ID.
const (
	// ProcessRefused: the machine would not signal it. Reason says why.
	ProcessRefused = "refused"
	// ProcessStoppedTerm: it ended after SIGTERM.
	ProcessStoppedTerm = "stopped_term"
	// ProcessStoppedKill: it was still alive after the wait, so it got SIGKILL
	// and ended.
	ProcessStoppedKill = "stopped_kill"
	// ProcessNotFound: no such process (or only a zombie, which no signal
	// removes).
	ProcessNotFound = "not_found"
	// ProcessFailed: it is allowed, but the signal failed or the process was
	// still there after SIGKILL. Reason says why.
	ProcessFailed = "failed"
)

// ProcessStopRequest names the machine and the process IDs. NodeID is
// required: a stop never defaults to "this machine".
type ProcessStopRequest struct {
	NodeID string `json:"node_id"`
	PIDs   []int  `json:"pids"`
}

// ProcessOutcome is the answer for one process ID. Command, ParentPID and
// Started are read before any signal is sent, and are set for a refused
// process too when the machine could read them.
type ProcessOutcome struct {
	PID       int    `json:"pid"`
	Outcome   string `json:"outcome"`
	Reason    string `json:"reason,omitempty"`
	Command   string `json:"command,omitempty"`
	ParentPID int    `json:"parent_pid,omitempty"`
	// Started is the process start time, RFC 3339 in UTC.
	Started string `json:"started,omitempty"`
}

// ProcessStopResult answers OpProcessStop. Processes has one entry for each
// distinct process ID asked for, in the order asked.
type ProcessStopResult struct {
	NodeID    string           `json:"node_id,omitempty"`
	Hostname  string           `json:"hostname,omitempty"`
	Processes []ProcessOutcome `json:"processes"`
}

// SessionProcessStopRelay is the owner's process stop sent to another machine.
type SessionProcessStopRelay struct {
	Owner string `json:"owner_session"`
	ProcessStopRequest
}

// SessionOwnerProcessStop is the gateway's frame to the machine that runs the
// processes. FromNode is the machine the owner session belongs to.
type SessionOwnerProcessStop struct {
	Owner    string `json:"owner_session"`
	FromNode string `json:"from_node"`
	ProcessStopRequest
}
