package protocol

// OpWorkerLog reads recent worker runs. Only the owner may call it.
const OpWorkerLog = "worker_log"

// WorkerLogQuery bounds the history returned to the owner.
type WorkerLogQuery struct {
	Limit int `json:"limit,omitempty"`
}

// WorkerLogEntry describes one start, including runs whose session has ended.
type WorkerLogEntry struct {
	EndedAt    int64             `json:"ended_at"`
	SessionID  string            `json:"session_id"`
	NodeID     string            `json:"node_id"`
	Hostname   string            `json:"hostname"`
	Project    string            `json:"project"`
	Harness    string            `json:"harness"`
	Model      string            `json:"model"`
	Title      string            `json:"title"`
	Difficulty string            `json:"difficulty"`
	Status     string            `json:"status"`
	StartedAt  int64             `json:"started_at"`
	RunSeconds int64             `json:"run_seconds"`
	Failure    string            `json:"failure,omitempty"`
	Assessment *WorkerAssessment `json:"assessment,omitempty"`
}
