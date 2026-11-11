package protocol

// FrameNodeModels asks a machine's daemon to list the models of its harnesses
// now, by running the harness itself. The answer is never a stored inventory.
const FrameNodeModels = "node.models"

// FrameModelTest asks a machine to try one model with a one-line task. It is
// the optional real test of a profile add, not a model list read.
const FrameModelTest = "worker.model_test"

// ModelTestRequest names the model to try.
type ModelTestRequest struct {
	Harness string `json:"harness"`
	Model   string `json:"model"`
	Effort  string `json:"effort,omitempty"`
}

// ModelTestResult is the harness's own answer. Output keeps the last lines of
// it, trimmed; OK is true only when the harness exited successfully.
type ModelTestResult struct {
	OK     bool   `json:"ok"`
	Output string `json:"output,omitempty"`
}

// ModelListRequest is a live model listing for one machine. An empty Harness
// lists every installed harness. Filter keeps only model ids that contain it,
// without case.
type ModelListRequest struct {
	Harness string `json:"harness,omitempty"`
	Filter  string `json:"filter,omitempty"`
}

// ModelListResult is the live list a machine reports. FetchedAt is Unix time
// in seconds of the harness read; true means the daemon served a cached read
// no older than its five minute limit.
type ModelListResult struct {
	Harnesses []HarnessModels `json:"harnesses"`
	FetchedAt int64           `json:"fetched_at,omitempty"`
	Cached    bool            `json:"cached,omitempty"`
}

// HarnessModels is one harness's live model ids.
type HarnessModels struct {
	Name   string   `json:"name"`
	Models []string `json:"models"`
	Cached bool     `json:"cached,omitempty"`
	Error  string   `json:"error,omitempty"`
}
