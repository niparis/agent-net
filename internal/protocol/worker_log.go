package protocol

// OpWorkerLog reads recent worker runs. Only the owner may call it.
const OpWorkerLog = "worker_log"

// WorkerLogQuery bounds the history returned to the owner.
type WorkerLogQuery struct {
	Limit int `json:"limit,omitempty"`
}

// WorkerLogEntry describes one start, including runs whose session has ended.
type WorkerLogEntry struct {
	// ModelNote is the stored picker explanation, when available.
	ModelNote string `json:"model_note,omitempty"`
	EndedAt   int64  `json:"ended_at"`
	SessionID string `json:"session_id"`
	NodeID    string `json:"node_id"`
	Hostname  string `json:"hostname"`
	Project   string `json:"project"`
	Harness   string `json:"harness"`
	Model     string `json:"model"`
	Title     string `json:"title"`
	// Caller is the display name of the session that started the worker:
	// "owner" for a start from the TUI or command line, "CTO" for a CTO
	// start, and the caller session's name otherwise. CallerHost is that
	// session's machine. Both are empty for a run whose caller is unknown.
	Caller     string `json:"caller,omitempty"`
	CallerHost string `json:"caller_host,omitempty"`
	// BriefDefects is how many brief defects are charged to this run's
	// caller. They are the caller's rework, not the worker's.
	BriefDefects int               `json:"brief_defects,omitempty"`
	Difficulty   string            `json:"difficulty"`
	Status       string            `json:"status"`
	StartedAt    int64             `json:"started_at"`
	RunSeconds   int64             `json:"run_seconds"`
	Failure      string            `json:"failure,omitempty"`
	Assessment   *WorkerAssessment `json:"assessment,omitempty"`
	// Tokens and Cost are the run's usage and its cost at API prices
	// (decision 0161). Both are empty for a run with no recorded usage.
	// PriceUnknown is true when the cost used the middle price because the
	// model has no listed one.
	Tokens       *RunTokens `json:"tokens,omitempty"`
	Cost         float64    `json:"cost,omitempty"`
	PriceUnknown bool       `json:"price_unknown,omitempty"`
}
